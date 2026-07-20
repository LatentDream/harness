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

// ActivityPresenter is optionally implemented by tools with a dedicated UI
// presentation. Present must return only metadata that is safe to display and
// persist in an output event.
type ActivityPresenter interface {
	Present(json.RawMessage, string, error) Activity
}

type Activity struct {
	Target  string
	Command string
	Output  string
}
