package execution

import (
	"context"
	"errors"
	"fmt"

	"latentdream/harness/internal/input"
	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/model"
	"latentdream/harness/internal/tracing"
)

func ToolCall(ctx context.Context, io input.IO, toolsByName map[string]model.Tool, call llm.ToolCall, turnID string) (llm.Message, error) {
	spanCtx, err := tracing.StartSpan(ctx, tracing.SpanStart{
		Kind:    tracing.SpanToolCall,
		TurnID:  turnID,
		Payload: call,
	})
	if err != nil {
		return llm.Message{}, fmt.Errorf("start tool trace: %w", err)
	}

	result := ""
	item := toolsByName[call.Name]
	status := "running tool"
	if item != nil {
		status = item.Status(call.Arguments)
	}
	var executionErr error
	statusErr := WithStatus(spanCtx, io, status, func() error {
		if item == nil {
			executionErr = fmt.Errorf("tool %q is not available", call.Name)
			result = fmt.Sprintf("error: tool %q is not available", call.Name)
		} else {
			output, err := item.Execute(spanCtx, call.Arguments)
			if err != nil {
				executionErr = err
				result = "error: " + err.Error()
			} else {
				result = output
			}
		}
		return nil
	})
	message := llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: result}
	operationErr := errors.Join(statusErr, executionErr)

	endErr := tracing.EndSpan(spanCtx, tracing.SpanEnd{
		Status:  toolTraceStatus(operationErr),
		Error:   toolTraceError(operationErr),
		Payload: message,
	})
	if statusErr != nil {
		return llm.Message{}, errors.Join(statusErr, endErr)
	}
	if endErr != nil {
		return llm.Message{}, fmt.Errorf("end tool trace: %w", endErr)
	}

	return message, nil
}

func toolTraceStatus(err error) tracing.Status {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return tracing.StatusCancelled
	}
	if err != nil {
		return tracing.StatusFailure
	}
	return tracing.StatusSuccess
}

func toolTraceError(err error) *tracing.TraceError {
	if err == nil {
		return nil
	}
	return &tracing.TraceError{Message: err.Error()}
}
