package geoip

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"netwatch/internal/regularfile"
)

// NWGEO v1: header (64 bytes), 64-byte sorted ranges, then variable-length locations.
// All offsets are little-endian. The converter never retains the CSV rows in memory.
const (
	binaryHeaderSize      = 64
	binaryRecordSize      = 64
	binaryBlockRows       = 256
	binaryVersion         = 1
	maxGeoRecordBytes     = 65535
	maxBinaryBytes        = 2 << 30
	maxConversionCSVBytes = 2 << 30
)

var binaryMagic = [8]byte{'N', 'W', 'G', 'E', 'O', '0', '0', '1'}

type blockEntry struct {
	first [16]byte
	row   uint64
}

type BinaryResolver struct {
	file          *os.File
	family        byte
	count         uint64
	metadataStart uint64
	metadataSize  uint64
	blocks        []blockEntry
}

// ConvertCSV converts a sorted eight-column decimal-range CSV to NWGEO v1.
// It writes into a temporary file and publishes the output only on success.
// Source and destination must be distinct paths. family must be 4 or 6.
func ConvertCSV(source, destination string, family int) error {
	return ConvertCSVContext(context.Background(), source, destination, family)
}

// ConvertCSVContext runs the same conversion with cancellation.
func ConvertCSVContext(ctx context.Context, source, destination string, family int) error {
	if family != 4 && family != 6 {
		return fmt.Errorf("family must be 4 or 6")
	}
	src, srcInfo, err := regularfile.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	if srcInfo.Size() > maxConversionCSVBytes {
		return fmt.Errorf("source CSV exceeds %d bytes", int64(maxConversionCSVBytes))
	}
	destDir := filepath.Dir(destination)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	if old, err := os.Stat(destination); err == nil {
		if os.SameFile(srcInfo, old) {
			return errors.New("source and destination refer to the same file")
		}
		return fmt.Errorf("destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	ranges, err := os.CreateTemp(destDir, ".nwgeo-ranges-*")
	if err != nil {
		return err
	}
	defer func() { ranges.Close(); os.Remove(ranges.Name()) }()
	meta, err := os.CreateTemp(destDir, ".nwgeo-meta-*")
	if err != nil {
		return err
	}
	defer func() { meta.Close(); os.Remove(meta.Name()) }()
	if _, err := ranges.Write(make([]byte, binaryHeaderSize)); err != nil {
		return err
	}
	wr := bufio.NewWriterSize(ranges, 64*1024)
	mw := bufio.NewWriterSize(meta, 64*1024)
	cr := newBoundedCSV(contextReader{ctx, io.NewSectionReader(src, 0, srcInfo.Size())}, -1)
	var count uint64
	var metaSize uint64
	var prevEnd [16]byte
	havePrev := false
	line := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return fmt.Errorf("CSV row %d: %w", line, err)
		}
		if len(row) < 8 {
			return fmt.Errorf("CSV row %d: expected at least 8 columns", line)
		}
		start, err := decimalIP(strings.TrimSpace(row[0]), family == 6)
		if err != nil {
			if line == 1 && rangeHeader(row) {
				continue
			}
			return fmt.Errorf("CSV row %d: start: %w", line, err)
		}
		end, err := decimalIP(strings.TrimSpace(row[1]), family == 6)
		if err != nil {
			return fmt.Errorf("CSV row %d: end: %w", line, err)
		}
		if bytes.Compare(start[:], end[:]) > 0 || havePrev && bytes.Compare(start[:], prevEnd[:]) <= 0 {
			return fmt.Errorf("CSV row %d: unordered or overlapping range", line)
		}
		lat, err := strconv.ParseFloat(strings.TrimSpace(row[6]), 64)
		if err != nil {
			return fmt.Errorf("CSV row %d: latitude: %w", line, err)
		}
		lon, err := strconv.ParseFloat(strings.TrimSpace(row[7]), 64)
		if err != nil {
			return fmt.Errorf("CSV row %d: longitude: %w", line, err)
		}
		if !validCoordinates(lat, lon) {
			return fmt.Errorf("CSV row %d: coordinates out of range", line)
		}
		var metadata []byte
		for _, column := range row[2:6] {
			val := []byte(strings.TrimSpace(column))
			if len(val) > maxGeoRecordBytes {
				return fmt.Errorf("CSV row %d: text too long", line)
			}
			metadata = binary.LittleEndian.AppendUint16(metadata, uint16(len(val)))
			metadata = append(metadata, val...)
		}
		if len(metadata) > maxGeoRecordBytes {
			return fmt.Errorf("CSV row %d: location exceeds %d bytes", line, maxGeoRecordBytes)
		}
		if binaryHeaderSize+(count+1)*binaryRecordSize+metaSize+uint64(len(metadata)) > maxBinaryBytes {
			return fmt.Errorf("converted NWGEO exceeds %d bytes", int64(maxBinaryBytes))
		}
		var entry [binaryRecordSize]byte
		copy(entry[0:16], start[:])
		copy(entry[16:32], end[:])
		binary.LittleEndian.PutUint64(entry[32:40], math.Float64bits(lat))
		binary.LittleEndian.PutUint64(entry[40:48], math.Float64bits(lon))
		binary.LittleEndian.PutUint64(entry[48:56], metaSize)
		binary.LittleEndian.PutUint32(entry[56:60], uint32(len(metadata)))
		if _, err := wr.Write(entry[:]); err != nil {
			return err
		}
		if _, err := mw.Write(metadata); err != nil {
			return err
		}
		metaSize += uint64(len(metadata))
		count++
		prevEnd = end
		havePrev = true
	}
	if count == 0 {
		return errors.New("CSV contained no ranges")
	}
	if err := wr.Flush(); err != nil {
		return err
	}
	if err := mw.Flush(); err != nil {
		return err
	}
	if _, err := meta.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if srcInfo.Size() != cr.csv.InputOffset() {
		return io.ErrUnexpectedEOF
	}
	if _, err := io.Copy(ranges, contextReader{ctx: ctx, source: meta}); err != nil {
		return err
	}
	var header [binaryHeaderSize]byte
	copy(header[0:8], binaryMagic[:])
	header[8] = binaryVersion
	header[9] = byte(family)
	binary.LittleEndian.PutUint64(header[16:24], count)
	binary.LittleEndian.PutUint64(header[24:32], binaryHeaderSize+count*binaryRecordSize)
	binary.LittleEndian.PutUint64(header[32:40], metaSize)
	if _, err := ranges.WriteAt(header[:], 0); err != nil {
		return err
	}
	if err := ranges.Sync(); err != nil {
		return err
	}
	if err := ranges.Close(); err != nil {
		return err
	}
	// Never silently overwrite an existing database.
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("destination appeared during conversion: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(ranges.Name(), destination); err != nil {
		return fmt.Errorf("publish converted file without overwrite: %w", err)
	}
	return nil
}

