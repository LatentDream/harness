package input

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

const defaultPrompt = "> "

type Terminal struct {
	reader    *bufio.Reader
	writer    io.Writer
	errWriter io.Writer
	prompt    string
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

func (t *Terminal) Write(response string) error {
	_, err := fmt.Fprintln(t.writer, response)
	return err
}

func (t *Terminal) Writef(format string, args ...any) error {
	return t.Write(fmt.Sprintf(format, args...))
}

func (t *Terminal) WriteErr(response string) error {
	_, err := fmt.Fprintln(t.errWriter, response)
	return err
}

func (t *Terminal) WriteErrf(format string, args ...any) error {
	return t.WriteErr(fmt.Sprintf(format, args...))
}

func trimLineEnding(line string) string {
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r")
}
