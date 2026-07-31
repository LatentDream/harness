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
	"time"

	"latentdream/harness/internal/input"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime/command"
	"latentdream/harness/internal/session"
	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool"
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

	runtime := New(aiProvider, userInput, userInput, Options{})
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
		tracing.KindOutput,
		tracing.KindOutput,
		tracing.KindOutput,
		tracing.KindAssistantMessage,
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
	payload, ok := recorder.run.events[0].Payload.(tracing.UserInputPayload)
	if !ok || payload.Mode != input.ModeBuild {
		t.Fatalf("user input did not record build mode: %#v", recorder.run.events[0].Payload)
	}
}

func TestRunUsesRestoredSessionWithoutAddingSystemPrompt(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "continue"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{responses: []provider.Response{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}}
	restored := session.Session{Conversation: []llm.Message{
		{Role: llm.RoleSystem, Content: "original system"},
		{Role: llm.RoleUser, Content: "earlier"},
		{Role: llm.RoleAssistant, Content: "earlier answer"},
	}}

	runtime := New(aiProvider, userInput, userInput, Options{InitialSession: &restored})
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	messages := aiProvider.queries[0].Messages
	if len(messages) != 4 || messages[0].Content != "original system" || messages[3].Content != "continue" {
		t.Fatalf("restored messages = %#v", messages)
	}
	if len(restored.Conversation) != 3 {
		t.Fatalf("runtime mutated supplied session: %#v", restored.Conversation)
	}
}

func TestRunSessionReturnsResolvedSwitchAction(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "/switch parser work"}}}
	aiProvider := &fakeProvider{}
	commands := command.DefaultRegistry()
	commands.Register(command.NewSwitchCmd(&runtimeSwitchResolver{sessionID: "target-session"}))
	runtime := New(aiProvider, userInput, userInput, Options{Commands: commands})

	action, err := runtime.RunSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if action != command.ActionSwitchSession || runtime.SwitchSessionID() != "target-session" {
		t.Fatalf("action=%v target=%q", action, runtime.SwitchSessionID())
	}
}

type runtimeSwitchResolver struct{ sessionID string }

func (r *runtimeSwitchResolver) ResolveSession(context.Context, string) (command.SessionSummary, error) {
	return command.SessionSummary{ID: r.sessionID}, nil
}
func (r *runtimeSwitchResolver) ListSessions(context.Context) ([]command.SessionSummary, error) {
	return nil, nil
}

func TestRunSendsUserInputAndWritesResponse(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "  hello  "}, {text: "/exit"}}}
	aiProvider := &fakeProvider{responses: []provider.Response{{Message: llm.Message{Role: "assistant", Content: "world"}}}}

	runtime := New(aiProvider, userInput, userInput, Options{})
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

func TestRunEmitsProgressiveAssistantEventsWithoutDuplicateOutput(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "hello"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{
		responses: []provider.Response{{Message: llm.Message{Role: llm.RoleAssistant, Content: "hello world"}}},
		chunks:    [][]string{{"hello", " world"}},
	}

	runtime := New(aiProvider, userInput, userInput, Options{})
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to exit cleanly, got %v", err)
	}
	if !reflect.DeepEqual(userInput.responses, []string{"hello world"}) {
		t.Fatalf("expected one assembled response, got %#v", userInput.responses)
	}

	var assistantEvents []input.Event
	for _, event := range userInput.events {
		switch event.Kind {
		case input.EventAssistantStarted, input.EventAssistantDelta, input.EventAssistantCompleted, input.EventAssistantAborted:
			assistantEvents = append(assistantEvents, event)
		}
	}
	wantKinds := []input.EventKind{
		input.EventAssistantStarted,
		input.EventAssistantDelta,
		input.EventAssistantDelta,
		input.EventAssistantCompleted,
	}
	gotKinds := make([]input.EventKind, len(assistantEvents))
	for index, event := range assistantEvents {
		gotKinds[index] = event.Kind
		if event.TurnID == "" || event.Round != 1 {
			t.Fatalf("assistant event lacks correlation metadata: %#v", event)
		}
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("expected assistant event lifecycle %#v, got %#v", wantKinds, gotKinds)
	}
	if assistantEvents[1].Text != "hello" || assistantEvents[2].Text != " world" {
		t.Fatalf("unexpected deltas: %#v", assistantEvents)
	}
	if assistantEvents[3].Text != "hello world" {
		t.Fatalf("expected canonical completed text, got %#v", assistantEvents[3])
	}
}

