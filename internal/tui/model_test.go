package tui

import (
	"context"
	"os"
	"path/filepath"
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

func TestModelResetsTranscriptForNewSession(t *testing.T) {
	state := model{
		blocks:             []transcriptBlock{{kind: blockUser, text: "/new"}},
		streams:            map[streamKey]int{{turnID: "turn", round: 1}: 0},
		status:             "working...",
		notice:             "notice",
		isInferenceRunning: true,
		scrollOffset:       4,
		editor:             editor{history: []string{"old prompt"}, historyIndex: 1},
	}

	state.apply(input.Event{Kind: input.EventSessionReset})

	if len(state.blocks) != 0 || len(state.streams) != 0 {
		t.Fatalf("session transcript was retained: blocks=%#v streams=%#v", state.blocks, state.streams)
	}
	if state.status != "" || state.notice != "" || state.scrollOffset != 0 || state.isInferenceRunning {
		t.Fatalf("session presentation state was retained: %#v", state)
	}
	if len(state.editor.history) != 0 || state.editor.historyIndex != 0 {
		t.Fatalf("session editor history was retained: %#v", state.editor)
	}
}

func TestModelLoadsRestoredSessionPresentation(t *testing.T) {
	state := model{streams: make(map[streamKey]int), tools: make(map[toolKey]int)}
	state.apply(input.Event{Kind: input.EventSessionLoaded, Messages: []input.PresentationMessage{
		{Role: "user", Content: "old question"},
		{Role: "assistant", Content: "old answer"},
	}})
	if len(state.blocks) != 2 || state.blocks[0].kind != blockUser || state.blocks[1].kind != blockAssistant {
		t.Fatalf("loaded transcript = %#v", state.blocks)
	}
	if len(state.editor.history) != 1 || state.editor.history[0] != "old question" {
		t.Fatalf("loaded editor history = %#v", state.editor.history)
	}
}

func TestInferenceSpinnerIsIndependentFromStatus(t *testing.T) {
	state := model{width: 40}
	state.apply(input.Event{Kind: input.EventInferenceStarted})

	if !state.isInferenceRunning {
		t.Fatal("inference did not start")
	}
	if got := stripANSI(state.renderStatus(40)); got != " |" {
		t.Fatalf("spinner-only status = %q", got)
	}

	state.apply(input.Event{Kind: input.EventStatus, Text: "Reading file sample.txt"})
	if got := stripANSI(state.renderStatus(40)); got != " | Reading file sample.txt" {
		t.Fatalf("combined inference status = %q", got)
	}

	state.apply(input.Event{Kind: input.EventInferenceEnded})
	if got := stripANSI(state.renderStatus(40)); got != "   Reading file sample.txt" {
		t.Fatalf("static tool status = %q", got)
	}
}

func TestModelRendersDedicatedToolActivities(t *testing.T) {
	workspace := t.TempDir()
	state := model{
		width: 80, workingDirectory: workspace,
		streams: make(map[streamKey]int), tools: make(map[toolKey]int),
	}
	readPath := filepath.Join(workspace, "internal", "read.go")
	writePath := filepath.Join(workspace, "internal", "write.go")
	webURL := "https://example.com/docs?topic=webfetch"

	state.apply(input.Event{
		Kind: input.EventToolStarted, TurnID: "turn", ToolCallID: "read-1", ToolName: "read",
		ToolActivity: input.ToolActivity{Target: readPath},
	})
	state.apply(input.Event{
		Kind: input.EventToolCompleted, TurnID: "turn", ToolCallID: "read-1", ToolName: "read",
		ToolActivity: input.ToolActivity{Target: readPath},
	})
	state.apply(input.Event{
		Kind: input.EventToolStarted, TurnID: "turn", ToolCallID: "write-1", ToolName: "write",
		ToolActivity: input.ToolActivity{Target: writePath},
	})
	state.apply(input.Event{
		Kind: input.EventToolCompleted, TurnID: "turn", ToolCallID: "write-1", ToolName: "write",
		ToolActivity: input.ToolActivity{Target: writePath},
	})
	state.apply(input.Event{
		Kind: input.EventToolStarted, TurnID: "turn", ToolCallID: "bash-1", ToolName: "bash",
		ToolActivity: input.ToolActivity{Command: "go test ./..."},
	})
	state.apply(input.Event{
		Kind: input.EventToolCompleted, TurnID: "turn", ToolCallID: "bash-1", ToolName: "bash",
		ToolActivity: input.ToolActivity{Command: "go test ./...", Output: "ok package"},
	})
	state.apply(input.Event{
		Kind: input.EventToolStarted, TurnID: "turn", ToolCallID: "webfetch-1", ToolName: "webfetch",
		ToolActivity: input.ToolActivity{Target: webURL},
	})
	state.apply(input.Event{
		Kind: input.EventToolCompleted, TurnID: "turn", ToolCallID: "webfetch-1", ToolName: "webfetch",
		ToolActivity: input.ToolActivity{Target: webURL},
	})
	state.apply(input.Event{Kind: input.EventAssistantStarted, TurnID: "turn", Round: 1})
	state.apply(input.Event{Kind: input.EventAssistantDelta, TurnID: "turn", Round: 1, Text: "done"})
	state.apply(input.Event{Kind: input.EventAssistantCompleted, TurnID: "turn", Round: 1, Text: "done"})

	view := strings.Join(state.renderTranscript(80, 12), "\n")
	for _, expected := range []string{
		"› Read internal/read.go",
		"› Wrote internal/write.go",
		"› Bash $ go test ./...",
		"  ok package",
		"› Fetched https://example.com/docs?topic=webfetch",
		"› Fetched https://example.com/docs?topic=webfetch\n\nASSISTANT",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("tool transcript does not contain %q: %q", expected, view)
		}
	}
	if len(state.tools) != 0 {
		t.Fatalf("completed tools retained: %#v", state.tools)
	}
}

