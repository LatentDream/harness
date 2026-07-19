package tui

import (
	"strings"
	"testing"

	"latentdream/harness/internal/input"
)

func TestModelReconcilesStreamWithCanonicalCompletion(t *testing.T) {
	state := model{streams: make(map[streamKey]int)}
	state.apply(input.Event{Kind: input.EventAssistantStarted, TurnID: "turn", Round: 1})
	state.apply(input.Event{Kind: input.EventAssistantDelta, TurnID: "turn", Round: 1, Text: "partial"})
	state.apply(input.Event{Kind: input.EventAssistantCompleted, TurnID: "turn", Round: 1, Text: "canonical response"})

	if len(state.blocks) != 1 || state.blocks[0].text != "canonical response" || state.blocks[0].interrupted {
		t.Fatalf("unexpected transcript: %#v", state.blocks)
	}
	if len(state.streams) != 0 {
		t.Fatalf("completed stream was retained: %#v", state.streams)
	}
}

func TestModelMarksAbortedStream(t *testing.T) {
	state := model{streams: make(map[streamKey]int)}
	state.apply(input.Event{Kind: input.EventAssistantStarted, TurnID: "turn", Round: 2})
	state.apply(input.Event{Kind: input.EventAssistantDelta, TurnID: "turn", Round: 2, Text: "part"})
	state.apply(input.Event{Kind: input.EventAssistantAborted, TurnID: "turn", Round: 2, Text: "partial"})

	if state.blocks[0].text != "partial" || !state.blocks[0].interrupted {
		t.Fatalf("unexpected aborted transcript: %#v", state.blocks[0])
	}
}

func TestRendererSanitizesUntrustedEscapeSequences(t *testing.T) {
	state := model{
		width: 60, height: 12, mode: input.ModeBuild, ready: true,
		colors: palette{enabled: true}, streams: make(map[streamKey]int),
		blocks: []transcriptBlock{{kind: blockAssistant, text: "safe\x1b[2Junsafe"}},
	}
	rendered := state.render()
	if strings.Contains(rendered, "\x1b[2Junsafe") || !strings.Contains(rendered, "safe[2Junsafe") {
		t.Fatalf("untrusted escape was not sanitized: %q", rendered)
	}
	if width := displayWidth(ansiCyan + "hello" + ansiReset); width != 5 {
		t.Fatalf("styled width = %d", width)
	}
}

func TestRenderEditorLinesKeepsCursorWhenWrapped(t *testing.T) {
	lines := renderEditorLines([]rune("abcdef"), 4, 3, true)
	joined := strings.Join(lines, "")
	if !strings.Contains(joined, ansiInverse+"e"+ansiReset) {
		t.Fatalf("wrapped editor lost cursor: %#v", lines)
	}
	if got := stripANSI(joined); got != "abcdef" {
		t.Fatalf("wrapped editor text = %q", got)
	}
}

func TestRenderEditorLinesHidesCursorWhenUnfocused(t *testing.T) {
	lines := renderEditorLines([]rune("abcdef"), 4, 10, false)
	joined := strings.Join(lines, "")
	if strings.Contains(joined, ansiInverse) {
		t.Fatalf("unfocused editor retained cursor marker: %q", joined)
	}
	if joined != "abcdef" {
		t.Fatalf("unfocused editor text = %q", joined)
	}
}

func TestSanitizeEditorMapsCursorAcrossTabsAndControls(t *testing.T) {
	runes, cursor := sanitizeEditor([]rune("a\tb\x1bc"), 3)
	if got := string(runes); got != "a    bc" {
		t.Fatalf("sanitized editor = %q", got)
	}
	if cursor != 6 {
		t.Fatalf("mapped cursor = %d, want 6", cursor)
	}
}
