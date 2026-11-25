package history

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"netwatch/internal/event"
)

func TestSnapshotInterval(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := func(tm time.Time) string {
		return `{"remote_addr":"192.0.2.1","status":200,"time":"` + tm.Format(time.RFC3339) + `"}` + "\n"
	}
	data := strings.Repeat("x", 8192) + "\n" + row(start) + row(start.Add(time.Minute)) + "partial"
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	count := 0
	skipped, err := Scan(context.Background(), p, "json", 128, start, start.Add(time.Minute), func(e event.AccessEvent) error { count++; return nil })
	if err != nil || count != 1 || skipped != 1 {
		t.Fatal(count, skipped, err)
	}
}

func TestLineLimitExcludesTerminator(t *testing.T) {
	start := time.Unix(100, 0)
	const base = `{"remote_addr":"8.8.8.8","time":"1970-01-01T00:01:40Z","status":200}`
	for _, limit := range []int{1024, 4095, 4096, 8191} {
		line := base + strings.Repeat(" ", limit-len(base))
		p := filepath.Join(t.TempDir(), "log")
		if err := os.WriteFile(p, []byte(line+"\r\n"+line+" \r\n"+line+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		n := 0
		skipped, err := Scan(context.Background(), p, "json", limit, start, start.Add(time.Minute), func(event.AccessEvent) error { n++; return nil })
		if err != nil || n != 2 || skipped != 1 {
			t.Errorf("limit %d: got %d, skipped %d, err %v", limit, n, skipped, err)
		}
	}
}

func TestScanChronology(t *testing.T) {
	start := time.Unix(100, 0)
	for _, tc := range []struct {
		name       string
		timestamps []int64
		wantError  bool
	}{
		{"ordered", []int64{100, 100, 101}, false},
		{"out of order", []int64{100, 102, 101}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "access.log")
			var body strings.Builder
			for _, stamp := range tc.timestamps {
				when := time.Unix(stamp, 0).UTC().Format("02/Jan/2006:15:04:05 -0700")
				fmt.Fprintf(&body, "192.0.2.1 - - [%s] \"GET / HTTP/1.1\" 200 12 \"-\" \"browser\"\n", when)
			}
			if err := os.WriteFile(path, []byte(body.String()), 0600); err != nil {
				t.Fatal(err)
			}
			received := 0
			_, err := Scan(t.Context(), path, "combined", 1024, start, start.Add(time.Minute), InOrder(
				func(event.AccessEvent) error { received++; return nil }))
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "timestamps out of order") || received != 2 {
					t.Fatalf("received %d, error %v", received, err)
				}
			} else if err != nil || received != len(tc.timestamps) {
				t.Fatalf("received %d, error %v", received, err)
			}
		})
	}
}
