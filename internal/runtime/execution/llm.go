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
	if err := Emit(callCtx, sink, input.Event{Kind: input.EventInferenceStarted, TurnID: turnID, Round: round}); err != nil {
		span.End(err, nil)
		return provider.Response{}, errors.Join(err, tracing.Checkpoint(callCtx))
	}
	if err := tracing.Checkpoint(callCtx); err != nil {
		endErr := Emit(callCtx, sink, input.Event{Kind: input.EventInferenceEnded, TurnID: turnID, Round: round})
		span.End(err, nil)
		return provider.Response{}, errors.Join(err, endErr, tracing.Checkpoint(callCtx))
	}

	reasoning := reasoningOutput{ctx: callCtx, sink: sink, turnID: turnID, round: round}
	response, callErr := aiProvider.Send(callCtx, request, func(event provider.StreamEvent) error {
		return errors.Join(reasoning.delta(event.ReasoningDelta), output.delta(event.TextDelta))
	})
	if callErr != nil {
		abortErr := errors.Join(
			reasoning.finish(input.EventReasoningAborted),
			output.finish(input.EventAssistantAborted, output.text.String()),
			Emit(callCtx, sink, input.Event{Kind: input.EventInferenceEnded, TurnID: turnID, Round: round}),
		)
		span.End(callErr, nil)
		return provider.Response{}, errors.Join(callErr, abortErr, tracing.Checkpoint(callCtx))
	}
	callErr = errors.Join(
		reasoning.finish(input.EventReasoningCompleted),
		Emit(callCtx, sink, input.Event{Kind: input.EventInferenceEnded, TurnID: turnID, Round: round}),
	)
	if callErr != nil {
		abortErr := output.finish(input.EventAssistantAborted, output.text.String())
		span.End(callErr, nil)
		return provider.Response{}, errors.Join(callErr, abortErr, tracing.Checkpoint(callCtx))
	}
	if !output.started && response.Message.Content != "" {
		if err := output.delta(response.Message.Content); err != nil {
			abortErr := output.finish(input.EventAssistantAborted, output.text.String())
			span.End(err, nil)
			return provider.Response{}, errors.Join(err, abortErr, tracing.Checkpoint(callCtx))
		}
	}
	if err := output.finish(input.EventAssistantCompleted, response.Message.Content); err != nil {
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

type reasoningOutput struct {
	ctx     context.Context
	sink    input.Sink
	turnID  string
	round   int
	started bool
}

func (o *reasoningOutput) delta(text string) error {
	if text == "" {
		return nil
	}
	if !o.started {
		if err := Emit(o.ctx, o.sink, input.Event{
			Kind: input.EventReasoningStarted, TurnID: o.turnID, Round: o.round,
		}); err != nil {
			return err
		}
		o.started = true
	}
	return Emit(o.ctx, o.sink, input.Event{
		Kind: input.EventReasoningDelta, TurnID: o.turnID, Round: o.round, Text: text,
	})
}

func (o *reasoningOutput) finish(kind input.EventKind) error {
	if !o.started {
		return nil
	}
	err := Emit(o.ctx, o.sink, input.Event{
		Kind: kind, TurnID: o.turnID, Round: o.round,
	})
	o.started = false
	return err
}

type assistantOutput struct {
	ctx     context.Context
	sink    input.Sink
	turnID  string
	round   int
	started bool
	text    strings.Builder
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
	o.text.WriteString(text)
	return Emit(o.ctx, o.sink, input.Event{
		Kind: input.EventAssistantDelta, TurnID: o.turnID, Round: o.round, Stream: input.StreamStdout, Text: text,
	})
}

func (o *assistantOutput) finish(kind input.EventKind, text string) error {
	if !o.started {
		return nil
	}
	err := Emit(o.ctx, o.sink, input.Event{
		Kind: kind, TurnID: o.turnID, Round: o.round, Stream: input.StreamStdout, Text: text,
	})
	o.started = false
	o.text.Reset()
	return err
}
