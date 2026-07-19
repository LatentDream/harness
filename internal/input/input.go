package input

import "context"

type Mode string

const (
	ModeBuild Mode = "build"
	ModePlan  Mode = "plan"
)

type Submission struct {
	Text string `json:"text"`
	Mode Mode   `json:"mode,omitempty"`
}

type Receiver interface {
	Receive(context.Context) (Submission, error)
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
)

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type Event struct {
	Kind   EventKind `json:"kind"`
	TurnID string    `json:"turnId,omitempty"`
	Round  int       `json:"round,omitempty"`
	Stream Stream    `json:"stream,omitempty"`
	Text   string    `json:"text,omitempty"`
}
