package todo

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/model"
)

func TestToolReplacesListAndProvidesPromptContext(t *testing.T) {
	item := New()
	result, err := item.Execute(context.Background(), json.RawMessage(`{"todos":[{"id":"build","content":"Build feature","status":"in_progress"},{"id":"test","content":"Run tests","status":"pending"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"id":"build"`) || !strings.Contains(item.PromptContext(), "[in_progress] build: Build feature") {
		t.Fatalf("result=%q context=%q", result, item.PromptContext())
	}
	if _, err := item.Execute(context.Background(), json.RawMessage(`{"todos":[]}`)); err != nil {
		t.Fatal(err)
	}
	if item.PromptContext() != EmptyInstruction {
		t.Fatalf("empty context = %q", item.PromptContext())
	}
}

func TestToolValidatesArguments(t *testing.T) {
	tests := []struct{ name, arguments, want string }{
		{"missing todos", `{}`, "todos is required"},
		{"unknown field", `{"todos":[],"extra":true}`, "unknown field"},
		{"duplicate", `{"todos":[{"id":"a","content":"one","status":"pending"},{"id":"a","content":"two","status":"completed"}]}`, "duplicated"},
		{"bad status", `{"todos":[{"id":"a","content":"one","status":"doing"}]}`, "invalid"},
		{"two active", `{"todos":[{"id":"a","content":"one","status":"in_progress"},{"id":"b","content":"two","status":"in_progress"}]}`, "at most one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New().Execute(context.Background(), json.RawMessage(test.arguments))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestToolHonorsCancellationWithoutChangingState(t *testing.T) {
	item := New()
	_, _ = item.Execute(context.Background(), json.RawMessage(`{"todos":[{"id":"a","content":"keep","status":"pending"}]}`))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := item.Execute(ctx, json.RawMessage(`{"todos":[]}`))
	if !errors.Is(err, context.Canceled) || !strings.Contains(item.PromptContext(), "keep") {
		t.Fatalf("err=%v context=%q", err, item.PromptContext())
	}
}

func TestToolRestoresOnlyMatchingSuccessfulResults(t *testing.T) {
	item := New()
	messages := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call", Name: Name}}},
		{Role: llm.RoleTool, ToolCallID: "other", Content: `{"todos":[]}`},
		{Role: llm.RoleTool, ToolCallID: "call", Content: `{"todos":[{"id":"a","content":"restored","status":"completed"}]}`},
	}
	item.Restore(messages)
	if !strings.Contains(item.PromptContext(), "restored") {
		t.Fatalf("context = %q", item.PromptContext())
	}
}

func TestToolDefinitionAndPresentation(t *testing.T) {
	item := New()
	definition := item.Definition()
	if definition.Name != Name || definition.Parameters.Properties["todos"].Items == nil {
		t.Fatalf("definition = %#v", definition)
	}
	result, err := item.Execute(context.Background(), json.RawMessage(`{"todos":[{"id":"a","content":"Shown","status":"completed"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	activity := item.Present(nil, result, nil)
	want := []model.ActivityItem{{ID: "a", Content: "Shown", Status: "completed"}}
	if !reflect.DeepEqual(activity.Items, want) {
		t.Fatalf("items = %#v", activity.Items)
	}
}
