package input

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestTerminalReceiveReadsLineAndWritesPrompt(t *testing.T) {
	var output bytes.Buffer
	var errOutput bytes.Buffer
	terminal := NewTerminal(strings.NewReader("hello\n"), &output, &errOutput)

	text, err := terminal.Receive(context.Background())
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

	text, err := terminal.Receive(context.Background())
	if err != nil {
		t.Fatalf("expected partial line before EOF, got %v", err)
	}
	if text != "hello" {
		t.Fatalf("expected hello, got %q", text)
	}

	_, err = terminal.Receive(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after partial line, got %v", err)
	}
}

func TestTerminalReceiveReturnsWhenContextIsCanceled(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	terminal := NewTerminal(reader, io.Discard, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())

	result := make(chan error, 1)
	go func() {
		_, err := terminal.Receive(ctx)
		result <- err
	}()
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("receive did not return after cancellation")
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

func TestTerminalSetStatusDisplaysAndClearsStatus(t *testing.T) {
	var output bytes.Buffer
	var errOutput bytes.Buffer
	terminal := NewTerminal(strings.NewReader(""), &output, &errOutput)

	if err := terminal.SetStatusf("reading %s", "sample.txt"); err != nil {
		t.Fatalf("expected status to write, got %v", err)
	}
	if err := terminal.SetStatus(""); err != nil {
		t.Fatalf("expected status to clear, got %v", err)
	}

	want := clearStatusLine + "reading sample.txt" + clearStatusLine
	if output.String() != want {
		t.Fatalf("expected status display and clear, got %q", output.String())
	}
}

func TestTerminalWriteKeepsStatusBelowText(t *testing.T) {
	var output bytes.Buffer
	var errOutput bytes.Buffer
	terminal := NewTerminal(strings.NewReader(""), &output, &errOutput)

	if err := terminal.SetStatus("inference"); err != nil {
		t.Fatalf("expected status to write, got %v", err)
	}
	if err := terminal.Write("assistant response"); err != nil {
		t.Fatalf("expected response to write, got %v", err)
	}

	want := clearStatusLine + "inference" + clearStatusLine + "assistant response\n" + clearStatusLine + "inference"
	if output.String() != want {
		t.Fatalf("expected status below text, got %q", output.String())
	}
}

func TestTerminalReceiveClearsStatusBeforePrompt(t *testing.T) {
	var output bytes.Buffer
	var errOutput bytes.Buffer
	terminal := NewTerminal(strings.NewReader("hello\n"), &output, &errOutput)

	if err := terminal.SetStatus("inference"); err != nil {
		t.Fatalf("expected status to write, got %v", err)
	}
	if _, err := terminal.Receive(context.Background()); err != nil {
		t.Fatalf("expected receive to succeed, got %v", err)
	}

	want := clearStatusLine + "inference" + clearStatusLine + "> "
	if output.String() != want {
		t.Fatalf("expected status to clear before prompt, got %q", output.String())
	}
}

func TestTerminalStreamsAssistantResponseOnOneLine(t *testing.T) {
	var output bytes.Buffer
	terminal := NewTerminal(strings.NewReader(""), &output, io.Discard)
	ctx := context.Background()

	if err := terminal.Emit(ctx, Event{Kind: EventStatus, Text: "working..."}); err != nil {
		t.Fatalf("set status: %v", err)
	}
	for _, event := range []Event{
		{Kind: EventAssistantStarted, Stream: StreamStdout},
		{Kind: EventAssistantDelta, Stream: StreamStdout, Text: "hello"},
		{Kind: EventAssistantDelta, Stream: StreamStdout, Text: " world"},
		{Kind: EventStatus},
		{Kind: EventAssistantCompleted, Stream: StreamStdout},
	} {
		if err := terminal.Emit(ctx, event); err != nil {
			t.Fatalf("emit %#v: %v", event, err)
		}
	}

	want := clearStatusLine + "working..." + clearStatusLine + "hello world\n"
	if output.String() != want {
		t.Fatalf("expected progressive response, got %q", output.String())
	}
}

func TestTerminalAbortsPartialAssistantResponseWithNewline(t *testing.T) {
	var output bytes.Buffer
	terminal := NewTerminal(strings.NewReader(""), &output, io.Discard)
	ctx := context.Background()

	for _, event := range []Event{
		{Kind: EventAssistantStarted, Stream: StreamStdout},
		{Kind: EventAssistantDelta, Stream: StreamStdout, Text: "partial"},
		{Kind: EventAssistantAborted, Stream: StreamStdout},
		{Kind: EventOutput, Stream: StreamStdout, Text: "error: failed"},
	} {
		if err := terminal.Emit(ctx, event); err != nil {
			t.Fatalf("emit %#v: %v", event, err)
		}
	}

	if output.String() != "partial\nerror: failed\n" {
		t.Fatalf("expected partial response to be finalized, got %q", output.String())
	}
}
