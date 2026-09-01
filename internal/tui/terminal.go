package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const (
	// Request disambiguated key events, encode all keys, and include their text.
	// This makes modified text keys such as Shift+Enter distinguishable.
	enterTerminal = "\x1b[?1049h\x1b[>25u\x1b[?25l\x1b[?2004h\x1b[?1004h\x1b[?1000h\x1b[?1006h"
	leaveTerminal = "\x1b[?1006l\x1b[?1000l\x1b[?1004l\x1b[?2004l\x1b[<u\x1b[?25h\x1b[?1049l"
)

var (
	errTerminalUnsupported    = errors.New("native terminal is unsupported on this platform")
	errTerminalNotInteractive = errors.New("input and output must be interactive terminals")
)

type terminalOperations interface {
	supported() bool
	isTerminal(*os.File) bool
	state(*os.File) (any, error)
	setRaw(*os.File, any) error
	restore(*os.File, any) error
	size(*os.File) (int, int, error)
	write(*os.File, string) error
}

type nativeTerminal struct {
	mu sync.Mutex

	in  *os.File
	out *os.File
	ops terminalOperations

	original any
	raw      bool
	screen   bool
}

func newNativeTerminal(in, out *os.File) *nativeTerminal {
	return &nativeTerminal{in: in, out: out, ops: nativeTerminalOSOperations{}}
}

func (t *nativeTerminal) IsInteractive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.isInteractive()
}

func (t *nativeTerminal) isInteractive() bool {
	return t.ops.supported() && t.ops.isTerminal(t.in) && t.ops.isTerminal(t.out)
}

func (t *nativeTerminal) Enter() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activate()
}

func (t *nativeTerminal) Resume() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activate()
}

func (t *nativeTerminal) activate() error {
	if t.raw && t.screen {
		return nil
	}
	if !t.ops.supported() {
		return errTerminalUnsupported
	}
	if !t.isInteractive() {
		return errTerminalNotInteractive
	}

	if t.original == nil {
		state, err := t.ops.state(t.in)
		if err != nil {
			return fmt.Errorf("read terminal mode: %w", err)
		}
		t.original = state
	}
	if !t.raw {
		if err := t.ops.setRaw(t.in, t.original); err != nil {
			t.original = nil
			return fmt.Errorf("enable terminal raw mode: %w", err)
		}
		t.raw = true
	}
	if !t.screen {
		// Mark the screen active first because a failed write may still be partial.
		t.screen = true
		if err := t.ops.write(t.out, enterTerminal); err != nil {
			activateErr := fmt.Errorf("enter terminal screen: %w", err)
			return errors.Join(activateErr, t.deactivate(true))
		}
	}
	return nil
}

func (t *nativeTerminal) Suspend() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.deactivate(false)
}

func (t *nativeTerminal) Restore() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.deactivate(true)
}

func (t *nativeTerminal) deactivate(discardState bool) error {
	var errs []error
	if t.screen {
		if err := t.ops.write(t.out, leaveTerminal); err != nil {
			errs = append(errs, fmt.Errorf("leave terminal screen: %w", err))
		} else {
			t.screen = false
		}
	}
	if t.raw {
		if err := t.ops.restore(t.in, t.original); err != nil {
			errs = append(errs, fmt.Errorf("restore terminal mode: %w", err))
		} else {
			t.raw = false
		}
	}
	if len(errs) == 0 && discardState {
		t.original = nil
	}
	return errors.Join(errs...)
}

func (t *nativeTerminal) Size() (width, height int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.ops.supported() {
		return 0, 0, errTerminalUnsupported
	}
	if !t.isInteractive() {
		return 0, 0, errTerminalNotInteractive
	}
	return t.ops.size(t.out)
}

type nativeTerminalOSOperations struct{}

func (nativeTerminalOSOperations) supported() bool { return platformTerminalSupported() }
func (nativeTerminalOSOperations) isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	return platformIsTerminal(file.Fd())
}
func (nativeTerminalOSOperations) state(file *os.File) (any, error) {
	return platformTerminalState(file.Fd())
}
func (nativeTerminalOSOperations) setRaw(file *os.File, state any) error {
	return platformSetRaw(file.Fd(), state)
}
func (nativeTerminalOSOperations) restore(file *os.File, state any) error {
	return platformRestore(file.Fd(), state)
}
func (nativeTerminalOSOperations) size(file *os.File) (int, int, error) {
	return platformTerminalSize(file.Fd())
}
func (nativeTerminalOSOperations) write(file *os.File, value string) error {
	_, err := io.WriteString(file, value)
	return err
}
