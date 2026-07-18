package tracing

import (
	"context"
	"errors"
	"testing"
)

func TestCheckpointReturnsAndClearsRecordingErrors(t *testing.T) {
	want := errors.New("record failed")
	run := &scopeTestRun{eventErr: want}
	ctx := Init(context.Background(), scopeTestRecorder{run: run})
	ctx, err := StartRun(ctx, RunMeta{})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_ = Record(ctx, Event{Kind: KindUserInput})
	err = Checkpoint(ctx)
	if !IsRecordingError(err) || !errors.Is(err, want) {
		t.Fatalf("expected recording error %v, got %v", want, err)
	}
	if err := Checkpoint(ctx); err != nil {
		t.Fatalf("expected checkpoint to clear the error, got %v", err)
	}
}

func TestRunScopeFinalizesSnapshotAndOutcome(t *testing.T) {
	run := &scopeTestRun{}
	ctx := Init(context.Background(), scopeTestRecorder{run: run})
	_, scope, err := BeginRun(ctx, RunMeta{})
	if err != nil {
		t.Fatalf("begin run: %v", err)
	}
	scope.SetReason(EndReasonEOF)

	var runErr error
	scope.End(&runErr, func() SessionState { return SessionState{} })
	if runErr != nil {
		t.Fatalf("end run: %v", runErr)
	}
	if run.snapshots != 1 {
		t.Fatalf("expected one final snapshot, got %d", run.snapshots)
	}
	if run.outcome.Status != StatusSuccess || run.outcome.Reason != EndReasonEOF {
		t.Fatalf("unexpected outcome: %#v", run.outcome)
	}
}

func TestSpanScopeDerivesFailureOutcome(t *testing.T) {
	run := &scopeTestRun{}
	ctx := Init(context.Background(), scopeTestRecorder{run: run})
	ctx, err := StartRun(ctx, RunMeta{})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	ctx, span, err := BeginSpan(ctx, SpanStart{Kind: SpanLLMCall})
	if err != nil {
		t.Fatalf("begin span: %v", err)
	}
	want := errors.New("provider failed")
	span.End(want, "response")
	if err := Checkpoint(ctx); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if run.span.end.Status != StatusFailure || run.span.end.Error == nil || run.span.end.Error.Message != want.Error() {
		t.Fatalf("unexpected span outcome: %#v", run.span.end)
	}
}

type scopeTestRecorder struct {
	run Run
}

func (r scopeTestRecorder) StartRun(ctx context.Context, _ RunMeta) (context.Context, Run, error) {
	return ctx, r.run, nil
}

type scopeTestRun struct {
	eventErr  error
	snapshots int
	outcome   RunOutcome
	span      *scopeTestSpan
}

func (r *scopeTestRun) Event(context.Context, Event) error {
	return r.eventErr
}

func (r *scopeTestRun) StartSpan(ctx context.Context, _ SpanStart) (context.Context, Span, error) {
	r.span = &scopeTestSpan{}
	return ctx, r.span, nil
}

func (r *scopeTestRun) Snapshot(context.Context, SessionState) error {
	r.snapshots++
	return nil
}

func (r *scopeTestRun) Close(_ context.Context, outcome RunOutcome) error {
	r.outcome = outcome
	return nil
}

type scopeTestSpan struct {
	end SpanEnd
}

func (s *scopeTestSpan) End(_ context.Context, end SpanEnd) error {
	s.end = end
	return nil
}
