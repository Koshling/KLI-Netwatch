package event

import (
	"testing"
	"time"
)

const nginxMainLine = `198.51.100.56 - - [16/Nov/2026:22:04:48 +0300] "GET / HTTP/1.1" 200 12904 "-" "Uptime-Kuma/2.5.9" "198.51.100.56"`

func TestParseMain(t *testing.T) {
	got, err := ParseAccess(nginxMainLine, "main")
	if err != nil {
		t.Fatal(err)
	}

	if got.ClientIP != "198.51.100.56" {
		t.Errorf("ClientIP = %q", got.ClientIP)
	}
	if got.Method != "GET" || got.URL != "/" || got.Protocol != "HTTP/1.1" {
		t.Errorf("unexpected request: %+v", got)
	}
	if got.Status != 200 || got.ResponseSize != 12904 {
		t.Errorf("unexpected response: %+v", got)
	}
	if got.UserAgent != "Uptime-Kuma/2.5.9" {
		t.Errorf("UserAgent = %q", got.UserAgent)
	}
	if got.XForwardedFor != "198.51.100.56" {
		t.Errorf("XForwardedFor = %q", got.XForwardedFor)
	}
	if got.Time.Location() != time.FixedZone("", 3*60*60) &&
		got.Time.Format("-0700") != "+0300" {
		t.Errorf("unexpected timezone: %v", got.Time)
	}
}

func TestParseCombined(t *testing.T) {
	line := `192.0.2.10 - user [16/Nov/2026:12:00:00 +0000] "POST /login HTTP/1.1" 302 128 "https://example.com/" "Mozilla/5.0"`

	got, err := ParseAccess(line, "combined")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "POST" || got.URL != "/login" || got.Status != 302 {
		t.Errorf("unexpected event: %+v", got)
	}
	if got.XForwardedFor != "" {
		t.Errorf("unexpected XForwardedFor: %q", got.XForwardedFor)
	}
}

func TestParseCommon(t *testing.T) {
	line := `192.0.2.10 - user [16/Nov/2026:12:00:00 +0000] "GET /health HTTP/1.1" 200 12`

	got, err := ParseAccess(line, "common")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "/health" || got.ResponseSize != 12 {
		t.Errorf("unexpected event: %+v", got)
	}
}

func TestParseError(t *testing.T) {
	line := `2026/11/16 09:13:14 [error] 1325210#1325210: *4740 access forbidden by rule, client: 203.0.113.124, server: _, request: "GET /.dev.vars HTTP/1.1"`

	got, err := ParseError(line)
	if err != nil {
		t.Fatal(err)
	}
	if got.Severity != "error" || got.PID != 1325210 || got.Connection != "4740" {
		t.Errorf("unexpected event: %+v", got)
	}
	if got.Message == "" {
		t.Error("empty error message")
	}
}

func TestMalformedLines(t *testing.T) {
	if _, err := ParseAccess("garbage", "main"); err == nil {
		t.Error("expected access parse error")
	}
	if _, err := ParseError("garbage"); err == nil {
		t.Error("expected error parse error")
	}
}