func TestRunAbortsPartialAssistantOutputOnProviderError(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "hello"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{
		responses:    []provider.Response{{Message: llm.Message{Role: llm.RoleAssistant, Content: "partial"}}},
		chunks:       [][]string{{"partial"}},
		streamErrors: []error{errors.New("stream failed")},
	}

	runtime := New(aiProvider, userInput, userInput, Options{})
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to continue after provider error, got %v", err)
	}
	if !reflect.DeepEqual(userInput.responses, []string{"error: stream failed"}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}

	var assistantEvents []input.Event
	for _, event := range userInput.events {
		switch event.Kind {
		case input.EventAssistantStarted, input.EventAssistantDelta, input.EventAssistantCompleted, input.EventAssistantAborted:
			assistantEvents = append(assistantEvents, event)
		}
	}
	want := []input.EventKind{input.EventAssistantStarted, input.EventAssistantDelta, input.EventAssistantAborted}
	assistantKinds := make([]input.EventKind, len(assistantEvents))
	for index, event := range assistantEvents {
		assistantKinds[index] = event.Kind
	}
	if !reflect.DeepEqual(assistantKinds, want) {
		t.Fatalf("expected aborted assistant lifecycle %#v, got %#v", want, assistantKinds)
	}
	if assistantEvents[2].Text != "partial" {
		t.Fatalf("expected aborted event to retain partial text, got %#v", assistantEvents[2])
	}
	var inferenceKinds []input.EventKind
	for _, event := range userInput.events {
		if event.Kind == input.EventInferenceStarted || event.Kind == input.EventInferenceEnded {
			inferenceKinds = append(inferenceKinds, event.Kind)
		}
	}
	if want := []input.EventKind{input.EventInferenceStarted, input.EventInferenceEnded}; !reflect.DeepEqual(inferenceKinds, want) {
		t.Fatalf("expected inference cleanup %#v, got %#v", want, inferenceKinds)
	}
}

func TestRunPlanModeUsesReadOnlyToolsAndEphemeralInstruction(t *testing.T) {
	arguments := json.RawMessage(`{"filePath":"/missing"}`)
	userInput := &scriptedInput{receives: []receiveResult{
		{text: "make a plan", mode: input.Mode(" PLAN ")},
		{text: "/exit"},
	}}
	aiProvider := &fakeProvider{responses: []provider.Response{
		{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Arguments: arguments}}}},
		{Message: llm.Message{Role: llm.RoleAssistant, Content: "the plan"}},
	}}
	recorder := &recordingTraceRecorder{run: &recordingTraceRun{}}
	runtime := New(aiProvider, userInput, userInput, Options{})

	if err := runtime.Run(tracing.Init(context.Background(), recorder)); err != nil {
		t.Fatalf("expected plan turn to exit cleanly, got %v", err)
	}
	if len(aiProvider.queries) != 2 {
		t.Fatalf("expected two plan requests, got %d", len(aiProvider.queries))
	}
	for _, query := range aiProvider.queries {
		if got := toolNames(query.Tools); !reflect.DeepEqual(got, []string{"read", "glob", "grep", "webfetch"}) {
			t.Fatalf("expected only read-only plan tools, got %#v", got)
		}
		first := query.Messages[0]
		if first.Role != llm.RoleSystem || !strings.Contains(first.Content, session.PlanModeInstruction) {
			t.Fatalf("expected plan instruction in the system message on every request, got %#v", first)
		}
	}
	for _, message := range runtime.Session.Conversation {
		if strings.Contains(message.Content, session.PlanModeInstruction) {
			t.Fatal("plan instruction was persisted in the conversation")
		}
	}
	payload := recorder.run.events[0].Payload.(tracing.UserInputPayload)
	if payload.Mode != input.ModePlan {
		t.Fatalf("expected normalized plan mode in trace, got %#v", payload)
	}
}

