package input

import (
	"bufio"
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
	reader    *bufio.Reader
	writer    io.Writer
	errWriter io.Writer
	prompt    string
	status    string
	visible   bool
}

func NewTerminal(reader io.Reader, writer io.Writer, errWriter io.Writer) *Terminal {
	return &Terminal{
		reader:    bufio.NewReader(reader),
		writer:    writer,
		errWriter: errWriter,
		prompt:    defaultPrompt,
	}
}

func (t *Terminal) Receive() (string, error) {
	if err := t.hideStatus(); err != nil {
		return "", err
	}
	if _, err := fmt.Fprint(t.writer, t.prompt); err != nil {
		return "", err
	}

	line, err := t.reader.ReadString('\n')
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
