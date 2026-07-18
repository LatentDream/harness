package execution

import (
	"latentdream/harness/internal/input"
	"strings"
)


func WithStatus(io input.IO, status string, run func() error) error {
	status = strings.TrimSpace(status)
	if status == "" {
		return run()
	}

	if err := io.SetStatus(status); err != nil {
		return err
	}
	err := run()
	if clearErr := io.SetStatus(""); clearErr != nil && err == nil {
		return clearErr
	}
	return err
}
