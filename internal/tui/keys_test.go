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

func TestKeyDecoderHandlesCtrlArrowWordNavigation(t *testing.T) {
	var decoder keyDecoder
	got := decoder.feed([]byte("\x1b[1;5D\x1b[1;5C\x1b[5D\x1b[5C"))
	want := []key{{kind: keyWordLeft}, {kind: keyWordRight}, {kind: keyWordLeft}, {kind: keyWordRight}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
}

func TestKeyDecoderRetainsSplitCtrlArrowSequence(t *testing.T) {
	var decoder keyDecoder
	if got := decoder.feed([]byte("\x1b[1;")); len(got) != 0 {
		t.Fatalf("ctrl-arrow prefix emitted early: %#v", got)
	}
	if got := decoder.feed([]byte("5D")); !reflect.DeepEqual(got, []key{{kind: keyWordLeft}}) {
		t.Fatalf("split ctrl-arrow sequence = %#v", got)
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

func TestKeyDecoderUsesNewlineShortcuts(t *testing.T) {
	tests := []struct {
		name     string
		sequence string
	}{
		{name: "ctrl-n", sequence: "\x0e"},
		{name: "kitty shift-enter", sequence: "\x1b[13;2u"},
		{name: "kitty shift-enter with event", sequence: "\x1b[13;2:1u"},
		{name: "xterm shift-enter", sequence: "\x1b[27;2;13~"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var decoder keyDecoder
			got := decoder.feed([]byte(test.sequence))
			want := []key{{kind: keyNewline}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("keys = %#v, want %#v", got, want)
			}
		})
	}
}

func TestKeyDecoderRetainsSplitShiftEnterSequence(t *testing.T) {
	var decoder keyDecoder
	if got := decoder.feed([]byte("\x1b[13;")); len(got) != 0 {
		t.Fatalf("shift-enter prefix emitted early: %#v", got)
	}
	if got := decoder.feed([]byte("2u")); !reflect.DeepEqual(got, []key{{kind: keyNewline}}) {
		t.Fatalf("split shift-enter sequence = %#v", got)
	}
}

func TestKeyDecoderHandlesEnhancedKeyboardInput(t *testing.T) {
	var decoder keyDecoder
	got := decoder.feed([]byte("\x1b[97;1;97u\x1b[13;1u\x1b[9;1u\x1b[27;1u\x1b[127;1u\x1b[57350;5u\x1b[57353;1u\x1b[101;3u"))
	want := []key{
		{kind: keyText, text: "a"},
		{kind: keyEnter},
		{kind: keyTab},
		{kind: keyEscape},
		{kind: keyBackspace},
		{kind: keyWordLeft},
		{kind: keyDown},
		{kind: keyExternalEditor},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
}

func TestKeyDecoderUsesCtrlUAndCtrlDForPageNavigation(t *testing.T) {
	var decoder keyDecoder
	got := decoder.feed([]byte{0x15, 0x04})
	want := []key{{kind: keyPageUp}, {kind: keyPageDown}}
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

func TestKeyDecoderHandlesMouseWheelScroll(t *testing.T) {
	var decoder keyDecoder
	got := decoder.feed([]byte("\x1b[<64;10;5M\x1b[<65;10;5M"))
	want := []key{{kind: keyScrollUp}, {kind: keyScrollDown}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
}

func TestKeyDecoderRetainsSplitMouseSequence(t *testing.T) {
	var decoder keyDecoder
	if got := decoder.feed([]byte("\x1b[<64;")); len(got) != 0 {
		t.Fatalf("mouse prefix emitted early: %#v", got)
	}
	if got := decoder.feed([]byte("10;5M")); !reflect.DeepEqual(got, []key{{kind: keyScrollUp}}) {
		t.Fatalf("split mouse sequence = %#v", got)
	}
}

func TestKeyDecoderIgnoresNonWheelMouseEvents(t *testing.T) {
	var decoder keyDecoder
	if got := decoder.feed([]byte("\x1b[<0;10;5M")); len(got) != 0 {
		t.Fatalf("non-wheel mouse sequence emitted keys: %#v", got)
	}
}

func TestKeyDecoderUsesF2ToToggleMarkdown(t *testing.T) {
	var decoder keyDecoder
	got := decoder.feed([]byte("\x1bOQ\x1b[12~"))
	want := []key{{kind: keyToggleMarkdown}, {kind: keyToggleMarkdown}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
}

func TestKeyDecoderUsesExternalEditorShortcuts(t *testing.T) {
	var decoder keyDecoder
	got := decoder.feed([]byte("OS[14~e"))
	want := []key{{kind: keyExternalEditor}, {kind: keyExternalEditor}, {kind: keyExternalEditor}, {kind: keyExternalEditor}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
}

func TestKeyDecoderRetainsSplitCtrlXCtrlESequence(t *testing.T) {
	var decoder keyDecoder
	if got := decoder.feed([]byte{0x18}); len(got) != 0 {
		t.Fatalf("ctrl+x prefix emitted early: %#v", got)
	}
	if got := decoder.feed([]byte{0x05}); !reflect.DeepEqual(got, []key{{kind: keyExternalEditor}}) {
		t.Fatalf("split ctrl+x ctrl+e sequence = %#v", got)
	}
}

func TestKeyDecoderFlushesLoneCtrlX(t *testing.T) {
	var decoder keyDecoder
	_ = decoder.feed([]byte{0x18})
	if got := decoder.flushPending(time.Now().Add(time.Second)); len(got) != 0 {
		t.Fatalf("flushed ctrl+x emitted key: %#v", got)
	}
	got := decoder.feed([]byte{0x05})
	want := []key{{kind: keyEnd}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("post-flush ctrl+e = %#v, want %#v", got, want)
	}
}
