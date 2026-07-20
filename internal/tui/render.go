package tui

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"latentdream/harness/internal/input"
)

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiInverse = "\x1b[7m"
	ansiCyan    = "\x1b[36m"
	ansiBlue    = "\x1b[34m"
	ansiMagenta = "\x1b[35m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiRed     = "\x1b[31m"
	ansiGray    = "\x1b[39m"
)

type palette struct{ enabled bool }

func (p palette) wrap(style, value string) string {
	if !p.enabled || value == "" {
		return value
	}
	return style + value + ansiReset
}

func (m *model) render() string {
	width := max(m.width, 1)
	height := max(m.height, 1)
	margin := 0
	if width > 2 {
		margin = 1
	}
	contentWidth := max(1, width-(2*margin))
	header := m.renderHeader(contentWidth)
	inputLines := m.renderInput(contentWidth)
	status := m.renderStatus(contentWidth)
	transcriptHeight := max(1, height-len(header)-len(inputLines)-1)
	transcript := m.renderTranscript(contentWidth, transcriptHeight)

	lines := make([]string, 0, height)
	lines = append(lines, header...)
	lines = append(lines, transcript...)
	lines = append(lines, status)
	lines = append(lines, inputLines...)
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	for len(lines) < height {
		insertAt := len(header)
		lines = append(lines, "")
		copy(lines[insertAt+1:], lines[insertAt:])
		lines[insertAt] = ""
	}

	var screen strings.Builder
	screen.WriteString("\x1b[H")
	for index, line := range lines {
		screen.WriteString("\x1b[2K")
		screen.WriteString(strings.Repeat(" ", margin))
		screen.WriteString(truncateDisplay(line, contentWidth))
		if index != len(lines)-1 {
			screen.WriteString("\r\n")
		}
	}
	return screen.String()
}

func (m *model) renderHeader(width int) []string {
	brand := m.colors.wrap(ansiBold+ansiCyan, "HARNESS")
	selection := sanitizeInline(strings.TrimSpace(m.provider + "/" + m.modelName))
	if selection == "/" {
		selection = "no provider"
	}
	plainLeft := "HARNESS  " + selection
	left := brand + "  " + m.colors.wrap(ansiGray, selection)
	cwd := displayWorkingDirectory(m.workingDirectory)
	available := width - displayWidth(plainLeft) - 1
	if available <= 0 {
		return []string{truncateDisplay(left, width), ""}
	}
	cwd = truncateDisplayStart(cwd, available)
	gap := max(1, width-displayWidth(plainLeft)-displayWidth(cwd))
	first := left + strings.Repeat(" ", gap) + m.colors.wrap(ansiDim+ansiGray, cwd)
	return []string{first, ""}
}

func (m *model) renderTranscript(width, height int) []string {
	all := make([]string, 0)
	contentWidth := max(1, width-4)
	for index, block := range m.blocks {
		if isToolBlock(block.kind) {
			all = append(all, m.renderToolBlock(block, width)...)
			if index == len(m.blocks)-1 || !isToolBlock(m.blocks[index+1].kind) {
				all = append(all, "")
			}
			continue
		}
		label, color := m.blockLabel(block)
		all = append(all, m.colors.wrap(ansiBold+color, label))
		wrapped := wrapText(sanitize(block.text), contentWidth)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for _, line := range wrapped {
			all = append(all, "  "+m.colors.wrap(color, line))
		}
		if block.interrupted {
			all = append(all, "  "+m.colors.wrap(ansiYellow+ansiDim, "response interrupted"))
		}
		all = append(all, "")
	}
	if len(all) == 0 {
		all = m.renderWelcome(width)
	}
	m.scrollOffset = min(max(0, m.scrollOffset), max(0, len(all)-height))
	end := len(all) - m.scrollOffset
	start := max(0, end-height)
	view := append([]string(nil), all[start:end]...)
	for len(view) < height {
		view = append(view, "")
	}
	return view
}

func (m *model) renderWelcome(width int) []string {
	contentWidth := max(1, width-2)
	lines := []string{"", m.colors.wrap(ansiBold+ansiCyan, "╭─ Ready when you are")}
	sections := []string{
		"Start with a question, @ to find a file, or / for commands.",
		"enter send  ·  ctrl+n newline  ·  tab mode",
		"@ files  ·  / commands  ·  pgup scroll  ·  ctrl+c quit",
	}
	for index, section := range sections {
		if index == 1 {
			lines = append(lines, m.colors.wrap(ansiDim+ansiGray, "│"))
		}
		for _, wrapped := range wrapText(section, contentWidth) {
			lines = append(lines, m.colors.wrap(ansiDim+ansiGray, "│ "+wrapped))
		}
	}
	return append(lines, m.colors.wrap(ansiDim+ansiGray, "╰─"))
}

