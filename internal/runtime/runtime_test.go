package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime/command"
	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/model"
	"latentdream/harness/internal/tracing"
)

func TestRunRecordsTraceLifecycle(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "hello"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{responses: []provider.Response{{
		Provider: "fake",
		Model:    "test",
		Message:  llm.Message{Role: llm.RoleAssistant, Content: "world"},
	}}}
	recorder := &recordingTraceRecorder{run: &recordingTraceRun{}}

	runtime := New(aiProvider, userInput)
	ctx := tracing.Init(context.Background(), recorder)
	if err := runtime.Run(ctx); err != nil {
		t.Fatalf("expected runtime to exit cleanly, got %v", err)
	}

	if recorder.starts != 1 {
		t.Fatalf("expected one trace run, got %d", recorder.starts)
	}
	if recorder.run.outcome.Status != tracing.StatusSuccess || recorder.run.outcome.Reason != tracing.EndReasonExit {
		t.Fatalf("unexpected run outcome: %#v", recorder.run.outcome)
	}
	if recorder.run.snapshots != 3 {
		t.Fatalf("expected initial, turn, and final snapshots, got %d", recorder.run.snapshots)
	}

	wantEvents := []tracing.Kind{
		tracing.KindUserInput,
		tracing.KindOutput,
		tracing.KindOutput,
		tracing.KindAssistantMessage,
		tracing.KindOutput,
		tracing.KindUserInput,
	}
	if got := traceEventKinds(recorder.run.events); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("expected events %#v, got %#v", wantEvents, got)
	}
	wantSpans := []tracing.SpanKind{tracing.SpanTurn, tracing.SpanLLMCall, tracing.SpanCommand}
	if got := traceSpanKinds(recorder.run.spans); !reflect.DeepEqual(got, wantSpans) {
		t.Fatalf("expected spans %#v, got %#v", wantSpans, got)
	}
	for _, span := range recorder.run.spans {
		if span.ends != 1 || span.end.Status != tracing.StatusSuccess {
			t.Fatalf("span was not completed successfully: %#v", span)
		}
	}
	if recorder.run.events[0].TurnID == "" || recorder.run.spans[0].start.TurnID != recorder.run.events[0].TurnID {
		t.Fatal("user input was not correlated with its turn")
	}
}

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
	expectedMessages := []llm.Message{
		{Role: llm.RoleSystem, Content: runtime.Session.BuildSystemPrompt()},
		{Role: "user", Content: "  hello  "},
	}
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
		{Role: llm.RoleSystem, Content: runtime.Session.BuildSystemPrompt()},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "first"},
		{Role: "user", Content: "again"},
	}
	if !reflect.DeepEqual(aiProvider.queries[1].Messages, expectedSecondQuery) {
		t.Fatalf("expected second query %#v, got %#v", expectedSecondQuery, aiProvider.queries[1].Messages)
	}

	expectedConversation := []llm.Message{
		{Role: llm.RoleSystem, Content: runtime.Session.BuildSystemPrompt()},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "first"},
		{Role: "user", Content: "again"},
		{Role: "assistant", Content: "second"},
	}
	if !reflect.DeepEqual(runtime.Session.Conversation, expectedConversation) {
		t.Fatalf("expected session conversation %#v, got %#v", expectedConversation, runtime.Session.Conversation)
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

	recorder := &recordingTraceRecorder{run: &recordingTraceRun{}}
	runtime := New(aiProvider, userInput)
	if err := runtime.Run(tracing.Init(context.Background(), recorder)); err != nil {
		t.Fatalf("expected runtime to exit cleanly, got %v", err)
	}

	if !reflect.DeepEqual(userInput.responses, []string{"read complete"}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
	expectedStatuses := []string{"inference", "", "Reading file " + path, "", "inference", ""}
	if !reflect.DeepEqual(userInput.statuses, expectedStatuses) {
		t.Fatalf("expected statuses %#v, got %#v", expectedStatuses, userInput.statuses)
	}
	if len(aiProvider.queries) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(aiProvider.queries))
	}
	if got := toolNames(aiProvider.queries[0].Tools); !reflect.DeepEqual(got, []string{"read", "write", "glob", "grep"}) {
		t.Fatalf("expected default tool definitions, got %#v", aiProvider.queries[0].Tools)
	}

	secondMessages := aiProvider.queries[1].Messages
	if len(secondMessages) != 4 {
		t.Fatalf("expected user, assistant tool call, and tool result, got %#v", secondMessages)
	}
	toolResult := secondMessages[3]
	if toolResult.Role != "tool" || toolResult.ToolCallID != "call_1" || !strings.Contains(toolResult.Content, "1: hello") {
		t.Fatalf("unexpected tool result message: %#v", toolResult)
	}
	toolSpan := findTraceSpan(recorder.run.spans, tracing.SpanToolCall)
	if toolSpan == nil || toolSpan.ends != 1 || toolSpan.end.Status != tracing.StatusSuccess {
		t.Fatalf("tool call was not traced successfully: %#v", toolSpan)
	}
}

