package tracing

import (
	"context"
	"testing"

	"latentdream/harness/internal/logging"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogRecorderLogsRunLifecycle(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logging.SetLogger(zap.New(core))
	t.Cleanup(func() { logging.SetLogger(zap.NewNop()) })

	ctx, err := StartRun(Init(context.Background(), LogRecorder()), RunMeta{Commit: "abc123"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	Record(ctx, Event{Kind: KindUserInput})
	spanCtx, err := StartSpan(ctx, SpanStart{Kind: SpanLLMCall})
	if err != nil {
		t.Fatalf("start span: %v", err)
	}
	EndSpan(spanCtx, SpanEnd{Status: StatusSuccess})
	Snapshot(ctx, SessionState{})
	if err := CloseRun(ctx, RunOutcome{Status: StatusSuccess, Reason: EndReasonExit}); err != nil {
		t.Fatalf("close run: %v", err)
	}

	for _, message := range []string{"StartRun", "Event", "span", "End", "Snapshot", "Close"} {
		if logs.FilterMessage(message).Len() != 1 {
			t.Fatalf("expected one %q log entry, got %d", message, logs.FilterMessage(message).Len())
		}
	}
}
