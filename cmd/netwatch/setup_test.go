package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"netwatch/internal/config"
)

func TestFirstRunRemembersCustomPathAndGeoIPChoice(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(root, "system"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))

	access := filepath.Join(root, "access.log")
	const common = "8.8.8.8 - - [22/Nov/2026:10:01:00 +0300] \"GET / HTTP/1.1\" 200 42\n"
	if err := os.WriteFile(access, []byte(common), 0600); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(root, "manual", "netwatch.json")
	var out bytes.Buffer
	input := strings.Join([]string{custom, access, "", "", ""}, "\n")
	cfg, err := runSetup("", true, false, strings.NewReader(input), &out)
	if err != nil || cfg.AccessFormat != "common" || !cfg.GeoIPPrompt ||
		cfg.ErrorLog != filepath.Join(root, "error.log") {
		t.Fatalf("setup failed: %+v, %v\n%s", cfg, err, &out)
	}
	if target, err := os.Readlink(filepath.Join(root, "config", "netwatch", "netwatch.json")); err != nil || target != custom {
		t.Fatalf("custom path was not remembered: %q, %v", target, err)
	}

	if _, err := runSetup("", false, true, strings.NewReader("never\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load(custom)
	if err != nil || stored.GeoIPPrompt || stored.AccessLog != access {
		t.Fatalf("unexpected saved configuration: %+v, %v", stored, err)
	}
	if _, err := runSetup("", false, true, strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("never should suppress the prompt: %v", err)
	}
}

func TestSetupUsesDetectedGeoIP(t *testing.T) {
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
	input := strings.NewReader(access + "\n\n\n")
	var out bytes.Buffer
	cfg, err := runSetup(path, true, false, input, &out)
	if err != nil || cfg.GeoIPIPv4DB != db || cfg.GeoIPPrompt || !strings.Contains(out.String(), "[auto]") {
		t.Fatalf("database was not selected: %+v, %v\n%s", cfg, err, &out)
	}
}
