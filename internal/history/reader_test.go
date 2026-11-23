package history

import (
	"context"
	"netwatch/internal/event"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
