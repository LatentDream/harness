package tui

import (
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
	header := m.renderHeader(width)
	inputLines := m.renderInput(width)
	footer := m.renderFooter(width)
	status := m.renderStatus(width)
	transcriptHeight := max(1, height-len(header)-len(inputLines)-2)
	transcript := m.renderTranscript(width, transcriptHeight)

	lines := make([]string, 0, height)
	lines = append(lines, header...)
	lines = append(lines, transcript...)
	lines = append(lines, status)
	lines = append(lines, inputLines...)
	lines = append(lines, footer)
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
		screen.WriteString(truncateDisplay(line, width))
		if index != len(lines)-1 {
			screen.WriteString("\r\n")
		}
	}
	return screen.String()
}

func (m *model) renderHeader(width int) []string {
	brand := m.colors.wrap(ansiBold+ansiCyan, " HARNESS ")
	selection := sanitizeInline(strings.TrimSpace(m.provider + "/" + m.modelName))
	if selection == "/" {
		selection = "no provider"
	}
	modeText := "[BUILD]"
	modeColor := ansiMagenta
	if m.mode == input.ModePlan {
		modeText = "[PLAN]"
		modeColor = ansiGreen
	}
	right := m.colors.wrap(modeColor+ansiBold, modeText)
	plainLeft := " HARNESS  " + selection
	gap := max(1, width-displayWidth(plainLeft)-displayWidth(modeText))
	first := brand + " " + m.colors.wrap(ansiGray, selection) + strings.Repeat(" ", gap) + right
	cwd := sanitizeInline(m.workingDirectory)
	if home, err := filepath.Abs(cwd); err == nil {
		cwd = home
	}
	second := m.colors.wrap(ansiDim+ansiGray, " "+cwd)
	return []string{first, second}
}

func (m *model) renderTranscript(width, height int) []string {
	all := make([]string, 0)
	contentWidth := max(1, width-4)
	for _, block := range m.blocks {
		label, color := blockLabel(block)
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
		welcome := "Start with a question, @ to find a file, or / for commands."
		all = []string{"", m.colors.wrap(ansiDim+ansiGray, "  "+welcome)}
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

func blockLabel(block transcriptBlock) (string, string) {
	switch block.kind {
	case blockUser:
		mode := strings.ToUpper(string(block.mode))
		if mode == "" {
			mode = "BUILD"
		}
		return "YOU  " + mode, ansiBlue
	case blockAssistant:
		return "ASSISTANT", ansiCyan
	case blockError:
		return "ERROR", ansiRed
	default:
		return "OUTPUT", ansiGray
	}
}

func (m *model) renderStatus(width int) string {
	if m.notice != "" {
		return truncateDisplay(m.colors.wrap(ansiYellow, " ! "+sanitizeInline(m.notice)), width)
	}
	if m.status == "" {
		return ""
	}
	spinner := []string{"|", "/", "-", "\\"}[m.spinner%4]
	return truncateDisplay(m.colors.wrap(ansiYellow, " "+spinner+" "+sanitizeInline(m.status)), width)
}

func (m *model) renderInput(width int) []string {
	accent := ansiGreen
	mode := "BUILD"
	if m.mode == input.ModePlan {
		accent = ansiMagenta
		mode = "PLAN"
	}
	separator := strings.Repeat("-", width)
	lines := []string{m.colors.wrap(ansiDim+accent, separator)}
	prefix := mode + " > "
	contentWidth := max(1, width-displayWidth(prefix)-1)
	runes, cursor := sanitizeEditor([]rune(m.editor.value()), m.editor.cursor)
	wrapped := renderEditorLines(runes, cursor, contentWidth, m.focused)
	if len(wrapped) == 0 {
		wrapped = []string{cursorMarker(nil, 0, m.focused) + m.colors.wrap(ansiDim+ansiGray, " Ask anything")}
	} else if len(runes) == 0 {
		wrapped[0] += m.colors.wrap(ansiDim+ansiGray, " Ask anything")
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

func (m *model) renderFooter(width int) string {
	state := "enter send  ctrl+n newline  tab mode  @ files  / commands  pgup scroll  ctrl+c quit"
	if !m.ready {
		state = "working  pgup scroll  tab next mode  ctrl+c cancel"
	}
	return truncateDisplay(m.colors.wrap(ansiDim+ansiGray, " "+state), width)
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
