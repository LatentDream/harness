package input

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestTerminalReceiveReadsLineAndWritesPrompt(t *testing.T) {
	var output bytes.Buffer
	var errOutput bytes.Buffer
	terminal := NewTerminal(strings.NewReader("hello\n"), &output, &errOutput)

	text, err := terminal.Receive()
	if err != nil {
		t.Fatalf("expected receive to succeed, got %v", err)
	}
	if text != "hello" {
		t.Fatalf("expected hello, got %q", text)
	}
	if output.String() != "> " {
		t.Fatalf("expected prompt, got %q", output.String())
	}
}

func TestTerminalReceiveReturnsPartialLineBeforeEOF(t *testing.T) {
	var output bytes.Buffer
	var errOutput bytes.Buffer
	terminal := NewTerminal(strings.NewReader("hello"), &output, &errOutput)

	text, err := terminal.Receive()
	if err != nil {
		t.Fatalf("expected partial line before EOF, got %v", err)
	}
	if text != "hello" {
		t.Fatalf("expected hello, got %q", text)
	}

	_, err = terminal.Receive()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after partial line, got %v", err)
	}
}

func TestTerminalWriteWritesLine(t *testing.T) {
	var output bytes.Buffer
	var errOutput bytes.Buffer
	terminal := NewTerminal(strings.NewReader(""), &output, &errOutput)

	if err := terminal.Write("assistant response"); err != nil {
		t.Fatalf("expected response to write, got %v", err)
	}
	if output.String() != "assistant response\n" {
		t.Fatalf("expected response line, got %q", output.String())
	}
}

func TestTerminalWritefWritesFormattedLine(t *testing.T) {
	var output bytes.Buffer
	var errOutput bytes.Buffer
	terminal := NewTerminal(strings.NewReader(""), &output, &errOutput)

	if err := terminal.Writef("hello %s", "world"); err != nil {
		t.Fatalf("expected formatted response to write, got %v", err)
	}
	if output.String() != "hello world\n" {
		t.Fatalf("expected formatted response line, got %q", output.String())
	}
}

func TestTerminalWriteErrfWritesFormattedErrorLine(t *testing.T) {
	var output bytes.Buffer
	var errOutput bytes.Buffer
	terminal := NewTerminal(strings.NewReader(""), &output, &errOutput)

	if err := terminal.WriteErrf("failed: %s", "boom"); err != nil {
		t.Fatalf("expected formatted error response to write, got %v", err)
	}
	if output.String() != "" {
		t.Fatalf("expected stdout to be empty, got %q", output.String())
	}
	if errOutput.String() != "failed: boom\n" {
		t.Fatalf("expected formatted stderr line, got %q", errOutput.String())
	}
}