// OpenBinary opens a verified NWGEO v1 database and builds a sparse, read-only RAM index.
func OpenBinary(path string) (*BinaryResolver, error) {
	f, info, err := regularfile.Open(path)
	if err != nil {
		return nil, err
	}
	r, err := openBinary(context.Background(), f, info.Size())
	if err != nil {
		f.Close()
	}
	return r, err
}

func openBinary(ctx context.Context, f *os.File, fileSize int64) (*BinaryResolver, error) {
	if fileSize > maxBinaryBytes {
		return nil, fmt.Errorf("NWGEO exceeds %d bytes", int64(maxBinaryBytes))
	}
	if fileSize < binaryHeaderSize {
		return nil, errors.New("truncated database header")
	}
	var h [binaryHeaderSize]byte
	if _, err := f.ReadAt(h[:], 0); err != nil {
		return nil, err
	}
	if !bytes.Equal(h[:8], binaryMagic[:]) || h[8] != binaryVersion || (h[9] != 4 && h[9] != 6) {
		return nil, errors.New("unsupported NWGEO database")
	}
	count := binary.LittleEndian.Uint64(h[16:24])
	start := binary.LittleEndian.Uint64(h[24:32])
	size := binary.LittleEndian.Uint64(h[32:40])
	if count == 0 || count > (math.MaxUint64-binaryHeaderSize)/binaryRecordSize || start != binaryHeaderSize+count*binaryRecordSize || size > math.MaxUint64-start || start+size != uint64(fileSize) {
		return nil, errors.New("invalid NWGEO file size or header")
	}
	r := &BinaryResolver{file: f, family: h[9], count: count, metadataStart: start, metadataSize: size}
	r.blocks = make([]blockEntry, 0, (count+binaryBlockRows-1)/binaryBlockRows)
	records := bufio.NewReaderSize(contextReader{ctx, io.NewSectionReader(f, binaryHeaderSize, int64(start)-binaryHeaderSize)}, 64<<10)
	var previousEnd [16]byte
	var record [binaryRecordSize]byte
	var ipv4Prefix [12]byte
	for row := uint64(0); row < count; row++ {
		if _, err := io.ReadFull(records, record[:]); err != nil {
			return nil, err
		}
		first, last := record[:16], record[16:32]
		if bytes.Compare(first, last) > 0 || row > 0 && bytes.Compare(first, previousEnd[:]) <= 0 ||
			r.family == 4 && (!bytes.Equal(first[:12], ipv4Prefix[:]) || !bytes.Equal(last[:12], ipv4Prefix[:])) {
			return nil, fmt.Errorf("invalid NWGEO range at record %d", row+1)
		}
		lat := math.Float64frombits(binary.LittleEndian.Uint64(record[32:40]))
		lon := math.Float64frombits(binary.LittleEndian.Uint64(record[40:48]))
		if _, _, ok := r.recordMetadata(record[:]); !ok || !validCoordinates(lat, lon) {
			return nil, fmt.Errorf("invalid NWGEO location at record %d", row+1)
		}
		copy(previousEnd[:], last)
		if row%binaryBlockRows == 0 {
			r.blocks = append(r.blocks, blockEntry{[16]byte(first), row})
		}
	}
	return r, nil
}

