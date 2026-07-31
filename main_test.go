package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"latentdream/harness/internal/config"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/logging"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime"
	"latentdream/harness/internal/runtime/command"
	"latentdream/harness/internal/session"
	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tracing"
)

func TestRunSessionsRotatesPersistenceAndConversation(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		Logging: logging.Config{
			Output:   logging.OutputFile,
			FilePath: filepath.Join(root, "logs", "harness.log"),
		},
	}
	cfg.Tracing.FilePath = filepath.Join(root, "traces", "{snapshot_id}.json")

	const firstSessionID = "first-session"
	if err := logging.ConfigureForSession(loggingConfigForSession(cfg.Logging, firstSessionID), firstSessionID); err != nil {
		t.Fatalf("configure initial logger: %v", err)
	}

	frontend := &sessionTestFrontend{submissions: []input.Submission{
		{Text: "/model other/next"},
		{Text: "/new"},
		{Text: "hello"},
		{Text: "/exit"},
	}}
	aiProvider := &sessionTestProvider{}

	err := runSessions(context.Background(), cfg, firstSessionID, aiProvider, frontend, frontend, runtime.Options{})
	if err != nil {
		t.Fatalf("run sessions: %v", err)
	}
	if len(aiProvider.requests) != 1 {
		t.Fatalf("expected one provider request, got %d", len(aiProvider.requests))
	}
	if aiProvider.Current() != (provider.Selection{Provider: "other", Model: "next"}) {
		t.Fatalf("new session did not preserve model selection: %#v", aiProvider.Current())
	}
	messages := aiProvider.requests[0].Messages
	if len(messages) != 2 || messages[0].Role != llm.RoleSystem || messages[1].Role != llm.RoleUser || messages[1].Content != "hello" {
		t.Fatalf("new session retained old conversation: %#v", messages)
	}

	resetCount := 0
	for _, event := range frontend.events {
		if event.Kind == input.EventSessionReset {
			resetCount++
		}
	}
	if resetCount != 1 {
		t.Fatalf("expected one frontend reset, got %d", resetCount)
	}

	if _, err := os.Stat(filepath.Join(root, "traces", "1.json")); err != nil {
		t.Fatalf("first session trace was not persisted: %v", err)
	}
	rotatedTraces, err := filepath.Glob(filepath.Join(root, "traces", "*", "1.json"))
	if err != nil {
		t.Fatalf("list rotated traces: %v", err)
	}
	if len(rotatedTraces) != 1 {
		t.Fatalf("expected one isolated rotated trace, got %#v", rotatedTraces)
	}
	rotatedLogs, err := filepath.Glob(filepath.Join(root, "logs", "*", "harness.log"))
	if err != nil {
		t.Fatalf("list rotated logs: %v", err)
	}
	if len(rotatedLogs) != 1 {
		t.Fatalf("expected one isolated rotated log, got %#v", rotatedLogs)
	}
}

func TestParseStartupArgs(t *testing.T) {
	for _, args := range [][]string{{"--continue"}, {"-c"}} {
		continued, err := parseStartupArgs(args)
		if err != nil || !continued {
			t.Fatalf("parse %v = %v, %v", args, continued, err)
		}
	}
	if _, err := parseStartupArgs([]string{"unexpected"}); err == nil {
		t.Fatal("expected positional argument error")
	}
}

func TestRunPersistentSessionsSwitchesAndHydratesTarget(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	store, err := tracing.NewFileStore(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Create(context.Background(), workspace, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(context.Background(), workspace, "second")
	if err != nil {
		t.Fatal(err)
	}
	secondSession := session.Session{Conversation: []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "old question"},
		{Role: llm.RoleAssistant, Content: "old answer"},
	}}
	ctx := tracing.Init(context.Background(), tracing.SessionRecorder(store, second.ID))
	ctx, run, err := tracing.BeginRun(ctx, tracing.RunMeta{})
	if err != nil {
		t.Fatal(err)
	}
	run.Snapshot(tracing.SessionState{Conversation: secondSession.Conversation})
	if err := run.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	var runErr error
	run.End(&runErr, nil)

	commands := command.DefaultRegistry()
	commands.Register(command.NewSwitchCmd(workspaceSessionResolver{store: store, workspace: workspace}))
	frontend := &sessionTestFrontend{submissions: []input.Submission{{Text: "/switch second.ID"}, {Text: "/exit"}}}
	// Replace the literal selector with the target ID while preserving one scripted stream.
	frontend.submissions[0].Text = "/switch " + second.ID
	cfg := config.Config{Logging: logging.Config{Output: logging.OutputStderr}}
	if err := runPersistentSessions(context.Background(), cfg, store, workspace, first, session.Session{}, &sessionTestProvider{}, frontend, frontend, runtime.Options{Commands: commands}); err != nil {
		t.Fatal(err)
	}
	loaded := false
	for _, event := range frontend.events {
		if event.Kind == input.EventSessionLoaded && event.SessionID == second.ID && len(event.Messages) == 2 {
			loaded = true
		}
	}
	if !loaded {
		t.Fatalf("target session was not hydrated: %#v", frontend.events)
	}
	active, _, err := store.Continue(context.Background(), workspace)
	if err != nil || active.ID != second.ID {
		t.Fatalf("active session=%#v err=%v", active, err)
	}
}

func TestLoggingConfigForSessionDoesNotMutateInput(t *testing.T) {
	original := logging.Config{InitialFields: map[string]any{"component": "test"}}
	configured := loggingConfigForSession(original, "session-123")

	if _, ok := original.InitialFields["sessionId"]; ok {
		t.Fatalf("input fields were mutated: %#v", original.InitialFields)
	}
	want := map[string]any{"component": "test", "sessionId": "session-123"}
	if !reflect.DeepEqual(configured.InitialFields, want) {
		t.Fatalf("configured fields = %#v, want %#v", configured.InitialFields, want)
	}
}

type sessionTestFrontend struct {
	submissions []input.Submission
	events      []input.Event
}

func (f *sessionTestFrontend) Receive(context.Context) (input.Submission, error) {
	if len(f.submissions) == 0 {
		return input.Submission{}, io.EOF
	}
	submission := f.submissions[0]
	f.submissions = f.submissions[1:]
	return submission, nil
}

func (f *sessionTestFrontend) Emit(_ context.Context, event input.Event) error {
	f.events = append(f.events, event)
	return nil
}

type sessionTestProvider struct {
	requests []llm.Request
	current  provider.Selection
}

func (p *sessionTestProvider) Current() provider.Selection {
	if p.current == (provider.Selection{}) {
		return provider.Selection{Provider: "fake", Model: "test"}
	}
	return p.current
}

func (p *sessionTestProvider) Available() []provider.Selection {
	return []provider.Selection{p.Current()}
}

func (p *sessionTestProvider) Use(providerName string, modelName string) error {
	p.current = provider.Selection{Provider: providerName, Model: modelName}
	return nil
}

func (p *sessionTestProvider) Send(_ context.Context, request llm.Request, stream provider.StreamHandler) (provider.Response, error) {
	request.Messages = append([]llm.Message(nil), request.Messages...)
	p.requests = append(p.requests, request)
	if stream != nil {
		if err := stream(provider.StreamEvent{TextDelta: "world"}); err != nil {
			return provider.Response{}, err
		}
	}
	return provider.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "world"}}, nil
}
