package dashboard

import (
	"os"
	"syscall"
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

	return int(ws.Cols), int(ws.Rows)
}