func (r *BinaryResolver) recordMetadata(record []byte) (offset uint64, length uint32, valid bool) {
	offset = binary.LittleEndian.Uint64(record[48:56])
	length = binary.LittleEndian.Uint32(record[56:60])
	valid = offset <= r.metadataSize && uint64(length) <= r.metadataSize-offset &&
		length >= 8 && length <= maxGeoRecordBytes
	return
}

func (r *BinaryResolver) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	return r.file.Close()
}

// Lookup performs a bounded block read, followed by one metadata read on a hit.
// It is safe for concurrent callers, provided Close is not called concurrently.
func (r *BinaryResolver) Lookup(rawIP string) Location {
	if r == nil || r.file == nil {
		return Location{}
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(rawIP))
	if err != nil {
		return Location{}
	}
	addr = addr.Unmap()
	if !isPublicCandidate(addr) {
		return Location{}
	}
	key, v6 := addressKey(addr)
	if v6 != (r.family == 6) {
		return Location{}
	}
	low, high := 0, len(r.blocks)
	for low < high {
		mid := int(uint(low+high) / 2)
		if bytes.Compare(r.blocks[mid].first[:], key[:]) <= 0 {
			low = mid + 1
		} else {
			high = mid
		}
	}
	if low == 0 {
		return Location{}
	}
	b := r.blocks[low-1]
	rows := r.count - b.row
	if rows > binaryBlockRows {
		rows = binaryBlockRows
	}
	var block [binaryBlockRows * binaryRecordSize]byte
	buf := block[:int(rows)*binaryRecordSize]
	if _, err := r.file.ReadAt(buf, int64(binaryHeaderSize+b.row*binaryRecordSize)); err != nil {
		return Location{}
	}
	lowRow, highRow := 0, int(rows)
	for lowRow < highRow {
		mid := int(uint(lowRow+highRow) / 2)
		if bytes.Compare(buf[mid*binaryRecordSize+16:mid*binaryRecordSize+32], key[:]) < 0 {
			lowRow = mid + 1
		} else {
			highRow = mid
		}
	}
	if lowRow == int(rows) {
		return Location{}
	}
	rec := buf[lowRow*binaryRecordSize : (lowRow+1)*binaryRecordSize]
	if bytes.Compare(rec[:16], key[:]) > 0 {
		return Location{}
	}
	lat := math.Float64frombits(binary.LittleEndian.Uint64(rec[32:40]))
	lon := math.Float64frombits(binary.LittleEndian.Uint64(rec[40:48]))
	if !validCoordinates(lat, lon) {
		return Location{}
	}
	offset, length, ok := r.recordMetadata(rec)
	if !ok {
		return Location{}
	}
	payload := make([]byte, length)
	if _, err := r.file.ReadAt(payload, int64(r.metadataStart+offset)); err != nil {
		return Location{}
	}
	fields := [4]string{}
	pos := 0
	for i := range fields {
		if pos+2 > len(payload) {
			return Location{}
		}
		n := int(binary.LittleEndian.Uint16(payload[pos : pos+2]))
		pos += 2
		if n > len(payload)-pos {
			return Location{}
		}
		fields[i] = string(payload[pos : pos+n])
		pos += n
	}
	if pos != len(payload) {
		return Location{}
	}
	return Location{CountryCode: fields[0], Country: fields[1], Region: fields[2], City: fields[3], Latitude: lat, Longitude: lon, Found: true}
}
