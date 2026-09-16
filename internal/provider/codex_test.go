package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"latentdream/harness/internal/config"
	"latentdream/harness/internal/session/llm"
)

func TestNewCodexDefaultsModelAndAuth(t *testing.T) {
	provider, err := New([]config.Provider{{
		Name:    "codex",
		Type:    "codex",
		Enabled: true,
	}})
	if err != nil {
		t.Fatalf("expected codex provider to initialize, got %v", err)
	}

	if current := provider.Current(); current != (Selection{Provider: "codex", Model: defaultCodexModel}) {
		t.Fatalf("unexpected current selection: %#v", current)
	}
}

func TestCodexRequestIncludesRequiredResponseFlags(t *testing.T) {
	payload := codexRequest(defaultCodexModel, llm.Request{Messages: []llm.Message{{Role: "user", Content: "hello"}}})
	contents, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal codex request: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(contents, &raw); err != nil {
		t.Fatalf("decode codex request: %v", err)
	}
	store, ok := raw["store"]
	if !ok {
		t.Fatal("expected codex request to include store")
	}
	if store != false {
		t.Fatalf("expected store false, got %#v", store)
	}
	stream, ok := raw["stream"]
	if !ok {
		t.Fatal("expected codex request to include stream")
	}
	if stream != true {
		t.Fatalf("expected stream true, got %#v", stream)
	}
	reasoning, ok := raw["reasoning"].(map[string]any)
	if !ok || reasoning["summary"] != "auto" {
		t.Fatalf("expected automatic reasoning summary, got %#v", raw["reasoning"])
	}
}

func TestCodexRequestIncludesToolsAndToolMessages(t *testing.T) {
	payload := codexRequest(defaultCodexModel, llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "read it"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"filePath":"/tmp/sample.txt"}`)}}},
			{Role: "tool", ToolCallID: "call_1", Content: "tool result"},
		},
		Tools: []llm.ToolDefinition{readDefinitionForTest()},
	})

	if len(payload.Tools) != 1 || payload.Tools[0].Type != "function" || payload.Tools[0].Name != "read" {
		t.Fatalf("unexpected tools: %#v", payload.Tools)
	}
	if len(payload.Input) != 3 {
		t.Fatalf("expected three input items, got %#v", payload.Input)
	}
	if payload.Input[1].Type != "function_call" || payload.Input[1].CallID != "call_1" || payload.Input[1].Name != "read" {
		t.Fatalf("unexpected function call input: %#v", payload.Input[1])
	}
	if payload.Input[2].Type != "function_call_output" || payload.Input[2].CallID != "call_1" || payload.Input[2].Output != "tool result" {
		t.Fatalf("unexpected function output input: %#v", payload.Input[2])
	}
}

func TestCodexStreamEmitsReasoningSummarySeparately(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"Inspecting files"}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"done"}`,
		``,
	}, "\n")
	var events []StreamEvent
	message, err := codexStreamResponseMessageReader(strings.NewReader(body), func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("decode stream: %v", err)
	}
	if message.Content != "done" {
		t.Fatalf("assistant content = %q", message.Content)
	}
	want := []StreamEvent{{ReasoningDelta: "Inspecting files"}, {TextDelta: "done"}}
	if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("stream events = %#v, want %#v", events, want)
	}
}

func TestCodexResponseContentReadsStream(t *testing.T) {
	body := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":" world"}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"output_text":"hello world"}}`,
		``,
	}, "\n")

	content, err := codexResponseContent([]byte(body))
	if err != nil {
		t.Fatalf("expected stream response to decode, got %v", err)
	}
	if content != "hello world" {
		t.Fatalf("expected stream content hello world, got %q", content)
	}
}

