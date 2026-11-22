package history

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"netwatch/internal/event"
)

// Scan reads complete lines within the size observed at entry.
func Scan(ctx context.Context, path, format string, limit int, from, until time.Time, emit func(event.AccessEvent) error) (skipped int, err error) {
	if limit < 1 || !from.Before(until) {
		return 0, fmt.Errorf("invalid line limit or interval")
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("expected regular log file")
	}
	return ScanSnapshot(ctx, f, info.Size(), format, limit, from, until, emit)
}

// ScanSnapshot uses a private cursor over the same open file and byte boundary on each pass.
func ScanSnapshot(ctx context.Context, f *os.File, size int64, format string, limit int, from, until time.Time, emit func(event.AccessEvent) error) (skipped int, err error) {
	if limit < 1 || !from.Before(until) {
		return 0, fmt.Errorf("invalid line limit or interval")
	}
	r := bufio.NewReader(io.NewSectionReader(f, 0, size))
	var line []byte
	var consumed int64
	oversized := false
	for {
		if err := ctx.Err(); err != nil {
			return skipped, err
		}
		part, err := r.ReadSlice('\n')
		consumed += int64(len(part))
		if err == io.EOF {
			if consumed != size {
				return skipped, io.ErrUnexpectedEOF
			}
			return skipped, nil
		}
		if err != nil && err != bufio.ErrBufferFull {
			return skipped, err
		}
		complete := err == nil
		if complete {
			part = part[:len(part)-1]
		}
		if len(part) > limit-len(line) {
			oversized = true
		}
		if !oversized {
			line = append(line, part...)
		}
		if !complete {
			continue
		}
		if oversized {
			skipped++
		} else {
			e, parseErr := event.ParseAccess(strings.TrimSuffix(string(line), "\r"), format)
			if parseErr != nil || e.Time.IsZero() {
				skipped++
			} else if !e.Time.Before(from) && e.Time.Before(until) {
				if err := emit(e); err != nil {
					return skipped, err
				}
			}
		}
		line = line[:0]
		oversized = false
	}
}
