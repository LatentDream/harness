package command

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"latentdream/harness/internal/provider"
)

func TestModelCommandListsAllSelections(t *testing.T) {
	provider := &fakeModelProvider{
		current: provider.Selection{Provider: "openai", Model: "gpt-4.1"},
		available: []provider.Selection{
			{Provider: "openai", Model: "gpt-4.1"},
			{Provider: "openai", Model: "gpt-4o"},
			{Provider: "anthropic", Model: "claude-sonnet-4"},
			{Provider: "local", Model: "llama"},
		},
	}
	cmd := NewModelCmd(provider)

	result, err := cmd.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	for _, expected := range []string{
		"* openai/gpt-4.1",
		"  openai/gpt-4o",
		"  anthropic/claude-sonnet-4",
		"  local/llama",
		"Usage: /model <provider>/<model>",
	} {
		if !strings.Contains(result.Output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, result.Output)
		}
	}
}

func TestModelCommandSwitchesModelNamesWithSpaces(t *testing.T) {
	modelProvider := &fakeModelProvider{}
	cmd := NewModelCmd(modelProvider).(interface {
		ExecuteRaw(context.Context, string) (Result, error)
	})

	result, err := cmd.ExecuteRaw(context.Background(), "anthropic/claude sonnet 4")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Output != "switched model to anthropic/claude sonnet 4" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	expectedCalls := []provider.Selection{{Provider: "anthropic", Model: "claude sonnet 4"}}
	if !reflect.DeepEqual(modelProvider.useCalls, expectedCalls) {
		t.Fatalf("expected use calls %#v, got %#v", expectedCalls, modelProvider.useCalls)
	}
}

func TestModelCommandSwitchesProviderAndModel(t *testing.T) {
	modelProvider := &fakeModelProvider{}
	cmd := NewModelCmd(modelProvider)

	result, err := cmd.Execute(context.Background(), []string{"anthropic/claude-sonnet-4"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Output != "switched model to anthropic/claude-sonnet-4" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	if result.Provider != "anthropic" || result.Model != "claude-sonnet-4" {
		t.Fatalf("expected result selection anthropic/claude-sonnet-4, got %#v", result)
	}
	expectedCalls := []provider.Selection{{Provider: "anthropic", Model: "claude-sonnet-4"}}
	if !reflect.DeepEqual(modelProvider.useCalls, expectedCalls) {
		t.Fatalf("expected use calls %#v, got %#v", expectedCalls, modelProvider.useCalls)
	}
}

func TestModelCommandRejectsInvalidArguments(t *testing.T) {
	cmd := NewModelCmd(&fakeModelProvider{})
	tests := [][]string{
		{"gpt-4o"},
		{"openai/"},
		{"/gpt-4o"},
		{"/"},
		{"openai/gpt-4o/extra"},
	}
	for _, args := range tests {
		result, err := cmd.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("expected no error for args %#v, got %v", args, err)
		}
		if result.Output != modelUsage {
			t.Fatalf("expected usage for args %#v, got %q", args, result.Output)
		}
	}
}

func TestModelCommandReturnsProviderErrorAsOutput(t *testing.T) {
	cmd := NewModelCmd(&fakeModelProvider{err: errors.New(`model "missing" is not configured for provider "openai"`)})

	result, err := cmd.Execute(context.Background(), []string{"openai/missing"})
	if err != nil {
		t.Fatalf("expected no command error, got %v", err)
	}
	expected := `error: model "missing" is not configured for provider "openai"`
	if result.Output != expected {
		t.Fatalf("expected output %q, got %q", expected, result.Output)
	}
}

func TestModelCommandHandlesNilProvider(t *testing.T) {
	cmd := NewModelCmd(nil)

	result, err := cmd.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Output != "error: provider is not configured" {
		t.Fatalf("unexpected output %q", result.Output)
	}
}

type fakeModelProvider struct {
	current   provider.Selection
	available []provider.Selection
	useCalls  []provider.Selection
	err       error
}

func (f *fakeModelProvider) Current() provider.Selection {
	return f.current
}

func (f *fakeModelProvider) Available() []provider.Selection {
	return append([]provider.Selection(nil), f.available...)
}

func (f *fakeModelProvider) Use(providerName string, modelName string) error {
	f.useCalls = append(f.useCalls, provider.Selection{Provider: providerName, Model: modelName})
	if f.err != nil {
		return f.err
	}
	f.current = provider.Selection{Provider: providerName, Model: modelName}
	return nil
}
