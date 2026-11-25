package geoip

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/big"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"netwatch/internal/regularfile"
)

const (
	maxCacheEntries   = 16384
	maxCacheTextBytes = 4 << 20
	maxLegacyCSVBytes = 16 << 20
	maxLegacyCSVRows  = 200000
)

type Location struct {
	CountryCode string
	Country     string
	Region      string
	City        string
	Latitude    float64
	Longitude   float64
	Found       bool
}

type ipRange struct {
	start [16]byte
	end   [16]byte
	loc   uint32
}

type Resolver struct {
	v4        []ipRange
	v6        []ipRange
	locations []Location
	binaryV4  *BinaryResolver
	binaryV6  *BinaryResolver

	cacheMu          sync.RWMutex
	cache            map[netip.Addr]Location
	cacheTextBytes   int
	cacheOrder       []netip.Addr
	cacheHead        int
	legacyInputBytes int64
}

// Open accepts legacy CSV or NWGEO v1 files independently for each address family.
// NWGEO files are searched on disk; CSV files retain the existing in-memory behavior.
func Open(ipv4Path, ipv6Path string) (*Resolver, error) {
	return OpenContext(context.Background(), ipv4Path, ipv6Path)
}

// OpenContext also observes cancellation while scanning each database.
func OpenContext(ctx context.Context, ipv4Path, ipv6Path string) (*Resolver, error) {
	r := &Resolver{cache: make(map[netip.Addr]Location)}
	if ipv4Path != "" {
		if err := r.loadDatabase(ctx, ipv4Path, false); err != nil {
			_ = r.Close()
			return nil, fmt.Errorf("load IPv4 GeoIP database: %w", err)
		}
	}
	if ipv6Path != "" {
		if err := r.loadDatabase(ctx, ipv6Path, true); err != nil {
			_ = r.Close()
			return nil, fmt.Errorf("load IPv6 GeoIP database: %w", err)
		}
	}
	if len(r.v4) == 0 && len(r.v6) == 0 && r.binaryV4 == nil && r.binaryV6 == nil {
		_ = r.Close()
		return nil, fmt.Errorf("no GeoIP database rows loaded")
	}
	return r, nil
}

// Close releases the file descriptors of binary databases. Do not call
// concurrently with Lookup; shutdown calls it after the dashboard exits.
func (r *Resolver) Close() error {
	if r == nil {
		return nil
	}
	var first error
	if r.binaryV4 != nil {
		if err := r.binaryV4.Close(); err != nil && first == nil {
			first = err
		}
		r.binaryV4 = nil
	}
	if r.binaryV6 != nil {
		if err := r.binaryV6.Close(); err != nil && first == nil {
			first = err
		}
		r.binaryV6 = nil
	}
	return first
}

func (r *Resolver) loadDatabase(ctx context.Context, path string, ipv6 bool) error {
	f, info, err := regularfile.Open(path)
	if err != nil {
		return err
	}
	var magic [8]byte
	n, readErr := f.ReadAt(magic[:], 0)
	if readErr != nil && readErr != io.EOF {
		f.Close()
		return readErr
	}
	// Explicit .nwgeo paths are always parsed as binary, including if corrupt.
	// Magic also permits binary files without the canonical extension.
	if filepath.Ext(path) == ".nwgeo" || n == len(magic) && magic == binaryMagic {
		br, err := openBinary(ctx, f, info.Size())
		if err != nil {
			f.Close()
			return err
		}
		family := byte(4)
		if ipv6 {
			family = 6
		}
		if br.family != family {
			_ = br.Close()
			return fmt.Errorf("NWGEO address family mismatch: expected IPv%d, got IPv%d", family, br.family)
		}
		if ipv6 {
			r.binaryV6 = br
		} else {
			r.binaryV4 = br
		}
		return nil
	}
	defer f.Close()
	if info.Size() > maxLegacyCSVBytes-r.legacyInputBytes {
		return fmt.Errorf("legacy GeoIP CSV exceeds %d total bytes; convert to NWGEO", maxLegacyCSVBytes)
	}
	r.legacyInputBytes += info.Size()
	return r.loadCSV(contextReader{ctx, io.NewSectionReader(f, 0, info.Size())}, info.Size(), ipv6)
}

