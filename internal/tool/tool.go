package tool

import (
	"context"
	"encoding/json"
)

type Tool interface {
	Definition() Definition
	Execute(context.Context, json.RawMessage) (string, error)
}

type Definition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  Schema `json:"parameters"`
}

type Schema struct {
	Type                 string            `json:"type"`
	Description          string            `json:"description,omitempty"`
	Properties           map[string]Schema `json:"properties,omitempty"`
	Required             []string          `json:"required,omitempty"`
	AdditionalProperties *bool             `json:"additionalProperties,omitempty"`
}

func Definitions(tools []Tool) []Definition {
	definitions := make([]Definition, 0, len(tools))
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
