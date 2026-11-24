package dashboard

import (
	"errors"
	"fmt"
)

// ErrMenu indicates that the current mode has returned to the mode picker.
var ErrMenu = errors.New("open mode menu")

// Session owns the keyboard and selected theme across mode changes.
type Session struct {
	keys       <-chan rune
	restore    func()
	themeIndex int
}

func (s *Session) open() {
	if s.restore != nil {
		return
	}
	s.keys, s.restore = keyboardInput()
	fmt.Print("\033[?25l")
}

func (s *Session) Close() {
	if s.restore == nil {
		return
	}
	s.restore()
	s.restore = nil
	fmt.Print("\033[?25h\033[0m\n")
}

func (s *Session) selectTheme(key rune) int {
	s.themeIndex = int(key - '1')
	return s.themeIndex
}
