package model

import (
	"context"
	"encoding/json"

	"latentdream/harness/internal/session/llm"
)

type Capability string

const (
	CapabilityReadOnly Capability = "read-only"
	CapabilityMutating Capability = "mutating"
)

type Tool interface {
	Definition() llm.ToolDefinition
	Capability() Capability
	Status(json.RawMessage) string
	Execute(context.Context, json.RawMessage) (string, error)
}
