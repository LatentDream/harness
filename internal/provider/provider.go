package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"latentdream/harness/internal/config"
	"latentdream/harness/internal/session/llm"
)

const (
	defaultOpenAIBaseURL     = "https://api.openai.com/v1"
	defaultAnthropicBaseURL  = "https://api.anthropic.com"
	defaultCodexBaseURL      = "https://chatgpt.com/backend-api/codex"
	defaultCodexAuthFile     = "~/.local/share/opencode/auth.json"
	defaultCodexAuthProvider = "openai"
	defaultCodexModel        = "gpt-5.3-codex"
	defaultAnthropicVersion  = "2023-06-01"
	defaultMaxTokens         = 1024
)

var ErrNoEnabledProviders = errors.New("provider: no enabled providers configured")

type Provider interface {
	Current() Selection
	Available() []Selection
	Use(providerName string, modelName string) error
	Send(ctx context.Context, request llm.Request) (Response, error)
}

type Selection struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type Response struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Message  llm.Message     `json:"message"`
	Raw      json.RawMessage `json:"raw,omitempty"`
}

func New(cfg []config.Provider) (Provider, error) {
	manager := &manager{client: http.DefaultClient}
	seen := map[string]struct{}{}

	for index, providerCfg := range cfg {
		if !providerCfg.Enabled {
			continue
		}

		configured, err := newConfiguredProvider(index, providerCfg)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[configured.name]; ok {
			return nil, fmt.Errorf("provider %q is configured more than once", configured.name)
		}

		seen[configured.name] = struct{}{}
		manager.providers = append(manager.providers, configured)
	}

	if len(manager.providers) == 0 {
		return nil, ErrNoEnabledProviders
	}

	manager.current = Selection{
		Provider: manager.providers[0].name,
		Model:    manager.providers[0].models[0],
	}

	return manager, nil
}

type providerKind string

const (
	providerKindOpenAICompatible providerKind = "openai-compatible"
	providerKindAnthropic        providerKind = "anthropic"
	providerKindCodex            providerKind = "codex"
)

type configuredProvider struct {
	name            string
	kind            providerKind
	baseURL         string
	authTokenEnvVar string
	authFile        string
	authProvider    string
	models          []string
}

type manager struct {
	mu        sync.RWMutex
	client    *http.Client
	providers []configuredProvider
	current   Selection
}

func (m *manager) Current() Selection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.current
}

func (m *manager) Available() []Selection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	selections := make([]Selection, 0)
	for _, configured := range m.providers {
		for _, model := range configured.models {
			selections = append(selections, Selection{Provider: configured.name, Model: model})
		}
	}

	return selections
}

func (m *manager) Use(providerName string, modelName string) error {
	providerName = strings.TrimSpace(providerName)
	modelName = strings.TrimSpace(modelName)
	if providerName == "" {
		return errors.New("provider name must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, configured := range m.providers {
		if configured.name != providerName {
			continue
		}

		if modelName == "" {
			modelName = configured.models[0]
		} else if !modelConfigured(configured, modelName) {
			return fmt.Errorf("model %q is not configured for provider %q", modelName, providerName)
		}

		m.current = Selection{Provider: providerName, Model: modelName}
		return nil
	}

	return fmt.Errorf("provider %q is not configured or not enabled", providerName)
}

func (m *manager) Send(ctx context.Context, request llm.Request) (Response, error) {
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}

	configured, model, err := m.selectedProvider()
	if err != nil {
		return Response{}, err
	}

	switch configured.kind {
	case providerKindOpenAICompatible:
		return m.sendOpenAICompatible(ctx, configured, model, request)
	case providerKindAnthropic:
		return m.sendAnthropic(ctx, configured, model, request)
	case providerKindCodex:
		return m.sendCodex(ctx, configured, model, request)
	default:
		return Response{}, fmt.Errorf("provider %q has unsupported type %q", configured.name, configured.kind)
	}
}

func (m *manager) selectedProvider() (configuredProvider, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, configured := range m.providers {
		if configured.name == m.current.Provider && modelConfigured(configured, m.current.Model) {
			return configured, m.current.Model, nil
		}
	}

	return configuredProvider{}, "", errors.New("current provider selection is no longer configured")
}

