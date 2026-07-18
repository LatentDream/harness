package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"latentdream/harness/internal/llm"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime/command"
)

func TestRunSendsUserInputAndWritesResponse(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "  hello  "}, {text: "/exit"}}}
	aiProvider := &fakeProvider{responses: []provider.Response{{Message: llm.Message{Role: "assistant", Content: "world"}}}}

	runtime := New(aiProvider, userInput)
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to exit cleanly, got %v", err)
	}

	if len(aiProvider.queries) != 1 {
		t.Fatalf("expected one query, got %d", len(aiProvider.queries))
	}
	expectedMessages := []llm.Message{{Role: "user", Content: "  hello  "}}
	if !reflect.DeepEqual(aiProvider.queries[0].Messages, expectedMessages) {
		t.Fatalf("expected messages %#v, got %#v", expectedMessages, aiProvider.queries[0].Messages)
	}
	if !reflect.DeepEqual(userInput.responses, []string{"world"}) {
		t.Fatalf("expected response world, got %#v", userInput.responses)
	}
}

func TestRunKeepsConversationHistory(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "hello"}, {text: "again"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{responses: []provider.Response{
		{Message: llm.Message{Role: "assistant", Content: "first"}},
		{Message: llm.Message{Role: "assistant", Content: "second"}},
	}}

	runtime := New(aiProvider, userInput)
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to exit cleanly, got %v", err)
	}

	if len(aiProvider.queries) != 2 {
		t.Fatalf("expected two queries, got %d", len(aiProvider.queries))
	}
	expectedSecondQuery := []llm.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "first"},
		{Role: "user", Content: "again"},
	}
	if !reflect.DeepEqual(aiProvider.queries[1].Messages, expectedSecondQuery) {
		t.Fatalf("expected second query %#v, got %#v", expectedSecondQuery, aiProvider.queries[1].Messages)
	}
}

func TestRunExecutesReadToolCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}
	arguments, err := json.Marshal(map[string]string{"filePath": path})
	if err != nil {
		t.Fatalf("marshal tool arguments: %v", err)
	}

	userInput := &scriptedInput{receives: []receiveResult{{text: "read the file"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{responses: []provider.Response{
		{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID:        "call_1",
			Name:      "read",
			Arguments: arguments,
		}}}},
		{Message: llm.Message{Role: "assistant", Content: "read complete"}},
	}}

	runtime := New(aiProvider, userInput)
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to exit cleanly, got %v", err)
	}

	if !reflect.DeepEqual(userInput.responses, []string{"read complete"}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
	if len(aiProvider.queries) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(aiProvider.queries))
	}
	if len(aiProvider.queries[0].Tools) != 1 || aiProvider.queries[0].Tools[0].Name != "read" {
		t.Fatalf("expected read tool definition, got %#v", aiProvider.queries[0].Tools)
	}

	secondMessages := aiProvider.queries[1].Messages
	if len(secondMessages) != 3 {
		t.Fatalf("expected user, assistant tool call, and tool result, got %#v", secondMessages)
	}
	toolResult := secondMessages[2]
	if toolResult.Role != "tool" || toolResult.ToolCallID != "call_1" || !strings.Contains(toolResult.Content, "1: hello") {
		t.Fatalf("unexpected tool result message: %#v", toolResult)
	}
}

func TestRunReturnsNilOnEOF(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{err: io.EOF}}}
	aiProvider := &fakeProvider{}

	runtime := New(aiProvider, userInput)
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected EOF to exit cleanly, got %v", err)
	}
	if len(aiProvider.queries) != 0 {
		t.Fatalf("expected no provider calls, got %d", len(aiProvider.queries))
	}
}

func TestRunWritesProviderErrorsAndContinues(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "broken"}, {text: "hello"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{
		errors:    []error{errors.New("provider down")},
		responses: []provider.Response{{Message: llm.Message{Role: "assistant", Content: "world"}}},
	}

	runtime := New(aiProvider, userInput)
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to continue after provider error, got %v", err)
	}

	if !reflect.DeepEqual(userInput.responses, []string{"error: provider down", "world"}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
	if len(aiProvider.queries) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(aiProvider.queries))
	}
	expectedSecondQuery := []llm.Message{{Role: "user", Content: "hello"}}
	if !reflect.DeepEqual(aiProvider.queries[1].Messages, expectedSecondQuery) {
		t.Fatalf("expected failed message to be removed from history, got %#v", aiProvider.queries[1].Messages)
	}
}

func TestRunHandlesUnknownCommandWithoutProviderCall(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "/unknown"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{}

	runtime := New(aiProvider, userInput)
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to handle unknown command cleanly, got %v", err)
	}

	if len(aiProvider.queries) != 0 {
		t.Fatalf("expected no provider calls, got %d", len(aiProvider.queries))
	}
	if !reflect.DeepEqual(userInput.responses, []string{"unknown command: /unknown"}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
}

func TestRunWritesHelpWithoutProviderCall(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "/help"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{}

	runtime := New(aiProvider, userInput)
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to handle help cleanly, got %v", err)
	}

	if len(aiProvider.queries) != 0 {
		t.Fatalf("expected no provider calls, got %d", len(aiProvider.queries))
	}
	if !reflect.DeepEqual(userInput.responses, []string{command.DefaultRegistry().Help()}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
}

type receiveResult struct {
	text string
	err  error
}

type scriptedInput struct {
	receives  []receiveResult
	responses []string
}

func (s *scriptedInput) Receive() (string, error) {
	if len(s.receives) == 0 {
		return "", io.EOF
	}

	result := s.receives[0]
	s.receives = s.receives[1:]
	return result.text, result.err
}

func (s *scriptedInput) Write(response string) error {
	s.responses = append(s.responses, response)
	return nil
}

func (s *scriptedInput) Writef(format string, args ...any) error {
	return nil
}

func (s *scriptedInput) WriteErr(response string) error {
	return nil
}

func (s *scriptedInput) WriteErrf(format string, args ...any) error {
	return nil
}

type fakeProvider struct {
	queries   []llm.Request
	responses []provider.Response
	errors    []error
}

func (f *fakeProvider) Current() provider.Selection {
	return provider.Selection{Provider: "fake", Model: "test"}
}

func (f *fakeProvider) Available() []provider.Selection {
	return []provider.Selection{{Provider: "fake", Model: "test"}}
}

func (f *fakeProvider) Use(providerName string, modelName string) error {
	return nil
}

func (f *fakeProvider) Send(ctx context.Context, query llm.Request) (provider.Response, error) {
	query.Messages = append([]llm.Message(nil), query.Messages...)
	f.queries = append(f.queries, query)

	if len(f.errors) > 0 {
		err := f.errors[0]
		f.errors = f.errors[1:]
		if err != nil {
			return provider.Response{}, err
		}
	}
	if len(f.responses) == 0 {
		return provider.Response{Message: llm.Message{Role: "assistant", Content: ""}}, nil
	}

	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}
