package dashboard

import (
	"bufio"
	"errors"
	"io"
	"os"
	"syscall"
	"time"
	"unsafe"
)

type windowSize struct {
	Rows uint16
	Cols uint16
	X    uint16
	Y    uint16
}

func terminalSize() (int, int) {
	var ws windowSize

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		os.Stdout.Fd(),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)

	if errno != 0 || ws.Cols == 0 || ws.Rows == 0 {
		return 120, 30
	}

	return min(500, int(ws.Cols)), min(200, int(ws.Rows))
}

const (
	keyUp rune = -1 - iota
	keyDown
	keyLeft
	keyRight
	keyEscape
)

func keyboardInput() (<-chan rune, func()) {
	var state syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&state)))
	if errno != 0 {
		return nil, func() {}
	}

	// Reopen the terminal with a separate file description. Go can then
	// interrupt reads on close and set a deadline without changing stdin's flags.
	input, err := os.OpenFile("/proc/self/fd/0", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, func() {}
	}
	mode := state
	mode.Lflag &^= syscall.ECHO | syscall.ICANON
	mode.Cc[syscall.VMIN], mode.Cc[syscall.VTIME] = 1, 0
	if err := setTerminalMode(os.Stdin, &mode); err != nil {
		input.Close()
		return nil, func() {}
	}

	keys := make(chan rune)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer close(keys)
		reader := bufio.NewReader(input)
		for {
			key, err := readKey(reader, input)
			if err != nil {
				return
			}
			if key != 0 {
				select {
				case keys <- key:
				case <-done:
					return
				}
			}
		}
	}()
	return keys, func() {
		close(done)
		input.Close()
		<-finished
		_ = setTerminalMode(os.Stdin, &state)
	}
}

func setTerminalMode(file *os.File, mode *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(mode)))
	if errno != 0 {
		return errno
	}
	return nil
}

func readKey(reader *bufio.Reader, input *os.File) (rune, error) {
	key, _, err := reader.ReadRune()
	if err != nil || key != 27 {
		return key, err
	}

	// A short deadline distinguishes a standalone Esc from an arrow sequence.
	_ = input.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	defer input.SetReadDeadline(time.Time{})
	prefix, _, err := reader.ReadRune()
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, io.EOF) {
		return keyEscape, nil
	}
	if err != nil {
		return 0, err
	}
	if prefix != '[' && prefix != 'O' {
		_ = reader.UnreadRune()
		return keyEscape, nil
	}
	for {
		key, _, err := reader.ReadRune()
		if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, io.EOF) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		switch key {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		case 'C':
			return keyRight, nil
		case 'D':
			return keyLeft, nil
		case 'M':
			if prefix == 'O' { // Application keypad Enter.
				return '\r', nil
			}
		}
		if key < 0x20 || key >= 0x40 {
			return 0, nil
		}
	}
}
