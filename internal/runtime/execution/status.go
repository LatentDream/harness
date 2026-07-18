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
	if err := recordStatus(ctx, status); err != nil {
		return errors.Join(err, io.SetStatus(""))
	}

	runErr := run()
	clearErr := io.SetStatus("")
	return errors.Join(runErr, clearErr, recordStatus(ctx, ""))
}

func recordStatus(ctx context.Context, status string) error {
	return tracing.Record(ctx, tracing.Event{
		Kind: tracing.KindOutput,
		Payload: struct {
			Stream string `json:"stream"`
			Text   string `json:"text"`
		}{Stream: "status", Text: status},
	})
}
