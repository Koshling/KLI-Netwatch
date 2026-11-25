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
	choice := strings.Index(prompt, "Download DB-IP Lite for GeoIP? (y/n/never/path) [Y]")
	license := strings.Index(prompt, "CC BY")
	detail := strings.Index(prompt, "GeoIP detail [1]")
	if choice < 0 || license <= choice || detail <= license {
		t.Fatalf("unexpected GeoIP prompts:\n%s", prompt)
	}
}

func TestSetupPersistsNginxTemplate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	access := filepath.Join(dir, "site.log")
	path := filepath.Join(dir, "netwatch.json")
	const line = "GET|192.0.2.1|2026-11-25T12:00:00+00:00|201|/check?q=1"
	const directive = "log_format site escape=none '$request_method|$remote_addr|$time_iso8601|$status|$request_uri';"
	if err := os.WriteFile(access, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader(access + "\nnginx\n" + directive + "\nnever\n")
	if _, err := runSetup(path, true, false, input, io.Discard); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessFormat != "nginx" || cfg.NginxLogFormat != directive {
		t.Fatalf("unexpected setup config: %+v", cfg)
	}
	parse, err := cfg.AccessParser()
	if err != nil {
		t.Fatal(err)
	}
	e, err := parse(line)
	if err != nil || e.Status != 201 || e.ClientIP != "192.0.2.1" || e.URL != "/check?q=1" {
		t.Fatalf("unexpected access event: %+v, %v", e, err)
	}
}

func TestChooseDiscoveredAccessLog(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"apache2/access.log", "caddy/access.log", "nginx/access.log", "nginx/shop-access.log"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	candidates := discoverAccessLogs(root)
	if len(candidates) != 4 {
		t.Fatal(candidates)
	}
	for _, tc := range []struct {
		candidates          []accessLogCandidate
		input, want, prompt string
	}{
		{candidates, "\n\n", "nginx/access.log", "(nginx/apache/caddy/path) [nginx]"},
		{candidates, "caddy\n", "caddy/access.log", "(nginx/apache/caddy/path) [nginx]"},
		{candidates, "nginx\n" + filepath.Join(root, "nginx/shop-access.log") + "\n", "nginx/shop-access.log", "shop-access.log"},
		{candidates, "path\n" + filepath.Join(root, "other.log") + "\n", "other.log", "Access log path ["},
		{candidates[2:], "\n", "apache2/access.log", "(apache/caddy/path) [apache]"},
	} {
		var out bytes.Buffer
		p := setupPrompts{bufio.NewScanner(strings.NewReader(tc.input)), &out}
		got, err := p.chooseAccessLog(tc.candidates, "")
		if err != nil || got != filepath.Join(root, tc.want) || !strings.Contains(out.String(), tc.prompt) {
			t.Errorf("got %q, %v; prompts %q", got, err, out.String())
		}
	}
}
