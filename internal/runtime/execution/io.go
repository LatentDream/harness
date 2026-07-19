package execution

import (
	"context"

	"latentdream/harness/internal/input"
	"latentdream/harness/internal/tracing"
)

func UserInput(ctx context.Context, turnID string, text string, mode input.Mode) {
	tracing.UserInput(ctx, turnID, text, mode)
}

func Emit(ctx context.Context, sink input.Sink, event input.Event) error {
	if err := sink.Emit(ctx, event); err != nil {
		return err
	}

	tracing.Record(ctx, tracing.Event{
		Kind:    tracing.KindOutput,
		TurnID:  event.TurnID,
		Payload: event,
	})
	return nil
}

func Write(ctx context.Context, sink input.Sink, stream input.Stream, text string) error {
	return Emit(ctx, sink, input.Event{Kind: input.EventOutput, Stream: stream, Text: text})
}