func TestRunChatModeUsesNoToolsAndEphemeralInstruction(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{
		{text: "just chat", mode: input.Mode(" CHAT ")},
		{text: "/exit"},
	}}
	aiProvider := &fakeProvider{responses: []provider.Response{{Message: llm.Message{Role: llm.RoleAssistant, Content: "hello"}}}}
	recorder := &recordingTraceRecorder{run: &recordingTraceRun{}}
	runtime := New(aiProvider, userInput, userInput, Options{})

	if err := runtime.Run(tracing.Init(context.Background(), recorder)); err != nil {
		t.Fatalf("expected chat turn to exit cleanly, got %v", err)
	}
	if len(aiProvider.queries) != 1 {
		t.Fatalf("expected one chat request, got %d", len(aiProvider.queries))
	}
	query := aiProvider.queries[0]
	if len(query.Tools) != 0 {
		t.Fatalf("expected no chat tools, got %#v", toolNames(query.Tools))
	}
	first := query.Messages[0]
	if first.Role != llm.RoleSystem || !strings.Contains(first.Content, session.ChatModeInstruction) {
		t.Fatalf("expected chat instruction in system message, got %#v", first)
	}
	for _, message := range runtime.Session.Conversation {
		if strings.Contains(message.Content, session.ChatModeInstruction) {
			t.Fatal("chat instruction was persisted in the conversation")
		}
	}
	payload := recorder.run.events[0].Payload.(tracing.UserInputPayload)
	if payload.Mode != input.ModeChat {
		t.Fatalf("expected normalized chat mode in trace, got %#v", payload)
	}
}

func TestRunChatModeCanAllowWebfetch(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{
		{text: "look online", mode: input.ModeChat},
		{text: "/exit"},
	}}
	aiProvider := &fakeProvider{responses: []provider.Response{{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}}}
	runtime := New(aiProvider, userInput, userInput, Options{ChatTools: tool.NewChatDefault()})

	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected chat turn to exit cleanly, got %v", err)
	}
	if len(aiProvider.queries) != 1 {
		t.Fatalf("expected one chat request, got %d", len(aiProvider.queries))
	}
	if got := toolNames(aiProvider.queries[0].Tools); !reflect.DeepEqual(got, []string{"webfetch"}) {
		t.Fatalf("expected only webfetch in chat mode, got %#v", got)
	}
}

func TestRunTracesCommandSubmissionMode(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{
		{text: "/help", mode: input.ModePlan},
		{text: "/exit"},
	}}
	recorder := &recordingTraceRecorder{run: &recordingTraceRun{}}
	runtime := New(&fakeProvider{}, userInput, userInput, Options{})

	if err := runtime.Run(tracing.Init(context.Background(), recorder)); err != nil {
		t.Fatalf("run command: %v", err)
	}
	payload, ok := recorder.run.events[0].Payload.(tracing.UserInputPayload)
	if !ok || payload.Mode != input.ModePlan {
		t.Fatalf("command mode was not traced: %#v", recorder.run.events[0].Payload)
	}
}

func TestRunSessionReturnsNewSessionAction(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "/new"}}}
	recorder := &recordingTraceRecorder{run: &recordingTraceRun{}}
	runtime := New(&fakeProvider{}, userInput, userInput, Options{})

	action, err := runtime.RunSession(tracing.Init(context.Background(), recorder))
	if err != nil {
		t.Fatalf("start new session: %v", err)
	}
	if action != command.ActionNewSession {
		t.Fatalf("expected new-session action, got %d", action)
	}
	if recorder.run.outcome.Status != tracing.StatusSuccess || recorder.run.outcome.Reason != tracing.EndReasonNewSession {
		t.Fatalf("unexpected run outcome: %#v", recorder.run.outcome)
	}
	if recorder.run.snapshots != 2 {
		t.Fatalf("expected initial and final snapshots, got %d", recorder.run.snapshots)
	}
}

