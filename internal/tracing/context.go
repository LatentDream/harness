package tracing

import (
	"context"
	"errors"
	"sync"
)

type recorderContextKey struct{}
type runContextKey struct{}
type spanContextKey struct{}

type runState struct {
	run Run

	mu  sync.Mutex
	err error
}

// Init adds the process-configured recorder to the root context. A nil
// recorder disables tracing through the no-op implementation.
func Init(ctx context.Context, recorder Recorder) context.Context {
	ctx = normalizedContext(ctx)
	if recorder == nil {
		recorder = Noop()
	}
	return context.WithValue(ctx, recorderContextKey{}, recorder)
}

// RecorderFromContext returns the configured recorder or a no-op recorder.
func RecorderFromContext(ctx context.Context) Recorder {
	if ctx != nil {
		if recorder, ok := ctx.Value(recorderContextKey{}).(Recorder); ok && recorder != nil {
			return recorder
		}
	}
	return Noop()
}

// StartRun starts a trace using the recorder configured in ctx and adds the
// active run to the returned context.
func StartRun(ctx context.Context, meta RunMeta) (context.Context, error) {
	ctx = normalizedContext(ctx)
	runCtx, run, err := RecorderFromContext(ctx).StartRun(ctx, meta)
	if err != nil {
		return ctx, err
	}
	if run == nil {
		run = noopRun{}
	}
	if runCtx == nil {
		runCtx = ctx
	}
	runCtx = Init(runCtx, RecorderFromContext(ctx))
	return context.WithValue(runCtx, runContextKey{}, &runState{run: run}), nil
}

// Record records an event and retains any recorder error for the next checkpoint.
func Record(ctx context.Context, event Event) {
	err := runFromContext(ctx).Event(normalizedContext(ctx), event)
	runStateFromContext(ctx).record(err)
}

// StartSpan starts a child operation on the active run and adds it to the
// returned context. It is a no-op when ctx has no active run.
func StartSpan(ctx context.Context, start SpanStart) (context.Context, error) {
	ctx = normalizedContext(ctx)
	spanCtx, span, err := runFromContext(ctx).StartSpan(ctx, start)
	if err != nil {
		runStateFromContext(ctx).record(err)
		return ctx, err
	}
	if span == nil {
		span = noopSpan{}
	}
	if spanCtx == nil {
		spanCtx = ctx
	}

	spanCtx = Init(spanCtx, RecorderFromContext(ctx))
	state := runStateFromContext(ctx)
	if state == nil {
		state = &runState{run: noopRun{}}
	}
	spanCtx = context.WithValue(spanCtx, runContextKey{}, state)
	return context.WithValue(spanCtx, spanContextKey{}, span), nil
}

// EndSpan ends the active span and retains any recorder error.
func EndSpan(ctx context.Context, end SpanEnd) {
	err := spanFromContext(ctx).End(normalizedContext(ctx), end)
	runStateFromContext(ctx).record(err)
}

// Snapshot records conversation state and retains any recorder error.
func Snapshot(ctx context.Context, state SessionState) {
	err := runFromContext(ctx).Snapshot(normalizedContext(ctx), state)
	runStateFromContext(ctx).record(err)
}

// CloseRun finalizes the active run.
func CloseRun(ctx context.Context, outcome RunOutcome) error {
	err := runFromContext(ctx).Close(normalizedContext(ctx), outcome)
	runStateFromContext(ctx).record(err)
	return err
}

func runFromContext(ctx context.Context) Run {
	if state := runStateFromContext(ctx); state != nil && state.run != nil {
		return state.run
	}
	return noopRun{}
}

func runStateFromContext(ctx context.Context) *runState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(runContextKey{}).(*runState)
	return state
}

func spanFromContext(ctx context.Context) Span {
	if ctx != nil {
		if span, ok := ctx.Value(spanContextKey{}).(Span); ok && span != nil {
			return span
		}
	}
	return noopSpan{}
}

func (s *runState) record(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	s.err = errors.Join(s.err, err)
	s.mu.Unlock()
}

func (s *runState) checkpoint() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	err := s.err
	s.err = nil
	s.mu.Unlock()
	return err
}
