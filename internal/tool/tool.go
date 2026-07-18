package tool

import (
	"context"
	"encoding/json"

	"latentdream/harness/internal/llm"
)

type Tool interface {
	Definition() llm.ToolDefinition
	Execute(context.Context, json.RawMessage) (string, error)
}

func Definitions(tools []Tool) []llm.ToolDefinition {
	definitions := make([]llm.ToolDefinition, 0, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		definitions = append(definitions, item.Definition())
	}
	return definitions
}

func ByName(tools []Tool) map[string]Tool {
	byName := make(map[string]Tool, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		definition := item.Definition()
		if definition.Name != "" {
			byName[definition.Name] = item
		}
	}
	return byName
}

func NewDefault() []Tool {
	return []Tool{NewReadTool()}
}
