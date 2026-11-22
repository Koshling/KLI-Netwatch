package geoip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenBinaryIntegrationAndMixedFormats(t *testing.T) {
	dir := t.TempDir()
	v4csv := filepath.Join(dir, "v4.csv")
	v6csv := filepath.Join(dir, "v6.csv")
	v4bin := filepath.Join(dir, "v4.nwgeo")
	v6bin := filepath.Join(dir, "v6.nwgeo")
	// 8.8.8.0/24 and IPv6 2001:4860::8888.
	for path, data := range map[string]string{
		v4csv: "134744064,134744319,US,United States,California,Mountain View,37.4,-122.1\n",
		v6csv: "42541956101370907050197289607612106888,42541956101370907050197289607612106888,US,United States,California,Mountain View,37.4,-122.1\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := ConvertCSV(v4csv, v4bin, 4); err != nil {
		t.Fatal(err)
	}
	if err := ConvertCSV(v6csv, v6bin, 6); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, v4, v6   string
		v4disk, v6disk bool
	}{
		{"both binary", v4bin, v6bin, true, true},
		{"v4 binary, v6 csv", v4bin, v6csv, true, false},
		{"v4 csv, v6 binary", v4csv, v6bin, false, true},
		{"both csv", v4csv, v6csv, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Open(tc.v4, tc.v6)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if (r.binaryV4 != nil) != tc.v4disk || (r.binaryV6 != nil) != tc.v6disk {
				t.Fatal("wrong resolver mode")
			}
			for _, ip := range []string{"8.8.8.8", "::ffff:8.8.8.8", "2001:4860::8888"} {
				a := r.Lookup(ip)
				b := r.Lookup(ip)
				if !a.Found || !b.Found || a.City != "Mountain View" {
					t.Fatalf("%s => %+v / %+v", ip, a, b)
				}
			}
			if r.Lookup("8.8.9.8").Found || r.Lookup("127.0.0.1").Found {
				t.Fatal("false positive")
			}
		})
	}
}

func TestOpenBinaryRejectFamilyAndCorruption(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "data.csv")
	dst := filepath.Join(dir, "data.nwgeo")
	if err := os.WriteFile(csv, []byte("134744064,134744319,US,United States,CA,City,37,-122\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ConvertCSV(csv, dst, 4); err != nil {
		t.Fatal(err)
	}
	if r, err := Open("", dst); err == nil {
		r.Close()
		t.Fatal("accepted v4 database for v6")
	}
	if r, err := Open(dst, "/file/that/does/not/exist"); err == nil {
		r.Close()
		t.Fatal("expected failure")
	}
	bad := filepath.Join(dir, "bad.nwgeo")
	if err := os.WriteFile(bad, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := Open(bad, ""); err == nil {
		r.Close()
		t.Fatal("accepted corrupt NWGEO")
	}
	if r, err := Open("", ""); err == nil {
		r.Close()
		t.Fatal("accepted empty paths")
	}
}

func TestOpenBinaryWithoutExtension(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "db.csv")
	bin := filepath.Join(dir, "db.bin")
	if err := os.WriteFile(csv, []byte("134744064,134744319,US,United States,CA,City,37,-122\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ConvertCSV(csv, bin, 4); err != nil {
		t.Fatal(err)
	}
	r, err := Open(bin, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.binaryV4 == nil || !strings.EqualFold(r.Lookup("8.8.8.8").CountryCode, "US") {
		t.Fatal("binary signature detection failed")
	}
}
