package command

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSwitchCommandResolvesRawTitle(t *testing.T) {
	resolver := &switchResolverStub{resolved: SessionSummary{ID: "session-id", Title: "parser work"}}
	registry := NewRegistry(NewSwitchCmd(resolver))

	result, handled, err := registry.Execute(context.Background(), "/switch parser work")
	if err != nil || !handled {
		t.Fatalf("execute switch: handled=%v err=%v", handled, err)
	}
	if resolver.selector != "parser work" {
		t.Fatalf("selector = %q", resolver.selector)
	}
	if result.Action != ActionSwitchSession || result.SessionID != "session-id" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSwitchCommandListsSessions(t *testing.T) {
	resolver := &switchResolverStub{sessions: []SessionSummary{
		{ID: "12345678-abcd", Title: "parser work"},
		{ID: "abcdef12-abcd"},
	}}
	registry := NewRegistry(NewSwitchCmd(resolver))
	result, handled, err := registry.Execute(context.Background(), "/switch")
	if err != nil || !handled {
		t.Fatalf("execute switch list: handled=%v err=%v", handled, err)
	}
	for _, expected := range []string{"12345678  parser work", "abcdef12  Untitled", switchUsage} {
		if !strings.Contains(result.Output, expected) {
			t.Fatalf("output %q does not contain %q", result.Output, expected)
		}
	}
}

func TestSwitchCommandReportsResolutionErrorWithoutTransition(t *testing.T) {
	resolver := &switchResolverStub{err: errors.New("session not found")}
	registry := NewRegistry(NewSwitchCmd(resolver))
	result, handled, err := registry.Execute(context.Background(), "/switch missing")
	if err != nil || !handled {
		t.Fatalf("execute switch: handled=%v err=%v", handled, err)
	}
	if result.Action != ActionContinue || result.Output != "error: session not found" {
		t.Fatalf("result = %#v", result)
	}
}

type switchResolverStub struct {
	selector string
	resolved SessionSummary
	sessions []SessionSummary
	err      error
}

func (r *switchResolverStub) ResolveSession(_ context.Context, selector string) (SessionSummary, error) {
	r.selector = selector
	return r.resolved, r.err
}

func (r *switchResolverStub) ListSessions(context.Context) ([]SessionSummary, error) {
	return r.sessions, r.err
}
