package tracing

import (
	"context"

	"latentdream/harness/internal/logging"

	"go.uber.org/zap"
)

type (
	logRecorder struct{}
	logRun      struct{}
	logSpan     struct{}
)

func LogRecorder() Recorder {
	return logRecorder{}
}

func (logRecorder) StartRun(ctx context.Context, metadata RunMeta) (context.Context, Run, error) {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("StartRun", zap.Any("metadata", metadata))
	return ctx, logRun{}, nil
}

func (logRun) Event(ctx context.Context, event Event) error {
	logging.Log(ctx).Debug("Event", zap.Any("event", event))
	return nil
}

func (logRun) StartSpan(ctx context.Context, span SpanStart) (context.Context, Span, error) {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("span", zap.Any("span", span))
	return ctx, logSpan{}, nil
}

func (logRun) Snapshot(ctx context.Context, state SessionState) error {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("Snapshot", zap.Any("state", state))
	return nil
}

func (logRun) Close(ctx context.Context, outcome RunOutcome) error {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("Close", zap.Any("outcome", outcome))
	return nil
}

func (logSpan) End(ctx context.Context, end SpanEnd) error {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("End", zap.Any("end", end))
	return nil
}
