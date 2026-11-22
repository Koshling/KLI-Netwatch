package geoip

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func binaryCSV(t *testing.T, rows string, family int) (*BinaryResolver, string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "source.csv")
	dst := filepath.Join(dir, "test.nwgeo")
	if err := os.WriteFile(src, []byte(rows), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ConvertCSV(src, dst, family); err != nil {
		t.Fatal(err)
	}
	r, err := OpenBinary(dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dst
}

func TestBinaryIPv4(t *testing.T) {
	r, _ := binaryCSV(t, "ip_from,ip_to,country_code,country,region,city,lat,lon\n134744064,134744319,US,United States,California,Los Angeles,34.05,-118.24\n134744576,134744831,CA,Canada,Ontario,Toronto,43.7,-79.4\n", 4)
	for _, ip := range []string{"8.8.8.0", "8.8.8.8", "8.8.8.255", "::ffff:8.8.8.8"} {
		if v := r.Lookup(ip); !v.Found || v.City != "Los Angeles" {
			t.Fatalf("%s => %+v", ip, v)
		}
	}
	for _, ip := range []string{"8.8.9.0", "127.0.0.1", "10.0.0.1", "garbage", "2001:4860::8888"} {
		if v := r.Lookup(ip); v.Found {
			t.Fatalf("false positive %s => %+v", ip, v)
		}
	}
	if v := r.Lookup("8.8.10.5"); !v.Found || v.CountryCode != "CA" {
		t.Fatalf("second range: %+v", v)
	}
}

func TestBinaryIPv6(t *testing.T) {
	a := netip.MustParseAddr("2001:4860::8888").As16()
	b := netip.MustParseAddr("2001:4860::8889").As16()
	r, _ := binaryCSV(t, fmt.Sprintf("%s,%s,US,United States,California,Mountain View,37.4,-122.1\n", new(big.Int).SetBytes(a[:]), new(big.Int).SetBytes(b[:])), 6)
	for _, ip := range []string{"2001:4860::8888", "2001:4860::8889"} {
		if v := r.Lookup(ip); !v.Found || v.City != "Mountain View" {
			t.Fatalf("%s => %+v", ip, v)
		}
	}
	if v := r.Lookup("2001:4860::8890"); v.Found {
		t.Fatalf("unexpected hit: %+v", v)
	}
}

func TestBinaryBlocks(t *testing.T) {
	var csv strings.Builder
	// Sorted non-adjacent ranges spanning multiple index blocks.
	for i := 0; i < 700; i++ {
		fmt.Fprintf(&csv, "%d,%d,US,Test,Region,City%d,1.25,2.5\n", 134217728+i*4, 134217728+i*4+1, i)
	}
	r, _ := binaryCSV(t, csv.String(), 4)
	for _, i := range []int{0, 1, 254, 255, 256, 257, 511, 512, 699} {
		ip := netip.AddrFrom4([4]byte{8, 0, byte(i * 4 >> 8), byte(i * 4)})
		v := r.Lookup(ip.String())
		if !v.Found || v.City != fmt.Sprintf("City%d", i) {
			t.Fatalf("row %d => %+v", i, v)
		}
		endGap := netip.AddrFrom4([4]byte{8, 0, byte((i*4 + 2) >> 8), byte(i*4 + 2)})
		if v := r.Lookup(endGap.String()); v.Found {
			t.Fatalf("gap %d => %+v", i, v)
		}
	}
}

func TestBinaryInvalidAndNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.csv")
	dst := filepath.Join(dir, "db.nwgeo")
	for _, input := range []string{
		"100,90,US,X,Y,Z,0,0\n",
		"100,110,US,X,Y,Z,NaN,0\n",
		"100,110,US,X,Y,Z,0,181\n",
		"100,120,US,X,Y,Z,0,0\n119,130,US,X,Y,Z,0,0\n",
	} {
		if err := os.WriteFile(src, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if err := ConvertCSV(src, dst, 4); err == nil {
			t.Fatalf("accepted invalid CSV: %q", input)
		}
		if _, err := os.Stat(dst); !os.IsNotExist(err) {
			t.Fatalf("invalid conversion produced output: %v", err)
		}
	}
	if err := os.WriteFile(src, []byte("134744064,134744065,US,X,Y,Z,0,0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ConvertCSV(src, dst, 4); err != nil {
		t.Fatal(err)
	}
	if err := ConvertCSV(src, dst, 4); err == nil {
		t.Fatal("overwrote existing file")
	}
	if err := ConvertCSV(src, src, 4); err == nil {
		t.Fatal("overwrote its source")
	}
}

func TestBinaryCorrupt(t *testing.T) {
	_, dst := binaryCSV(t, "134744064,134744065,US,X,Y,Z,0,0\n", 4)
	f, err := os.OpenFile(dst, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], 123)
	if _, err := f.WriteAt(b[:], 24); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if r, err := OpenBinary(dst); err == nil {
		r.Close()
		t.Fatal("accepted corrupt header")
	}
}
