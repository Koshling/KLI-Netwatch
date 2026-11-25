package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGeoIPLimitsCSVRecords(t *testing.T) {
	valid := "1,2,US,Country,Region,\"City\nName\",1,2\r\n"
	large := "1,2,US,Country,Region,\"" + strings.Repeat("x\n", maxCSVRecordBytes/2) + "\",1,2\n"
	reader := newBoundedCSV(strings.NewReader(valid+large), 8)
	row, err := reader.Read()
	if err != nil || row[5] != "City\nName" {
		t.Fatalf("multiline CSV row: %q, %v", row, err)
	}
	if _, err := reader.Read(); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("large CSV row: %v", err)
	}
	for _, size := range []int{maxCSVRecordBytes, maxCSVRecordBytes + 1} {
		for _, suffix := range []string{"", "\n"} {
			input := strings.Repeat("x", size-len(suffix)) + suffix
			_, err := newBoundedCSV(strings.NewReader(input), 1).Read()
			if (err == nil) != (size == maxCSVRecordBytes) {
				t.Fatalf("size %d ending %q: %v", size, suffix, err)
			}
		}
	}
}

func TestLegacyCSVTotalLimit(t *testing.T) {
	dir := t.TempDir()
	v4 := filepath.Join(dir, "ipv4.csv")
	v6 := filepath.Join(dir, "ipv6.csv")
	if err := os.WriteFile(v4, []byte("1,2,US,X,Y,Z,1,2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(v6)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(maxLegacyCSVBytes)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if r, err := Open(v4, v6); err == nil {
		r.Close()
		t.Fatal("accepted oversized combined legacy inputs")
	}
}

func TestBinaryRejectsOverlappingRangesOnOpen(t *testing.T) {
	_, path := binaryCSV(t, "1,10,US,X,Y,Z,1,2\n11,12,US,X,Y,Z,1,2\n", 4)
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	var start [16]byte
	binary.BigEndian.PutUint32(start[12:], 10)
	_, err = f.WriteAt(start[:], binaryHeaderSize+binaryRecordSize)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if r, err := OpenBinary(path); err == nil {
		r.Close()
		t.Fatal("opened a database with overlapping ranges")
	}
}

func TestBinaryCacheLimitsMetadataBytes(t *testing.T) {
	resolver := &Resolver{cache: make(map[netip.Addr]Location)}
	location := Location{City: strings.Repeat("x", maxGeoRecordBytes-8)}
	for i := range 256 {
		resolver.cacheLocation(netip.AddrFrom4([4]byte{8, 0, 0, byte(i)}), location)
		if resolver.cacheTextBytes > maxCacheTextBytes {
			t.Fatal("cache exceeded text budget")
		}
	}
	used := len(resolver.cache) * len(location.City)
	if resolver.cacheTextBytes != used || used == 0 {
		t.Fatalf("accounted %d bytes, used %d", resolver.cacheTextBytes, used)
	}
}

func TestArchiveByteLimitDistinguishesEOF(t *testing.T) {
	for _, input := range []string{"abc", "abcd"} {
		reader := &archiveLimit{source: strings.NewReader(input), remaining: 3, name: "archive"}
		data, err := io.ReadAll(reader)
		if string(data) != "abc" || (err == nil) != (len(input) == 3) {
			t.Fatalf("input %q: got %q, %v", input, data, err)
		}
	}
}

func TestGeoIPRejectsUntrustedRedirects(t *testing.T) {
	origin, err := http.NewRequest(http.MethodGet, "https://download.db-ip.com/free", nil)
	if err != nil {
		t.Fatal(err)
	}
	for url, allowed := range map[string]bool{
		"https://download.db-ip.com/archive":      true,
		"https://download.db-ip.com:443/archive":  true,
		"http://download.db-ip.com/archive":       false,
		"https://elsewhere.invalid/archive":       false,
		"https://download.db-ip.com:8443/archive": false,
		"https://user@download.db-ip.com/archive": false,
	} {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := sameOriginRedirect(req, []*http.Request{origin}); (err == nil) != allowed {
			t.Errorf("%s: got %v", url, err)
		}
	}
}

func TestDownloadRejectsOversizedResponse(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxDownloadBytes+1, 10))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	_, err := downloadDBIP(t.Context(), dir, "country", server.URL, time.Now(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "compressed") {
		t.Fatalf("got %v", err)
	}
}

func TestInvalidGzipLeavesNoInstallation(t *testing.T) {
	var archive bytes.Buffer
	writer := gzip.NewWriter(&archive)
	if _, err := io.WriteString(writer, "8.8.8.0,8.8.8.255,US\n2001:4860::,2001:4860::ffff,US\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	archive.Truncate(archive.Len() - 1)
	if _, err := installDBIP(context.Background(), &archive, dir, "country", "test", nil); err == nil {
		t.Fatal("accepted truncated gzip stream")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial installation: %v, %v", entries, err)
	}
}

func FuzzBoundedCSV(f *testing.F) {
	f.Add("1,2,US,Country,Region,\"City\nName\",1,2\n")
	f.Add("unterminated,\"field")
	f.Fuzz(func(t *testing.T, input string) {
		reader := newBoundedCSV(strings.NewReader(input), -1)
		for {
			offset := reader.csv.InputOffset()
			_, err := reader.Read()
			if err != nil {
				return
			}
			if consumed := reader.csv.InputOffset() - offset; consumed <= 0 || consumed > maxCSVRecordBytes {
				t.Fatalf("consumed %d bytes", consumed)
			}
		}
	})
}
