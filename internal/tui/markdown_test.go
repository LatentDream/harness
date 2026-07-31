package tui

import (
	"context"
	"strings"
	"testing"

	"latentdream/harness/internal/input"
)

func TestRenderMarkdownFormatsCommonElements(t *testing.T) {
	source := strings.Join([]string{
		"# Heading",
		"",
		"A **bold** and *italic* [link](https://example.com) with `code`.",
		"",
		"> quoted text",
		"",
		"- first",
		"- [x] done",
		"",
		"```go",
		"fmt.Println(\"hello\")",
		"```",
	}, "\n")

	rendered := strings.Join(renderMarkdown(source, 72, palette{enabled: true}), "\n")
	plain := stripANSI(rendered)
	for _, expected := range []string{
		"# Heading",
		"A bold and italic link (https://example.com) with code.",
		"│ quoted text",
		"• first",
		"• [✓] done",
		"go",
		"fmt.Println(\"hello\")",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("rendered Markdown does not contain %q:\n%s", expected, plain)
		}
	}
	for _, style := range []string{ansiBold, ansiItalic, ansiUnderline, ansiYellow} {
		if !strings.Contains(rendered, style) {
			t.Fatalf("rendered Markdown does not use style %q: %q", style, rendered)
		}
	}
}

func TestRenderMarkdownSupportsGFMTableAndStrikethrough(t *testing.T) {
	source := "| Name | State |\n| --- | --- |\n| old | ~~removed~~ |"
	rendered := strings.Join(renderMarkdown(source, 60, palette{enabled: true}), "\n")
	plain := stripANSI(rendered)

	for _, expected := range []string{"│ Name │ State │", "│ old │ removed │"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("rendered table does not contain %q: %q", expected, plain)
		}
	}
	if !strings.Contains(rendered, ansiStrike) {
		t.Fatalf("strikethrough style is missing: %q", rendered)
	}
}

func TestRenderMarkdownWithoutColorKeepsStructure(t *testing.T) {
	rendered := renderMarkdown("## Title\n\n- item\n\n```\nvalue\n```", 40, palette{})
	joined := strings.Join(rendered, "\n")

	if strings.Contains(joined, "\x1b[") {
		t.Fatalf("color-disabled Markdown contains ANSI escapes: %q", joined)
	}
	for _, expected := range []string{"## Title", "• item", "code", "value"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("color-disabled Markdown does not contain %q: %q", expected, joined)
		}
	}
}

func TestRenderMarkdownSanitizesControlSequencesAndRespectsWidth(t *testing.T) {
	lines := renderMarkdown("**safe\x1b[2Junsafe** and a long line", 12, palette{enabled: true})
	joined := strings.Join(lines, "\n")

	if strings.Contains(joined, "\x1b[2J") || !strings.Contains(stripANSI(joined), "safe[2Junsaf") {
		t.Fatalf("Markdown did not sanitize untrusted escape sequence: %q", joined)
	}
	for _, line := range lines {
		if width := displayWidth(line); width > 12 {
			t.Fatalf("rendered line width = %d, want at most 12: %q", width, line)
		}
	}
}

func TestAssistantMarkdownRenderingDoesNotMutateRawBlock(t *testing.T) {
	raw := "# Heading\n\n**bold**"
	state := model{blocks: []transcriptBlock{{kind: blockAssistant, text: raw}}}

	view := strings.Join(state.renderTranscript(60, 10), "\n")
	if !strings.Contains(stripANSI(view), "# Heading") || strings.Contains(stripANSI(view), "**bold**") {
		t.Fatalf("assistant response was not rendered as Markdown: %q", view)
	}
	if state.blocks[0].text != raw {
		t.Fatalf("raw assistant response changed to %q, want %q", state.blocks[0].text, raw)
	}
}

