package input

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	defaultPrompt   = "> "
	clearStatusLine = "\r\x1b[2K"
)

type Terminal struct {
	reader       *bufio.Reader
	readerCloser io.Closer
	writer       io.Writer
	errWriter    io.Writer
	prompt       string
	status       string
	visible      bool
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

func (t *Terminal) Receive(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := t.hideStatus(); err != nil {
		return "", err
	}
	if _, err := fmt.Fprint(t.writer, t.prompt); err != nil {
		return "", err
	}

	stopCancellation := func() bool { return true }
	if t.readerCloser != nil {
		stopCancellation = context.AfterFunc(ctx, func() {
			_ = t.readerCloser.Close()
		})
	}
	line, err := t.reader.ReadString('\n')
	stopCancellation()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return trimLineEnding(line), nil
		}

		return "", err
	}

	return trimLineEnding(line), nil
}

func (t *Terminal) SetStatus(status string) error {
	t.status = status
	return t.redrawStatus()
}

func (t *Terminal) SetStatusf(format string, args ...any) error {
	return t.SetStatus(fmt.Sprintf(format, args...))
}

func (t *Terminal) Write(response string) error {
	if err := t.hideStatus(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(t.writer, response); err != nil {
		return err
	}
	return t.showStatus()
}

func (t *Terminal) Writef(format string, args ...any) error {
	return t.Write(fmt.Sprintf(format, args...))
}

func (t *Terminal) WriteErr(response string) error {
	if err := t.hideStatus(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(t.errWriter, response); err != nil {
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
