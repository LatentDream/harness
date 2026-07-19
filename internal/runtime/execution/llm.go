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
	io input.IO,
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
	callErr := WithStatus(callCtx, io, "working...", func() error {
		var sendErr error
		response, sendErr = aiProvider.Send(callCtx, request)
		return sendErr
	})
	if callErr != nil {
		span.End(callErr, nil)
		return provider.Response{}, errors.Join(callErr, tracing.Checkpoint(callCtx))
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