func TestSendCodexUsesOpencodeAuthFile(t *testing.T) {
	authPath := writeCodexAuth(t, map[string]codexAuthInfo{
		"openai": {
			Type:      "oauth",
			Access:    "access-token",
			Refresh:   "refresh-token",
			Expires:   time.Now().Add(time.Hour).UnixMilli(),
			AccountID: "acct_123",
		},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/responses" {
			t.Fatalf("expected /responses path, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer access-token" {
			t.Fatalf("expected bearer access token, got %q", auth)
		}
		if accountID := r.Header.Get("ChatGPT-Account-Id"); accountID != "acct_123" {
			t.Fatalf("expected account id header, got %q", accountID)
		}
		if originator := r.Header.Get("originator"); originator != codexHeaderOriginator {
			t.Fatalf("expected originator %q, got %q", codexHeaderOriginator, originator)
		}

		var request codexResponsesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != defaultCodexModel {
			t.Fatalf("expected model %q, got %q", defaultCodexModel, request.Model)
		}
		if request.Instructions != "be concise" {
			t.Fatalf("expected instructions from system message, got %q", request.Instructions)
		}
		if request.MaxOutputTokens != 128 {
			t.Fatalf("expected max output tokens 128, got %d", request.MaxOutputTokens)
		}
		if request.Store {
			t.Fatal("expected store false")
		}
		if !request.Stream {
			t.Fatal("expected stream true")
		}
		if len(request.Input) != 2 {
			t.Fatalf("expected two input messages, got %#v", request.Input)
		}
		if request.Input[0].Type != "message" || request.Input[0].Role != "user" || request.Input[0].Content[0] != (codexContentPart{Type: "input_text", Text: "hello"}) {
			t.Fatalf("unexpected user message: %#v", request.Input[0])
		}
		if request.Input[1].Type != "message" || request.Input[1].Role != "assistant" || request.Input[1].Content[0] != (codexContentPart{Type: "output_text", Text: "previous"}) {
			t.Fatalf("unexpected assistant message: %#v", request.Input[1])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"codex\"}\n\n"))
		_, _ = w.Write([]byte("event: response.output_text.delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\" response\"}\n\n"))
	}))
	defer server.Close()

	provider, err := New([]config.Provider{{
		Name:     "codex",
		Type:     "codex",
		BaseURL:  server.URL,
		AuthFile: authPath,
		Enabled:  true,
	}})
	if err != nil {
		t.Fatalf("expected codex provider to initialize, got %v", err)
	}

	response, err := provider.Send(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "previous"},
		},
		MaxTokens: 128,
	}, nil)
	if err != nil {
		t.Fatalf("expected send to succeed, got %v", err)
	}
	if response.Message.Role != "assistant" || response.Message.Content != "codex response" || len(response.Message.ToolCalls) != 0 {
		t.Fatalf("unexpected response message: %#v", response.Message)
	}
}

func TestSendCodexStreamsBeforeResponseCompletesAndAggregatesCompletion(t *testing.T) {
	t.Setenv("TEST_CODEX_KEY", "secret-token")

	serverFinished := make(chan struct{})
	releaseServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
		w.(http.Flusher).Flush()
		<-releaseServer
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello world\"}]},{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read\",\"arguments\":\"{\\\"filePath\\\":\\\"/tmp/sample.txt\\\"}\"}]}}\n\n"))
		close(serverFinished)
	}))
	defer server.Close()

	provider, err := New([]config.Provider{{
		Name:            "codex",
		Type:            "codex",
		BaseURL:         server.URL,
		AuthTokenEnvVar: "TEST_CODEX_KEY",
		Enabled:         true,
	}})
	if err != nil {
		t.Fatalf("initialize codex provider: %v", err)
	}

	var events []StreamEvent
	response, err := provider.Send(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	}, func(event StreamEvent) error {
		select {
		case <-serverFinished:
			t.Fatal("stream callback ran after server finished")
		default:
		}
		events = append(events, event)
		close(releaseServer)
		return nil
	})
	if err != nil {
		t.Fatalf("send codex request: %v", err)
	}
	if len(events) != 1 || events[0].TextDelta != "hello" {
		t.Fatalf("unexpected stream events: %#v", events)
	}
	if response.Message.Content != "hello world" || len(response.Message.ToolCalls) != 1 {
		t.Fatalf("unexpected aggregate message: %#v", response.Message)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "read" || string(call.Arguments) != `{"filePath":"/tmp/sample.txt"}` {
		t.Fatalf("unexpected aggregate tool call: %#v", call)
	}
	if !strings.Contains(string(response.Raw), "response.output_text.delta") || !strings.Contains(string(response.Raw), "response.completed") {
		t.Fatalf("expected raw SSE response, got %q", response.Raw)
	}
}

func TestSendCodexReturnsStreamCallbackError(t *testing.T) {
	t.Setenv("TEST_CODEX_KEY", "secret-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
	}))
	defer server.Close()

	provider, err := New([]config.Provider{{
		Name:            "codex",
		Type:            "codex",
		BaseURL:         server.URL,
		AuthTokenEnvVar: "TEST_CODEX_KEY",
		Enabled:         true,
	}})
	if err != nil {
		t.Fatalf("initialize codex provider: %v", err)
	}

	callbackErr := errors.New("stop streaming")
	_, err = provider.Send(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	}, func(StreamEvent) error { return callbackErr })
	if !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error, got %v", err)
	}
}

func TestCodexStreamIdleTimeout(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })

	_, err := codexStreamResponseMessageReader(newIdleTimeoutReader(context.Background(), reader, 20*time.Millisecond), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected stream idle timeout, got %v", err)
	}
	if !strings.Contains(err.Error(), "codex stream idle") {
		t.Fatalf("expected descriptive idle timeout, got %v", err)
	}
}

