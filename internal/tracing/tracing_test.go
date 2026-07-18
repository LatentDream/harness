package tracing

import (
	"context"
	"errors"
	"testing"

	"latentdream/harness/internal/session/llm"
)

func TestNoopProvidesCompleteLifecycle(t *testing.T) {
	ctx, run, err := Noop().StartRun(nil, RunMeta{})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if ctx == nil {
		t.Fatal("expected a non-nil context")
	}
	if run == nil {
		t.Fatal("expected a non-nil run")
	}

	if err := run.Event(ctx, Event{Kind: KindUserInput}); err != nil {
		t.Fatalf("record event: %v", err)
	}
	spanCtx, span, err := run.StartSpan(nil, SpanStart{Kind: SpanLLMCall})
	if err != nil {
		t.Fatalf("start span: %v", err)
	}
	if spanCtx == nil || span == nil {
		t.Fatal("expected a non-nil span and context")
	}
	if err := span.End(spanCtx, SpanEnd{Status: StatusSuccess}); err != nil {
		t.Fatalf("end span: %v", err)
	}
	if err := run.Snapshot(ctx, SessionState{}); err != nil {
		t.Fatalf("record snapshot: %v", err)
	}
	if err := run.Close(ctx, RunOutcome{Status: StatusSuccess, Reason: EndReasonEOF}); err != nil {
		t.Fatalf("close run: %v", err)
	}
}

func TestTraceSessionUsesLatestSnapshot(t *testing.T) {
	arguments := []byte(`{"path":"original"}`)
	trace := &Trace{Snapshots: []SessionSnapshot{
		{Sequence: 2, Conversation: []llm.Message{{Role: llm.RoleUser, Content: "first"}}},
		{Sequence: 5, Conversation: []llm.Message{{
			Role:    llm.RoleUser,
			Content: "latest",
			ToolCalls: []llm.ToolCall{{
				Arguments: arguments,
			}},
		}}},
	}}

	state, err := trace.Session()
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if got := state.Conversation[0].Content; got != "latest" {
		t.Fatalf("expected latest snapshot, got %q", got)
	}

	state.Conversation[0].Content = "changed"
	state.Conversation[0].ToolCalls[0].Arguments[9] = 'X'
	if got := trace.Snapshots[1].Conversation[0].Content; got != "latest" {
		t.Fatalf("session must not alias persisted snapshot, got %q", got)
	}
	if got := string(trace.Snapshots[1].Conversation[0].ToolCalls[0].Arguments); got != `{"path":"original"}` {
		t.Fatalf("tool arguments must not alias persisted snapshot, got %q", got)
	}
}

func TestTraceSessionAtUsesLatestSnapshotAtSequence(t *testing.T) {
	trace := &Trace{Snapshots: []SessionSnapshot{
		{Sequence: 2, Conversation: []llm.Message{{Content: "first"}}},
		{Sequence: 5, Conversation: []llm.Message{{Content: "second"}}},
		{Sequence: 9, Conversation: []llm.Message{{Content: "third"}}},
	}}

	state, err := trace.SessionAt(7)
	if err != nil {
		t.Fatalf("load session at sequence: %v", err)
	}
	if got := state.Conversation[0].Content; got != "second" {
		t.Fatalf("expected snapshot at sequence 5, got %q", got)
	}
}

func TestTraceSessionReturnsErrNoSnapshot(t *testing.T) {
	tests := []struct {
		name string
		load func() error
	}{
		{
			name: "nil trace",
			load: func() error {
				var trace *Trace
				_, err := trace.Session()
				return err
			},
		},
		{
			name: "empty trace",
			load: func() error {
				_, err := (&Trace{}).Session()
				return err
			},
		},
		{
			name: "before first snapshot",
			load: func() error {
				_, err := (&Trace{Snapshots: []SessionSnapshot{{Sequence: 2}}}).SessionAt(1)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.load(); !errors.Is(err, ErrNoSnapshot) {
				t.Fatalf("expected ErrNoSnapshot, got %v", err)
			}
		})
	}
}

func TestNoopPreservesContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "value")

	runCtx, _, err := Noop().StartRun(ctx, RunMeta{})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if got := runCtx.Value(key{}); got != "value" {
		t.Fatalf("expected context value to be preserved, got %v", got)
	}
}
