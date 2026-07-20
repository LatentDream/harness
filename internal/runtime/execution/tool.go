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

func ToolCall(ctx context.Context, sink input.Sink, toolsByName map[string]model.Tool, call llm.ToolCall, turnID string) (llm.Message, error) {
	spanCtx, span, err := tracing.BeginSpan(ctx, tracing.SpanStart{
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
	activity := toolActivity(item, call.Arguments, "", nil)
	if err := Emit(spanCtx, sink, input.Event{
		Kind:         input.EventToolStarted,
		TurnID:       turnID,
		ToolCallID:   call.ID,
		ToolName:     call.Name,
		ToolActivity: activity,
	}); err != nil {
		span.End(err, nil)
		return llm.Message{}, errors.Join(err, tracing.Checkpoint(spanCtx))
	}
	var executionErr error
	statusErr := WithStatus(spanCtx, sink, input.Event{TurnID: turnID, Text: status}, func() error {
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
	activity = toolActivity(item, call.Arguments, result, executionErr)
	if executionErr != nil {
		activity.Error = executionErr.Error()
	}
	completionErr := Emit(spanCtx, sink, input.Event{
		Kind:         input.EventToolCompleted,
		TurnID:       turnID,
		ToolCallID:   call.ID,
		ToolName:     call.Name,
		ToolActivity: activity,
	})
	operationErr := errors.Join(statusErr, executionErr)

	span.End(operationErr, message)
	traceErr := tracing.Checkpoint(spanCtx)
	if statusErr != nil || completionErr != nil {
		return llm.Message{}, errors.Join(statusErr, completionErr, traceErr)
	}
	if traceErr != nil {
		return llm.Message{}, traceErr
	}

	return message, nil
}

func toolActivity(item model.Tool, arguments []byte, result string, executionErr error) input.ToolActivity {
	presenter, ok := item.(model.ActivityPresenter)
	if !ok {
		return input.ToolActivity{}
	}
	presentation := presenter.Present(arguments, result, executionErr)
	return input.ToolActivity{
		Target:  presentation.Target,
		Command: presentation.Command,
		Output:  presentation.Output,
	}
}