func TestRunTracesToolFailureAndLetsModelRecover(t *testing.T) {
	arguments := json.RawMessage(`{"value":"bad"}`)
	userInput := &scriptedInput{receives: []receiveResult{{text: "run it"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{responses: []provider.Response{
		{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID:        "call_1",
			Name:      "fail",
			Arguments: arguments,
		}}}},
		{Message: llm.Message{Role: llm.RoleAssistant, Content: "recovered"}},
	}}
	recorder := &recordingTraceRecorder{run: &recordingTraceRun{}}
	runtime := New(aiProvider, userInput)
	runtime.Tools = []model.Tool{failingTool{}}

	if err := runtime.Run(tracing.Init(context.Background(), recorder)); err != nil {
		t.Fatalf("expected model to recover from tool failure, got %v", err)
	}
	if !reflect.DeepEqual(userInput.responses, []string{"recovered"}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
	toolSpan := findTraceSpan(recorder.run.spans, tracing.SpanToolCall)
	if toolSpan == nil || toolSpan.ends != 1 || toolSpan.end.Status != tracing.StatusFailure {
		t.Fatalf("failed tool call was not traced as a failure: %#v", toolSpan)
	}
	if toolSpan.end.Error == nil || !strings.Contains(toolSpan.end.Error.Message, "tool failed") {
		t.Fatalf("tool failure was not retained: %#v", toolSpan.end.Error)
	}
	toolResult := aiProvider.queries[1].Messages[3]
	if !strings.Contains(toolResult.Content, "error: tool failed") {
		t.Fatalf("tool failure was not returned to the model: %#v", toolResult)
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

func TestRunReturnsCancellationWithoutWritingAnError(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "hello"}}}
	aiProvider := &fakeProvider{errors: []error{context.Canceled}}
	recorder := &recordingTraceRecorder{run: &recordingTraceRun{}}

	runtime := New(aiProvider, userInput)
	err := runtime.Run(tracing.Init(context.Background(), recorder))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if len(userInput.responses) != 0 {
		t.Fatalf("expected no cancellation error output, got %#v", userInput.responses)
	}
	if recorder.run.outcome.Status != tracing.StatusCancelled || recorder.run.outcome.Reason != tracing.EndReasonCancelled {
		t.Fatalf("unexpected run outcome: %#v", recorder.run.outcome)
	}
}