func newConfiguredProvider(index int, providerCfg config.Provider) (configuredProvider, error) {
	kind, err := providerType(providerCfg.Type, providerCfg.AuthTokenEnvVar, providerCfg.Name)
	if err != nil {
		return configuredProvider{}, err
	}

	name := strings.TrimSpace(providerCfg.Name)
	if name == "" {
		name = defaultProviderName(index, kind, providerCfg.Type, providerCfg.AuthTokenEnvVar)
	}

	models, err := enabledModels(name, kind, providerCfg.Models)
	if err != nil {
		return configuredProvider{}, err
	}

	baseURL, err := providerBaseURL(name, kind, providerCfg.Type, providerCfg.BaseURL)
	if err != nil {
		return configuredProvider{}, err
	}

	authFile := strings.TrimSpace(providerCfg.AuthFile)
	authProvider := strings.TrimSpace(providerCfg.AuthProvider)
	if kind == providerKindCodex {
		if authFile == "" {
			authFile = defaultCodexAuthFile
		}
		if authProvider == "" {
			authProvider = defaultCodexAuthProvider
		}
	}

	return configuredProvider{
		name:            name,
		kind:            kind,
		baseURL:         baseURL,
		authTokenEnvVar: strings.TrimSpace(providerCfg.AuthTokenEnvVar),
		authFile:        authFile,
		authProvider:    authProvider,
		models:          models,
	}, nil
}

func providerType(providerType string, authTokenEnvVar string, name string) (providerKind, error) {
	rawType := strings.ToLower(strings.TrimSpace(providerType))
	switch rawType {
	case "", "openai", "openai-compatible", "litellm", "custom-litellm":
		if rawType == "" && looksAnthropic(authTokenEnvVar, name) {
			return providerKindAnthropic, nil
		}
		return providerKindOpenAICompatible, nil
	case "anthropic", "claude":
		return providerKindAnthropic, nil
	case "codex", "chatgpt", "chatgpt-codex":
		return providerKindCodex, nil
	default:
		return "", fmt.Errorf("provider type %q is not supported", providerType)
	}
}

func defaultProviderName(index int, kind providerKind, providerType string, authTokenEnvVar string) string {
	rawType := strings.ToLower(strings.TrimSpace(providerType))
	if rawType != "" {
		return rawType
	}
	if looksOpenAI(authTokenEnvVar) {
		return "openai"
	}
	if kind == providerKindAnthropic {
		return "anthropic"
	}
	if kind == providerKindCodex {
		return "codex"
	}

	return fmt.Sprintf("provider-%d", index+1)
}

func looksOpenAI(authTokenEnvVar string) bool {
	return strings.Contains(strings.ToLower(authTokenEnvVar), "openai")
}

func looksAnthropic(authTokenEnvVar string, name string) bool {
	return strings.Contains(strings.ToLower(authTokenEnvVar), "anthropic") || strings.EqualFold(strings.TrimSpace(name), "anthropic")
}

func providerBaseURL(name string, kind providerKind, providerType string, baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		switch kind {
		case providerKindOpenAICompatible:
			if strings.EqualFold(strings.TrimSpace(providerType), "litellm") || strings.EqualFold(strings.TrimSpace(providerType), "custom-litellm") {
				return "", fmt.Errorf("provider %q base_url must not be empty", name)
			}
			baseURL = defaultOpenAIBaseURL
		case providerKindAnthropic:
			baseURL = defaultAnthropicBaseURL
		case providerKindCodex:
			baseURL = defaultCodexBaseURL
		default:
			return "", fmt.Errorf("provider %q has unsupported type %q", name, kind)
		}
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("provider %q base_url must be an absolute URL", name)
	}

	return strings.TrimRight(baseURL, "/"), nil
}

func enabledModels(providerName string, kind providerKind, models []config.ProviderModel) ([]string, error) {
	if len(models) == 0 && kind == providerKindCodex {
		return []string{defaultCodexModel}, nil
	}

	modelNames := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for index, model := range models {
		if model.Enabled != nil && !*model.Enabled {
			continue
		}

		name := strings.TrimSpace(model.Name)
		if name == "" {
			return nil, fmt.Errorf("provider %q model %d name must not be empty", providerName, index)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("provider %q model %q is configured more than once", providerName, name)
		}

		seen[name] = struct{}{}
		modelNames = append(modelNames, name)
	}

	if len(modelNames) == 0 {
		return nil, fmt.Errorf("provider %q must configure at least one enabled model", providerName)
	}

	return modelNames, nil
}

func modelConfigured(configured configuredProvider, modelName string) bool {
	for _, model := range configured.models {
		if model == modelName {
			return true
		}
	}

	return false
}