func TestRunRejectsUnknownSubmissionMode(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "hello", mode: "unsafe"}}}
	runtime := New(&fakeProvider{}, userInput, userInput, Options{})

	err := runtime.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `unsupported submission mode "unsafe"`) {
		t.Fatalf("expected unsupported mode error, got %v", err)
	}
}

func TestRunInterruptCancelsCurrentTurnAndContinues(t *testing.T) {
	interrupts := make(chan struct{}, 1)
	userInput := &scriptedInput{
		receives:   []receiveResult{{text: "long task"}, {text: "/exit"}},
		interrupts: interrupts,
	}
	aiProvider := &fakeProvider{
		blockUntilCancel: true,
		started:          make(chan struct{}),
		cancelled:        make(chan struct{}),
	}
	runtime := New(aiProvider, userInput, userInput, Options{})

	done := make(chan error, 1)
	go func() { done <- runtime.Run(context.Background()) }()

	select {
	case <-aiProvider.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	interrupts <- struct{}{}

	select {
	case <-aiProvider.cancelled:
	case <-time.After(time.Second):
		t.Fatal("provider context was not cancelled")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runtime did not continue to /exit after interrupt: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not finish")
	}
	if len(runtime.Session.Conversation) != 1 || runtime.Session.Conversation[0].Role != llm.RoleSystem {
		t.Fatalf("cancelled turn was not rolled back: %#v", runtime.Session.Conversation)
	}
}

func TestRunParentCancellationStillStopsRuntime(t *testing.T) {
	userInput := &scriptedInput{
		receives:   []receiveResult{{text: "long task"}},
		interrupts: make(chan struct{}, 1),
	}
	aiProvider := &fakeProvider{
		blockUntilCancel: true,
		started:          make(chan struct{}),
		cancelled:        make(chan struct{}),
	}
	runtime := New(aiProvider, userInput, userInput, Options{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	select {
	case <-aiProvider.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	cancel()

	select {
	case <-aiProvider.cancelled:
	case <-time.After(time.Second):
		t.Fatal("provider context was not cancelled")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runtime error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not finish")
	}
}

func TestRunKeepsConversationHistory(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "hello"}, {text: "again"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{responses: []provider.Response{
		{Message: llm.Message{Role: "assistant", Content: "first"}},
		{Message: llm.Message{Role: "assistant", Content: "second"}},
	}}

	runtime := New(aiProvider, userInput, userInput, Options{})
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
	runtime := New(aiProvider, userInput, userInput, Options{})
	if err := runtime.Run(tracing.Init(context.Background(), recorder)); err != nil {
		t.Fatalf("expected runtime to exit cleanly, got %v", err)
	}

	if !reflect.DeepEqual(userInput.responses, []string{"read complete"}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
	expectedStatuses := []string{"Reading file " + path, ""}
	if !reflect.DeepEqual(userInput.statuses, expectedStatuses) {
		t.Fatalf("expected statuses %#v, got %#v", expectedStatuses, userInput.statuses)
	}
	expectedKinds := []input.EventKind{
		input.EventInferenceStarted,
		input.EventInferenceEnded,
		input.EventToolStarted,
		input.EventStatus,
		input.EventStatus,
		input.EventToolCompleted,
		input.EventInferenceStarted,
		input.EventAssistantStarted,
		input.EventAssistantDelta,
		input.EventInferenceEnded,
		input.EventAssistantCompleted,
	}
	if got := eventKinds(userInput.events); !reflect.DeepEqual(got, expectedKinds) {
		t.Fatalf("expected event kinds %#v, got %#v", expectedKinds, got)
	}
	if activity := userInput.events[2].ToolActivity; activity.Target != path {
		t.Fatalf("read activity target = %q, want %q", activity.Target, path)
	}
	if len(aiProvider.queries) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(aiProvider.queries))
	}
	if got := toolNames(aiProvider.queries[0].Tools); !reflect.DeepEqual(got, []string{"read", "write", "glob", "grep", "webfetch", "bash"}) {
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
	runtime := New(aiProvider, userInput, userInput, Options{})
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

	runtime := New(aiProvider, userInput, userInput, Options{})
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

	runtime := New(aiProvider, userInput, userInput, Options{})
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
	runtime := New(aiProvider, userInput, userInput, Options{})
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

func TestRunCopiesLatestMessageWithoutProviderCall(t *testing.T) {
	for _, commandText := range []string{"/copy", ":copy"} {
		t.Run(commandText, func(t *testing.T) {
			userInput := &scriptedInput{receives: []receiveResult{{text: "hello"}, {text: commandText}, {text: "/exit"}}}
			aiProvider := &fakeProvider{responses: []provider.Response{{Message: llm.Message{Role: llm.RoleAssistant, Content: "world"}}}}
			clipboard := &fakeRuntimeClipboard{}

			runtime := New(aiProvider, userInput, userInput, Options{Clipboard: clipboard})
			if err := runtime.Run(context.Background()); err != nil {
				t.Fatalf("expected runtime to exit cleanly, got %v", err)
			}
			if len(aiProvider.queries) != 1 {
				t.Fatalf("expected only the original provider call, got %d", len(aiProvider.queries))
			}
			if clipboard.text != "world" {
				t.Fatalf("expected latest response to be copied, got %q", clipboard.text)
			}
			expected := []string{"world", "copied latest message to clipboard"}
			if !reflect.DeepEqual(userInput.responses, expected) {
				t.Fatalf("unexpected responses: %#v", userInput.responses)
			}
		})
	}
}

func TestLatestCopyableMessageSkipsUserMessages(t *testing.T) {
	runtime := &Runtime{Session: session.Session{Conversation: []llm.Message{
		{Role: llm.RoleAssistant, Content: "assistant response"},
		{Role: llm.RoleUser, Content: "newer user message"},
	}}}

	message, ok := runtime.latestCopyableMessage()
	if !ok || message != "assistant response" {
		t.Fatalf("latest copyable message = %q, %v", message, ok)
	}
}

func TestRunCopyHandlesNoMessage(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "/copy"}, {text: "/exit"}}}
	clipboard := &fakeRuntimeClipboard{}

	runtime := New(&fakeProvider{}, userInput, userInput, Options{Clipboard: clipboard})
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to exit cleanly, got %v", err)
	}
	if clipboard.text != "" {
		t.Fatalf("expected clipboard not to be called, got %q", clipboard.text)
	}
	if !reflect.DeepEqual(userInput.responses, []string{"no message to copy"}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
}

func TestRunHandlesUnknownCommandWithoutProviderCall(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "/unknown"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{}

	runtime := New(aiProvider, userInput, userInput, Options{})
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

	runtime := New(aiProvider, userInput, userInput, Options{})
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to handle help cleanly, got %v", err)
	}

	if len(aiProvider.queries) != 0 {
		t.Fatalf("expected no provider calls, got %d", len(aiProvider.queries))
	}
	expectedHelp := "Available commands:\n  :copy, /copy - copy latest message to clipboard\n  :q, /exit, /quit - exit harness\n  :help, /help - show available commands\n  :model, /model - switch provider/model\n  :new, /new - start a new session"
	if !reflect.DeepEqual(userInput.responses, []string{expectedHelp}) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
}

func TestRunModelListsAllSelectionsWithoutProviderCall(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "/model"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{
		current: provider.Selection{Provider: "openai", Model: "gpt-4.1"},
		available: []provider.Selection{
			{Provider: "openai", Model: "gpt-4.1"},
			{Provider: "openai", Model: "gpt-4o"},
			{Provider: "anthropic", Model: "claude-sonnet-4"},
		},
	}

	runtime := New(aiProvider, userInput, userInput, Options{})
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to handle model list cleanly, got %v", err)
	}
	if len(aiProvider.queries) != 0 {
		t.Fatalf("expected no provider calls, got %d", len(aiProvider.queries))
	}
	if len(userInput.responses) != 1 {
		t.Fatalf("expected one response, got %#v", userInput.responses)
	}
	for _, expected := range []string{"* openai/gpt-4.1", "  openai/gpt-4o", "  anthropic/claude-sonnet-4"} {
		if !strings.Contains(userInput.responses[0], expected) {
			t.Fatalf("expected model output to contain %q, got %q", expected, userInput.responses[0])
		}
	}
}