func (m *model) blockLabel(block transcriptBlock) (string, string) {
	switch block.kind {
	case blockUser:
		mode := "[Build]"
		if block.mode == input.ModePlan {
			mode = "[Plan]"
		}
		username := sanitizeInline(strings.TrimSpace(m.username))
		if username == "" {
			username = "YOU"
		}
		return username + "  " + mode, ansiBlue
	case blockAssistant:
		return "ASSISTANT", ansiCyan
	case blockError:
		return "ERROR", ansiRed
	default:
		return "OUTPUT", ansiGray
	}
}

func displayWorkingDirectory(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return sanitizeInline(path)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return sanitizeInline(absolute)
	}
	relative, err := filepath.Rel(home, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return sanitizeInline(absolute)
	}
	if relative == "." {
		return "~"
	}
	return sanitizeInline(filepath.Join("~", relative))
}

func (m *model) renderStatus(width int) string {
	if m.notice != "" {
		return truncateDisplay(m.colors.wrap(ansiYellow, " ! "+sanitizeInline(m.notice)), width)
	}
	if m.status == "" && !m.isInferenceRunning {
		return ""
	}
	if !m.isInferenceRunning {
		return truncateDisplay(m.colors.wrap(ansiYellow, "   "+sanitizeInline(m.status)), width)
	}
	spinner := []string{"|", "/", "-", "\\"}[m.spinner%4]
	status := strings.TrimSpace(sanitizeInline(m.status))
	if status == "" {
		return truncateDisplay(m.colors.wrap(ansiYellow, " "+spinner), width)
	}
	return truncateDisplay(m.colors.wrap(ansiYellow, " "+spinner+" "+status), width)
}

func isToolBlock(kind blockKind) bool {
	return kind == blockToolRead || kind == blockToolWrite || kind == blockToolBash
}

func (m *model) renderToolBlock(block transcriptBlock, width int) []string {
	switch block.kind {
	case blockToolRead:
		label := "Reading"
		if block.completed {
			label = "Read"
		}
		return m.renderFileActivity(label, block, width)
	case blockToolWrite:
		label := "Writing"
		if block.completed {
			label = "Wrote"
		}
		return m.renderFileActivity(label, block, width)
	case blockToolBash:
		return m.renderBashActivity(block, width)
	default:
		return nil
	}
}

func (m *model) renderFileActivity(label string, block transcriptBlock, width int) []string {
	target := m.displayToolPath(block.activity.Target)
	line := "› " + label + " " + target
	color := ansiGray
	if block.activity.Error != "" {
		color = ansiRed
		line += " (failed: " + sanitizeInline(block.activity.Error) + ")"
	}
	return []string{truncateDisplay(m.colors.wrap(ansiDim+color, line), width)}
}

func (m *model) renderBashActivity(block transcriptBlock, width int) []string {
	command := sanitizeInline(block.activity.Command)
	lines := []string{truncateDisplay(m.colors.wrap(ansiDim+ansiGray, "› Bash $ "+command), width)}
	if block.activity.Error != "" {
		lines = append(lines, truncateDisplay(m.colors.wrap(ansiRed, "  "+sanitizeInline(block.activity.Error)), width))
	} else if block.activity.Output != "" {
		for _, outputLine := range strings.Split(sanitize(block.activity.Output), "\n") {
			lines = append(lines, truncateDisplay(m.colors.wrap(ansiDim+ansiGray, "  "+outputLine), width))
		}
	}
	return lines
}

func (m *model) displayToolPath(target string) string {
	target = filepath.Clean(target)
	if target == "." || target == "" {
		return target
	}
	base, baseErr := filepath.Abs(m.workingDirectory)
	absolute, targetErr := filepath.Abs(target)
	if baseErr == nil && targetErr == nil {
		if relative, err := filepath.Rel(base, absolute); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return sanitizeInline(relative)
		}
		return sanitizeInline(absolute)
	}
	return sanitizeInline(target)
}

func (m *model) renderInput(width int) []string {
	accent := ansiGreen
	mode := "[Build]"
	if m.mode == input.ModePlan {
		accent = ansiMagenta
		mode = "[Plan]"
	}
	separator := strings.Repeat("-", width)
	lines := []string{m.colors.wrap(ansiDim+accent, separator)}
	prefix := mode + " > "
	contentWidth := max(1, width-displayWidth(prefix)-1)
	runes, cursor := sanitizeEditor([]rune(m.editor.value()), m.editor.cursor)
	wrapped := renderEditorLines(runes, cursor, contentWidth, m.focused)
	placeholder := m.tip
	if placeholder == "" {
		placeholder = inputTips[0]
	}
	if len(wrapped) == 0 {
		wrapped = []string{cursorMarker(nil, 0, m.focused) + m.colors.wrap(ansiDim+ansiGray, " "+placeholder)}
	} else if len(runes) == 0 {
		wrapped[0] += m.colors.wrap(ansiDim+ansiGray, " "+placeholder)
	}
	if len(wrapped) > 6 {
		wrapped = wrapped[len(wrapped)-6:]
	}
	for index, line := range wrapped {
		linePrefix := strings.Repeat(" ", displayWidth(prefix))
		if index == 0 {
			linePrefix = m.colors.wrap(ansiBold+accent, prefix)
		}
		lines = append(lines, linePrefix+line)
	}
	return lines
}

