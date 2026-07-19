package input

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const (
	defaultPrompt   = "> "
	clearStatusLine = "\r\x1b[2K"
)

type Terminal struct {
	mu           sync.Mutex
	reader       *bufio.Reader
	readerCloser io.Closer
	writer       io.Writer
	errWriter    io.Writer
	prompt       string
	status       string
	visible      bool
	streaming    bool
}

func NewTerminal(reader io.Reader, writer io.Writer, errWriter io.Writer) *Terminal {
	terminal := &Terminal{
		reader:    bufio.NewReader(reader),
		writer:    writer,
		errWriter: errWriter,
		prompt:    defaultPrompt,
	}
	terminal.readerCloser, _ = reader.(io.Closer)
	return terminal
}

func (t *Terminal) Receive(ctx context.Context) (Submission, error) {
	if err := ctx.Err(); err != nil {
		return Submission{}, err
	}
	t.mu.Lock()
	if err := t.hideStatus(); err != nil {
		t.mu.Unlock()
		return Submission{}, err
	}
	if _, err := fmt.Fprint(t.writer, t.prompt); err != nil {
		t.mu.Unlock()
		return Submission{}, err
	}
	t.mu.Unlock()

	stopCancellation := func() bool { return true }
	if t.readerCloser != nil {
		stopCancellation = context.AfterFunc(ctx, func() {
			_ = t.readerCloser.Close()
		})
	}
	line, err := t.reader.ReadString('\n')
	stopCancellation()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Submission{}, ctxErr
	}
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return Submission{Text: trimLineEnding(line), Mode: ModeBuild}, nil
		}

		return Submission{}, err
	}

	return Submission{Text: trimLineEnding(line), Mode: ModeBuild}, nil
}

func (t *Terminal) Emit(_ context.Context, event Event) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch event.Kind {
	case EventOutput:
		return t.writeLine(event.Stream, event.Text)
	case EventStatus:
		t.status = event.Text
		return t.redrawStatus()
	case EventAssistantStarted:
		if t.streaming {
			return nil
		}
		if err := t.hideStatus(); err != nil {
			return err
		}
		t.streaming = true
		return nil
	case EventAssistantDelta:
		if !t.streaming {
			if err := t.hideStatus(); err != nil {
				return err
			}
			t.streaming = true
		}
		_, err := fmt.Fprint(t.writer, event.Text)
		return err
	case EventAssistantCompleted, EventAssistantAborted:
		if !t.streaming {
			return nil
		}
		if _, err := fmt.Fprintln(t.writer); err != nil {
			return err
		}
		t.streaming = false
		return t.showStatus()
	default:
		return fmt.Errorf("unsupported input event kind %q", event.Kind)
	}
}

func (t *Terminal) SetStatus(status string) error {
	return t.Emit(context.Background(), Event{Kind: EventStatus, Text: status})
}

func (t *Terminal) SetStatusf(format string, args ...any) error {
	return t.SetStatus(fmt.Sprintf(format, args...))
}

func (t *Terminal) Write(response string) error {
	return t.Emit(context.Background(), Event{Kind: EventOutput, Stream: StreamStdout, Text: response})
}

func (t *Terminal) Writef(format string, args ...any) error {
	return t.Write(fmt.Sprintf(format, args...))
}

func (t *Terminal) WriteErr(response string) error {
	return t.Emit(context.Background(), Event{Kind: EventOutput, Stream: StreamStderr, Text: response})
}

func (t *Terminal) writeLine(stream Stream, response string) error {
	if err := t.hideStatus(); err != nil {
		return err
	}
	writer := t.writer
	if stream == StreamStderr {
		writer = t.errWriter
	}
	if _, err := fmt.Fprintln(writer, response); err != nil {
		return err
	}
	return t.showStatus()
}

func (t *Terminal) WriteErrf(format string, args ...any) error {
	return t.WriteErr(fmt.Sprintf(format, args...))
}

func (t *Terminal) redrawStatus() error {
	if err := t.hideStatus(); err != nil {
		return err
	}
	return t.showStatus()
}

func (t *Terminal) hideStatus() error {
	if !t.visible {
		return nil
	}
	if _, err := fmt.Fprint(t.writer, clearStatusLine); err != nil {
		return err
	}
	t.visible = false
	return nil
}

func (t *Terminal) showStatus() error {
	if t.status == "" {
		return nil
	}
	if _, err := fmt.Fprint(t.writer, clearStatusLine, t.status); err != nil {
		return err
	}
	t.visible = true
	return nil
}

func trimLineEnding(line string) string {
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r")
}