func TestRunModelSwitchesProviderAndModel(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "/model anthropic/claude-sonnet-4"}, {text: "hello"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{
		current:   provider.Selection{Provider: "openai", Model: "gpt-4.1"},
		responses: []provider.Response{{Message: llm.Message{Role: llm.RoleAssistant, Content: "world"}}},
	}
	recorder := &recordingTraceRecorder{run: &recordingTraceRun{}}

	runtime := New(aiProvider, userInput, userInput, Options{})
	if err := runtime.Run(tracing.Init(context.Background(), recorder)); err != nil {
		t.Fatalf("expected runtime to switch model cleanly, got %v", err)
	}
	expectedUseCalls := []provider.Selection{{Provider: "anthropic", Model: "claude-sonnet-4"}}
	if !reflect.DeepEqual(aiProvider.useCalls, expectedUseCalls) {
		t.Fatalf("expected use calls %#v, got %#v", expectedUseCalls, aiProvider.useCalls)
	}
	if aiProvider.current != (provider.Selection{Provider: "anthropic", Model: "claude-sonnet-4"}) {
		t.Fatalf("expected current selection to switch, got %#v", aiProvider.current)
	}
	if !containsProviderSelectionEvent(userInput.events, provider.Selection{Provider: "anthropic", Model: "claude-sonnet-4"}) {
		t.Fatalf("expected provider selection event, got %#v", userInput.events)
	}
	span := findTraceSpan(recorder.run.spans, tracing.SpanLLMCall)
	if span == nil {
		t.Fatal("expected LLM call span")
	}
	payload, ok := span.start.Payload.(struct {
		Provider string      `json:"provider"`
		Model    string      `json:"model"`
		Round    int         `json:"round"`
		Request  llm.Request `json:"request"`
	})
	if !ok || payload.Provider != "anthropic" || payload.Model != "claude-sonnet-4" {
		t.Fatalf("expected LLM span to use switched selection, got %#v", span.start.Payload)
	}
}

