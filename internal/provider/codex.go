package provider

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	codexClientID         = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexOAuthTokenURL    = "https://auth.openai.com/oauth/token"
	codexAuthRefreshSkew  = 30 * time.Second
	codexResponsesPath    = "/responses"
	codexHeaderOriginator = "harness"
	codexHeaderUserAgent  = "harness"
	codexResponseRole     = "assistant"
)

var codexTokenURL = codexOAuthTokenURL

type codexAuthInfo struct {
	Type      string `json:"type"`
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	Expires   int64  `json:"expires"`
	AccountID string `json:"accountId,omitempty"`
}

type codexTokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type codexClaims struct {
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	OpenAIAuth       struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
	Organizations []struct {
		ID string `json:"id"`
	} `json:"organizations"`
}

type codexCredential struct {
	Access    string
	AccountID string
}

type codexResponsesRequest struct {
	Model           string              `json:"model"`
	Input           []codexInputMessage `json:"input"`
	Store           bool                `json:"store"`
	Stream          bool                `json:"stream"`
	Instructions    string              `json:"instructions,omitempty"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
}

type codexInputMessage struct {
	Role    string             `json:"role"`
	Content []codexContentPart `json:"content"`
}

type codexContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexResponsesResponse struct {
	OutputText string `json:"output_text"`
	Output     []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func (m *manager) sendCodex(ctx context.Context, configured configuredProvider, model string, query Query) (Response, error) {
	payload := codexRequest(model, query)
	headers, err := m.codexHeaders(ctx, configured)
	if err != nil {
		return Response{}, err
	}

	body, err := m.postJSON(ctx, configured, codexPath(configured), payload, headers)
	if err != nil {
		return Response{}, err
	}

	content, err := codexResponseContent(body)
	if err != nil {
		return Response{}, fmt.Errorf("decode provider %q response: %w", configured.name, err)
	}

	return Response{
		Provider: configured.name,
		Model:    model,
		Message:  Message{Role: codexResponseRole, Content: content},
		Raw:      body,
	}, nil
}

func codexRequest(model string, query Query) codexResponsesRequest {
	request := codexResponsesRequest{
		Model:           model,
		Input:           make([]codexInputMessage, 0, len(query.Messages)),
		Store:           false,
		Stream:          true,
		MaxOutputTokens: query.MaxTokens,
	}
	instructions := make([]string, 0)

	for _, message := range query.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "system", "developer":
			instructions = append(instructions, message.Content)
		case "assistant":
			request.Input = append(request.Input, codexInputMessage{
				Role: role,
				Content: []codexContentPart{{
					Type: "output_text",
					Text: message.Content,
				}},
			})
		default:
			request.Input = append(request.Input, codexInputMessage{
				Role: role,
				Content: []codexContentPart{{
					Type: "input_text",
					Text: message.Content,
				}},
			})
		}
	}

	request.Instructions = strings.Join(instructions, "\n\n")
	return request
}

func codexPath(configured configuredProvider) string {
	if strings.HasSuffix(configured.baseURL, codexResponsesPath) {
		return ""
	}

	return codexResponsesPath
}

func (m *manager) codexHeaders(ctx context.Context, configured configuredProvider) (map[string]string, error) {
	credential, err := m.codexCredential(ctx, configured)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"authorization": "Bearer " + credential.Access,
		"originator":    codexHeaderOriginator,
		"User-Agent":    codexHeaderUserAgent,
	}
	if credential.AccountID != "" {
		headers["ChatGPT-Account-Id"] = credential.AccountID
	}

	return headers, nil
}

func (m *manager) codexCredential(ctx context.Context, configured configuredProvider) (codexCredential, error) {
	if configured.authTokenEnvVar != "" {
		token, err := authToken(configured)
		if err != nil {
			return codexCredential{}, err
		}
		return codexCredential{Access: token}, nil
	}

	authFile, err := expandHome(configured.authFile)
	if err != nil {
		return codexCredential{}, err
	}

	records, err := readCodexAuthFile(authFile)
	if err != nil {
		return codexCredential{}, err
	}

	auth, ok := records[configured.authProvider]
	if !ok {
		return codexCredential{}, fmt.Errorf("provider %q codex auth %q not found in %s", configured.name, configured.authProvider, authFile)
	}
	if auth.Type != "oauth" {
		return codexCredential{}, fmt.Errorf("provider %q codex auth %q in %s has type %q, want oauth", configured.name, configured.authProvider, authFile, auth.Type)
	}

	if auth.Refresh != "" && codexAuthExpired(auth.Expires) {
		auth, err = m.refreshCodexAuth(ctx, configured, authFile, records, auth)
		if err != nil {
			return codexCredential{}, err
		}
	}
	if strings.TrimSpace(auth.Access) == "" {
		return codexCredential{}, fmt.Errorf("provider %q codex auth %q in %s has no access token", configured.name, configured.authProvider, authFile)
	}

	return codexCredential{Access: auth.Access, AccountID: auth.AccountID}, nil
}

func readCodexAuthFile(path string) (map[string]codexAuthInfo, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read codex auth file %q: %w", path, err)
	}

	records := map[string]codexAuthInfo{}
	if err := json.Unmarshal(contents, &records); err != nil {
		return nil, fmt.Errorf("decode codex auth file %q: %w", path, err)
	}

	return records, nil
}

func codexAuthExpired(expires int64) bool {
	if expires == 0 {
		return false
	}

	return time.UnixMilli(expires).Before(time.Now().Add(codexAuthRefreshSkew))
}

func (m *manager) refreshCodexAuth(ctx context.Context, configured configuredProvider, authFile string, records map[string]codexAuthInfo, auth codexAuthInfo) (codexAuthInfo, error) {
	tokens, err := m.requestCodexRefresh(ctx, configured, auth.Refresh)
	if err != nil {
		return codexAuthInfo{}, err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return codexAuthInfo{}, fmt.Errorf("provider %q codex token refresh returned no access token", configured.name)
	}

	auth.Access = tokens.AccessToken
	if tokens.RefreshToken != "" {
		auth.Refresh = tokens.RefreshToken
	}
	if tokens.ExpiresIn > 0 {
		auth.Expires = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).UnixMilli()
	}
	if accountID := codexAccountID(tokens); accountID != "" {
		auth.AccountID = accountID
	}

	records[configured.authProvider] = auth
	if err := writeCodexAuthFile(authFile, records); err != nil {
		return codexAuthInfo{}, err
	}

	return auth, nil
}

func (m *manager) requestCodexRefresh(ctx context.Context, configured configuredProvider, refreshToken string) (codexTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {codexClientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return codexTokenResponse{}, fmt.Errorf("create provider %q codex token refresh request: %w", configured.name, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.client.Do(req)
	if err != nil {
		return codexTokenResponse{}, fmt.Errorf("send provider %q codex token refresh request: %w", configured.name, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return codexTokenResponse{}, fmt.Errorf("read provider %q codex token refresh response: %w", configured.name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return codexTokenResponse{}, fmt.Errorf("provider %q codex token refresh returned HTTP %d: %s", configured.name, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokens codexTokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return codexTokenResponse{}, fmt.Errorf("decode provider %q codex token refresh response: %w", configured.name, err)
	}

	return tokens, nil
}

func writeCodexAuthFile(path string, records map[string]codexAuthInfo) error {
	contents, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("encode codex auth file %q: %w", path, err)
	}
	contents = append(contents, '\n')

	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write codex auth file %q: %w", path, err)
	}

	return nil
}

func codexAccountID(tokens codexTokenResponse) string {
	for _, token := range []string{tokens.IDToken, tokens.AccessToken} {
		claims, ok := parseCodexClaims(token)
		if !ok {
			continue
		}
		if claims.ChatGPTAccountID != "" {
			return claims.ChatGPTAccountID
		}
		if claims.OpenAIAuth.ChatGPTAccountID != "" {
			return claims.OpenAIAuth.ChatGPTAccountID
		}
		if len(claims.Organizations) > 0 && claims.Organizations[0].ID != "" {
			return claims.Organizations[0].ID
		}
	}

	return ""
}

func parseCodexClaims(token string) (codexClaims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return codexClaims{}, false
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return codexClaims{}, false
	}

	var claims codexClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return codexClaims{}, false
	}

	return claims, true
}

func codexResponseContent(body []byte) (string, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "event:") || strings.HasPrefix(trimmed, "data:") || strings.Contains(trimmed, "\ndata:") {
		return codexStreamResponseContent(body)
	}

	return codexJSONResponseContent(body)
}

func codexJSONResponseContent(body []byte) (string, error) {
	var decoded codexResponsesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", err
	}

	if strings.TrimSpace(decoded.OutputText) != "" {
		return decoded.OutputText, nil
	}

	parts := make([]string, 0)
	for _, item := range decoded.Output {
		if item.Type != "" && item.Type != "message" {
			continue
		}
		if item.Role != "" && item.Role != codexResponseRole {
			continue
		}
		for _, content := range item.Content {
			if content.Type != "" && content.Type != "output_text" && content.Type != "text" {
				continue
			}
			if content.Text != "" {
				parts = append(parts, content.Text)
			}
		}
	}

	if len(parts) == 0 {
		return "", errors.New("codex response contained no text output")
	}

	return strings.Join(parts, "\n"), nil
}

func codexStreamResponseContent(body []byte) (string, error) {
	var deltas strings.Builder
	finalText := ""
	dataLines := make([]string, 0)
	scanner := bufio.NewScanner(strings.NewReader(string(body)))

	process := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if payload == "" || payload == "[DONE]" {
			return nil
		}

		var event struct {
			Type       string          `json:"type"`
			Delta      string          `json:"delta"`
			Text       string          `json:"text"`
			OutputText string          `json:"output_text"`
			Response   json.RawMessage `json:"response"`
			Error      *struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("decode codex stream event: %w", err)
		}
		if event.Error != nil {
			if event.Error.Code != "" {
				return fmt.Errorf("codex stream error %s: %s", event.Error.Code, event.Error.Message)
			}
			return fmt.Errorf("codex stream error: %s", event.Error.Message)
		}
		if event.Delta != "" {
			deltas.WriteString(event.Delta)
		}
		if event.OutputText != "" {
			finalText = event.OutputText
		}
		if event.Text != "" && strings.Contains(event.Type, "output_text") {
			finalText = event.Text
		}
		if len(event.Response) > 0 {
			content, err := codexJSONResponseContent(event.Response)
			if err == nil {
				finalText = content
			}
		}

		return nil
	}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := process(); err != nil {
				return "", err
			}
			continue
		}
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			dataLines = append(dataLines, strings.TrimSpace(data))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read codex stream: %w", err)
	}
	if err := process(); err != nil {
		return "", err
	}

	if deltas.Len() > 0 {
		return deltas.String(), nil
	}
	if finalText != "" {
		return finalText, nil
	}

	return "", errors.New("codex stream contained no text output")
}

func expandHome(path string) (string, error) {
	if path == "" || (path != "~" && !strings.HasPrefix(path, "~/")) {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}

	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}
