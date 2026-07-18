package tracing

import "context"

type noopRecorder struct{}
type noopRun struct{}
type noopSpan struct{}

// Noop returns a recorder that discards all tracing operations.
func Noop() Recorder {
	return noopRecorder{}
}

func (noopRecorder) StartRun(ctx context.Context, _ RunMeta) (context.Context, Run, error) {
	return normalizedContext(ctx), noopRun{}, nil
}

func (noopRun) Event(context.Context, Event) error {
	return nil
}

func (noopRun) StartSpan(ctx context.Context, _ SpanStart) (context.Context, Span, error) {
	return normalizedContext(ctx), noopSpan{}, nil
}

func (noopRun) Snapshot(context.Context, SessionState) error {
	return nil
}

func (noopRun) Close(context.Context, RunOutcome) error {
	return nil
}

func (noopSpan) End(context.Context, SpanEnd) error {
	return nil
}