func TestRunWritesProviderErrorsAndContinues(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "broken"}, {text: "hello"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{
		errors:    []error{errors.New("provider down")},
		responses: []provider.Response{{Message: llm.Message{Role: "assistant", Content: "world"}}},
	}

	recorder := &recordingTraceRecorder{run: &recordingTraceRun{}}
	runtime := New(aiProvider, userInput)
	if err := runtime.Run(tracing.Init(context.Background(), recorder)); err != nil {
		t.Fatalf("expected runtime to continue after provider error, got %v", err)
	}

	if !reflect.DeepEqual(userInput.responses, []string{"error: provider down", "world"}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
	if len(aiProvider.queries) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(aiProvider.queries))
	}
	expectedSecondQuery := []llm.Message{
		{Role: llm.RoleSystem, Content: runtime.Session.BuildSystemPrompt()},
		{Role: "user", Content: "hello"},
	}
	if !reflect.DeepEqual(aiProvider.queries[1].Messages, expectedSecondQuery) {
		t.Fatalf("expected failed message to be removed from history, got %#v", aiProvider.queries[1].Messages)
	}
	if !containsTraceEvent(recorder.run.events, tracing.KindSessionRollback) {
		t.Fatal("expected the failed turn rollback to be traced")
	}
	if findTraceSpanWithStatus(recorder.run.spans, tracing.SpanLLMCall, tracing.StatusFailure) == nil {
		t.Fatal("expected the failed provider call to be traced")
	}
	if findTraceSpanWithStatus(recorder.run.spans, tracing.SpanTurn, tracing.StatusFailure) == nil {
		t.Fatal("expected the failed turn to be traced")
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
	statuses  []string
}

func (s *scriptedInput) Receive(context.Context) (string, error) {
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

func (s *scriptedInput) SetStatus(status string) error {
	s.statuses = append(s.statuses, status)
	return nil
}

func (s *scriptedInput) SetStatusf(format string, args ...any) error {
	return s.SetStatus(fmt.Sprintf(format, args...))
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

func toolNames(definitions []llm.ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
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

type failingTool struct{}

func (failingTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: "fail", Parameters: llm.Schema{Type: "object"}}
}

func (failingTool) Status(json.RawMessage) string {
	return "Failing tool"
}

func (failingTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", errors.New("tool failed")
}

type recordingTraceRecorder struct {
	run    *recordingTraceRun
	starts int
	meta   tracing.RunMeta
}

func (r *recordingTraceRecorder) StartRun(ctx context.Context, meta tracing.RunMeta) (context.Context, tracing.Run, error) {
	r.starts++
	r.meta = meta
	return ctx, r.run, nil
}

type recordingTraceRun struct {
	events    []tracing.Event
	spans     []*recordingTraceSpan
	snapshots int
	outcome   tracing.RunOutcome
}

func (r *recordingTraceRun) Event(_ context.Context, event tracing.Event) error {
	r.events = append(r.events, event)
	return nil
}

func (r *recordingTraceRun) StartSpan(ctx context.Context, start tracing.SpanStart) (context.Context, tracing.Span, error) {
	span := &recordingTraceSpan{start: start}
	r.spans = append(r.spans, span)
	return ctx, span, nil
}

func (r *recordingTraceRun) Snapshot(context.Context, tracing.SessionState) error {
	r.snapshots++
	return nil
}

func (r *recordingTraceRun) Close(_ context.Context, outcome tracing.RunOutcome) error {
	r.outcome = outcome
	return nil
}

type recordingTraceSpan struct {
	start tracing.SpanStart
	end   tracing.SpanEnd
	ends  int
}

func (s *recordingTraceSpan) End(_ context.Context, end tracing.SpanEnd) error {
	s.end = end
	s.ends++
	return nil
}

func traceEventKinds(events []tracing.Event) []tracing.Kind {
	kinds := make([]tracing.Kind, len(events))
	for index, event := range events {
		kinds[index] = event.Kind
	}
	return kinds
}

func traceSpanKinds(spans []*recordingTraceSpan) []tracing.SpanKind {
	kinds := make([]tracing.SpanKind, len(spans))
	for index, span := range spans {
		kinds[index] = span.start.Kind
	}
	return kinds
}

func findTraceSpan(spans []*recordingTraceSpan, kind tracing.SpanKind) *recordingTraceSpan {
	for _, span := range spans {
		if span.start.Kind == kind {
			return span
		}
	}
	return nil
}

func findTraceSpanWithStatus(spans []*recordingTraceSpan, kind tracing.SpanKind, status tracing.Status) *recordingTraceSpan {
	for _, span := range spans {
		if span.start.Kind == kind && span.end.Status == status {
			return span
		}
	}
	return nil
}

func containsTraceEvent(events []tracing.Event, kind tracing.Kind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}
