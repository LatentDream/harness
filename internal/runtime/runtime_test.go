package runtime

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"latentdream/harness/internal/provider"
)

func TestRunSendsUserInputAndWritesResponse(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "  hello  "}, {text: "/exit"}}}
	aiProvider := &fakeProvider{responses: []provider.Response{{Message: provider.Message{Role: "assistant", Content: "world"}}}}

	runtime := New(aiProvider, userInput)
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to exit cleanly, got %v", err)
	}

	if len(aiProvider.queries) != 1 {
		t.Fatalf("expected one query, got %d", len(aiProvider.queries))
	}
	expectedMessages := []provider.Message{{Role: "user", Content: "  hello  "}}
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
		{Message: provider.Message{Role: "assistant", Content: "first"}},
		{Message: provider.Message{Role: "assistant", Content: "second"}},
	}}

	runtime := New(aiProvider, userInput)
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to exit cleanly, got %v", err)
	}

	if len(aiProvider.queries) != 2 {
		t.Fatalf("expected two queries, got %d", len(aiProvider.queries))
	}
	expectedSecondQuery := []provider.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "first"},
		{Role: "user", Content: "again"},
	}
	if !reflect.DeepEqual(aiProvider.queries[1].Messages, expectedSecondQuery) {
		t.Fatalf("expected second query %#v, got %#v", expectedSecondQuery, aiProvider.queries[1].Messages)
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
		responses: []provider.Response{{Message: provider.Message{Role: "assistant", Content: "world"}}},
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
	expectedSecondQuery := []provider.Message{{Role: "user", Content: "hello"}}
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
	queries   []provider.Query
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

func (f *fakeProvider) Send(ctx context.Context, query provider.Query) (provider.Response, error) {
	query.Messages = append([]provider.Message(nil), query.Messages...)
	f.queries = append(f.queries, query)

	if len(f.errors) > 0 {
		err := f.errors[0]
		f.errors = f.errors[1:]
		if err != nil {
			return provider.Response{}, err
		}
	}
	if len(f.responses) == 0 {
		return provider.Response{Message: provider.Message{Role: "assistant", Content: ""}}, nil
	}

	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}
