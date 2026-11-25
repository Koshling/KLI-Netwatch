package regularfile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenRegularInputs(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "log")
	link := filepath.Join(dir, "link")
	pipe := filepath.Join(dir, "pipe")
	if err := os.WriteFile(real, []byte("log"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	file, info, err := Open(link)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if info.Size() != 3 {
		t.Fatalf("unexpected file size: %d", info.Size())
	}
	for _, path := range []string{dir, pipe, "/dev/null"} {
		done := make(chan error, 1)
		go func() {
			f, _, err := Open(path)
			if f != nil {
				f.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("accepted %s", path)
			}
		case <-time.After(time.Second):
			t.Fatalf("opening %s blocked", path)
		}
	}
}
