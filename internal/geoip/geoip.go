package geoip

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const maxCacheEntries = 65536

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

	cacheMu sync.RWMutex
	cache   map[netip.Addr]Location
}

// Open accepts legacy CSV or NWGEO v1 files independently for each address family.
// NWGEO files are searched on disk; CSV files retain the existing in-memory behavior.
func Open(ipv4Path, ipv6Path string) (*Resolver, error) {
	r := &Resolver{cache: make(map[netip.Addr]Location)}
	if ipv4Path != "" {
		if err := r.loadDatabase(ipv4Path, false); err != nil {
			_ = r.Close()
			return nil, fmt.Errorf("load IPv4 GeoIP database: %w", err)
		}
	}
	if ipv6Path != "" {
		if err := r.loadDatabase(ipv6Path, true); err != nil {
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

func (r *Resolver) loadDatabase(path string, ipv6 bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	var magic [8]byte
	n, readErr := f.Read(magic[:])
	closeErr := f.Close()
	if readErr != nil && readErr != io.EOF {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	// Explicit .nwgeo paths are always parsed as binary, including if corrupt.
	// Magic also permits binary files without the canonical extension.
	if filepath.Ext(path) == ".nwgeo" || n == len(magic) && magic == binaryMagic {
		br, err := OpenBinary(path)
		if err != nil {
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
	return r.loadFile(path, ipv6)
}

func (r *Resolver) loadFile(path string, ipv6 bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true

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
			if line == 1 {
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
		if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
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

	return nil
}

func decimalIP(value string, ipv6 bool) ([16]byte, error) {
	var result [16]byte

	n, ok := new(big.Int).SetString(value, 10)
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
	addr = addr.Unmap()

	if !isPublicCandidate(addr) {
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
		loc = disk.Lookup(addr.String())
		r.cacheMu.Lock()
		if len(r.cache) >= maxCacheEntries {
			clear(r.cache)
		}
		r.cache[addr] = loc
		r.cacheMu.Unlock()
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

	r.cacheMu.Lock()
	if len(r.cache) >= maxCacheEntries {
		clear(r.cache)
	}
	r.cache[addr] = loc
	r.cacheMu.Unlock()

	return loc
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
