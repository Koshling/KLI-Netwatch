package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeoIPCLIConvertWorksWithoutMonitorConfig(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "ipv4.csv")
	output := filepath.Join(dir, "ipv4.nwgeo")
	csv := "start,end,cc,country,region,city,lat,lon\n" +
		"0,16777215,US,United States,Test Region,Test City,40.0,-73.0\n"
	if err := os.WriteFile(input, []byte(csv), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	status := runGeoIPCLI([]string{"convert", "-input", input, "-output", output, "-family", "4"}, &stdout, &stderr)
	if status != 0 {
		t.Fatalf("exit=%d, stdout=%q, stderr=%q", status, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Created NWGEO database:") {
		t.Fatalf("unexpected success output: %q", stdout.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 64 || string(data[:8]) != "NWGEO001" {
		t.Fatalf("invalid NWGEO header: %q", data[:min(len(data), 8)])
	}

	stdout.Reset()
	stderr.Reset()
	if status := runGeoIPCLI([]string{"convert", "-input", input, "-output", output}, &stdout, &stderr); status != 1 {
		t.Fatalf("existing destination must fail safely; exit=%d, stderr=%q", status, stderr.String())
	}
}

func TestGeoIPCLIInvalidArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"missing command", nil, 2},
		{"unknown command", []string{"wrong"}, 2},
		{"missing flags", []string{"convert"}, 2},
		{"invalid family", []string{"convert", "-input", "a", "-output", "b", "-family", "5"}, 2},
		{"extra positional", []string{"convert", "-input", "a", "-output", "b", "extra"}, 2},
		{"unknown flag", []string{"convert", "-bogus"}, 2},
		{"help", []string{"--help"}, 0},
		{"convert help", []string{"convert", "--help"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			status := runGeoIPCLI(tc.args, &stdout, &stderr)
			if status != tc.code {
				t.Fatalf("exit=%d (want %d), stdout=%q, stderr=%q", status, tc.code, stdout.String(), stderr.String())
			}
		})
	}
}
