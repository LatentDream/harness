package tracing

import (
	"context"
	"errors"
	"testing"
)

func TestContextLifecycleUsesConfiguredRecorder(t *testing.T) {
	span := &recordingSpan{}
	run := &recordingRun{span: span}
	recorder := &recordingRecorder{run: run}
	root := Init(context.Background(), recorder)

	runCtx, err := StartRun(root, RunMeta{Commit: "abc123"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if recorder.starts != 1 || recorder.meta.Commit != "abc123" {
		t.Fatalf("unexpected run start: starts=%d meta=%#v", recorder.starts, recorder.meta)
	}
	if RecorderFromContext(runCtx) != recorder {
		t.Fatal("configured recorder was not propagated to the run context")
	}

	if err := Record(runCtx, Event{Kind: KindUserInput}); err != nil {
		t.Fatalf("record event: %v", err)
	}
	spanCtx, err := StartSpan(runCtx, SpanStart{Kind: SpanLLMCall})
	if err != nil {
		t.Fatalf("start span: %v", err)
	}
	if err := EndSpan(spanCtx, SpanEnd{Status: StatusSuccess}); err != nil {
		t.Fatalf("end span: %v", err)
	}
	if err := Snapshot(spanCtx, SessionState{}); err != nil {
		t.Fatalf("record snapshot: %v", err)
	}
	if err := CloseRun(spanCtx, RunOutcome{Status: StatusSuccess, Reason: EndReasonExit}); err != nil {
		t.Fatalf("close run: %v", err)
	}

	if run.events != 1 || run.spans != 1 || run.snapshots != 1 || run.closes != 1 {
		t.Fatalf("unexpected run calls: %#v", run)
	}
	if span.ends != 1 {
		t.Fatalf("expected one span end, got %d", span.ends)
	}
}

func TestContextHelpersDefaultToNoop(t *testing.T) {
	ctx, err := StartRun(nil, RunMeta{})
	if err != nil {
		t.Fatalf("start no-op run: %v", err)
	}
	if err := Record(ctx, Event{}); err != nil {
		t.Fatalf("record no-op event: %v", err)
	}
	ctx, err = StartSpan(ctx, SpanStart{})
	if err != nil {
		t.Fatalf("start no-op span: %v", err)
	}
	if err := EndSpan(ctx, SpanEnd{}); err != nil {
		t.Fatalf("end no-op span: %v", err)
	}
	if err := Snapshot(ctx, SessionState{}); err != nil {
		t.Fatalf("record no-op snapshot: %v", err)
	}
	if err := CloseRun(ctx, RunOutcome{}); err != nil {
		t.Fatalf("close no-op run: %v", err)
	}
}

func TestContextRecordersAreIsolated(t *testing.T) {
	firstRun := &recordingRun{}
	secondRun := &recordingRun{}
	firstCtx, err := StartRun(Init(context.Background(), &recordingRecorder{run: firstRun}), RunMeta{})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	secondCtx, err := StartRun(Init(context.Background(), &recordingRecorder{run: secondRun}), RunMeta{})
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}

	if err := Record(firstCtx, Event{}); err != nil {
		t.Fatalf("record first event: %v", err)
	}
	if err := Record(secondCtx, Event{}); err != nil {
		t.Fatalf("record second event: %v", err)
	}

	if firstRun.events != 1 || secondRun.events != 1 {
		t.Fatalf("contexts shared recorder state: first=%d second=%d", firstRun.events, secondRun.events)
	}
}

func TestStartRunPropagatesRecorderError(t *testing.T) {
	want := errors.New("start failed")
	ctx := Init(context.Background(), &recordingRecorder{err: want})

	returned, err := StartRun(ctx, RunMeta{})
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
	if returned != ctx {
		t.Fatal("expected the original context when starting the run failed")
	}
}

type recordingRecorder struct {
	run    Run
	err    error
	starts int
	meta   RunMeta
}

func (r *recordingRecorder) StartRun(ctx context.Context, meta RunMeta) (context.Context, Run, error) {
	r.starts++
	r.meta = meta
	return ctx, r.run, r.err
}

type recordingRun struct {
	span      Span
	events    int
	spans     int
	snapshots int
	closes    int
}

func (r *recordingRun) Event(context.Context, Event) error {
	r.events++
	return nil
}

func (r *recordingRun) StartSpan(ctx context.Context, _ SpanStart) (context.Context, Span, error) {
	r.spans++
	return ctx, r.span, nil
}

func (r *recordingRun) Snapshot(context.Context, SessionState) error {
	r.snapshots++
	return nil
}

func (r *recordingRun) Close(context.Context, RunOutcome) error {
	r.closes++
	return nil
}

type recordingSpan struct {
	ends int
}

func (s *recordingSpan) End(context.Context, SpanEnd) error {
	s.ends++
	return nil
}
