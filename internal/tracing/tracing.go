package tracing

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"latentdream/harness/internal/input"
	"latentdream/harness/internal/session"
	"latentdream/harness/internal/session/llm"
)

const CurrentVersion = 1

var ErrNoSnapshot = errors.New("trace contains no session snapshot")

type Config struct {
	Enabled              bool          `json:"enabled"`
	Directory            string        `json:"directory"`
	FilePath             string        `json:"filePath"`
	FailurePolicy        FailurePolicy `json:"failurePolicy"`
	RecordRaw            bool          `json:"recordRaw"`
	EnvironmentAllowlist []string      `json:"environmentAllowlist"`
	RecordDiffs          bool          `json:"recordDiffs"`
	RedactPatterns       []string      `json:"redactPatterns"`
	MaxPayloadBytes      int64         `json:"maxPayloadBytes"`
}

// Recorder starts traces. The returned Run owns all events and snapshots for
// that trace.
type Recorder interface {
	StartRun(context.Context, RunMeta) (context.Context, Run, error)
}

type Run interface {
	Event(context.Context, Event) error
	StartSpan(context.Context, SpanStart) (context.Context, Span, error)
	Snapshot(context.Context, SessionState) error
	Close(context.Context, RunOutcome) error
}

type Span interface {
	End(context.Context, SpanEnd) error
}

// Reader loads persisted traces without restoring session or workspace state.
type Reader interface {
	Open(context.Context, string) (*Trace, error)
}

type Trace struct {
	Version   int               `json:"version"`
	ID        string            `json:"id"`
	StartedAt time.Time         `json:"startedAt"`
	EndedAt   time.Time         `json:"endedAt,omitempty"`
	Meta      RunMeta           `json:"meta"`
	Outcome   *RunOutcome       `json:"outcome,omitempty"`
	Events    []RecordedEvent   `json:"events,omitempty"`
	Snapshots []SessionSnapshot `json:"snapshots,omitempty"`
}

// Event describes an event to record. The recorder supplies envelope fields
// such as ID, sequence, timestamp, and trace ID.
type Event struct {
	Kind    Kind
	TurnID  string
	Payload any
}

type RecordedEvent struct {
	Version      int             `json:"version"`
	ID           string          `json:"id"`
	TraceID      string          `json:"traceId"`
	Sequence     uint64          `json:"sequence"`
	Timestamp    time.Time       `json:"timestamp"`
	Kind         Kind            `json:"kind"`
	TurnID       string          `json:"turnId,omitempty"`
	SpanID       string          `json:"spanId,omitempty"`
	ParentSpanID string          `json:"parentSpanId,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

type SpanStart struct {
	Kind    SpanKind
	TurnID  string
	Payload any
}

type SpanEnd struct {
	Status  Status
	Error   *TraceError
	Payload any
}

type SessionState struct {
	Conversation []llm.Message `json:"conversation"`
}

type SessionSnapshot struct {
	Version      int           `json:"version"`
	Sequence     uint64        `json:"sequence"`
	Timestamp    time.Time     `json:"timestamp"`
	Conversation []llm.Message `json:"conversation"`
}

type RunMeta struct {
	SessionID        string            `json:"sessionId,omitempty"`
	RunID            string            `json:"runId,omitempty"`
	WorkingDirectory string            `json:"workingDirectory,omitempty"`
	ConfigPath       string            `json:"configPath,omitempty"`
	Repository       string            `json:"repository,omitempty"`
	Commit           string            `json:"commit,omitempty"`
	HarnessVersion   string            `json:"harnessVersion,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
}

type RunOutcome struct {
	Status Status      `json:"status"`
	Reason EndReason   `json:"reason"`
	Error  *TraceError `json:"error,omitempty"`
}

type TurnOutcome struct {
	Status      Status      `json:"status"`
	Error       *TraceError `json:"error,omitempty"`
	FinalAnswer string      `json:"finalAnswer,omitempty"`
}

type UserInputPayload struct {
	Text string     `json:"text"`
	Mode input.Mode `json:"mode,omitempty"`
}

type TraceError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type FailurePolicy string

const (
	FailureBestEffort FailurePolicy = "bestEffort"
	FailureStrict     FailurePolicy = "strict"
)

type Status string

const (
	StatusSuccess   Status = "success"
	StatusFailure   Status = "failure"
	StatusCancelled Status = "cancelled"
)

type EndReason string

const (
	EndReasonEOF           EndReason = "eof"
	EndReasonExit          EndReason = "exit"
	EndReasonNewSession    EndReason = "new_session"
	EndReasonSessionSwitch EndReason = "session_switch"
	EndReasonCancelled     EndReason = "cancelled"
	EndReasonError         EndReason = "error"
)

type Kind string

const (
	KindRunStarted       Kind = "run.started"
	KindRunEnded         Kind = "run.ended"
	KindTurnStarted      Kind = "turn.started"
	KindTurnEnded        Kind = "turn.ended"
	KindUserInput        Kind = "user.input"
	KindCommandStarted   Kind = "command.started"
	KindCommandEnded     Kind = "command.ended"
	KindLLMRequest       Kind = "llm.request"
	KindLLMResponse      Kind = "llm.response"
	KindLLMError         Kind = "llm.error"
	KindAssistantMessage Kind = "assistant.message"
	KindToolStarted      Kind = "tool.started"
	KindToolEnded        Kind = "tool.ended"
	KindSessionRollback  Kind = "session.rollback"
	KindSessionSnapshot  Kind = "session.snapshot"
	KindArtifactObserved Kind = "artifact.observed"
	KindOutput           Kind = "io.output"
	KindError            Kind = "error"
)

type SpanKind string

const (
	SpanTurn     SpanKind = "turn"
	SpanCommand  SpanKind = "command"
	SpanLLMCall  SpanKind = "llm.call"
	SpanToolCall SpanKind = "tool.call"
)

// Session returns the conversation from the latest session snapshot.
func (t *Trace) Session() (session.Session, error) {
	if t == nil || len(t.Snapshots) == 0 {
		return session.Session{}, ErrNoSnapshot
	}

	return sessionFromSnapshot(t.Snapshots[len(t.Snapshots)-1]), nil
}

// SessionAt returns the latest conversation snapshot at or before sequence.
func (t *Trace) SessionAt(sequence uint64) (session.Session, error) {
	if t == nil {
		return session.Session{}, ErrNoSnapshot
	}

	var selected *SessionSnapshot
	for index := range t.Snapshots {
		snapshot := &t.Snapshots[index]
		if snapshot.Sequence <= sequence && (selected == nil || snapshot.Sequence > selected.Sequence) {
			selected = snapshot
		}
	}
	if selected != nil {
		return sessionFromSnapshot(*selected), nil
	}

	return session.Session{}, ErrNoSnapshot
}

func sessionFromSnapshot(snapshot SessionSnapshot) session.Session {
	conversation := make([]llm.Message, len(snapshot.Conversation))
	for index, message := range snapshot.Conversation {
		conversation[index] = message
		if len(message.ToolCalls) == 0 {
			continue
		}

		conversation[index].ToolCalls = make([]llm.ToolCall, len(message.ToolCalls))
		for callIndex, call := range message.ToolCalls {
			conversation[index].ToolCalls[callIndex] = call
			conversation[index].ToolCalls[callIndex].Arguments = append(json.RawMessage(nil), call.Arguments...)
		}
	}
	return session.Session{Conversation: conversation}
}

func normalizedContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
