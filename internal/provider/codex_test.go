package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"latentdream/harness/internal/config"
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

func TestCodexRequestIncludesStoreFalse(t *testing.T) {
	payload := codexRequest(defaultCodexModel, Query{Messages: []Message{{Role: "user", Content: "hello"}}})
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
		if len(request.Input) != 2 {
			t.Fatalf("expected two input messages, got %#v", request.Input)
		}
		if request.Input[0].Role != "user" || request.Input[0].Content[0] != (codexContentPart{Type: "input_text", Text: "hello"}) {
			t.Fatalf("unexpected user message: %#v", request.Input[0])
		}
		if request.Input[1].Role != "assistant" || request.Input[1].Content[0] != (codexContentPart{Type: "output_text", Text: "previous"}) {
			t.Fatalf("unexpected assistant message: %#v", request.Input[1])
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]string{
						{"type": "output_text", "text": "codex response"},
					},
				},
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
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

	response, err := provider.Send(context.Background(), Query{
		Messages: []Message{
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "previous"},
		},
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("expected send to succeed, got %v", err)
	}
	if response.Message != (Message{Role: "assistant", Content: "codex response"}) {
		t.Fatalf("unexpected response message: %#v", response.Message)
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

	response, err := provider.Send(context.Background(), Query{Messages: []Message{{Role: "user", Content: "hello"}}})
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
