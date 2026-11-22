package geoip

import (
	"fmt"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func makeDB(t *testing.T, name, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIPv4Lookup(t *testing.T) {
	path := makeDB(t, "ipv4.csv",
		"134744064,134744319,US,United States,California,Los Angeles,34.05,-118.24,,\n")

	r, err := Open(path, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, ip := range []string{"8.8.8.0", "8.8.8.1", "8.8.8.255"} {
		loc := r.Lookup(ip)
		if !loc.Found || loc.CountryCode != "US" {
			t.Errorf("Lookup(%q) = %+v, want US", ip, loc)
		}
	}

	if loc := r.Lookup("8.8.9.1"); loc.Found {
		t.Errorf("out-of-range IP matched: %+v", loc)
	}
}

func TestPrivateAndInvalidIPs(t *testing.T) {
	path := makeDB(t, "ipv4.csv",
		"0,4294967295,ZZ,Test,Test,Test,0,0,,\n")

	r, err := Open(path, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, ip := range []string{
		"127.0.0.1", "192.168.1.10", "10.0.0.1",
		"169.254.1.1", "::1", "not-an-ip", "",
	} {
		if loc := r.Lookup(ip); loc.Found {
			t.Errorf("Lookup(%q) = %+v, want UNKNOWN", ip, loc)
		}
	}
}

func TestIPv6Lookup(t *testing.T) {
	a := netip.MustParseAddr("2001:4860::8888").As16()
	b := netip.MustParseAddr("2001:4860::8889").As16()

	start := new(big.Int).SetBytes(a[:])
	end := new(big.Int).SetBytes(b[:])

	path := makeDB(t, "ipv6.csv", fmt.Sprintf(
		"%s,%s,US,United States,California,Mountain View,37.4,-122.1,,\n",
		start.String(), end.String(),
	))

	r, err := Open("", path)
	if err != nil {
		t.Fatal(err)
	}

	loc := r.Lookup("2001:4860::8888")
	if !loc.Found || loc.City != "Mountain View" {
		t.Fatalf("unexpected IPv6 result: %+v", loc)
	}
}

func TestIPv4MappedIPv6(t *testing.T) {
	path := makeDB(t, "ipv4.csv",
		"134744064,134744319,US,United States,California,Los Angeles,34.05,-118.24,,\n")

	r, err := Open(path, "")
	if err != nil {
		t.Fatal(err)
	}

	loc := r.Lookup("::ffff:8.8.8.8")
	if !loc.Found || loc.CountryCode != "US" {
		t.Fatalf("mapped IPv4 lookup failed: %+v", loc)
	}
}
