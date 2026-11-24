package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"netwatch/internal/config"
)

func TestFirstRunUsesDefaultConfigPathAndRemembersGeoIPChoice(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(root, "system"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))

	access := filepath.Join(root, "access.log")
	const common = "8.8.8.8 - - [22/Nov/2026:10:01:00 +0300] \"GET / HTTP/1.1\" 200 42\n"
	if err := os.WriteFile(access, []byte(common), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config", "netwatch", "netwatch.json")
	var out bytes.Buffer
	input := access + "\nn\n"
	cfg, err := runSetup("", true, false, strings.NewReader(input), &out)
	if err != nil || cfg.AccessFormat != "common" || !cfg.GeoIPPrompt ||
		cfg.ErrorLog != config.Default().ErrorLog {
		t.Fatalf("setup failed: %+v, %v\n%s", cfg, err, &out)
	}
	if !strings.Contains(out.String(), "Saved configuration: "+path) ||
		strings.Contains(out.String(), "Config path") || strings.Contains(out.String(), "Error log path") ||
		strings.Contains(out.String(), "Access log format") || strings.Contains(out.String(), "CC BY") {
		t.Fatalf("unexpected setup prompts:\n%s", &out)
	}

	if _, err := runSetup("", false, true, strings.NewReader("never\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load(path)
	if err != nil || stored.GeoIPPrompt || stored.AccessLog != access {
		t.Fatalf("unexpected saved configuration: %+v, %v", stored, err)
	}
	if _, err := runSetup("", false, true, strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("never should suppress the prompt: %v", err)
	}
}

func TestSetupUsesExplicitConfigPathAndDetectedGeoIP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "xdg"))
	path := filepath.Join(dir, "netwatch.json")
	access := filepath.Join(dir, "access.log")
	db := filepath.Join(dir, "ipv4.csv")
	for path, contents := range map[string]string{
		access: "",
		db:     "134744064,134744319,US,,,Mountain View,37,-122\n",
	} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	input := strings.NewReader(access + "\njson\n\n")
	var out bytes.Buffer
	cfg, err := runSetup(path, true, false, input, &out)
	if err != nil || cfg.GeoIPIPv4DB != db || cfg.GeoIPPrompt || cfg.AccessFormat != "json" ||
		!strings.Contains(out.String(), "[auto]") || !strings.Contains(out.String(), "could not detect") ||
		strings.Contains(out.String(), "CC BY") {
		t.Fatalf("database was not selected: %+v, %v\n%s", cfg, err, &out)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatal(err)
	}
}

func TestGeoIPDownloadDefaultShowsLicenseBeforeDownload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	cfg := config.Default()
	var out bytes.Buffer
	// Enter selects download. EOF at the detail menu prevents any network call.
	err := (setupPrompts{lines: bufio.NewScanner(strings.NewReader("\n")), out: &out}).configureGeoIP(filepath.Join(dir, "netwatch.json"), &cfg)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected setup to wait for detail selection, got %v", err)
	}
	prompt := out.String()
	choice := strings.Index(prompt, "Download DB-IP Lite? (y/n/never/path) [Y]")
	license := strings.Index(prompt, "CC BY")
	detail := strings.Index(prompt, "GeoIP detail [1]")
	if choice < 0 || license <= choice || detail <= license {
		t.Fatalf("unexpected GeoIP prompts:\n%s", prompt)
	}
}
