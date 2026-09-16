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

// TurnLifecycle is optionally implemented by sinks that observe complete turn
// boundaries without exposing transport state as frontend events.
type TurnLifecycle interface {
	TurnStarted(string)
	TurnCompleted(string)
}

type EventKind string

const (
	EventOutput              EventKind = "output"
	EventStatus              EventKind = "status"
	EventAssistantStarted    EventKind = "assistant.started"
	EventAssistantDelta      EventKind = "assistant.delta"
	EventAssistantCompleted  EventKind = "assistant.completed"
	EventAssistantAborted    EventKind = "assistant.aborted"
	EventReasoningStarted    EventKind = "reasoning.started"
	EventReasoningDelta      EventKind = "reasoning.delta"
	EventReasoningCompleted  EventKind = "reasoning.completed"
	EventReasoningAborted    EventKind = "reasoning.aborted"
	EventInferenceStarted    EventKind = "inference.started"
	EventInferenceEnded      EventKind = "inference.ended"
	EventToolStarted         EventKind = "tool.started"
	EventToolCompleted       EventKind = "tool.completed"
	EventProviderSelection   EventKind = "provider.selection"
	EventSessionReset        EventKind = "session.reset"
	EventSessionLoaded       EventKind = "session.loaded"
	EventSessionTitleChanged EventKind = "session.title.changed"
)

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type PresentationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Event struct {
	Kind         EventKind             `json:"kind"`
	TurnID       string                `json:"turnId,omitempty"`
	Round        int                   `json:"round,omitempty"`
	Stream       Stream                `json:"stream,omitempty"`
	Text         string                `json:"text,omitempty"`
	Provider     string                `json:"provider,omitempty"`
	Model        string                `json:"model,omitempty"`
	SessionID    string                `json:"sessionId,omitempty"`
	SessionTitle string                `json:"sessionTitle,omitempty"`
	Messages     []PresentationMessage `json:"messages,omitempty"`
	ToolCallID   string                `json:"toolCallId,omitempty"`
	ToolName     string                `json:"toolName,omitempty"`
	ToolActivity ToolActivity          `json:"toolActivity,omitempty"`
}

// ToolActivity contains presentation-safe tool metadata. It must not contain
// raw arguments because those may include file contents or other large values.
type ToolActivity struct {
	Summary string `json:"summary,omitempty"`
	Target  string `json:"target,omitempty"`
	Command string `json:"command,omitempty"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}
