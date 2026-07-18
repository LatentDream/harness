package execution

import (
	"context"
	"fmt"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/llm"
	"latentdream/harness/internal/tool/model"
)

func ToolCall(ctx context.Context, io input.IO, toolsByName map[string]model.Tool, call llm.ToolCall) (llm.Message, error) {
	result := ""
	item := toolsByName[call.Name]
	status := "running tool"
	if item != nil {
		status = item.Status(call.Arguments)
	}
	err := WithStatus(io, status, func() error {
		if item == nil {
			result = fmt.Sprintf("error: tool %q is not available", call.Name)
		} else {
			output, err := item.Execute(ctx, call.Arguments)
			if err != nil {
				result = "error: " + err.Error()
			} else {
				result = output
			}
		}
		return nil
	})
	if err != nil {
		return llm.Message{}, err
	}

	return llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: result}, nil
}