func cursorMarker(runes []rune, cursor int, focused bool) string {
	character := " "
	if cursor < len(runes) && runes[cursor] != '\n' {
		character = string(runes[cursor])
	}
	if !focused {
		return character
	}
	return ansiInverse + character + ansiReset
}

func sanitize(value string) string {
	var output strings.Builder
	for _, r := range value {
		switch {
		case r == '\n':
			output.WriteRune(r)
		case r == '\t':
			output.WriteString("    ")
		case !unicode.IsControl(r) && r != 0x1b:
			output.WriteRune(r)
		}
	}
	return output.String()
}

func sanitizeInline(value string) string {
	return strings.ReplaceAll(sanitize(value), "\n", " ")
}

func sanitizeEditor(value []rune, cursor int) ([]rune, int) {
	clean := make([]rune, 0, len(value))
	cleanCursor := 0
	for index, r := range value {
		normalized := []rune(sanitize(string(r)))
		clean = append(clean, normalized...)
		if index < cursor {
			cleanCursor += len(normalized)
		}
	}
	return clean, min(cleanCursor, len(clean))
}

func wrapText(value string, width int) []string {
	if width <= 0 {
		return nil
	}
	lines := make([]string, 0)
	for _, paragraph := range strings.Split(value, "\n") {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		var line strings.Builder
		lineWidth := 0
		for _, r := range paragraph {
			rWidth := runeWidth(r)
			if lineWidth+rWidth > width && lineWidth > 0 {
				lines = append(lines, line.String())
				line.Reset()
				lineWidth = 0
			}
			line.WriteRune(r)
			lineWidth += rWidth
		}
		lines = append(lines, line.String())
	}
	return lines
}

func renderEditorLines(runes []rune, cursor, width int, focused bool) []string {
	lines := make([]string, 1)
	lineWidth := 0
	for index := 0; index <= len(runes); index++ {
		if index == cursor {
			marker := cursorMarker(runes, cursor, focused)
			markerWidth := 1
			if cursor < len(runes) {
				markerWidth = max(1, runeWidth(runes[cursor]))
			}
			if lineWidth+markerWidth > width && lineWidth > 0 {
				lines = append(lines, "")
				lineWidth = 0
			}
			lines[len(lines)-1] += marker
			lineWidth += markerWidth
			if cursor < len(runes) {
				if runes[cursor] == '\n' {
					lines = append(lines, "")
					lineWidth = 0
				}
				continue
			}
		}
		if index == len(runes) {
			break
		}
		r := runes[index]
		if r == '\n' {
			lines = append(lines, "")
			lineWidth = 0
			continue
		}
		w := runeWidth(r)
		if lineWidth+w > width && lineWidth > 0 {
			lines = append(lines, "")
			lineWidth = 0
		}
		lines[len(lines)-1] += string(r)
		lineWidth += w
	}
	return lines
}

func truncateDisplay(value string, width int) string {
	if displayWidth(value) <= width {
		return value
	}
	plain := stripANSI(value)
	var output strings.Builder
	used := 0
	for _, r := range plain {
		w := runeWidth(r)
		if used+w > max(0, width-1) {
			break
		}
		output.WriteRune(r)
		used += w
	}
	return output.String() + "…"
}

func truncateDisplayStart(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if displayWidth(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(stripANSI(value))
	used := 0
	start := len(runes)
	for start > 0 {
		w := runeWidth(runes[start-1])
		if used+w > width-1 {
			break
		}
		start--
		used += w
	}
	return "…" + string(runes[start:])
}

func displayWidth(value string) int {
	width := 0
	escape := 0
	for _, r := range value {
		switch escape {
		case 1:
			if r == '[' {
				escape = 2
			} else {
				escape = 0
			}
			continue
		case 2:
			if r >= '@' && r <= '~' {
				escape = 0
			}
			continue
		}
		if r == 0x1b {
			escape = 1
			continue
		}
		width += runeWidth(r)
	}
	return width
}

func stripANSI(value string) string {
	var output strings.Builder
	escape := 0
	for _, r := range value {
		switch escape {
		case 1:
			if r == '[' {
				escape = 2
			} else {
				escape = 0
			}
			continue
		case 2:
			if r >= '@' && r <= '~' {
				escape = 0
			}
			continue
		}
		if r == 0x1b {
			escape = 1
			continue
		}
		output.WriteRune(r)
	}
	return output.String()
}

func runeWidth(r rune) int {
	if r == 0 || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf) || (r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) || (r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) || (r >= 0x1f300 && r <= 0x1faff)) {
		return 2
	}
	return 1
}
