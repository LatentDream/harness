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

	"latentdream/harness/internal/logging"
	"latentdream/harness/internal/session/llm"

	"go.uber.org/zap"
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
	Model           string           `json:"model"`
	Input           []codexInputItem `json:"input"`
	Store           bool             `json:"store"`
	Stream          bool             `json:"stream"`
	Instructions    string           `json:"instructions,omitempty"`
	MaxOutputTokens int              `json:"max_output_tokens,omitempty"`
	Tools           []codexTool      `json:"tools,omitempty"`
}

type codexInputItem struct {
	Type      string             `json:"type,omitempty"`
	Role      string             `json:"role,omitempty"`
	Content   []codexContentPart `json:"content,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	Output    string             `json:"output,omitempty"`
}

type codexContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexTool struct {
	Type        string     `json:"type"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  llm.Schema `json:"parameters"`
}

type codexResponsesResponse struct {
	OutputText string            `json:"output_text"`
	Output     []codexOutputItem `json:"output"`
}

type codexOutputItem struct {
	Type      string `json:"type"`
	Role      string `json:"role"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (m *manager) sendCodex(ctx context.Context, configured configuredProvider, model string, request llm.Request, stream StreamHandler) (Response, error) {
	payload := codexRequest(model, request)
	headers, err := m.codexHeaders(ctx, configured)
	if err != nil {
		return Response{}, err
	}

	resp, err := m.postJSONResponse(ctx, configured, codexPath(configured), payload, headers)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return Response{}, fmt.Errorf("read provider %q response: %w", configured.name, readErr)
		}
		return Response{}, fmt.Errorf("provider %q returned HTTP %d: %s", configured.name, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var raw strings.Builder
	reader := io.TeeReader(resp.Body, &raw)
	var message llm.Message
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		message, err = codexStreamResponseMessageReader(reader, stream)
	} else {
		body, readErr := io.ReadAll(reader)
		if readErr != nil {
			return Response{}, fmt.Errorf("read provider %q response: %w", configured.name, readErr)
		}
		message, err = codexResponseMessage(body)
		if err == nil {
			err = emitText(stream, message.Content)
		}
	}
	body := []byte(raw.String())

	logging.Log(ctx).Debug("received codex response body",
		zap.String("provider", configured.name),
		zap.String("model", model),
		zap.String("body", string(body)),
	)

	if err != nil {
		return Response{}, fmt.Errorf("decode provider %q response: %w", configured.name, err)
	}
	if message.Role == "" {
		message.Role = codexResponseRole
	}

	return Response{
		Provider: configured.name,
		Model:    model,
		Message:  message,
		Raw:      body,
	}, nil
}

func codexRequest(model string, input llm.Request) codexResponsesRequest {
	request := codexResponsesRequest{
		Model:           model,
		Input:           make([]codexInputItem, 0, len(input.Messages)),
		Store:           false,
		Stream:          true,
		MaxOutputTokens: input.MaxTokens,
		Tools:           codexTools(input.Tools),
	}
	instructions := make([]string, 0)

	for _, message := range input.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "system", "developer":
			instructions = append(instructions, message.Content)
		case "tool":
			request.Input = append(request.Input, codexInputItem{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: message.Content,
			})
		case "assistant":
			if message.Content != "" {
				request.Input = append(request.Input, codexInputItem{
					Type: "message",
					Role: role,
					Content: []codexContentPart{{
						Type: "output_text",
						Text: message.Content,
					}},
				})
			}
			for _, call := range message.ToolCalls {
				request.Input = append(request.Input, codexInputItem{
					Type:      "function_call",
					CallID:    call.ID,
					Name:      call.Name,
					Arguments: rawArgumentsString(call.Arguments),
				})
			}
		default:
			request.Input = append(request.Input, codexInputItem{
				Type: "message",
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

func codexTools(definitions []llm.ToolDefinition) []codexTool {
	if len(definitions) == 0 {
		return nil
	}

	tools := make([]codexTool, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "" {
			continue
		}
		tools = append(tools, codexTool{
			Type:        "function",
			Name:        definition.Name,
			Description: definition.Description,
			Parameters:  definition.Parameters,
		})
	}
	return tools
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
	message, err := codexResponseMessage(body)
	if err != nil {
		return "", err
	}
	if message.Content == "" {
		return "", errors.New("codex response contained no text output")
	}
	return message.Content, nil
}

func codexResponseMessage(body []byte) (llm.Message, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "event:") || strings.HasPrefix(trimmed, "data:") || strings.Contains(trimmed, "\ndata:") {
		return codexStreamResponseMessage(body)
	}

	return codexJSONResponseMessage(body)
}

func codexJSONResponseMessage(body []byte) (llm.Message, error) {
	var decoded codexResponsesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return llm.Message{}, err
	}

	content := codexJSONText(decoded)
	calls := codexToolCalls(decoded.Output)
	if content == "" && len(calls) == 0 {
		return llm.Message{}, errors.New("codex response contained no output")
	}

	return llm.Message{Role: codexResponseRole, Content: content, ToolCalls: calls}, nil
}

func codexJSONText(decoded codexResponsesResponse) string {
	if strings.TrimSpace(decoded.OutputText) != "" {
		return decoded.OutputText
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

	return strings.Join(parts, "\n")
}

func codexToolCalls(items []codexOutputItem) []llm.ToolCall {
	calls := make([]llm.ToolCall, 0)
	for _, item := range items {
		if item.Type != "function_call" || item.Name == "" {
			continue
		}
		calls = append(calls, llm.ToolCall{
			ID:        item.CallID,
			Name:      item.Name,
			Arguments: rawArguments(item.Arguments),
		})
	}
	if len(calls) == 0 {
		return nil
	}
	return calls
}

type codexStreamCall struct {
	ID        string
	Name      string
	Arguments string
}

func codexStreamResponseMessage(body []byte) (llm.Message, error) {
	return codexStreamResponseMessageReader(strings.NewReader(string(body)), nil)
}

func codexStreamResponseMessageReader(reader io.Reader, stream StreamHandler) (llm.Message, error) {
	var deltas strings.Builder
	finalText := ""
	var finalMessage *llm.Message
	calls := make(map[string]*codexStreamCall)
	callOrder := make([]string, 0)
	dataLines := make([]string, 0)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	streamCall := func(key string) *codexStreamCall {
		if key == "" {
			key = fmt.Sprintf("call_%d", len(callOrder)+1)
		}
		call, ok := calls[key]
		if !ok {
			call = &codexStreamCall{}
			calls[key] = call
			callOrder = append(callOrder, key)
		}
		return call
	}

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
			Type       string `json:"type"`
			Delta      string `json:"delta"`
			Text       string `json:"text"`
			OutputText string `json:"output_text"`
			Arguments  string `json:"arguments"`
			ItemID     string `json:"item_id"`
			Item       *struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"item"`
			Response json.RawMessage `json:"response"`
			Error    *struct {
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
		if event.Delta != "" && (event.Type == "" || strings.Contains(event.Type, "output_text")) {
			deltas.WriteString(event.Delta)
			if err := emitText(stream, event.Delta); err != nil {
				return err
			}
		}
		if event.OutputText != "" {
			finalText = event.OutputText
		}
		if event.Text != "" && strings.Contains(event.Type, "output_text") {
			finalText = event.Text
		}
		if event.Item != nil && event.Item.Type == "function_call" {
			key := event.Item.ID
			if key == "" {
				key = event.Item.CallID
			}
			call := streamCall(key)
			call.ID = event.Item.CallID
			call.Name = event.Item.Name
			call.Arguments = event.Item.Arguments
		}
		if strings.Contains(event.Type, "function_call_arguments") {
			call := streamCall(event.ItemID)
			if event.Arguments != "" {
				call.Arguments = event.Arguments
			} else if event.Delta != "" {
				call.Arguments += event.Delta
			}
		}
		if len(event.Response) > 0 {
			message, err := codexJSONResponseMessage(event.Response)
			if err == nil {
				finalMessage = &message
			}
		}

		return nil
	}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := process(); err != nil {
				return llm.Message{}, err
			}
			continue
		}
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			dataLines = append(dataLines, strings.TrimSpace(data))
		}
	}
	if err := scanner.Err(); err != nil {
		return llm.Message{}, fmt.Errorf("read codex stream: %w", err)
	}
	if err := process(); err != nil {
		return llm.Message{}, err
	}

	if finalMessage != nil {
		if finalMessage.Content == "" && deltas.Len() > 0 {
			finalMessage.Content = deltas.String()
		}
		return *finalMessage, nil
	}

	toolCalls := make([]llm.ToolCall, 0, len(callOrder))
	for _, key := range callOrder {
		call := calls[key]
		if call == nil || call.Name == "" {
			continue
		}
		toolCalls = append(toolCalls, llm.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: rawArguments(call.Arguments),
		})
	}
	if deltas.Len() > 0 {
		return llm.Message{Role: codexResponseRole, Content: deltas.String(), ToolCalls: toolCalls}, nil
	}
	if finalText != "" {
		return llm.Message{Role: codexResponseRole, Content: finalText, ToolCalls: toolCalls}, nil
	}
	if len(toolCalls) > 0 {
		return llm.Message{Role: codexResponseRole, ToolCalls: toolCalls}, nil
	}

	return llm.Message{}, errors.New("codex stream contained no output")
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