func TestRenderAppliesMarginAndTitleCaseMode(t *testing.T) {
	state := model{
		width: 40, height: 8, mode: input.ModeBuild, ready: true,
		provider: "codex", modelName: "test", workingDirectory: "/workspace",
		streams: make(map[streamKey]int), tools: make(map[toolKey]int),
	}

	rendered := stripANSI(state.render())
	lines := strings.Split(rendered, "\r\n")
	for index, line := range lines {
		line = strings.TrimPrefix(line, "\x1b[H")
		line = strings.TrimPrefix(line, "\x1b[2K")
		if !strings.HasPrefix(line, " ") {
			t.Fatalf("line %d has no horizontal margin: %q", index, line)
		}
	}
	if !strings.Contains(rendered, "[Build]") || strings.Contains(rendered, "[BUILD]") {
		t.Fatalf("mode badge is not title-cased: %q", rendered)
	}
	if !strings.Contains(rendered, " "+strings.Repeat("─", 38)) {
		t.Fatalf("separator does not respect margin: %q", rendered)
	}

	state.mode = input.ModePlan
	rendered = stripANSI(state.render())
	if !strings.Contains(rendered, "[Plan]") || strings.Contains(rendered, "[PLAN]") {
		t.Fatalf("plan mode badge is not title-cased: %q", rendered)
	}

	state.mode = input.ModeChat
	rendered = stripANSI(state.render())
	if !strings.Contains(rendered, "[Chat]") || strings.Contains(rendered, "[CHAT]") {
		t.Fatalf("chat mode badge is not title-cased: %q", rendered)
	}
}

