package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDownloadDBIPInstallsBothFamilies(t *testing.T) {
	const countryCSV = "8.8.8.0,8.8.8.255,US\n2001:4860::,2001:4860::ffff,US\n"
	const cityCSV = "8.8.8.0,8.8.8.127,NA,US,California,Mountain View,37.42,-122.08\n" +
		"8.8.8.128,8.8.8.255,NA,US,California,Sunnyvale,37.37,-122.04\n" +
		"2001:4860::,2001:4860::ffff,NA,US,California,Los Angeles,34,-118\n"

	for _, detail := range []string{"country", "region", "city"} {
		t.Run(detail, func(t *testing.T) {
			edition, rows := "city", cityCSV
			if detail == "country" {
				edition, rows = "country", countryCSV
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/dbip-"+edition+"-lite-2025-12.csv.gz" {
					http.NotFound(w, r)
					return
				}
				gz := gzip.NewWriter(w)
				io.WriteString(gz, rows)
				gz.Close()
			}))
			defer server.Close()

			// The January archive is absent, so the downloader must cross the year boundary.
			now := time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC)
			var progress bytes.Buffer
			dir, err := downloadDBIP(context.Background(), t.TempDir(), detail, server.URL, now, nil, &progress)
			if err != nil {
				t.Fatal(err)
			}
			output := progress.String()
			downloaded := strings.Index(output, "[########] 100%")
			ipv4 := strings.Index(output, "Converting IPv4 to NWGEO")
			ipv6 := strings.Index(output, "Converting IPv6 to NWGEO")
			if downloaded < 0 || ipv4 <= downloaded || ipv6 <= ipv4 {
				t.Fatalf("unexpected progress: %q", output)
			}
			r, err := Open(filepath.Join(dir, "ipv4.nwgeo"), filepath.Join(dir, "ipv6.nwgeo"))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			v4 := r.Lookup("8.8.8.200")
			v6 := r.Lookup("2001:4860::1")
			for _, loc := range []Location{v4, v6} {
				if !loc.Found || loc.CountryCode != "US" || loc.Latitude == 0 {
					t.Fatalf("unexpected lookup: %+v", loc)
				}
			}
			switch detail {
			case "country":
				if v4.Region != "" || v4.City != "" {
					t.Fatalf("unexpected country detail: %+v", v4)
				}
			case "region":
				if v4.Region != "California" || v4.City != "" || v4.Latitude != v6.Latitude || r.binaryV4.count != 1 {
					t.Fatalf("unexpected region detail: IPv4 %+v, IPv6 %+v, ranges %d", v4, v6, r.binaryV4.count)
				}
			case "city":
				if v4.City != "Sunnyvale" || v6.City != "Los Angeles" {
					t.Fatalf("unexpected cities: IPv4 %+v, IPv6 %+v", v4, v6)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			if !slices.Equal(names, []string{"ATTRIBUTION.txt", "ipv4.nwgeo", "ipv6.nwgeo"}) {
				t.Fatalf("unexpected installed files: %v", names)
			}
		})
	}
}

func TestDownloadDBIPRemovesPartialInstallation(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gz := gzip.NewWriter(w)
		io.WriteString(gz, "8.8.8.0,8.8.8.255,US\n") // Missing IPv6.
		gz.Close()
	}))
	defer server.Close()
	if _, err := downloadDBIP(context.Background(), root, "country", server.URL, time.Now(), nil, nil); err == nil {
		t.Fatal("expected an incomplete archive to fail")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial installation was left behind: %v, %v", entries, err)
	}
}
