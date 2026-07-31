package input

import "context"

type Mode string

const (
	ModeBuild Mode = "build"
	ModePlan  Mode = "plan"
	ModeChat  Mode = "chat"
)

type Submission struct {
	Text string `json:"text"`
	Mode Mode   `json:"mode,omitempty"`
}

type Receiver interface {
	Receive(context.Context) (Submission, error)
}

// Interrupter is optionally implemented by interactive receivers that can
// request cancellation of the current in-flight submission without ending the
// entire input stream.
type Interrupter interface {
	Interrupts() <-chan struct{}
}

type Sink interface {
	Emit(context.Context, Event) error
}

type EventKind string

const (
	EventOutput             EventKind = "output"
	EventStatus             EventKind = "status"
	EventAssistantStarted   EventKind = "assistant.started"
	EventAssistantDelta     EventKind = "assistant.delta"
	EventAssistantCompleted EventKind = "assistant.completed"
	EventAssistantAborted   EventKind = "assistant.aborted"
	EventInferenceStarted   EventKind = "inference.started"
	EventInferenceEnded     EventKind = "inference.ended"
	EventToolStarted        EventKind = "tool.started"
	EventToolCompleted      EventKind = "tool.completed"
	EventProviderSelection  EventKind = "provider.selection"
	EventSessionReset       EventKind = "session.reset"
)

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type Event struct {
	Kind         EventKind    `json:"kind"`
	TurnID       string       `json:"turnId,omitempty"`
	Round        int          `json:"round,omitempty"`
	Stream       Stream       `json:"stream,omitempty"`
	Text         string       `json:"text,omitempty"`
	Provider     string       `json:"provider,omitempty"`
	Model        string       `json:"model,omitempty"`
	ToolCallID   string       `json:"toolCallId,omitempty"`
	ToolName     string       `json:"toolName,omitempty"`
	ToolActivity ToolActivity `json:"toolActivity,omitempty"`
}

// ToolActivity contains presentation-safe tool metadata. It must not contain
// raw arguments because those may include file contents or other large values.
type ToolActivity struct {
	Target  string `json:"target,omitempty"`
	Command string `json:"command,omitempty"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}