func TestWelcomeContainsShortcutsAndDisappearsWithConversation(t *testing.T) {
	state := model{
		width: 80, height: 16, mode: input.ModeBuild, ready: true,
		provider: "codex", modelName: "test", workingDirectory: "/workspace",
		streams: make(map[streamKey]int), tools: make(map[toolKey]int),
	}

	rendered := stripANSI(state.render())
	if strings.Contains(rendered, "Tip:") {
		t.Fatalf("input tip is visible alongside welcome: %q", rendered)
	}
	for _, expected := range []string{
		"╭─ Ready when you are",
		"Start with a question, @ to find a file, or / for commands.",
		"enter send  ·  ctrl+n newline  ·  f4/alt+e editor",
		"tab build/plan/chat  ·  f2 raw/md",
		"@ files  ·  / commands  ·  pgup/wheel scroll  ·  ctrl+c quit",
		"╰─",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("welcome does not contain %q: %q", expected, rendered)
		}
	}

	state.blocks = []transcriptBlock{{kind: blockUser, mode: input.ModeBuild, text: "hello"}}
	rendered = stripANSI(state.render())
	if strings.Contains(rendered, "Ready when you are") || strings.Contains(rendered, "ctrl+n newline") {
		t.Fatalf("welcome remained after conversation started: %q", rendered)
	}
	if !strings.Contains(rendered, inputTips[0]) {
		t.Fatalf("input tip did not replace dismissed welcome: %q", rendered)
	}
	lines := strings.Split(rendered, "\r\n")
	if got := lines[len(lines)-1]; !strings.Contains(got, "[Build] >") {
		t.Fatalf("persistent footer still follows input: %q", got)
	}
}

func TestInputUsesStableTipPlaceholder(t *testing.T) {
	state := model{
		mode: input.ModeBuild, focused: true, tip: "Tip: @ searches project files",
		blocks: []transcriptBlock{{kind: blockUser, text: "hello"}},
	}
	first := strings.Join(state.renderInput(60), "\n")
	second := strings.Join(state.renderInput(60), "\n")
	if first != second {
		t.Fatalf("placeholder changed between redraws: %q != %q", first, second)
	}
	if !strings.Contains(stripANSI(first), state.tip) || strings.Contains(first, "Ask anything") {
		t.Fatalf("input did not render selected tip: %q", first)
	}
}

func TestHeaderShowsClippedHomeRelativePathWithoutMode(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	state := model{
		provider: "codex", modelName: "gpt-5.5",
		workingDirectory: filepath.Join(home, "projects", "a-very-long-directory", "harness"),
	}

	header := state.renderHeader(42)
	if len(header) != 2 || header[1] != "" {
		t.Fatalf("header does not end with one blank row: %#v", header)
	}
	first := stripANSI(header[0])
	if strings.Contains(first, "[Build]") || strings.Contains(first, "[Plan]") {
		t.Fatalf("header still contains mode: %q", first)
	}
	if !strings.Contains(first, "…") || !strings.HasSuffix(first, "harness") {
		t.Fatalf("header path did not preserve its tail: %q", first)
	}
	if displayWidth(header[0]) > 42 {
		t.Fatalf("header width = %d, want at most 42", displayWidth(header[0]))
	}
}

func TestDisplayWorkingDirectoryUsesHomeShortcut(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := displayWorkingDirectory(home); got != "~" {
		t.Fatalf("home path = %q, want ~", got)
	}
	want := filepath.Join("~", "projects", "harness")
	if got := displayWorkingDirectory(filepath.Join(home, "projects", "harness")); got != want {
		t.Fatalf("nested home path = %q, want %q", got, want)
	}
}

func TestUserBlockUsesConfiguredUsername(t *testing.T) {
	state := model{username: "latent"}
	label, _ := state.blockLabel(transcriptBlock{kind: blockUser, mode: input.ModeBuild})
	if label != "latent  [Build]" {
		t.Fatalf("user label = %q", label)
	}
}