func (r *Resolver) loadCSV(src io.Reader, size int64, ipv6 bool) error {
	reader := newBoundedCSV(src, -1)

	var previousEnd [16]byte
	havePrevious := false
	line := 0

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return fmt.Errorf("CSV line %d: %w", line, err)
		}
		if len(record) < 8 {
			return fmt.Errorf("CSV line %d: expected at least 8 columns, got %d", line, len(record))
		}

		start, err := decimalIP(strings.TrimSpace(record[0]), ipv6)
		if err != nil {
			// Some CSV exports include a header row.
			if line == 1 && rangeHeader(record) {
				continue
			}
			return fmt.Errorf("CSV line %d: invalid ip_from: %w", line, err)
		}
		end, err := decimalIP(strings.TrimSpace(record[1]), ipv6)
		if err != nil {
			return fmt.Errorf("CSV line %d: invalid ip_to: %w", line, err)
		}
		if bytes.Compare(start[:], end[:]) > 0 {
			return fmt.Errorf("CSV line %d: ip_from is greater than ip_to", line)
		}
		if havePrevious && bytes.Compare(start[:], previousEnd[:]) <= 0 {
			return fmt.Errorf("CSV line %d: ranges are not strictly ordered or overlap", line)
		}

		lat, err := strconv.ParseFloat(strings.TrimSpace(record[6]), 64)
		if err != nil {
			return fmt.Errorf("CSV line %d: invalid latitude: %w", line, err)
		}
		lon, err := strconv.ParseFloat(strings.TrimSpace(record[7]), 64)
		if err != nil {
			return fmt.Errorf("CSV line %d: invalid longitude: %w", line, err)
		}
		if !validCoordinates(lat, lon) {
			return fmt.Errorf("CSV line %d: coordinates out of range", line)
		}

		loc := Location{
			CountryCode: strings.TrimSpace(record[2]),
			Country:     strings.TrimSpace(record[3]),
			Region:      strings.TrimSpace(record[4]),
			City:        strings.TrimSpace(record[5]),
			Latitude:    lat,
			Longitude:   lon,
			Found:       true,
		}

		if len(r.locations) >= maxLegacyCSVRows {
			return fmt.Errorf("legacy GeoIP CSV exceeds %d total ranges; convert to NWGEO", maxLegacyCSVRows)
		}
		index := uint32(len(r.locations))
		r.locations = append(r.locations, loc)

		entry := ipRange{start: start, end: end, loc: index}
		if ipv6 {
			r.v6 = append(r.v6, entry)
		} else {
			r.v4 = append(r.v4, entry)
		}

		previousEnd = end
		havePrevious = true
	}

	if reader.csv.InputOffset() != size {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func rangeHeader(row []string) bool {
	first := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(row[0], "\uFEFF")))
	last := strings.ToLower(strings.TrimSpace(row[1]))
	return (first == "start" && last == "end") || (first == "ip_from" && last == "ip_to") || (first == "from" && last == "to")
}

