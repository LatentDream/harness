package execution

import (
	"context"

	"latentdream/harness/internal/input"
	"latentdream/harness/internal/tracing"
)

func UserInput(ctx context.Context, turnID string, text string) {
	tracing.UserInput(ctx, turnID, text)
}

func Write(ctx context.Context, io input.IO, stream string, text string) error {
	var err error
	if stream == "stderr" {
		err = io.WriteErr(text)
	} else {
		err = io.Write(text)
	}
	if err != nil {
		return err
	}

	tracing.Record(ctx, tracing.Event{
		Kind: tracing.KindOutput,
		Payload: struct {
			Stream string `json:"stream"`
			Text   string `json:"text"`
		}{Stream: stream, Text: text},
	})
	return nil
}
