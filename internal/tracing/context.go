package tracing

import "context"

type recorderContextKey struct{}
type runContextKey struct{}
type spanContextKey struct{}

// Init adds the process-configured recorder to the root context. A nil
// recorder disables tracing through the no-op implementation.
func Init(ctx context.Context, recorder Recorder) context.Context {
	ctx = normalizedContext(ctx)
	if recorder == nil {
		recorder = Noop()
	}
	return context.WithValue(ctx, recorderContextKey{}, recorder)
}

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
	return context.WithValue(runCtx, runContextKey{}, run), nil
}

// Record records an event on the active run. It is a no-op when ctx has no
// active run.
func Record(ctx context.Context, event Event) error {
	return runFromContext(ctx).Event(normalizedContext(ctx), event)
}

// StartSpan starts a child operation on the active run and adds it to the
// returned context. It is a no-op when ctx has no active run.
func StartSpan(ctx context.Context, start SpanStart) (context.Context, error) {
	ctx = normalizedContext(ctx)
	spanCtx, span, err := runFromContext(ctx).StartSpan(ctx, start)
	if err != nil {
		return ctx, err
	}
	if span == nil {
		span = noopSpan{}
	}
	if spanCtx == nil {
		spanCtx = ctx
	}

	spanCtx = Init(spanCtx, RecorderFromContext(ctx))
	spanCtx = context.WithValue(spanCtx, runContextKey{}, runFromContext(ctx))
	return context.WithValue(spanCtx, spanContextKey{}, span), nil
}

// EndSpan ends the span in ctx. It is a no-op when ctx has no active span.
func EndSpan(ctx context.Context, end SpanEnd) error {
	return spanFromContext(ctx).End(normalizedContext(ctx), end)
}

// Snapshot records conversation state on the active run.
func Snapshot(ctx context.Context, state SessionState) error {
	return runFromContext(ctx).Snapshot(normalizedContext(ctx), state)
}

// CloseRun finalizes the active run.
func CloseRun(ctx context.Context, outcome RunOutcome) error {
	return runFromContext(ctx).Close(normalizedContext(ctx), outcome)
}

func runFromContext(ctx context.Context) Run {
	if ctx != nil {
		if run, ok := ctx.Value(runContextKey{}).(Run); ok && run != nil {
			return run
		}
	}
	return noopRun{}
}

func spanFromContext(ctx context.Context) Span {
	if ctx != nil {
		if span, ok := ctx.Value(spanContextKey{}).(Span); ok && span != nil {
			return span
		}
	}
	return noopSpan{}
}