func TestRunModelErrorDoesNotTerminateRuntime(t *testing.T) {
	userInput := &scriptedInput{receives: []receiveResult{{text: "/model openai/missing"}, {text: "/exit"}}}
	aiProvider := &fakeProvider{useErr: errors.New(`model "missing" is not configured for provider "openai"`)}

	runtime := New(aiProvider, userInput, userInput, Options{})
	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("expected runtime to handle model error cleanly, got %v", err)
	}
	if len(aiProvider.queries) != 0 {
		t.Fatalf("expected no provider calls, got %d", len(aiProvider.queries))
	}
	expected := []string{`error: model "missing" is not configured for provider "openai"`}
	if !reflect.DeepEqual(userInput.responses, expected) {
		t.Fatalf("unexpected responses: %#v", userInput.responses)
	}
}

type receiveResult struct {
	text string
	mode input.Mode
	err  error
}

type scriptedInput struct {
	receives   []receiveResult
	responses  []string
	statuses   []string
	events     []input.Event
	stream     strings.Builder
	interrupts chan struct{}
}

func (s *scriptedInput) Receive(context.Context) (input.Submission, error) {
	if len(s.receives) == 0 {
		return input.Submission{}, io.EOF
	}

	result := s.receives[0]
	s.receives = s.receives[1:]
	return input.Submission{Text: result.text, Mode: result.mode}, result.err
}

