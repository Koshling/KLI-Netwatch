package reader

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFollowFromEndAndPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")

	if err := os.WriteFile(path, []byte("old record\n"), 0600); err != nil {
		t.Fatal(err)
	}

	r := New(path, 1024, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan string, 4)

	go func() {
		_ = r.Follow(ctx, true, func(line string) error {
			received <- line
			return nil
		})
	}()

	// Allow the reader to open the file and seek to its end.
	time.Sleep(50 * time.Millisecond)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.WriteString("new "); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)

	if _, err := f.WriteString("record\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	select {
	case line := <-received:
		if line != "new record" {
			t.Fatalf("got %q, want %q", line, "new record")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not deliver completed line")
	}

	select {
	case line := <-received:
		t.Fatalf("unexpected old or duplicate line: %q", line)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFollowMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.log")

	r := New(path, 1024, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := r.Follow(ctx, true, func(string) error {
		t.Error("unexpected line")
		return nil
	})

	if err != nil {
		t.Fatalf("Follow returned error: %v", err)
	}
}

func TestFollowRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	oldPath := filepath.Join(dir, "access.log.1")

	if err := os.WriteFile(path, []byte("initial\n"), 0600); err != nil {
		t.Fatal(err)
	}

	r := New(path, 1024, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan string, 8)

	go func() {
		_ = r.Follow(ctx, true, func(line string) error {
			received <- line
			return nil
		})
	}()

	time.Sleep(50 * time.Millisecond)

	if err := os.Rename(path, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after rotation\n"), 0600); err != nil {
		t.Fatal(err)
	}

	select {
	case line := <-received:
		if line != "after rotation" {
			t.Fatalf("got %q, want %q", line, "after rotation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not follow rotated log")
	}
}

func TestFollowFlushesPartialLineOnRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")

	if err := os.WriteFile(path, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}

	r := New(path, 1024, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan string, 4)

	go func() {
		_ = r.Follow(ctx, false, func(line string) error {
			received <- line
			return nil
		})
	}()

	time.Sleep(50 * time.Millisecond)

	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}

	select {
	case line := <-received:
		if line != "partial" {
			t.Fatalf("got %q, want %q", line, "partial")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("partial line was lost during rotation")
	}
}
