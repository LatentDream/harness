package tui

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

type fakeTerminalOperations struct {
	interactive bool
	stateErr    error
	rawErr      error
	restoreErr  error
	writeErrAt  int

	stateCalls   int
	rawCalls     int
	restoreCalls int
	writes       []string
}

func (*fakeTerminalOperations) supported() bool { return true }
func (f *fakeTerminalOperations) isTerminal(*os.File) bool {
	return f.interactive
}
func (f *fakeTerminalOperations) state(*os.File) (any, error) {
	f.stateCalls++
	return "original", f.stateErr
}
func (f *fakeTerminalOperations) setRaw(*os.File, any) error {
	f.rawCalls++
	return f.rawErr
}
func (f *fakeTerminalOperations) restore(*os.File, any) error {
	f.restoreCalls++
	return f.restoreErr
}
func (*fakeTerminalOperations) size(*os.File) (int, int, error) { return 80, 24, nil }
func (f *fakeTerminalOperations) write(_ *os.File, value string) error {
	f.writes = append(f.writes, value)
	if f.writeErrAt == len(f.writes) {
		return errors.New("write failed")
	}
	return nil
}

func TestNativeTerminalLifecycleIsIdempotent(t *testing.T) {
	ops := &fakeTerminalOperations{interactive: true}
	terminal := &nativeTerminal{ops: ops}

	if err := terminal.Enter(); err != nil {
		t.Fatalf("Enter() error = %v", err)
	}
	if err := terminal.Enter(); err != nil {
		t.Fatalf("second Enter() error = %v", err)
	}
	if err := terminal.Suspend(); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if err := terminal.Suspend(); err != nil {
		t.Fatalf("second Suspend() error = %v", err)
	}
	if err := terminal.Resume(); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if err := terminal.Restore(); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if err := terminal.Restore(); err != nil {
		t.Fatalf("second Restore() error = %v", err)
	}

	if ops.stateCalls != 1 || ops.rawCalls != 2 || ops.restoreCalls != 2 {
		t.Fatalf("calls = state %d, raw %d, restore %d", ops.stateCalls, ops.rawCalls, ops.restoreCalls)
	}
	wantWrites := []string{enterTerminal, leaveTerminal, enterTerminal, leaveTerminal}
	if !reflect.DeepEqual(ops.writes, wantWrites) {
		t.Fatalf("writes = %q, want %q", ops.writes, wantWrites)
	}
}

func TestNativeTerminalEnterFailureRestoresTerminal(t *testing.T) {
	ops := &fakeTerminalOperations{interactive: true, writeErrAt: 1}
	terminal := &nativeTerminal{ops: ops}

	if err := terminal.Enter(); err == nil {
		t.Fatal("Enter() unexpectedly succeeded")
	}
	if terminal.raw || terminal.screen || terminal.original != nil {
		t.Fatalf("terminal retained state after cleanup: raw=%v screen=%v original=%v", terminal.raw, terminal.screen, terminal.original)
	}
	if ops.restoreCalls != 1 {
		t.Fatalf("restore calls = %d, want 1", ops.restoreCalls)
	}
	if want := []string{enterTerminal, leaveTerminal}; !reflect.DeepEqual(ops.writes, want) {
		t.Fatalf("writes = %q, want %q", ops.writes, want)
	}
}

func TestNativeTerminalRestoreRetriesPartialFailure(t *testing.T) {
	ops := &fakeTerminalOperations{interactive: true}
	terminal := &nativeTerminal{ops: ops}
	if err := terminal.Enter(); err != nil {
		t.Fatalf("Enter() error = %v", err)
	}

	ops.restoreErr = errors.New("restore failed")
	if err := terminal.Restore(); err == nil {
		t.Fatal("Restore() unexpectedly succeeded")
	}
	if terminal.screen || !terminal.raw {
		t.Fatalf("state after partial restore: raw=%v screen=%v", terminal.raw, terminal.screen)
	}

	ops.restoreErr = nil
	if err := terminal.Restore(); err != nil {
		t.Fatalf("retry Restore() error = %v", err)
	}
	if ops.restoreCalls != 2 || len(ops.writes) != 2 {
		t.Fatalf("retry repeated completed work: restore=%d writes=%d", ops.restoreCalls, len(ops.writes))
	}
}

func TestNativeTerminalRejectsNonInteractiveFiles(t *testing.T) {
	ops := &fakeTerminalOperations{}
	terminal := &nativeTerminal{ops: ops}

	if terminal.IsInteractive() {
		t.Fatal("IsInteractive() = true")
	}
	if !errors.Is(terminal.Enter(), errTerminalNotInteractive) {
		t.Fatalf("Enter() did not return noninteractive error")
	}
}
