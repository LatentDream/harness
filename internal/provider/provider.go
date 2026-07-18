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
)

const (
	defaultOpenAIBaseURL    = "https://api.openai.com/v1"
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultAnthropicVersion = "2023-06-01"
	defaultMaxTokens        = 1024
)

var ErrNoEnabledProviders = errors.New("provider: no enabled providers configured")

type Provider interface {
	Current() Selection
	Available() []Selection
	Use(providerName string, modelName string) error
	Send(ctx context.Context, query Query) (Response, error)
}

type Selection struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type Query struct {
	Messages    []Message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Response struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Message  Message         `json:"message"`
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
)

type configuredProvider struct {
	name            string
	kind            providerKind
	baseURL         string
	authTokenEnvVar string
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

func (m *manager) Send(ctx context.Context, query Query) (Response, error) {
	if err := validateQuery(query); err != nil {
		return Response{}, err
	}

	configured, model, err := m.selectedProvider()
	if err != nil {
		return Response{}, err
	}

	switch configured.kind {
	case providerKindOpenAICompatible:
		return m.sendOpenAICompatible(ctx, configured, model, query)
	case providerKindAnthropic:
		return m.sendAnthropic(ctx, configured, model, query)
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

	models, err := enabledModels(name, providerCfg.Models)
	if err != nil {
		return configuredProvider{}, err
	}

	baseURL, err := providerBaseURL(name, kind, providerCfg.Type, providerCfg.BaseURL)
	if err != nil {
		return configuredProvider{}, err
	}

	return configuredProvider{
		name:            name,
		kind:            kind,
		baseURL:         baseURL,
		authTokenEnvVar: strings.TrimSpace(providerCfg.AuthTokenEnvVar),
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

func enabledModels(providerName string, models []config.ProviderModel) ([]string, error) {
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

func validateQuery(query Query) error {
	if len(query.Messages) == 0 {
		return errors.New("query must include at least one message")
	}

	for index, message := range query.Messages {
		if strings.TrimSpace(message.Role) == "" {
			return fmt.Errorf("query message %d role must not be empty", index)
		}
	}

	return nil
}

type openAIChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

func (m *manager) sendOpenAICompatible(ctx context.Context, configured configuredProvider, model string, query Query) (Response, error) {
	payload := openAIChatRequest{
		Model:       model,
		Messages:    query.Messages,
		Temperature: query.Temperature,
		MaxTokens:   query.MaxTokens,
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

	message := decoded.Choices[0].Message
	if message.Role == "" {
		message.Role = "assistant"
	}

	return Response{Provider: configured.name, Model: model, Message: message, Raw: body}, nil
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
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens"`
	System      string    `json:"system,omitempty"`
	Messages    []Message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
}

type anthropicMessageResponse struct {
	Role    string `json:"role"`
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (m *manager) sendAnthropic(ctx context.Context, configured configuredProvider, model string, query Query) (Response, error) {
	messages, system := anthropicMessages(query.Messages)
	if len(messages) == 0 {
		return Response{}, errors.New("anthropic query must include at least one non-system message")
	}

	maxTokens := query.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}

	payload := anthropicMessageRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		System:      system,
		Messages:    messages,
		Temperature: query.Temperature,
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

	content := anthropicText(decoded.Content)
	if content == "" {
		return Response{}, fmt.Errorf("provider %q returned no text content", configured.name)
	}

	role := decoded.Role
	if role == "" {
		role = "assistant"
	}

	return Response{Provider: configured.name, Model: model, Message: Message{Role: role, Content: content}, Raw: body}, nil
}

func anthropicMessages(messages []Message) ([]Message, string) {
	converted := make([]Message, 0, len(messages))
	systemParts := make([]string, 0)

	for _, message := range messages {
		if strings.EqualFold(message.Role, "system") {
			systemParts = append(systemParts, message.Content)
			continue
		}

		converted = append(converted, message)
	}

	return converted, strings.Join(systemParts, "\n\n")
}

func anthropicText(content []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if item.Type == "" || item.Type == "text" {
			parts = append(parts, item.Text)
		}
	}

	return strings.Join(parts, "\n")
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
