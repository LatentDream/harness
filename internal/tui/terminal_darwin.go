//go:build darwin

package tui

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

type terminalWindowSize struct {
	row uint16
	col uint16
	x   uint16
	y   uint16
}

func platformTerminalSupported() bool { return true }

func platformResizeSignal() os.Signal { return syscall.SIGWINCH }

func platformIsTerminal(fd uintptr) bool {
	_, err := platformTerminalState(fd)
	return err == nil
}

func platformTerminalState(fd uintptr) (any, error) {
	var state syscall.Termios
	if err := terminalIOCTL(fd, syscall.TIOCGETA, unsafe.Pointer(&state)); err != nil {
		return nil, err
	}
	return state, nil
}

func platformSetRaw(fd uintptr, original any) error {
	state, ok := original.(syscall.Termios)
	if !ok {
		return errorsInvalidTerminalState(original)
	}
	state.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	state.Oflag &^= syscall.OPOST
	state.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	state.Cflag &^= syscall.CSIZE | syscall.PARENB
	state.Cflag |= syscall.CS8
	state.Cc[syscall.VMIN] = 1
	state.Cc[syscall.VTIME] = 0
	return terminalIOCTL(fd, syscall.TIOCSETA, unsafe.Pointer(&state))
}

func platformRestore(fd uintptr, original any) error {
	state, ok := original.(syscall.Termios)
	if !ok {
		return errorsInvalidTerminalState(original)
	}
	return terminalIOCTL(fd, syscall.TIOCSETA, unsafe.Pointer(&state))
}

func platformTerminalSize(fd uintptr) (int, int, error) {
	var size terminalWindowSize
	if err := terminalIOCTL(fd, syscall.TIOCGWINSZ, unsafe.Pointer(&size)); err != nil {
		return 0, 0, fmt.Errorf("read terminal size: %w", err)
	}
	return int(size.col), int(size.row), nil
}

func platformWaitReadable(fd uintptr, timeout time.Duration) (bool, error) {
	var set syscall.FdSet
	index := int(fd / 32)
	if index >= len(set.Bits) {
		return false, fmt.Errorf("terminal file descriptor %d exceeds select capacity", fd)
	}
	set.Bits[index] |= 1 << (fd % 32)
	timeval := syscall.NsecToTimeval(timeout.Nanoseconds())
	err := syscall.Select(int(fd)+1, &set, nil, nil, &timeval)
	if err == syscall.EINTR {
		return false, nil
	}
	return set.Bits[index]&(1<<(fd%32)) != 0, err
}

func terminalIOCTL(fd uintptr, request uintptr, value unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(value))
	if errno != 0 {
		return errno
	}
	return nil
}

func errorsInvalidTerminalState(state any) error {
	return fmt.Errorf("invalid terminal state %T", state)
}
