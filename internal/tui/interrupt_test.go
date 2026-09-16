package tui

import (
	"context"
	"testing"

	"latentdream/harness/internal/input"
)

func TestHandleKeyEscapeClearsEditorWhenReady(t *testing.T) {
	ui := &UI{interrupts: make(chan struct{}, 1)}
	state := model{ready: true}
	state.editor.insert("hello")

	quit, err := ui.handleKey(context.Background(), &state, key{kind: keyEscape})
	if err != nil {
		t.Fatalf("handle escape: %v", err)
	}
	if quit {
		t.Fatal("escape quit while editor was ready")
	}
	if got := state.editor.value(); got != "" {
		t.Fatalf("editor value = %q, want empty", got)
	}
	select {
	case <-ui.interrupts:
		t.Fatal("escape sent interrupt while editor was ready")
	default:
	}
}

func TestHandleKeyEnterSendsGuidanceWhenBusy(t *testing.T) {
	sender := &steeringSenderStub{}
	ui := &UI{options: Options{Steering: sender}}
	state := model{ready: false, mode: input.ModeBuild}
	state.editor.insert("focus on tests")

	quit, err := ui.handleKey(context.Background(), &state, key{kind: keyEnter})
	if err != nil || quit {
		t.Fatalf("handle enter = (%v, %v)", quit, err)
	}
	if sender.text != "focus on tests" || state.editor.value() != "" || len(state.blocks) != 1 || state.blocks[0].text != "focus on tests" {
		t.Fatalf("guidance was not accepted cleanly: sender=%#v state=%#v", sender, state)
	}
}

func TestHandleKeyEnterRetainsGuidanceWhenRejected(t *testing.T) {
	sender := &steeringSenderStub{err: input.ErrNoActiveTurn}
	ui := &UI{options: Options{Steering: sender}}
	state := model{ready: false}
	state.editor.insert("too late")

	_, err := ui.handleKey(context.Background(), &state, key{kind: keyEnter})
	if err != nil {
		t.Fatal(err)
	}
	if state.editor.value() != "too late" || sender.text != "too late" || state.notice == "" {
		t.Fatalf("rejected guidance state = %#v", state)
	}
}

type steeringSenderStub struct {
	text string
	err  error
}

func (s *steeringSenderStub) SendMessage(_ context.Context, text string) error {
	s.text = text
	return s.err
}

func TestHandleKeyEscapeInterruptsWhenBusy(t *testing.T) {
	ui := &UI{interrupts: make(chan struct{}, 1)}
	state := model{ready: false}

	quit, err := ui.handleKey(context.Background(), &state, key{kind: keyEscape})
	if err != nil {
		t.Fatalf("handle escape: %v", err)
	}
	if quit {
		t.Fatal("escape quit while work was in flight")
	}
	select {
	case <-ui.interrupts:
	default:
		t.Fatal("escape did not send interrupt while work was in flight")
	}
	if state.notice == "" {
		t.Fatal("escape did not set cancellation notice")
	}
}
