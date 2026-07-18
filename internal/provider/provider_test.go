package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"latentdream/harness/internal/config"
	"latentdream/harness/internal/llm"
)

func TestNewSelectsFirstEnabledProviderAndModel(t *testing.T) {
	disabled := false

	provider, err := New([]config.Provider{
		{
			Name:    "disabled",
			Type:    "openai-compatible",
			BaseURL: "http://127.0.0.1:1111/v1",
			Models:  []config.ProviderModel{{Name: "ignored"}},
		},
		{
			Name:    "openai",
			Type:    "openai",
			Enabled: true,
			Models: []config.ProviderModel{
				{Name: "gpt-4.1"},
				{Name: "disabled-model", Enabled: &disabled},
				{Name: "gpt-4o"},
			},
		},
		{
			Name:    "local",
			Type:    "openai-compatible",
			BaseURL: "http://127.0.0.1:2222/v1",
			Enabled: true,
			Models:  []config.ProviderModel{{Name: "llama"}},
		},
	})
	if err != nil {
		t.Fatalf("expected provider to initialize, got %v", err)
	}

	if current := provider.Current(); current != (Selection{Provider: "openai", Model: "gpt-4.1"}) {
		t.Fatalf("unexpected current selection: %#v", current)
	}

	expectedAvailable := []Selection{
		{Provider: "openai", Model: "gpt-4.1"},
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "local", Model: "llama"},
	}
	if available := provider.Available(); !reflect.DeepEqual(available, expectedAvailable) {
		t.Fatalf("expected available selections %#v, got %#v", expectedAvailable, available)
	}
}

func TestNewReturnsErrorWithoutEnabledProviders(t *testing.T) {
	_, err := New(nil)
	if !errors.Is(err, ErrNoEnabledProviders) {
		t.Fatalf("expected ErrNoEnabledProviders, got %v", err)
	}
}

func TestUseSwitchesProviderAndModel(t *testing.T) {
	provider, err := New([]config.Provider{
		{
			Name:    "openai",
			Type:    "openai",
			Enabled: true,
			Models:  []config.ProviderModel{{Name: "gpt-4.1"}, {Name: "gpt-4o"}},
		},
		{
			Name:    "local",
			Type:    "openai-compatible",
			BaseURL: "http://127.0.0.1:2222/v1",
			Enabled: true,
			Models:  []config.ProviderModel{{Name: "llama"}},
		},
	})
	if err != nil {
		t.Fatalf("expected provider to initialize, got %v", err)
	}

	if err := provider.Use("local", ""); err != nil {
		t.Fatalf("expected switch to local provider, got %v", err)
	}
	if current := provider.Current(); current != (Selection{Provider: "local", Model: "llama"}) {
		t.Fatalf("unexpected current selection after provider switch: %#v", current)
	}

	if err := provider.Use("openai", "gpt-4o"); err != nil {
		t.Fatalf("expected switch to openai gpt-4o, got %v", err)
	}
	if current := provider.Current(); current != (Selection{Provider: "openai", Model: "gpt-4o"}) {
		t.Fatalf("unexpected current selection after model switch: %#v", current)
	}

	if err := provider.Use("openai", "missing"); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("expected unknown model error, got %v", err)
	}
	if err := provider.Use("missing", ""); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
}

func TestSendOpenAICompatible(t *testing.T) {
	t.Setenv("TEST_OPENAI_KEY", "secret-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("expected /chat/completions path, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer secret-token" {
			t.Fatalf("expected bearer token, got %q", auth)
		}

		var request openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "gpt-test" {
			t.Fatalf("expected model gpt-test, got %q", request.Model)
		}
		if request.MaxTokens != 42 {
			t.Fatalf("expected max_tokens 42, got %d", request.MaxTokens)
		}
		if len(request.Messages) != 1 || request.Messages[0].Role != "user" || request.Messages[0].Content != "hello" {
			t.Fatalf("unexpected messages: %#v", request.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "world"}},
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	provider, err := New([]config.Provider{{
		Name:            "test-openai",
		Type:            "openai-compatible",
		BaseURL:         server.URL,
		AuthTokenEnvVar: "TEST_OPENAI_KEY",
		Enabled:         true,
		Models:          []config.ProviderModel{{Name: "gpt-test"}},
	}})
	if err != nil {
		t.Fatalf("expected provider to initialize, got %v", err)
	}

	response, err := provider.Send(context.Background(), llm.Request{
		Messages:  []llm.Message{{Role: "user", Content: "hello"}},
		MaxTokens: 42,
	})
	if err != nil {
		t.Fatalf("expected send to succeed, got %v", err)
	}
	if response.Provider != "test-openai" || response.Model != "gpt-test" {
		t.Fatalf("unexpected response selection: %#v", response)
	}
	if !reflect.DeepEqual(response.Message, llm.Message{Role: "assistant", Content: "world"}) {
		t.Fatalf("unexpected response message: %#v", response.Message)
	}
}

func TestSendOpenAICompatibleHandlesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(request.Tools) != 1 || request.Tools[0].Type != "function" || request.Tools[0].Function.Name != "read" {
			t.Fatalf("unexpected tools: %#v", request.Tools)
		}
		if request.Tools[0].Function.Parameters.Properties["filePath"].Type != "string" {
			t.Fatalf("expected filePath string schema, got %#v", request.Tools[0].Function.Parameters)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":   "call_1",
						"type": "function",
						"function": map[string]string{
							"name":      "read",
							"arguments": `{"filePath":"/tmp/sample.txt"}`,
						},
					}},
				},
			}},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	provider, err := New([]config.Provider{{
		Name:    "test-openai",
		Type:    "openai-compatible",
		BaseURL: server.URL,
		Enabled: true,
		Models:  []config.ProviderModel{{Name: "gpt-test"}},
	}})
	if err != nil {
		t.Fatalf("expected provider to initialize, got %v", err)
	}

	response, err := provider.Send(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "read it"}},
		Tools:    []llm.ToolDefinition{readDefinitionForTest()},
	})
	if err != nil {
		t.Fatalf("expected send to succeed, got %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "read" || string(call.Arguments) != `{"filePath":"/tmp/sample.txt"}` {
		t.Fatalf("unexpected tool call: %#v", call)
	}
}

func TestSendReturnsErrorWhenAuthEnvVarIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not be sent without auth token")
	}))
	defer server.Close()

	provider, err := New([]config.Provider{{
		Name:            "test-openai",
		Type:            "openai-compatible",
		BaseURL:         server.URL,
		AuthTokenEnvVar: "TEST_MISSING_OPENAI_KEY",
		Enabled:         true,
		Models:          []config.ProviderModel{{Name: "gpt-test"}},
	}})
	if err != nil {
		t.Fatalf("expected provider to initialize, got %v", err)
	}

	_, err = provider.Send(context.Background(), llm.Request{Messages: []llm.Message{{Role: "user", Content: "hello"}}})
	if err == nil || !strings.Contains(err.Error(), "TEST_MISSING_OPENAI_KEY") {
		t.Fatalf("expected missing auth env var error, got %v", err)
	}
}

func TestSendAnthropic(t *testing.T) {
	t.Setenv("TEST_ANTHROPIC_KEY", "anthropic-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("expected /v1/messages path, got %s", r.URL.Path)
		}
		if token := r.Header.Get("x-api-key"); token != "anthropic-token" {
			t.Fatalf("expected anthropic token, got %q", token)
		}
		if version := r.Header.Get("anthropic-version"); version == "" {
			t.Fatal("expected anthropic-version header")
		}

		var request anthropicMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "claude-test" {
			t.Fatalf("expected model claude-test, got %q", request.Model)
		}
		if request.MaxTokens != defaultMaxTokens {
			t.Fatalf("expected default max tokens %d, got %d", defaultMaxTokens, request.MaxTokens)
		}
		if request.System != "be concise" {
			t.Fatalf("expected system prompt, got %q", request.System)
		}
		if len(request.Messages) != 1 || request.Messages[0].Role != "user" || request.Messages[0].Content != "hello" {
			t.Fatalf("unexpected messages: %#v", request.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"role": "assistant",
			"content": []map[string]string{
				{"type": "text", "text": "hello back"},
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	provider, err := New([]config.Provider{{
		Name:            "anthropic",
		Type:            "anthropic",
		BaseURL:         server.URL,
		AuthTokenEnvVar: "TEST_ANTHROPIC_KEY",
		Enabled:         true,
		Models:          []config.ProviderModel{{Name: "claude-test"}},
	}})
	if err != nil {
		t.Fatalf("expected provider to initialize, got %v", err)
	}

	response, err := provider.Send(context.Background(), llm.Request{Messages: []llm.Message{
		{Role: "system", Content: "be concise"},
		{Role: "user", Content: "hello"},
	}})
	if err != nil {
		t.Fatalf("expected send to succeed, got %v", err)
	}
	if !reflect.DeepEqual(response.Message, llm.Message{Role: "assistant", Content: "hello back"}) {
		t.Fatalf("unexpected response message: %#v", response.Message)
	}
}

func TestSendAnthropicHandlesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request anthropicMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(request.Tools) != 1 || request.Tools[0].Name != "read" {
			t.Fatalf("unexpected tools: %#v", request.Tools)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"role": "assistant",
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    "toolu_1",
				"name":  "read",
				"input": map[string]string{"filePath": "/tmp/sample.txt"},
			}},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	provider, err := New([]config.Provider{{
		Name:    "anthropic",
		Type:    "anthropic",
		BaseURL: server.URL,
		Enabled: true,
		Models:  []config.ProviderModel{{Name: "claude-test"}},
	}})
	if err != nil {
		t.Fatalf("expected provider to initialize, got %v", err)
	}

	response, err := provider.Send(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "read it"}},
		Tools:    []llm.ToolDefinition{readDefinitionForTest()},
	})
	if err != nil {
		t.Fatalf("expected send to succeed, got %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "toolu_1" || call.Name != "read" || string(call.Arguments) != `{"filePath":"/tmp/sample.txt"}` {
		t.Fatalf("unexpected tool call: %#v", call)
	}
}

func readDefinitionForTest() llm.ToolDefinition {
	additionalProperties := false
	return llm.ToolDefinition{
		Name:        "read",
		Description: "Read file contents",
		Parameters: llm.Schema{
			Type: "object",
			Properties: map[string]llm.Schema{
				"filePath": {Type: "string", Description: "The absolute path to read"},
			},
			Required:             []string{"filePath"},
			AdditionalProperties: &additionalProperties,
		},
	}
}
