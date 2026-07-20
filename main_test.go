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
	"latentdream/harness/internal/session/llm"
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
