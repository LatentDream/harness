package model

import (
	"context"
	"encoding/json"

	"latentdream/harness/internal/llm"
)

type Tool interface {
	Definition() llm.ToolDefinition
	Status(json.RawMessage) string
	Execute(context.Context, json.RawMessage) (string, error)
}