func validateRequest(request llm.Request) error {
	if len(request.Messages) == 0 {
		return errors.New("request must include at least one message")
	}

	for index, message := range request.Messages {
		if strings.TrimSpace(message.Role) == "" {
			return fmt.Errorf("request message %d role must not be empty", index)
		}
	}

	return nil
}

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Tools       []openAITool        `json:"tools,omitempty"`
	Temperature *float64            `json:"temperature,omitempty"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIChatMessage `json:"message"`
	} `json:"choices"`
}

type openAIChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string                   `json:"type"`
	Function openAIFunctionDefinition `json:"function"`
}

type openAIFunctionDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  llm.Schema `json:"parameters"`
}

type openAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (m *manager) sendOpenAICompatible(ctx context.Context, configured configuredProvider, model string, request llm.Request) (Response, error) {
	payload := openAIChatRequest{
		Model:       model,
		Messages:    openAIChatMessages(request.Messages),
		Tools:       openAITools(request.Tools),
		Temperature: request.Temperature,
		MaxTokens:   request.MaxTokens,
	}

	headers, err := openAIHeaders(configured)
	if err != nil {
		return Response{}, err
	}

	body, err := m.postJSON(ctx, configured, "/chat/completions", payload, headers)
	if err != nil {
		return Response{}, err
	}

	var decoded openAIChatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Response{}, fmt.Errorf("decode provider %q response: %w", configured.name, err)
	}
	if len(decoded.Choices) == 0 {
		return Response{}, fmt.Errorf("provider %q returned no choices", configured.name)
	}

	message := messageFromOpenAI(decoded.Choices[0].Message)
	if message.Role == "" {
		message.Role = "assistant"
	}

	return Response{Provider: configured.name, Model: model, Message: message, Raw: body}, nil
}

func openAIChatMessages(messages []llm.Message) []openAIChatMessage {
	converted := make([]openAIChatMessage, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, openAIChatMessage{
			Role:       message.Role,
			Content:    message.Content,
			ToolCalls:  openAIToolCalls(message.ToolCalls),
			ToolCallID: message.ToolCallID,
		})
	}
	return converted
}

func openAITools(definitions []llm.ToolDefinition) []openAITool {
	if len(definitions) == 0 {
		return nil
	}

	tools := make([]openAITool, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "" {
			continue
		}
		tools = append(tools, openAITool{
			Type: "function",
			Function: openAIFunctionDefinition{
				Name:        definition.Name,
				Description: definition.Description,
				Parameters:  definition.Parameters,
			},
		})
	}
	return tools
}

func openAIToolCalls(calls []llm.ToolCall) []openAIToolCall {
	if len(calls) == 0 {
		return nil
	}

	converted := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		converted = append(converted, openAIToolCall{
			ID:   call.ID,
			Type: "function",
			Function: openAIToolCallFunction{
				Name:      call.Name,
				Arguments: rawArgumentsString(call.Arguments),
			},
		})
	}
	return converted
}

func messageFromOpenAI(message openAIChatMessage) llm.Message {
	return llm.Message{
		Role:      message.Role,
		Content:   message.Content,
		ToolCalls: toolCallsFromOpenAI(message.ToolCalls),
	}
}

func toolCallsFromOpenAI(calls []openAIToolCall) []llm.ToolCall {
	if len(calls) == 0 {
		return nil
	}

	converted := make([]llm.ToolCall, 0, len(calls))
	for _, call := range calls {
		if call.Function.Name == "" {
			continue
		}
		converted = append(converted, llm.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: rawArguments(call.Function.Arguments),
		})
	}
	if len(converted) == 0 {
		return nil
	}
	return converted
}

func openAIHeaders(configured configuredProvider) (map[string]string, error) {
	headers := map[string]string{}
	token, err := authToken(configured)
	if err != nil {
		return nil, err
	}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}

	return headers, nil
}

type anthropicMessageRequest struct {
	Model       string                    `json:"model"`
	MaxTokens   int                       `json:"max_tokens"`
	System      string                    `json:"system,omitempty"`
	Messages    []anthropicRequestMessage `json:"messages"`
	Tools       []anthropicTool           `json:"tools,omitempty"`
	Temperature *float64                  `json:"temperature,omitempty"`
}

type anthropicMessageResponse struct {
	Role    string                     `json:"role"`
	Model   string                     `json:"model"`
	Content []anthropicResponseContent `json:"content"`
}

type anthropicRequestMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema llm.Schema `json:"input_schema"`
}

type anthropicContentPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicResponseContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (m *manager) sendAnthropic(ctx context.Context, configured configuredProvider, model string, request llm.Request) (Response, error) {
	messages, system := anthropicMessages(request.Messages)
	if len(messages) == 0 {
		return Response{}, errors.New("anthropic query must include at least one non-system message")
	}

	maxTokens := request.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}

	payload := anthropicMessageRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		System:      system,
		Messages:    messages,
		Tools:       anthropicTools(request.Tools),
		Temperature: request.Temperature,
	}

	headers, err := anthropicHeaders(configured)
	if err != nil {
		return Response{}, err
	}

	body, err := m.postJSON(ctx, configured, "/v1/messages", payload, headers)
	if err != nil {
		return Response{}, err
	}

	var decoded anthropicMessageResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Response{}, fmt.Errorf("decode provider %q response: %w", configured.name, err)
	}

	message := messageFromAnthropic(decoded)
	if message.Role == "" {
		message.Role = "assistant"
	}
	if message.Content == "" && len(message.ToolCalls) == 0 {
		return Response{}, fmt.Errorf("provider %q returned no content", configured.name)
	}

	return Response{Provider: configured.name, Model: model, Message: message, Raw: body}, nil
}

func anthropicMessages(messages []llm.Message) ([]anthropicRequestMessage, string) {
	converted := make([]anthropicRequestMessage, 0, len(messages))
	systemParts := make([]string, 0)

	for _, message := range messages {
		if strings.EqualFold(message.Role, "system") {
			systemParts = append(systemParts, message.Content)
			continue
		}

		if strings.EqualFold(message.Role, "tool") {
			converted = append(converted, anthropicRequestMessage{
				Role: "user",
				Content: []anthropicContentPart{{
					Type:      "tool_result",
					ToolUseID: message.ToolCallID,
					Content:   message.Content,
				}},
			})
			continue
		}

		if len(message.ToolCalls) > 0 {
			parts := make([]anthropicContentPart, 0, len(message.ToolCalls)+1)
			if message.Content != "" {
				parts = append(parts, anthropicContentPart{Type: "text", Text: message.Content})
			}
			for _, call := range message.ToolCalls {
				parts = append(parts, anthropicContentPart{
					Type:  "tool_use",
					ID:    call.ID,
					Name:  call.Name,
					Input: rawArgumentsFromJSON(call.Arguments),
				})
			}
			converted = append(converted, anthropicRequestMessage{Role: message.Role, Content: parts})
			continue
		}

		converted = append(converted, anthropicRequestMessage{Role: message.Role, Content: message.Content})
	}

	return converted, strings.Join(systemParts, "\n\n")
}

func anthropicTools(definitions []llm.ToolDefinition) []anthropicTool {
	if len(definitions) == 0 {
		return nil
	}

	tools := make([]anthropicTool, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "" {
			continue
		}
		tools = append(tools, anthropicTool{
			Name:        definition.Name,
			Description: definition.Description,
			InputSchema: definition.Parameters,
		})
	}
	return tools
}

func messageFromAnthropic(response anthropicMessageResponse) llm.Message {
	return llm.Message{
		Role:      response.Role,
		Content:   anthropicText(response.Content),
		ToolCalls: toolCallsFromAnthropic(response.Content),
	}
}

func anthropicText(content []anthropicResponseContent) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if item.Type == "" || item.Type == "text" {
			parts = append(parts, item.Text)
		}
	}

	return strings.Join(parts, "\n")
}

func toolCallsFromAnthropic(content []anthropicResponseContent) []llm.ToolCall {
	calls := make([]llm.ToolCall, 0)
	for _, item := range content {
		if item.Type != "tool_use" || item.Name == "" {
			continue
		}
		calls = append(calls, llm.ToolCall{
			ID:        item.ID,
			Name:      item.Name,
			Arguments: rawArgumentsFromJSON(item.Input),
		})
	}
	if len(calls) == 0 {
		return nil
	}
	return calls
}

func anthropicHeaders(configured configuredProvider) (map[string]string, error) {
	headers := map[string]string{"anthropic-version": defaultAnthropicVersion}
	token, err := authToken(configured)
	if err != nil {
		return nil, err
	}
	if token != "" {
		headers["x-api-key"] = token
	}

	return headers, nil
}

func (m *manager) postJSON(ctx context.Context, configured configuredProvider, path string, payload any, headers map[string]string) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode provider %q request: %w", configured.name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, configured.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create provider %q request: %w", configured.name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send provider %q request: %w", configured.name, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read provider %q response: %w", configured.name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider %q returned HTTP %d: %s", configured.name, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return respBody, nil
}

func authToken(configured configuredProvider) (string, error) {
	if configured.authTokenEnvVar == "" {
		return "", nil
	}

	token, ok := os.LookupEnv(configured.authTokenEnvVar)
	if !ok || strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("provider %q auth token env var %s is not set", configured.name, configured.authTokenEnvVar)
	}

	return token, nil
}

func rawArgumentsString(arguments json.RawMessage) string {
	return string(rawArgumentsFromJSON(arguments))
}

func rawArgumentsFromJSON(arguments json.RawMessage) json.RawMessage {
	return rawArguments(string(arguments))
}

func rawArguments(arguments string) json.RawMessage {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}

	encoded, err := json.Marshal(trimmed)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}