func TestModelDoesNotPersistUnsupportedTool(t *testing.T) {
	state := model{}
	state.apply(input.Event{Kind: input.EventToolStarted, ToolCallID: "glob-1", ToolName: "glob"})
	if len(state.blocks) != 0 {
		t.Fatalf("unsupported tool added transcript block: %#v", state.blocks)
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

func TestRenderTranscriptClampsScrollOffsetAtFirstPage(t *testing.T) {
	state := model{
		height:       8,
		scrollOffset: 100,
		blocks: []transcriptBlock{
			{kind: blockUser, text: "first", mode: input.ModeBuild},
			{kind: blockAssistant, text: "second"},
			{kind: blockUser, text: "third", mode: input.ModeBuild},
		},
	}

	view := state.renderTranscript(40, 4)

	if state.scrollOffset != 5 {
		t.Fatalf("scroll offset = %d, want 5", state.scrollOffset)
	}
	if got := strings.Join(view, "\n"); !strings.Contains(got, "YOU  [Build]\n  first") {
		t.Fatalf("first transcript page is not visible: %q", got)
	}

	state.renderTranscript(40, 8)
	if state.scrollOffset != 1 {
		t.Fatalf("scroll offset after viewport growth = %d, want 1", state.scrollOffset)
	}
}

func TestRenderTranscriptDoesNotScrollShortTranscript(t *testing.T) {
	state := model{
		scrollOffset: 100,
		blocks:       []transcriptBlock{{kind: blockUser, text: "only", mode: input.ModeBuild}},
	}

	view := state.renderTranscript(40, 4)

	if state.scrollOffset != 0 {
		t.Fatalf("scroll offset = %d, want 0", state.scrollOffset)
	}
	if got := strings.Join(view, "\n"); !strings.Contains(got, "YOU  [Build]\n  only") {
		t.Fatalf("short transcript is not visible: %q", got)
	}
}

func TestPageDownMovesImmediatelyAfterPageUpAtFirstPage(t *testing.T) {
	state := model{
		height: 8,
		blocks: []transcriptBlock{
			{kind: blockUser, text: "first", mode: input.ModeBuild},
			{kind: blockAssistant, text: "second"},
			{kind: blockUser, text: "third", mode: input.ModeBuild},
			{kind: blockAssistant, text: "fourth"},
		},
	}
	ui := UI{}

	for range 10 {
		if _, err := ui.handleKey(context.Background(), &state, key{kind: keyPageUp}); err != nil {
			t.Fatal(err)
		}
		state.renderTranscript(40, 4)
	}
	if state.scrollOffset != 8 {
		t.Fatalf("scroll offset at first page = %d, want 8", state.scrollOffset)
	}

	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyPageDown}); err != nil {
		t.Fatal(err)
	}
	if state.scrollOffset != 4 {
		t.Fatalf("scroll offset after page down = %d, want 4", state.scrollOffset)
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

func TestMouseWheelScrollsTranscript(t *testing.T) {
	state := model{height: 12}
	ui := UI{}

	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyScrollUp}); err != nil {
		t.Fatal(err)
	}
	if state.scrollOffset != 3 {
		t.Fatalf("scroll offset after wheel up = %d, want 3", state.scrollOffset)
	}
	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyScrollDown}); err != nil {
		t.Fatal(err)
	}
	if state.scrollOffset != 0 {
		t.Fatalf("scroll offset after wheel down = %d, want 0", state.scrollOffset)
	}
	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyScrollDown}); err != nil {
		t.Fatal(err)
	}
	if state.scrollOffset != 0 {
		t.Fatalf("scroll offset after extra wheel down = %d, want 0", state.scrollOffset)
	}
}

func TestTabCyclesBuildPlanChatModes(t *testing.T) {
	ui := UI{}
	state := model{mode: input.ModeBuild}

	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyTab}); err != nil {
		t.Fatal(err)
	}
	if state.mode != input.ModePlan {
		t.Fatalf("first tab mode = %q, want plan", state.mode)
	}
	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyTab}); err != nil {
		t.Fatal(err)
	}
	if state.mode != input.ModeChat {
		t.Fatalf("second tab mode = %q, want chat", state.mode)
	}
	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyTab}); err != nil {
		t.Fatal(err)
	}
	if state.mode != input.ModeBuild {
		t.Fatalf("third tab mode = %q, want build", state.mode)
	}
}