func (s *scriptedInput) Interrupts() <-chan struct{} {
	return s.interrupts
}

func (s *scriptedInput) Emit(_ context.Context, event input.Event) error {
	s.events = append(s.events, event)
	switch event.Kind {
	case input.EventOutput:
		if event.Stream == input.StreamStdout {
			s.responses = append(s.responses, event.Text)
		}
	case input.EventStatus:
		s.statuses = append(s.statuses, event.Text)
	case input.EventAssistantStarted:
		s.stream.Reset()
	case input.EventAssistantDelta:
		s.stream.WriteString(event.Text)
	case input.EventAssistantCompleted:
		s.responses = append(s.responses, event.Text)
		s.stream.Reset()
	case input.EventAssistantAborted:
		s.stream.Reset()
	}
	return nil
}

type fakeProvider struct {
	current          provider.Selection
	available        []provider.Selection
	useCalls         []provider.Selection
	useErr           error
	queries          []llm.Request
	responses        []provider.Response
	errors           []error
	chunks           [][]string
	streamErrors     []error
	blockUntilCancel bool
	started          chan struct{}
	cancelled        chan struct{}
}

func toolNames(definitions []llm.ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func eventKinds(events []input.Event) []input.EventKind {
	kinds := make([]input.EventKind, len(events))
	for index, event := range events {
		kinds[index] = event.Kind
	}
	return kinds
}

func (f *fakeProvider) Current() provider.Selection {
	if f.current == (provider.Selection{}) {
		return provider.Selection{Provider: "fake", Model: "test"}
	}
	return f.current
}

func (f *fakeProvider) Available() []provider.Selection {
	if f.available == nil {
		return []provider.Selection{{Provider: "fake", Model: "test"}}
	}
	return append([]provider.Selection(nil), f.available...)
}

func (f *fakeProvider) Use(providerName string, modelName string) error {
	f.useCalls = append(f.useCalls, provider.Selection{Provider: providerName, Model: modelName})
	if f.useErr != nil {
		return f.useErr
	}
	f.current = provider.Selection{Provider: providerName, Model: modelName}
	return nil
}

func (f *fakeProvider) Send(ctx context.Context, query llm.Request, stream provider.StreamHandler) (provider.Response, error) {
	query.Messages = append([]llm.Message(nil), query.Messages...)
	f.queries = append(f.queries, query)

	if f.blockUntilCancel {
		if f.started != nil {
			close(f.started)
		}
		<-ctx.Done()
		if f.cancelled != nil {
			close(f.cancelled)
		}
		return provider.Response{}, ctx.Err()
	}

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
	chunks := []string{response.Message.Content}
	if len(f.chunks) > 0 {
		chunks = f.chunks[0]
		f.chunks = f.chunks[1:]
	}
	for _, chunk := range chunks {
		if chunk == "" || stream == nil {
			continue
		}
		if err := stream(provider.StreamEvent{TextDelta: chunk}); err != nil {
			return provider.Response{}, err
		}
	}
	if len(f.streamErrors) > 0 {
		err := f.streamErrors[0]
		f.streamErrors = f.streamErrors[1:]
		if err != nil {
			return provider.Response{}, err
		}
	}
	return response, nil
}

type failingTool struct{}

func (failingTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: "fail", Parameters: llm.Schema{Type: "object"}}
}

func (failingTool) Capability() model.Capability {
	return model.CapabilityMutating
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

func containsProviderSelectionEvent(events []input.Event, selection provider.Selection) bool {
	for _, event := range events {
		if event.Kind == input.EventProviderSelection && event.Provider == selection.Provider && event.Model == selection.Model {
			return true
		}
	}
	return false
}

func containsTraceEvent(events []tracing.Event, kind tracing.Kind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

type fakeRuntimeClipboard struct {
	text string
	err  error
}

func (f *fakeRuntimeClipboard) Copy(_ context.Context, text string) error {
	f.text = text
	return f.err
}
