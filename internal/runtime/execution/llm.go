package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"latentdream/harness/internal/input"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tracing"
)

func LLMCall(
	ctx context.Context,
	sink input.Sink,
	aiProvider provider.Provider,
	request llm.Request,
	round int,
	turnID string,
) (provider.Response, error) {
	selection := aiProvider.Current()
	callCtx, span, err := tracing.BeginSpan(ctx, tracing.SpanStart{
		Kind:   tracing.SpanLLMCall,
		TurnID: turnID,
		Payload: struct {
			Provider string      `json:"provider"`
			Model    string      `json:"model"`
			Round    int         `json:"round"`
			Request  llm.Request `json:"request"`
		}{Provider: selection.Provider, Model: selection.Model, Round: round, Request: request},
	})
	if err != nil {
		return provider.Response{}, fmt.Errorf("start LLM trace: %w", err)
	}

	var response provider.Response
	output := assistantOutput{ctx: callCtx, sink: sink, turnID: turnID, round: round}
	callErr := WithStatus(callCtx, sink, input.Event{TurnID: turnID, Round: round, Text: "working..."}, func() error {
		var sendErr error
		response, sendErr = aiProvider.Send(callCtx, request, func(event provider.StreamEvent) error {
			return output.delta(event.TextDelta)
		})
		return sendErr
	})
	if callErr != nil {
		abortErr := output.finish(input.EventAssistantAborted)
		span.End(callErr, nil)
		return provider.Response{}, errors.Join(callErr, abortErr, tracing.Checkpoint(callCtx))
	}
	if !output.started && response.Message.Content != "" {
		if err := output.delta(response.Message.Content); err != nil {
			abortErr := output.finish(input.EventAssistantAborted)
			span.End(err, nil)
			return provider.Response{}, errors.Join(err, abortErr, tracing.Checkpoint(callCtx))
		}
	}
	if err := output.finish(input.EventAssistantCompleted); err != nil {
		span.End(err, nil)
		return provider.Response{}, errors.Join(err, tracing.Checkpoint(callCtx))
	}

	for index := range response.Message.ToolCalls {
		if strings.TrimSpace(response.Message.ToolCalls[index].ID) == "" {
			response.Message.ToolCalls[index].ID = fmt.Sprintf("call_%d", index+1)
		}
	}
	span.End(nil, struct {
		Provider string      `json:"provider"`
		Model    string      `json:"model"`
		Round    int         `json:"round"`
		Message  llm.Message `json:"message"`
	}{Provider: response.Provider, Model: response.Model, Round: round, Message: response.Message})
	tracing.Record(ctx, tracing.Event{
		Kind:   tracing.KindAssistantMessage,
		TurnID: turnID,
		Payload: struct {
			Message llm.Message `json:"message"`
		}{Message: response.Message},
	})
	return response, tracing.Checkpoint(callCtx)
}

type assistantOutput struct {
	ctx     context.Context
	sink    input.Sink
	turnID  string
	round   int
	started bool
}

func (o *assistantOutput) delta(text string) error {
	if text == "" {
		return nil
	}
	if !o.started {
		if err := Emit(o.ctx, o.sink, input.Event{
			Kind: input.EventAssistantStarted, TurnID: o.turnID, Round: o.round, Stream: input.StreamStdout,
		}); err != nil {
			return err
		}
		o.started = true
	}
	return Emit(o.ctx, o.sink, input.Event{
		Kind: input.EventAssistantDelta, TurnID: o.turnID, Round: o.round, Stream: input.StreamStdout, Text: text,
	})
}

func (o *assistantOutput) finish(kind input.EventKind) error {
	if !o.started {
		return nil
	}
	err := Emit(o.ctx, o.sink, input.Event{
		Kind: kind, TurnID: o.turnID, Round: o.round, Stream: input.StreamStdout,
	})
	o.started = false
	return err
}