func TestShellPromptRendersDistinctlyAndPreservesMode(t *testing.T) {
	state := model{mode: input.ModePlan, promptMode: promptShell, focused: true}

	rendered := stripANSI(strings.Join(state.renderInput(60), "\n"))
	if !strings.Contains(rendered, "[Shell] $") {
		t.Fatalf("shell prompt not rendered: %q", rendered)
	}
	if state.mode != input.ModePlan {
		t.Fatalf("shell prompt changed build/plan mode: %q", state.mode)
	}
}

func TestBangKeyEntersShellModeOnlyAtEmptyPrompt(t *testing.T) {
	ui := UI{}
	state := model{ready: true, mode: input.ModeBuild}
	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyText, text: "!"}); err != nil {
		t.Fatal(err)
	}
	if state.promptMode != promptShell || state.editor.value() != "" {
		t.Fatalf("bang did not enter shell mode: promptMode=%v editor=%q", state.promptMode, state.editor.value())
	}

	state = model{ready: true, mode: input.ModeBuild}
	state.editor.insert("echo ")
	if _, err := ui.handleKey(context.Background(), &state, key{kind: keyText, text: "!"}); err != nil {
		t.Fatal(err)
	}
	if state.promptMode != promptNormal || state.editor.value() != "echo !" {
		t.Fatalf("bang in non-empty prompt was not inserted: promptMode=%v editor=%q", state.promptMode, state.editor.value())
	}
}

func TestLocalShellResultPopulatesNextMessageAndTranscript(t *testing.T) {
	state := model{ready: false, promptMode: promptShell}
	state.applyLocalShellResult(localShellRunResult{result: localShellResult{
		Command:          "go test ./...",
		WorkingDirectory: "/workspace",
		ExitCode:         0,
		Stdout:           "ok\n",
	}})

	if !state.ready || state.promptMode != promptNormal || state.isLocalShellRunning {
		t.Fatalf("shell result did not reset prompt state: %#v", state)
	}
	if len(state.blocks) != 1 || state.blocks[0].kind != blockShell || !strings.Contains(state.blocks[0].text, "ok") {
		t.Fatalf("shell result not added to transcript: %#v", state.blocks)
	}
	if got := state.editor.value(); !strings.Contains(got, "Command: go test ./...") || !strings.Contains(got, "Stdout:\nok") {
		t.Fatalf("shell result not inserted into next message: %q", got)
	}
}

func TestModelUpdatesSessionTitleAndIgnoresStaleEvents(t *testing.T) {
	state := model{provider: "codex", modelName: "test", workingDirectory: "/workspace"}
	state.apply(input.Event{Kind: input.EventSessionLoaded, SessionID: "session-123456", SessionTitle: "Initial"})
	state.apply(input.Event{Kind: input.EventSessionTitleChanged, SessionID: "session-123456", SessionTitle: "Fix Parser Commas"})
	if state.sessionTitle != "Fix Parser Commas" {
		t.Fatalf("session title = %q", state.sessionTitle)
	}
	if header := stripANSI(state.renderHeader(80)[0]); !strings.Contains(header, "Fix Parser Commas · codex/test") {
		t.Fatalf("header = %q", header)
	}
	state.apply(input.Event{Kind: input.EventSessionTitleChanged, SessionID: "another", SessionTitle: "Stale"})
	if state.sessionTitle != "Fix Parser Commas" {
		t.Fatalf("stale event changed title to %q", state.sessionTitle)
	}
}

func TestHeaderUsesUntitledSessionIDFallback(t *testing.T) {
	state := model{provider: "codex", modelName: "test", workingDirectory: "/workspace", sessionID: "12345678-abcd"}
	if header := stripANSI(state.renderHeader(80)[0]); !strings.Contains(header, "Untitled (12345678)") {
		t.Fatalf("header = %q", header)
	}
}
