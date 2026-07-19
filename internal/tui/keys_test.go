package tui

import (
	"reflect"
	"testing"
	"time"
)

func TestKeyDecoderHandlesNavigationAndUnicode(t *testing.T) {
	var decoder keyDecoder
	got := decoder.feed([]byte("a界\x1b[D\x7f\r"))
	want := []key{
		{kind: keyText, text: "a"},
		{kind: keyText, text: "界"},
		{kind: keyLeft},
		{kind: keyBackspace},
		{kind: keyEnter},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
}

func TestKeyDecoderRetainsSplitEscapeSequence(t *testing.T) {
	var decoder keyDecoder
	if got := decoder.feed([]byte("\x1b")); len(got) != 0 {
		t.Fatalf("escape prefix emitted early: %#v", got)
	}
	if got := decoder.feed([]byte("[")); len(got) != 0 {
		t.Fatalf("CSI prefix emitted early: %#v", got)
	}
	if got := decoder.feed([]byte("D")); !reflect.DeepEqual(got, []key{{kind: keyLeft}}) {
		t.Fatalf("split sequence = %#v", got)
	}
}

func TestKeyDecoderFlushesLoneEscape(t *testing.T) {
	var decoder keyDecoder
	_ = decoder.feed([]byte("\x1b"))
	got := decoder.flushPending(time.Now().Add(time.Second))
	if !reflect.DeepEqual(got, []key{{kind: keyEscape}}) {
		t.Fatalf("flushed escape = %#v", got)
	}
}

func TestKeyDecoderTreatsBracketedPasteAsText(t *testing.T) {
	var decoder keyDecoder
	first := decoder.feed([]byte("\x1b[200~first\n"))
	second := decoder.feed([]byte("second\x1b[201~"))
	var text string
	for _, pressed := range append(first, second...) {
		if pressed.kind != keyText {
			t.Fatalf("paste emitted non-text key %#v", pressed)
		}
		text += pressed.text
	}
	if text != "first\nsecond" {
		t.Fatalf("paste text = %q", text)
	}
}

func TestKeyDecoderUsesCtrlNForNewline(t *testing.T) {
	var decoder keyDecoder
	got := decoder.feed([]byte{'\n', 0x0e})
	want := []key{{kind: keyEnter}, {kind: keyNewline}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
}

func TestKeyDecoderHandlesTerminalFocusReporting(t *testing.T) {
	var decoder keyDecoder
	got := decoder.feed([]byte("\x1b[O\x1b[I"))
	want := []key{{kind: keyFocusOut}, {kind: keyFocusIn}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
}