func TestCodexStreamIdleTimeoutResetsAfterProgress(t *testing.T) {
	reader, writer := io.Pipe()
	var writerErr error
	var writerDone sync.WaitGroup
	writerDone.Add(1)
	go func() {
		defer writerDone.Done()
		defer writer.Close()
		for _, delta := range []string{"one", " two", " three"} {
			if _, err := fmt.Fprintf(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", delta); err != nil {
				writerErr = err
				return
			}
			time.Sleep(15 * time.Millisecond)
		}
	}()

	message, err := codexStreamResponseMessageReader(newIdleTimeoutReader(context.Background(), reader, 40*time.Millisecond), nil)
	writerDone.Wait()
	if writerErr != nil {
		t.Fatalf("write stream: %v", writerErr)
	}
	if err != nil {
		t.Fatalf("expected active stream to complete, got %v", err)
	}
	if message.Content != "one two three" {
		t.Fatalf("unexpected streamed content %q", message.Content)
	}
}

func TestSendCodexRefreshesExpiredOpencodeAuth(t *testing.T) {
	authPath := writeCodexAuth(t, map[string]codexAuthInfo{
		"openai": {
			Type:      "oauth",
			Access:    "expired-token",
			Refresh:   "refresh-token",
			Expires:   time.Now().Add(-time.Hour).UnixMilli(),
			AccountID: "old-account",
		},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse refresh form: %v", err)
			}
			if grantType := r.Form.Get("grant_type"); grantType != "refresh_token" {
				t.Fatalf("expected refresh grant, got %q", grantType)
			}
			if refresh := r.Form.Get("refresh_token"); refresh != "refresh-token" {
				t.Fatalf("expected refresh token, got %q", refresh)
			}
			if clientID := r.Form.Get("client_id"); clientID != codexClientID {
				t.Fatalf("expected client id %q, got %q", codexClientID, clientID)
			}

			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "fresh-token",
				"refresh_token": "fresh-refresh-token",
				"expires_in":    3600,
				"id_token":      unsignedJWT(t, map[string]any{"chatgpt_account_id": "fresh-account"}),
			}); err != nil {
				t.Fatalf("encode refresh response: %v", err)
			}
		case "/responses":
			if auth := r.Header.Get("Authorization"); auth != "Bearer fresh-token" {
				t.Fatalf("expected refreshed bearer token, got %q", auth)
			}
			if accountID := r.Header.Get("ChatGPT-Account-Id"); accountID != "fresh-account" {
				t.Fatalf("expected refreshed account id, got %q", accountID)
			}

			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{"output_text": "fresh response"}); err != nil {
				t.Fatalf("encode response: %v", err)
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	previousTokenURL := codexTokenURL
	codexTokenURL = server.URL + "/oauth/token"
	t.Cleanup(func() { codexTokenURL = previousTokenURL })

	provider, err := New([]config.Provider{{
		Name:     "codex",
		Type:     "codex",
		BaseURL:  server.URL,
		AuthFile: authPath,
		Enabled:  true,
	}})
	if err != nil {
		t.Fatalf("expected codex provider to initialize, got %v", err)
	}

	response, err := provider.Send(context.Background(), llm.Request{Messages: []llm.Message{{Role: "user", Content: "hello"}}}, nil)
	if err != nil {
		t.Fatalf("expected send to succeed, got %v", err)
	}
	if response.Message.Content != "fresh response" {
		t.Fatalf("unexpected response content: %#v", response.Message)
	}

	records := readCodexAuthForTest(t, authPath)
	auth := records["openai"]
	if auth.Access != "fresh-token" {
		t.Fatalf("expected refreshed access token, got %q", auth.Access)
	}
	if auth.Refresh != "fresh-refresh-token" {
		t.Fatalf("expected refreshed refresh token, got %q", auth.Refresh)
	}
	if auth.AccountID != "fresh-account" {
		t.Fatalf("expected refreshed account id, got %q", auth.AccountID)
	}
	if auth.Expires <= time.Now().UnixMilli() {
		t.Fatalf("expected future expiry, got %d", auth.Expires)
	}
}

func writeCodexAuth(t *testing.T, records map[string]codexAuthInfo) string {
	t.Helper()

	path := t.TempDir() + "/auth.json"
	contents, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	return path
}

func readCodexAuthForTest(t *testing.T, path string) map[string]codexAuthInfo {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth: %v", err)
	}

	var records map[string]codexAuthInfo
	if err := json.Unmarshal(contents, &records); err != nil {
		t.Fatalf("decode auth: %v", err)
	}

	return records
}

func unsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	return strings.Join([]string{"e30", base64.RawURLEncoding.EncodeToString(payload), "e30"}, ".")
}
