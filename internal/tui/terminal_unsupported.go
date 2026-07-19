//go:build !linux && !darwin

package tui

import (
	"os"
	"time"
)

func platformTerminalSupported() bool { return false }
func platformResizeSignal() os.Signal { return nil }
func platformIsTerminal(uintptr) bool { return false }

func platformTerminalState(uintptr) (any, error) {
	return nil, errTerminalUnsupported
}

func platformSetRaw(uintptr, any) error {
	return errTerminalUnsupported
}

func platformRestore(uintptr, any) error {
	return errTerminalUnsupported
}

func platformTerminalSize(uintptr) (int, int, error) {
	return 0, 0, errTerminalUnsupported
}
func platformWaitReadable(uintptr, time.Duration) (bool, error) {
	return false, errTerminalUnsupported
}
