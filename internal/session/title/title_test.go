package title

import (
	"context"
	"errors"
	"testing"

	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/session/llm"
)

func TestNormalize(t *testing.T) {
	tests := []struct{ input, want string }{
		{`"Fix nested trailing commas."`, "Fix nested trailing commas"},
		{"\n## Investigate Parser Failure!\nignored", "Investigate Parser Failure"},
		{"`Add OAuth authentication`", "Add OAuth authentication"},
		{"  compact   repeated   whitespace  ", "compact repeated whitespace"},
	}
	for _, test := range tests {
		got, err := Normalize(test.input)
		if err != nil || got != test.want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
}

func TestNormalizeRejectsEmptyAndBoundsUnicode(t *testing.T) {
	if _, err := Normalize(" \n "); err == nil {
		t.Fatal("expected empty title error")
	}
	got, err := Normalize("界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界")
	if err != nil || len([]rune(got)) != maxTitleRunes {
		t.Fatalf("unicode title = %q (%d), %v", got, len([]rune(got)), err)
	}
}

func TestFallback(t *testing.T) {
	got := Fallback("Please investigate why nested parser objects reject trailing commas in configuration")
	if got != "Please investigate why nested parser objects reject" {
		t.Fatalf("fallback = %q", got)
	}
	if got := Fallback("\n\t"); got != "Untitled" {
		t.Fatalf("empty fallback = %q", got)
	}
}

func TestGeneratorUsesSeparateToollessRequest(t *testing.T) {
	fake := &titleProvider{response: `"Fix Parser Commas."`}
	generator := NewGenerator(fake)
	got, err := generator.Generate(context.Background(), "please fix commas")
	if err != nil || got != "Fix Parser Commas" {
		t.Fatalf("generate = %q, %v", got, err)
	}
	if len(fake.request.Messages) != 2 || fake.request.Messages[1].Content != "please fix commas" || len(fake.request.Tools) != 0 || fake.request.MaxTokens != 32 {
		t.Fatalf("request = %#v", fake.request)
	}
}

func TestGeneratorPropagatesProviderFailure(t *testing.T) {
	generator := NewGenerator(&titleProvider{err: errors.New("offline")})
	if _, err := generator.Generate(context.Background(), "prompt"); err == nil {
		t.Fatal("expected provider error")
	}
}

type titleProvider struct {
	request  llm.Request
	response string
	err      error
}

func (p *titleProvider) Current() provider.Selection {
	return provider.Selection{Provider: "fake", Model: "title"}
}
func (p *titleProvider) Available() []provider.Selection { return []provider.Selection{p.Current()} }
func (p *titleProvider) Use(string, string) error        { return nil }
func (p *titleProvider) Send(_ context.Context, request llm.Request, _ provider.StreamHandler) (provider.Response, error) {
	p.request = request
	return provider.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: p.response}}, p.err
}
