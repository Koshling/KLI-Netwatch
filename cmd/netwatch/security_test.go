package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"netwatch/internal/config"
	"netwatch/internal/event"
)

func TestLiveCountsSkippedLines(t *testing.T) {
	cfg := config.Default()
	cfg.AccessLog = filepath.Join(t.TempDir(), "access.log")
	cfg.MaxLineSize = 1024
	cfg.PollInterval = time.Millisecond
	parse, err := cfg.AccessParser()
	if err != nil {
		t.Fatal(err)
	}
	keep, err := cfg.TrafficFilter()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	events := make(chan event.AccessEvent, 1)
	done := make(chan error, 1)
	var skipped atomic.Uint64
	line := "192.0.2.1 - - [25/Nov/2026:12:00:00 +0000] \"GET / HTTP/1.1\" 200 12 \"-\" \"browser\"\n"
	if err := os.WriteFile(cfg.AccessLog, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(cfg.AccessLog, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	go func() { done <- followAccess(ctx, cfg, parse, keep, events, &skipped) }()
	// Retry until the follower has positioned itself and observed a marker.
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("timed out receiving initial live event")
		case <-tick.C:
			if _, err := f.WriteString(line); err != nil {
				t.Fatal(err)
			}
			continue
		case <-events:
		}
		break
	}
	if _, err := f.WriteString("broken\n" + strings.Repeat("x", 1025) + "\n" + line); err != nil {
		t.Fatal(err)
	}
	for skipped.Load() < 2 {
		select {
		case <-events:
		case <-tick.C:
		case <-ctx.Done():
			t.Fatalf("skipped %d lines, want 2", skipped.Load())
		}
	}
	cancel()
	<-done
	if got := skipped.Load(); got != 2 {
		t.Fatalf("skipped %d lines, want 2", got)
	}
}

func TestDiagnosticTextReplacesControlCharacters(t *testing.T) {
	if got := cleanDiagnostic("a\x1b\n\u202eb"); got != "a???b" {
		t.Fatalf("unexpected diagnostic: %q", got)
	}
}
