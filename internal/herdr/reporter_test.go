package herdr

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"latentdream/harness/internal/input"
)

type recordingSink struct {
	events []input.Event
}

func (s *recordingSink) Emit(_ context.Context, event input.Event) error {
	s.events = append(s.events, event)
	return nil
}

type invocation struct {
	name string
	args []string
}

type recordingRunner struct {
	mu    sync.Mutex
	calls []invocation
	wake  chan struct{}
}

func (r *recordingRunner) run(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	r.calls = append(r.calls, invocation{name: name, args: append([]string(nil), args...)})
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return nil
}

func (r *recordingRunner) wait(t *testing.T, count int) []invocation {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		r.mu.Lock()
		calls := append([]invocation(nil), r.calls...)
		r.mu.Unlock()
		if len(calls) >= count {
			return calls
		}
		select {
		case <-r.wake:
		case <-deadline:
			t.Fatalf("timed out waiting for %d calls; got %d", count, len(calls))
		}
	}
}

func TestReporterForwardsEventsAndReportsLifecycle(t *testing.T) {
	sink := &recordingSink{}
	runner := &recordingRunner{wake: make(chan struct{}, 8)}
	reporter := newReporter(sink, "/bin/herdr", "pane-1", runner.run)
	defer reporter.Close()

	runner.wait(t, 1)
	event := input.Event{Kind: input.EventInferenceStarted, SessionID: "session-1"}
	if err := reporter.Emit(context.Background(), event); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	calls := runner.wait(t, 2)

	if !reflect.DeepEqual(sink.events, []input.Event{event}) {
		t.Fatalf("forwarded events = %#v", sink.events)
	}
	want := invocation{name: "/bin/herdr", args: []string{
		"pane", "report-agent", "pane-1", "--source", "custom:harness",
		"--agent", "harness", "--state", "working", "--seq", "2",
		"--agent-session-id", "session-1",
	}}
	if !reflect.DeepEqual(calls[1], want) {
		t.Fatalf("working report = %#v, want %#v", calls[1], want)
	}
}

func TestReporterReleasesAuthority(t *testing.T) {
	runner := &recordingRunner{wake: make(chan struct{}, 8)}
	reporter := newReporter(&recordingSink{}, "/bin/herdr", "pane-1", runner.run)
	runner.wait(t, 1)

	reporter.Close()
	calls := runner.wait(t, 2)
	want := invocation{name: "/bin/herdr", args: []string{
		"pane", "release-agent", "pane-1", "--source", "custom:harness", "--agent", "harness",
	}}
	if !reflect.DeepEqual(calls[1], want) {
		t.Fatalf("release call = %#v, want %#v", calls[1], want)
	}
}

func TestReporterDoesNotBecomeIdleBetweenInferenceAndTools(t *testing.T) {
	runner := &recordingRunner{wake: make(chan struct{}, 8)}
	reporter := newReporter(&recordingSink{}, "/bin/herdr", "pane-1", runner.run)
	defer reporter.Close()
	runner.wait(t, 1)

	for _, event := range []input.Event{
		{Kind: input.EventInferenceStarted},
		{Kind: input.EventInferenceEnded},
		{Kind: input.EventToolStarted},
		{Kind: input.EventToolCompleted},
	} {
		if err := reporter.Emit(context.Background(), event); err != nil {
			t.Fatalf("Emit(%s) error = %v", event.Kind, err)
		}
	}
	calls := runner.wait(t, 3)
	for _, call := range calls[1:] {
		if got := argumentValue(call.args, "--state"); got != "working" {
			t.Fatalf("intermediate state = %q, want working", got)
		}
	}

	if err := reporter.Emit(context.Background(), input.Event{Kind: input.EventAssistantCompleted}); err != nil {
		t.Fatalf("Emit(assistant.completed) error = %v", err)
	}
	calls = runner.wait(t, 4)
	if got := argumentValue(calls[3].args, "--state"); got != "idle" {
		t.Fatalf("completed state = %q, want idle", got)
	}
}

func TestReporterIsDisabledWithoutHerdrContext(t *testing.T) {
	sink := &recordingSink{}
	runner := &recordingRunner{wake: make(chan struct{}, 1)}
	reporter := newReporter(sink, "", "", runner.run)
	event := input.Event{Kind: input.EventInferenceStarted}

	if err := reporter.Emit(context.Background(), event); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	reporter.Close()

	if !reflect.DeepEqual(sink.events, []input.Event{event}) {
		t.Fatalf("forwarded events = %#v", sink.events)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v", runner.calls)
	}
}

func argumentValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}
