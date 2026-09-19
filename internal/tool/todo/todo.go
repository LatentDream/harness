package todo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/model"
)

const (
	Name             = "todo"
	maxItems         = 100
	maxIDRunes       = 128
	maxContentRunes  = 1000
	EmptyInstruction = "Current TODO list: empty. Use the todo tool for work that benefits from multiple tracked steps."
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
)

type Item struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  Status `json:"status"`
}

type payload struct {
	Todos []Item `json:"todos"`
}

type Tool struct {
	mu    sync.RWMutex
	items []Item
}

func New() *Tool { return &Tool{} }

func (t *Tool) Definition() llm.ToolDefinition {
	additionalProperties := false
	itemSchema := llm.Schema{
		Type: "object",
		Properties: map[string]llm.Schema{
			"id":      {Type: "string", Description: "Stable identifier for the task"},
			"content": {Type: "string", Description: "Concise description of the task"},
			"status":  {Type: "string", Enum: []string{string(StatusPending), string(StatusInProgress), string(StatusCompleted)}},
		},
		Required:             []string{"id", "content", "status"},
		AdditionalProperties: &additionalProperties,
	}
	return llm.ToolDefinition{
		Name: Name,
		Description: "Replace the current session TODO list. Reuse this tool whenever tracked work changes. " +
			"Send the complete list on every call; use an empty list to clear it. Prefer at most one in_progress item.",
		Parameters: llm.Schema{
			Type: "object",
			Properties: map[string]llm.Schema{
				"todos": {Type: "array", Description: "The complete ordered TODO list", Items: &itemSchema},
			},
			Required:             []string{"todos"},
			AdditionalProperties: &additionalProperties,
		},
	}
}

func (*Tool) Capability() model.Capability { return model.CapabilityAgentState }

func (*Tool) Status(json.RawMessage) string { return "Updating TODO list" }

func (t *Tool) Execute(ctx context.Context, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	parsed, err := decode(arguments)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	t.mu.Lock()
	t.items = cloneItems(parsed.Todos)
	t.mu.Unlock()
	encoded, err := json.Marshal(payload{Todos: parsed.Todos})
	if err != nil {
		return "", fmt.Errorf("encode todo result: %w", err)
	}
	return string(encoded), nil
}

func (t *Tool) Present(_ json.RawMessage, result string, executionErr error) model.Activity {
	activity := model.Activity{}
	if executionErr != nil {
		return activity
	}
	var parsed payload
	if json.Unmarshal([]byte(result), &parsed) != nil {
		return activity
	}
	activity.Items = make([]model.ActivityItem, len(parsed.Todos))
	for i, item := range parsed.Todos {
		activity.Items[i] = model.ActivityItem{ID: item.ID, Content: item.Content, Status: string(item.Status)}
	}
	return activity
}

// PromptContext returns the ephemeral context injected into the next model request.
func (t *Tool) PromptContext() string {
	t.mu.RLock()
	items := cloneItems(t.items)
	t.mu.RUnlock()
	if len(items) == 0 {
		return EmptyInstruction
	}
	var output strings.Builder
	output.WriteString("Current TODO list:\n")
	for _, item := range items {
		fmt.Fprintf(&output, "- [%s] %s: %s\n", item.Status, item.ID, item.Content)
	}
	output.WriteString("Keep this list current by reusing the todo tool whenever priorities or statuses change.")
	return output.String()
}

// Restore rebuilds state solely from successful tool results in conversation history.
func (t *Tool) Restore(messages []llm.Message) {
	var latest []Item
	calls := make(map[string]struct{})
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Name == Name {
				calls[call.ID] = struct{}{}
			}
		}
		if message.Role != llm.RoleTool || strings.HasPrefix(strings.TrimSpace(message.Content), "error:") {
			continue
		}
		if _, ok := calls[message.ToolCallID]; !ok {
			continue
		}
		var parsed payload
		if err := json.Unmarshal([]byte(message.Content), &parsed); err != nil {
			continue
		}
		validated, err := validate(parsed)
		if err != nil {
			continue
		}
		latest = cloneItems(validated.Todos)
	}
	t.mu.Lock()
	t.items = latest
	t.mu.Unlock()
}

func decode(arguments json.RawMessage) (payload, error) {
	if len(bytes.TrimSpace(arguments)) == 0 {
		return payload{}, errors.New("todo arguments must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	var parsed payload
	if err := decoder.Decode(&parsed); err != nil {
		return payload{}, fmt.Errorf("decode todo arguments: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return payload{}, errors.New("todo arguments must contain one JSON object")
	} else if !errors.Is(err, io.EOF) {
		return payload{}, fmt.Errorf("decode todo arguments: %w", err)
	}
	return validate(parsed)
}

func validate(parsed payload) (payload, error) {
	if parsed.Todos == nil {
		return payload{}, errors.New("todos is required and must be an array")
	}
	if len(parsed.Todos) > maxItems {
		return payload{}, fmt.Errorf("todos must contain at most %d items", maxItems)
	}
	seen := make(map[string]struct{}, len(parsed.Todos))
	inProgress := 0
	for i := range parsed.Todos {
		item := &parsed.Todos[i]
		item.ID = strings.TrimSpace(item.ID)
		item.Content = strings.TrimSpace(item.Content)
		if item.ID == "" {
			return payload{}, fmt.Errorf("todos[%d].id must not be empty", i)
		}
		if len([]rune(item.ID)) > maxIDRunes {
			return payload{}, fmt.Errorf("todos[%d].id is too long", i)
		}
		if _, exists := seen[item.ID]; exists {
			return payload{}, fmt.Errorf("todos[%d].id %q is duplicated", i, item.ID)
		}
		seen[item.ID] = struct{}{}
		if item.Content == "" {
			return payload{}, fmt.Errorf("todos[%d].content must not be empty", i)
		}
		if len([]rune(item.Content)) > maxContentRunes {
			return payload{}, fmt.Errorf("todos[%d].content is too long", i)
		}
		switch item.Status {
		case StatusPending, StatusCompleted:
		case StatusInProgress:
			inProgress++
		default:
			return payload{}, fmt.Errorf("todos[%d].status %q is invalid", i, item.Status)
		}
	}
	if inProgress > 1 {
		return payload{}, errors.New("todos must contain at most one in_progress item")
	}
	return parsed, nil
}

func cloneItems(items []Item) []Item { return append([]Item(nil), items...) }
