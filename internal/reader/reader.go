package reader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

type Reader struct {
	Path         string
	MaxLineSize  int
	PollInterval time.Duration
}

func New(path string, maxLineSize int, pollInterval time.Duration) *Reader {
	return &Reader{
		Path:         path,
		MaxLineSize:  maxLineSize,
		PollInterval: pollInterval,
	}
}

func (r *Reader) Follow(
	ctx context.Context,
	fromEnd bool,
	handle func(string) error,
) error {
	maxSize := r.MaxLineSize
	if maxSize <= 0 {
		maxSize = 65536
	}

	interval := r.PollInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var file *os.File
	var identity os.FileInfo
	var pending []byte
	var dropping bool
	// Remember a small suffix so truncation is detectable after rapid regrowth.
	const suffixSize = 64
	suffix := make([]byte, 0, suffixSize)
	initialized := false

	closeFile := func(flush bool) error {
		var err error

		if flush && !dropping && len(pending) > 0 {
			err = deliver(pending, handle)
		}

		if file != nil {
			_ = file.Close()
			file = nil
		}

		suffix = suffix[:0]
		identity = nil
		pending = nil
		dropping = false
		return err
	}

	defer func() {
		_ = closeFile(false)
	}()

	buffer := make([]byte, 32768)
	readAvailable := func(available io.Reader) error {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := available.Read(buffer)
			if n >= suffixSize {
				suffix = append(suffix[:0], buffer[n-suffixSize:n]...)
			} else if n > 0 {
				keep := min(len(suffix), suffixSize-n)
				copy(suffix, suffix[len(suffix)-keep:])
				suffix = append(suffix[:keep], buffer[:n]...)
			}

			for _, b := range buffer[:n] {
				if b == '\n' {
					if !dropping {
						if callbackErr := deliver(pending, handle); callbackErr != nil {
							return callbackErr
						}
					}
					pending = pending[:0]
					dropping = false
					continue
				}

				if dropping {
					continue
				}

				if len(pending) > maxSize || (len(pending) == maxSize && b != '\r') {
					pending = pending[:0]
					dropping = true
					continue
				}

				pending = append(pending, b)
			}

			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}

			if n == 0 {
				return nil
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		if file == nil {
			opened, err := os.Open(r.Path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					// A log created after this poll contains only new data.
					initialized = true
					continue
				}
				return err
			}

			info, err := opened.Stat()
			if err != nil {
				_ = opened.Close()
				return err
			}

			file = opened
			identity = info

			if !initialized && fromEnd {
				offset, err := file.Seek(0, io.SeekEnd)
				if err != nil {
					return err
				}
				if offset > 0 {
					length := min(offset, int64(suffixSize))
					suffix = suffix[:length]
					n, err := file.ReadAt(suffix, offset-length)
					if err != nil && err != io.EOF {
						return err
					}
					suffix = suffix[:n]
					dropping = n > 0 && suffix[n-1] != '\n'
				}
			}

			initialized = true
			continue
		}

		pathInfo, err := os.Stat(r.Path)
		if err == nil && !os.SameFile(identity, pathInfo) {
			if err := readAvailable(file); err != nil {
				return err
			}
			if err := closeFile(true); err != nil {
				return err
			}
			continue
		}

		var available io.Reader = file
		if err == nil {
			offset, seekErr := file.Seek(0, io.SeekCurrent)
			if seekErr != nil {
				return seekErr
			}

			rewritten := pathInfo.Size() < offset
			if !rewritten && len(suffix) > 0 {
				var probe [suffixSize]byte
				n, err := file.ReadAt(probe[:len(suffix)], offset-int64(len(suffix)))
				if err != nil && err != io.EOF {
					return err
				}
				rewritten = !bytes.Equal(suffix, probe[:n])
			}
			if rewritten {
				suffix = suffix[:0]
				if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
					return seekErr
				}
				pending = nil
				dropping = false
				offset = 0
			}
			if pathInfo.Size() == offset {
				continue
			}
			available = io.LimitReader(file, pathInfo.Size()-offset)
		}

		if err := readAvailable(available); err != nil {
			return err
		}
	}
}

func deliver(data []byte, handle func(string) error) error {
	if len(data) == 0 {
		return nil
	}
	line := strings.TrimSuffix(string(data), "\r")
	return handle(line)
}
