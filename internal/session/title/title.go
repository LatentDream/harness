package title

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tracing"
)

const (
	maxTitleRunes = 64
	maxTitleWords = 7
	titlePrompt   = `Generate a short title for a coding-agent session.

Rules:
- Use 3 to 7 words.
- Maximum 64 characters.
- Return only the title.
- Do not use quotation marks.
- Do not end with punctuation.
- Preserve important technical names.
- Do not answer the user's request.`
)

// Generator creates session titles without changing the active conversation.
type Generator struct {
	provider provider.Provider
}

func NewGenerator(aiProvider provider.Provider) *Generator {
	return &Generator{provider: aiProvider}
}

func (g *Generator) Generate(ctx context.Context, prompt string) (string, error) {
	if g == nil || g.provider == nil {
		return "", errors.New("title generator: provider is required")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("title generator: initial prompt is empty")
	}

	selection := g.provider.Current()
	spanCtx, span, err := tracing.BeginSpan(ctx, tracing.SpanStart{
		Kind: tracing.SpanSessionTitle,
		Payload: struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Prompt   string `json:"prompt"`
		}{Provider: selection.Provider, Model: selection.Model, Prompt: prompt},
	})
	if err != nil {
		return "", fmt.Errorf("start title generation trace: %w", err)
	}

	response, callErr := g.provider.Send(spanCtx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: titlePrompt},
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens: 32,
	}, nil)
	if callErr != nil {
		span.End(callErr, nil)
		return "", errors.Join(fmt.Errorf("generate session title: %w", callErr), tracing.Checkpoint(spanCtx))
	}

	title, normalizeErr := Normalize(response.Message.Content)
	span.End(normalizeErr, struct {
		Title string `json:"title,omitempty"`
	}{Title: title})
	if normalizeErr != nil {
		return "", errors.Join(normalizeErr, tracing.Checkpoint(spanCtx))
	}
	return title, tracing.Checkpoint(spanCtx)
}

// Normalize sanitizes untrusted model output into one bounded display title.
func Normalize(value string) (string, error) {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	var selected string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			selected = line
			break
		}
	}
	selected = strings.TrimSpace(strings.TrimLeft(selected, "#"))
	selected = strings.TrimSpace(selected)
	for len(selected) >= 2 {
		first, last := selected[0], selected[len(selected)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') || (first == '`' && last == '`') {
			selected = strings.TrimSpace(selected[1 : len(selected)-1])
			continue
		}
		break
	}
	selected = strings.Join(strings.Fields(selected), " ")
	selected = strings.TrimRightFunc(selected, func(r rune) bool {
		return strings.ContainsRune(".:;!?", r)
	})
	selected = truncateRunes(strings.TrimSpace(selected), maxTitleRunes)
	if selected == "" {
		return "", errors.New("generated session title is empty")
	}
	for _, r := range selected {
		if unicode.IsControl(r) {
			return "", errors.New("generated session title contains control characters")
		}
	}
	if !utf8.ValidString(selected) {
		return "", errors.New("generated session title is not valid UTF-8")
	}
	return selected, nil
}

// Fallback derives a deterministic title when model generation is unavailable.
func Fallback(prompt string) string {
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	line := ""
	for _, candidate := range strings.Split(prompt, "\n") {
		candidate = strings.TrimSpace(strings.TrimLeft(candidate, "#>*-`"))
		if candidate != "" {
			line = candidate
			break
		}
	}
	words := strings.Fields(line)
	if len(words) > maxTitleWords {
		words = words[:maxTitleWords]
	}
	candidate, err := Normalize(strings.Join(words, " "))
	if err != nil {
		return "Untitled"
	}
	return candidate
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit]))
}
