package execution

import (
	"context"
	"errors"
	"strings"

	"latentdream/harness/internal/input"
	"latentdream/harness/internal/tracing"
)

func WithStatus(ctx context.Context, sink input.Sink, status input.Event, run func() error) error {
	status.Text = strings.TrimSpace(status.Text)
	if status.Text == "" {
		return run()
	}
	status.Kind = input.EventStatus

	if err := Emit(ctx, sink, status); err != nil {
		return err
	}
	clear := status
	clear.Text = ""
	if traceErr := tracing.Checkpoint(ctx); traceErr != nil {
		return errors.Join(traceErr, Emit(ctx, sink, clear))
	}

	runErr := run()
	clearErr := Emit(ctx, sink, clear)
	return errors.Join(runErr, clearErr)
}
