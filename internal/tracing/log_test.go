package tracing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"latentdream/harness/internal/logging"
	"latentdream/harness/internal/session/llm"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogRecorderLogsRunLifecycle(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logging.SetLogger(zap.New(core))
	t.Cleanup(func() { logging.SetLogger(zap.NewNop()) })

	pathTemplate := filepath.Join(t.TempDir(), "session", snapshotIDPlaceholder+".json")
	ctx, err := StartRun(Init(context.Background(), LogRecorder(pathTemplate, "session-id")), RunMeta{Commit: "abc123"})
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

func TestLogRecorderWritesSnapshotsToSessionDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	recorder := LogRecorder("~/.harness/logs/{sessionId}/{snapshot_id}.json", "run-123")
	ctx, err := StartRun(Init(context.Background(), recorder), RunMeta{})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	states := []SessionState{
		{Conversation: []llm.Message{{Role: llm.RoleUser, Content: "first"}}},
		{Conversation: []llm.Message{
			{Role: llm.RoleUser, Content: "first"},
			{Role: llm.RoleAssistant, Content: "second"},
		}},
	}
	for _, state := range states {
		Snapshot(ctx, state)
	}
	if err := Checkpoint(ctx); err != nil {
		t.Fatalf("write snapshots: %v", err)
	}

	for index, expected := range states {
		path := filepath.Join(home, ".harness", "logs", "run-123", strconv.Itoa(index+1)+".json")
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read snapshot %d: %v", index+1, err)
		}
		var actual SessionState
		if err := json.Unmarshal(contents, &actual); err != nil {
			t.Fatalf("decode snapshot %d: %v", index+1, err)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("snapshot %d: got %#v, want %#v", index+1, actual, expected)
		}
	}
}

func TestLogRecorderReportsSnapshotWriteFailure(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatalf("create parent file: %v", err)
	}
	pathTemplate := filepath.Join(parent, snapshotIDPlaceholder+".json")
	ctx, err := StartRun(Init(context.Background(), LogRecorder(pathTemplate, "run-123")), RunMeta{})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	Snapshot(ctx, SessionState{})
	if err := Checkpoint(ctx); err == nil {
		t.Fatal("expected snapshot write failure")
	}
}
