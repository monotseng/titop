//go:build linux

package terminal

import (
	"os"
	"syscall"
	"unsafe"
)

type State struct {
	fd  int
	old syscall.Termios
}

type windowSize struct{ Rows, Cols, Xpixel, Ypixel uint16 }

func Width(f *os.File) int {
	var size windowSize
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
		return 0
	}
	return int(size.Cols)
}

func MakeRaw(f *os.File) (*State, error) {
	fd := int(f.Fd())
	var old syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&old))); errno != 0 {
		return nil, errno
	}
	raw := old
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	raw.Cc[syscall.VMIN], raw.Cc[syscall.VTIME] = 1, 0
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil, errno
	}
	return &State{fd: fd, old: old}, nil
}

func (s *State) Restore() {
	if s != nil {
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, uintptr(s.fd), syscall.TCSETS, uintptr(unsafe.Pointer(&s.old)))
	}
}
