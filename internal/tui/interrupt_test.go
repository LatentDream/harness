package tui

import (
	"context"
	"testing"
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