func validCoordinates(lat, lon float64) bool {
	return lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

func decimalIP(value string, ipv6 bool) ([16]byte, error) {
	var result [16]byte

	// Drop leading zeroes before bounding big.Int work, retaining the
	// historical acceptance of zero-padded decimal addresses.
	digits, positive := strings.CutPrefix(value, "+")
	if positive && (digits == "" || digits[0] < '0' || digits[0] > '9') {
		return result, fmt.Errorf("not an unsigned decimal integer")
	}
	if strings.HasPrefix(digits, "0") {
		digits = strings.TrimLeft(digits, "0")
		if digits == "" {
			digits = "0"
		} else if digits[0] < '0' || digits[0] > '9' {
			return result, fmt.Errorf("not an unsigned decimal integer")
		}
	}
	if len(digits) > 39 {
		return result, fmt.Errorf("decimal address exceeds 39 digits")
	}
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok || n.Sign() < 0 {
		return result, fmt.Errorf("not an unsigned decimal integer")
	}

	maxBits := uint(32)
	if ipv6 {
		maxBits = 128
	}
	if n.BitLen() > int(maxBits) {
		return result, fmt.Errorf("integer exceeds %d bits", maxBits)
	}

	raw := n.Bytes()
	if ipv6 {
		copy(result[16-len(raw):], raw)
	} else {
		copy(result[12+(4-len(raw)):], raw)
	}
	return result, nil
}

func (r *Resolver) Lookup(rawIP string) Location {
	addr, err := netip.ParseAddr(strings.TrimSpace(rawIP))
	if err != nil {
		return Location{}
	}
	return r.LookupAddr(addr)
}

// LookupAddr uses an already parsed address from the monitor.
func (r *Resolver) LookupAddr(addr netip.Addr) Location {
	addr = addr.Unmap()
	if addr.Zone() != "" || !isPublicCandidate(addr) {
		return Location{}
	}

	r.cacheMu.RLock()
	loc, ok := r.cache[addr]
	r.cacheMu.RUnlock()
	if ok {
		return loc
	}

	key, isV6 := addressKey(addr)
	// Resolve NWGEO directly from the SSD. Cache the result like legacy CSV.
	var disk *BinaryResolver
	if isV6 {
		disk = r.binaryV6
	} else {
		disk = r.binaryV4
	}
	if disk != nil {
		loc = disk.lookupAddress(key, isV6)
		r.cacheLocation(addr, loc)
		return loc
	}
	ranges := r.v4
	if isV6 {
		ranges = r.v6
	}

	index := sort.Search(len(ranges), func(i int) bool {
		return bytes.Compare(ranges[i].end[:], key[:]) >= 0
	})

	if index < len(ranges) &&
		bytes.Compare(ranges[index].start[:], key[:]) <= 0 {
		loc = r.locations[ranges[index].loc]
	}

	// Legacy CSV lookups already reference resident metadata.
	return loc
}

func locationTextBytes(loc Location) int {
	return len(loc.CountryCode) + len(loc.Country) + len(loc.Region) + len(loc.City)
}

func (r *Resolver) cacheLocation(addr netip.Addr, loc Location) {
	textBytes := locationTextBytes(loc)
	if textBytes > maxCacheTextBytes {
		return
	}
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if _, exists := r.cache[addr]; exists {
		return
	}
	for len(r.cache) >= maxCacheEntries || r.cacheTextBytes+textBytes > maxCacheTextBytes {
		oldest := r.cacheOrder[r.cacheHead]
		r.cacheTextBytes -= locationTextBytes(r.cache[oldest])
		delete(r.cache, oldest)
		r.cacheOrder[r.cacheHead] = netip.Addr{}
		r.cacheHead = (r.cacheHead + 1) % len(r.cacheOrder)
	}
	if len(r.cache) == len(r.cacheOrder) {
		order := make([]netip.Addr, min(maxCacheEntries, max(16, 2*len(r.cacheOrder))))
		n := copy(order, r.cacheOrder[r.cacheHead:])
		copy(order[n:], r.cacheOrder[:r.cacheHead])
		r.cacheOrder = order
		r.cacheHead = 0
	}
	slot := (r.cacheHead + len(r.cache)) % len(r.cacheOrder)
	r.cacheOrder[slot] = addr
	r.cache[addr] = loc
	r.cacheTextBytes += textBytes
}

func addressKey(addr netip.Addr) ([16]byte, bool) {
	var key [16]byte
	if addr.Is4() {
		v := addr.As4()
		copy(key[12:], v[:])
		return key, false
	}
	return addr.As16(), true
}

func isPublicCandidate(addr netip.Addr) bool {
	return addr.IsValid() &&
		addr.IsGlobalUnicast() &&
		!addr.IsPrivate() &&
		!addr.IsLoopback() &&
		!addr.IsLinkLocalUnicast() &&
		!addr.IsLinkLocalMulticast() &&
		!addr.IsMulticast() &&
		!addr.IsUnspecified()
}