func TestF2TogglesAssistantMarkdownRendering(t *testing.T) {
	raw := "# Heading\n\n**bold**"
	state := model{
		ready:  true,
		blocks: []transcriptBlock{{kind: blockAssistant, text: raw}},
	}
	ui := UI{}

	rendered := stripANSI(strings.Join(state.renderTranscript(60, 10), "\n"))
	if strings.Contains(rendered, "**bold**") {
		t.Fatalf("Markdown starts in raw mode: %q", rendered)
	}

	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyToggleMarkdown}); err != nil {
		t.Fatal(err)
	}
	if !state.markdownDisabled || !strings.Contains(state.notice, "off") {
		t.Fatalf("Markdown was not disabled: %#v", state)
	}
	rawView := stripANSI(strings.Join(state.renderTranscript(60, 10), "\n"))
	if !strings.Contains(rawView, "# Heading") || !strings.Contains(rawView, "**bold**") {
		t.Fatalf("raw Markdown is not visible: %q", rawView)
	}

	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyToggleMarkdown}); err != nil {
		t.Fatal(err)
	}
	if state.markdownDisabled || !strings.Contains(state.notice, "on") {
		t.Fatalf("Markdown was not enabled: %#v", state)
	}
	styledView := stripANSI(strings.Join(state.renderTranscript(60, 10), "\n"))
	if strings.Contains(styledView, "**bold**") {
		t.Fatalf("raw Markdown remained after enabling rendering: %q", styledView)
	}
	if state.blocks[0].text != raw {
		t.Fatalf("toggle changed raw response to %q", state.blocks[0].text)
	}
}

func TestMarkdownSlashCommandTogglesWithoutRuntimeSubmission(t *testing.T) {
	state := model{ready: true}
	state.editor.set("/markdown")
	ui := UI{submissions: make(chan input.Submission, 1)}

	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if !state.markdownDisabled || state.editor.value() != "" || !state.ready {
		t.Fatalf("slash command did not toggle locally: %#v", state)
	}
	select {
	case submission := <-ui.submissions:
		t.Fatalf("local command reached runtime: %#v", submission)
	default:
	}

	state.editor.set("/markdown rendered")
	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if state.markdownDisabled {
		t.Fatal("/markdown rendered did not enable rendering")
	}

	state.editor.set("/markdown raw")
	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if !state.markdownDisabled {
		t.Fatal("/markdown raw did not disable rendering")
	}
}

func TestMarkdownSlashCommandReportsUsage(t *testing.T) {
	state := model{ready: true}
	state.editor.set("/markdown invalid")
	ui := UI{submissions: make(chan input.Submission, 1)}

	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if state.notice != "usage: /markdown [toggle|on|off|raw|rendered]" {
		t.Fatalf("notice = %q", state.notice)
	}
}

func TestRenderCodeBlockUsesCopyFriendlyStyling(t *testing.T) {
	rendered := strings.Join(renderMarkdown("```json\n{\n  \\\"version\\\": 1\n}\n```", 40, palette{enabled: true}), "\n")
	plain := stripANSI(rendered)

	if strings.ContainsAny(plain, "│┌└`") {
		t.Fatalf("code block contains copy-hostile decoration: %q", plain)
	}
	lines := strings.Split(plain, "\n")
	if len(lines) != 6 {
		t.Fatalf("code panel lines = %d, want 6: %q", len(lines), plain)
	}
	if strings.TrimSpace(lines[1]) != "" {
		t.Fatalf("code block missing padding below language badge: %q", lines[1])
	}
	for _, line := range lines {
		if !strings.HasSuffix(line, "  ") {
			t.Fatalf("code panel line missing normal-background right padding: %q", line)
		}
		if width := displayWidth(line); width != 40 {
			t.Fatalf("code panel line width = %d, want 40: %q", width, line)
		}
	}
	code := make([]string, 0, 3)
	for _, line := range lines[2:5] {
		code = append(code, strings.TrimRight(strings.TrimPrefix(line, "  "), " "))
	}
	if got := strings.Join(code, "\n"); got != "{\n  \\\"version\\\": 1\n}" {
		t.Fatalf("code block content = %q", got)
	}
	if strings.TrimSpace(lines[0]) != "json" {
		t.Fatalf("language badge = %q", lines[0])
	}
	if !strings.Contains(rendered, ansiCodeBackground) || !strings.Contains(rendered, ansiCodeAccentBackground) {
		t.Fatalf("code panel styles are missing: %q", rendered)
	}
}
