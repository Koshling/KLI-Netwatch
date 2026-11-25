package regularfile

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Open opens an ordinary input file, including symlinks to ordinary files.
func Open(path string) (*os.File, os.FileInfo, error) {
	// Linux O_PATH obtains a reference without opening the object for I/O.
	// syscall does not expose the constant on every Linux architecture.
	const oPath = 0x200000
	ref, err := os.OpenFile(path, oPath, 0)
	if err != nil {
		return nil, nil, err
	}
	defer ref.Close()
	info, err := ref.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("expected a regular file: %q", path)
	}

	const flags = os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOCTTY
	file, err := os.OpenFile(fmt.Sprintf("/proc/self/fd/%d", ref.Fd()), flags, 0)
	if errors.Is(err, os.ErrNotExist) {
		// Procfs may be absent in a chroot. Keep the nonblocking fallback
		// and verify that the I/O descriptor still refers to the checked file.
		file, err = os.OpenFile(path, flags, 0)
	}
	if err != nil {
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: unwrapPathError(err)}
	}
	current, err := file.Stat()
	if err == nil && (!current.Mode().IsRegular() || !os.SameFile(info, current)) {
		err = fmt.Errorf("input changed while opening: %q", path)
	}
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, current, nil
}

func unwrapPathError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}
