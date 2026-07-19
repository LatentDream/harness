package command

import (
	"context"
	"errors"
	"testing"
)

func TestCopyCommandCopiesLatestMessage(t *testing.T) {
	clipboard := &fakeCopyClipboard{}
	cmd := NewCopyCmd(func() (string, bool) { return "latest assistant message", true }, clipboard)

	result, err := cmd.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Output != "copied latest message to clipboard" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	if clipboard.text != "latest assistant message" {
		t.Fatalf("expected copied text, got %q", clipboard.text)
	}
}

func TestCopyCommandHandlesNoMessage(t *testing.T) {
	clipboard := &fakeCopyClipboard{}
	cmd := NewCopyCmd(func() (string, bool) { return "", false }, clipboard)

	result, err := cmd.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Output != "no message to copy" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	if clipboard.text != "" {
		t.Fatalf("expected clipboard not to be called, got %q", clipboard.text)
	}
}

func TestCopyCommandReturnsClipboardFailureAsOutput(t *testing.T) {
	clipboard := &fakeCopyClipboard{err: errors.New("missing command")}
	cmd := NewCopyCmd(func() (string, bool) { return "latest", true }, clipboard)

	result, err := cmd.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no command error, got %v", err)
	}
	if result.Output != "error: copy failed: missing command" {
		t.Fatalf("unexpected output %q", result.Output)
	}
}

type fakeCopyClipboard struct {
	text string
	err  error
}

func (f *fakeCopyClipboard) Copy(_ context.Context, text string) error {
	f.text = text
	return f.err
}
