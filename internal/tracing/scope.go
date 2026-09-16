package tracing

import (
	"context"
	"errors"

	"latentdream/harness/internal/input"
)

type RecordingError struct {
	Err error
}

// Error returns the underlying recording error with tracing context.
func (e *RecordingError) Error() string {
	return "tracing: " + e.Err.Error()
}

// Unwrap returns the underlying recorder error.
func (e *RecordingError) Unwrap() error {
	return e.Err
}

type RunScope struct {
	ctx    context.Context
	reason EndReason
}

type SpanScope struct {
	ctx context.Context
}

type TurnScope struct {
	ctx         context.Context
	id          string
	span        *SpanScope
	snapshotted bool
}

// BeginRun starts a run and returns a scope that owns its finalization.
func BeginRun(ctx context.Context, meta RunMeta) (context.Context, *RunScope, error) {
	ctx, err := StartRun(ctx, meta)
	if err != nil {
		return ctx, nil, err
	}
	return ctx, &RunScope{ctx: ctx, reason: EndReasonError}, nil
}

// SetReason sets the reason reported when the run scope ends.
func (s *RunScope) SetReason(reason EndReason) {
	if s != nil {
		s.reason = reason
	}
}

// Snapshot records conversation state for the run.
func (s *RunScope) Snapshot(state SessionState) {
	if s != nil {
		Snapshot(s.ctx, state)
	}
}

// Checkpoint returns and clears recording errors accumulated by the run.
func (s *RunScope) Checkpoint() error {
	if s == nil {
		return nil
	}
	return Checkpoint(s.ctx)
}

// End snapshots state, derives the outcome, and finalizes the run.
func (s *RunScope) End(runErr *error, state func() SessionState) {
	if s == nil || runErr == nil {
		return
	}
	ctx := context.WithoutCancel(s.ctx)
	if state != nil {
		Snapshot(ctx, state())
	}

	traceErr := Checkpoint(ctx)
	combined := errors.Join(*runErr, traceErr)
	status := StatusFromError(combined)
	reason := s.reason
	if status == StatusCancelled {
		reason = EndReasonCancelled
	} else if status == StatusFailure {
		reason = EndReasonError
	}
	_ = CloseRun(ctx, RunOutcome{Status: status, Reason: reason, Error: ErrorFrom(combined)})
	*runErr = errors.Join(combined, Checkpoint(ctx))
}

// BeginSpan starts a span and returns a scope that owns its completion.
func BeginSpan(ctx context.Context, start SpanStart) (context.Context, *SpanScope, error) {
	ctx, err := StartSpan(ctx, start)
	if err != nil {
		return ctx, nil, err
	}
	return ctx, &SpanScope{ctx: ctx}, nil
}

// End completes the span with a status derived from err.
func (s *SpanScope) End(err error, payload any) {
	if s == nil {
		return
	}
	EndSpan(s.ctx, SpanEnd{
		Status:  StatusFromError(err),
		Error:   ErrorFrom(err),
		Payload: payload,
	})
}

// Checkpoint returns and clears recording errors accumulated by the span.
func (s *SpanScope) Checkpoint() error {
	if s == nil {
		return nil
	}
	return Checkpoint(s.ctx)
}

// BeginCommand starts a command span and records the exact user input.
func BeginCommand(ctx context.Context, commandInput string, userInput string, mode input.Mode) (context.Context, *SpanScope, error) {
	ctx, span, err := BeginSpan(ctx, SpanStart{
		Kind: SpanCommand,
		Payload: struct {
			Input string `json:"input"`
		}{Input: commandInput},
	})
	if err != nil {
		return ctx, nil, err
	}
	UserInput(ctx, "", userInput, mode)
	if err := Checkpoint(ctx); err != nil {
		span.End(err, nil)
		return ctx, nil, errors.Join(err, Checkpoint(ctx))
	}
	return ctx, span, nil
}

// BeginTurn starts a turn span and records its user input.
func BeginTurn(ctx context.Context, turnID string, text string, mode input.Mode) (context.Context, *TurnScope, error) {
	ctx, span, err := BeginSpan(ctx, SpanStart{Kind: SpanTurn, TurnID: turnID})
	if err != nil {
		return ctx, nil, err
	}
	UserInput(ctx, turnID, text, mode)
	if err := Checkpoint(ctx); err != nil {
		span.End(err, nil)
		return ctx, nil, errors.Join(err, Checkpoint(ctx))
	}
	return ctx, &TurnScope{ctx: ctx, id: turnID, span: span}, nil
}

// Rollback records a conversation rollback and its resulting state.
func (s *TurnScope) Rollback(conversationLength int, state SessionState) {
	if s == nil {
		return
	}
	Record(s.ctx, Event{
		Kind:   KindSessionRollback,
		TurnID: s.id,
		Payload: struct {
			ConversationLength int `json:"conversationLength"`
		}{ConversationLength: conversationLength},
	})
	Snapshot(s.ctx, state)
	s.snapshotted = true
}

// End snapshots the turn state and records its outcome.
func (s *TurnScope) End(err error, finalAnswer string, state SessionState) {
	if s == nil {
		return
	}
	if !s.snapshotted {
		Snapshot(s.ctx, state)
	}
	s.span.End(err, TurnOutcome{
		Status:      StatusFromError(err),
		Error:       ErrorFrom(err),
		FinalAnswer: finalAnswer,
	})
}

// Checkpoint returns and clears recording errors accumulated by the turn.
func (s *TurnScope) Checkpoint() error {
	if s == nil {
		return nil
	}
	return Checkpoint(s.ctx)
}

// UserInput records user-provided text and associates it with an optional turn.
func UserInput(ctx context.Context, turnID string, text string, mode input.Mode) {
	Record(ctx, Event{
		Kind:    KindUserInput,
		TurnID:  turnID,
		Payload: UserInputPayload{Text: text, Mode: mode},
	})
}

// SteeringInput records guidance accepted while a turn is executing.
func SteeringInput(ctx context.Context, turnID string, text string, mode input.Mode) {
	Record(ctx, Event{
		Kind:    KindSteeringInput,
		TurnID:  turnID,
		Payload: UserInputPayload{Text: text, Mode: mode},
	})
}

// Checkpoint returns and clears recording errors accumulated on the active run.
func Checkpoint(ctx context.Context) error {
	err := runStateFromContext(ctx).checkpoint()
	if err == nil {
		return nil
	}
	return &RecordingError{Err: err}
}

// IsRecordingError reports whether err contains a tracing recording failure.
func IsRecordingError(err error) bool {
	var target *RecordingError
	return errors.As(err, &target)
}

// StatusFromError maps an operation error to its trace status.
func StatusFromError(err error) Status {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return StatusCancelled
	}
	if err != nil {
		return StatusFailure
	}
	return StatusSuccess
}

// ErrorFrom converts an operation error to its persisted trace representation.
func ErrorFrom(err error) *TraceError {
	if err == nil {
		return nil
	}
	return &TraceError{Message: err.Error()}
}
