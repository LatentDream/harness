package execution

import (
	"context"
	"errors"
	"strings"

	"latentdream/harness/internal/input"
	"latentdream/harness/internal/tracing"
)

func WithStatus(ctx context.Context, io input.IO, status string, run func() error) error {
	status = strings.TrimSpace(status)
	if status == "" {
		return run()
	}

	if err := io.SetStatus(status); err != nil {
		return err
	}
	recordStatus(ctx, status)
	if traceErr := tracing.Checkpoint(ctx); traceErr != nil {
		return errors.Join(traceErr, io.SetStatus(""))
	}

	runErr := run()
	clearErr := io.SetStatus("")
	recordStatus(ctx, "")
	return errors.Join(runErr, clearErr)
}

func recordStatus(ctx context.Context, status string) {
	tracing.Record(ctx, tracing.Event{
		Kind: tracing.KindOutput,
		Payload: struct {
			Stream string `json:"stream"`
			Text   string `json:"text"`
		}{Stream: "status", Text: status},
	})
}
