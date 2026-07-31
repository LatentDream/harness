package tui

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

const (
	ansiItalic               = "\x1b[3m"
	ansiUnderline            = "\x1b[4m"
	ansiStrike               = "\x1b[9m"
	ansiCodeBackground       = "\x1b[48;5;236m"
	ansiCodeAccentBackground = "\x1b[48;5;24m"
	ansiEraseLineEnd         = "\x1b[K"
)

type markdownSpan struct {
	text  string
	style string
}

type terminalMarkdown struct {
	source []byte
	width  int
	colors palette
	lines  []string
}

func renderMarkdown(value string, width int, colors palette) []string {
	// Sanitize before parsing so model-provided terminal control sequences can
	// never be reintroduced by a styled node.
	source := []byte(sanitize(value))
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()
	document := parser.Parse(text.NewReader(source))
	renderer := &terminalMarkdown{source: source, width: max(1, width), colors: colors}
	for child := document.FirstChild(); child != nil; child = child.NextSibling() {
		renderer.renderBlock(child)
	}
	renderer.trimBlankLines()
	if len(renderer.lines) == 0 {
		return []string{""}
	}
	return renderer.lines
}

func (r *terminalMarkdown) renderBlock(node ast.Node) {
	switch n := node.(type) {
	case *ast.Paragraph:
		r.emitInline(n, "", "")
		r.blank()
	case *ast.TextBlock:
		r.emitInline(n, "", "")
	case *ast.Heading:
		r.emitInline(n, strings.Repeat("#", n.Level)+" ", ansiBold+ansiCyan)
		r.blank()
	case *ast.ThematicBreak:
		r.lines = append(r.lines, r.colors.wrap(ansiDim+ansiGray, strings.Repeat("─", min(r.width, 24))))
		r.blank()
	case *ast.Blockquote:
		r.renderContainer(n, "│ ", ansiDim+ansiCyan)
		r.blank()
	case *ast.List:
		r.renderList(n)
		r.blank()
	case *ast.FencedCodeBlock:
		r.renderCode(n.Lines(), string(n.Language(r.source)))
		r.blank()
	case *ast.CodeBlock:
		r.renderCode(n.Lines(), "")
		r.blank()
	case *ast.HTMLBlock:
		r.emitPlain(string(n.Text(r.source)), "", ansiDim+ansiGray)
		r.blank()
	case *extast.Table:
		r.renderTable(n)
		r.blank()
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			r.renderBlock(child)
		}
	}
}

func (r *terminalMarkdown) renderContainer(node ast.Node, prefix, prefixStyle string) {
	inner := &terminalMarkdown{source: r.source, width: max(1, r.width-displayWidth(prefix)), colors: r.colors}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		inner.renderBlock(child)
	}
	inner.trimBlankLines()
	for _, line := range inner.lines {
		r.lines = append(r.lines, r.colors.wrap(prefixStyle, prefix)+line)
	}
}

func (r *terminalMarkdown) renderList(list *ast.List) {
	ordinal := list.Start
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "• "
		if list.IsOrdered() {
			marker = fmt.Sprintf("%d. ", ordinal)
			ordinal++
		}
		inner := &terminalMarkdown{source: r.source, width: max(1, r.width-displayWidth(marker)), colors: r.colors}
		for child := item.FirstChild(); child != nil; child = child.NextSibling() {
			inner.renderBlock(child)
		}
		inner.trimBlankLines()
		if len(inner.lines) == 0 {
			r.lines = append(r.lines, marker)
			continue
		}
		for index, line := range inner.lines {
			prefix := strings.Repeat(" ", displayWidth(marker))
			if index == 0 {
				prefix = r.colors.wrap(ansiCyan, marker)
			}
			r.lines = append(r.lines, prefix+line)
		}
	}
}

func (r *terminalMarkdown) renderCode(segments *text.Segments, language string) {
	label := "code"
	if language != "" {
		label = sanitizeInline(language)
	}
	if !r.colors.enabled {
		r.lines = append(r.lines, label)
		for index := range segments.Len() {
			segment := segments.At(index)
			value := strings.TrimSuffix(string(segment.Value(r.source)), "\n")
			r.lines = append(r.lines, wrapCodeLine(value, r.width)...)
		}
		if segments.Len() == 0 {
			r.lines = append(r.lines, "")
		}
		return
	}

	// The language badge occupies the top padding row. Code rows use explicit
	// padding instead of border glyphs so selecting code remains copy-friendly.
	badgeText := " " + label + " "
	badge := ansiCodeAccentBackground + ansiBold + badgeText + ansiReset
	filler := strings.Repeat(" ", max(0, r.width-2-displayWidth(badgeText)))
	r.lines = append(r.lines, badge+ansiCodeBackground+filler+ansiReset+"  ")
	r.lines = append(r.lines, r.codePanelLine(""))
	contentWidth := max(1, r.width-4)
	for index := range segments.Len() {
		segment := segments.At(index)
		value := strings.TrimSuffix(string(segment.Value(r.source)), "\n")
		for _, line := range wrapCodeLine(value, contentWidth) {
			r.lines = append(r.lines, r.codePanelLine(line))
		}
	}
	if segments.Len() == 0 {
		r.lines = append(r.lines, r.codePanelLine(""))
	}
	r.lines = append(r.lines, r.codePanelLine(""))
}

func (r *terminalMarkdown) codePanelLine(value string) string {
	inner := "  " + value
	if padding := r.width - 2 - displayWidth(inner); padding > 0 {
		inner += strings.Repeat(" ", padding)
	}
	return ansiCodeBackground + inner + ansiReset + "  "
}

func wrapCodeLine(value string, width int) []string {
	wrapped := wrapText(value, width)
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}

func (r *terminalMarkdown) renderTable(table *extast.Table) {
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		isHeader := row.Kind() == extast.KindTableHeader
		parts := make([]string, 0)
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			parts = append(parts, strings.TrimSpace(renderSpans(r.inlineSpans(cell, ""), r.colors)))
		}
		line := "│ " + strings.Join(parts, " │ ") + " │"
		if displayWidth(line) > r.width {
			line = truncateDisplay(line, r.width)
		}
		style := ""
		if isHeader {
			style = ansiBold + ansiCyan
		}
		r.lines = append(r.lines, r.colors.wrap(style, line))
		if isHeader {
			r.lines = append(r.lines, r.colors.wrap(ansiDim+ansiCyan, strings.Repeat("─", min(r.width, 24))))
		}
	}
}

func (r *terminalMarkdown) emitInline(node ast.Node, prefix, baseStyle string) {
	r.emitSpans(r.inlineSpans(node, baseStyle), prefix)
}

func (r *terminalMarkdown) emitPlain(value, prefix, style string) {
	r.emitSpans([]markdownSpan{{text: value, style: style}}, prefix)
}

func (r *terminalMarkdown) emitSpans(spans []markdownSpan, prefix string) {
	available := max(1, r.width-displayWidth(prefix))
	lineSpans := make([]markdownSpan, 0)
	lineWidth := 0
	wrote := false
	continuation := strings.Repeat(" ", displayWidth(prefix))
	linePrefix := prefix
	appendCharacter := func(character rune, style string) {
		if len(lineSpans) > 0 && lineSpans[len(lineSpans)-1].style == style {
			lineSpans[len(lineSpans)-1].text += string(character)
		} else {
			lineSpans = append(lineSpans, markdownSpan{text: string(character), style: style})
		}
	}
	flush := func() {
		r.lines = append(r.lines, linePrefix+renderSpans(lineSpans, r.colors))
		linePrefix = continuation
		lineSpans = lineSpans[:0]
		lineWidth = 0
		wrote = false
	}
	for _, span := range spans {
		for _, character := range span.text {
			if character == '\n' {
				flush()
				continue
			}
			characterWidth := runeWidth(character)
			if lineWidth+characterWidth > available && lineWidth > 0 {
				flush()
			}
			appendCharacter(character, span.style)
			lineWidth += characterWidth
			wrote = true
		}
	}
	if wrote || len(r.lines) == 0 || prefix != "" {
		r.lines = append(r.lines, linePrefix+renderSpans(lineSpans, r.colors))
	}
}

func (r *terminalMarkdown) inlineSpans(node ast.Node, inherited string) []markdownSpan {
	spans := make([]markdownSpan, 0)
	var walk func(ast.Node, string)
	walk = func(current ast.Node, style string) {
		switch n := current.(type) {
		case *ast.Text:
			value := string(n.Value(r.source))
			if n.SoftLineBreak() {
				value += " "
			} else if n.HardLineBreak() {
				value += "\n"
			}
			spans = append(spans, markdownSpan{text: value, style: style})
			return
		case *ast.String:
			spans = append(spans, markdownSpan{text: string(n.Value), style: style})
			return
		case *ast.Emphasis:
			if n.Level == 2 {
				style += ansiBold
			} else {
				style += ansiItalic
			}
		case *ast.CodeSpan:
			style += ansiYellow
		case *ast.Link:
			style += ansiUnderline + ansiCyan
			start := len(spans)
			for child := current.FirstChild(); child != nil; child = child.NextSibling() {
				walk(child, style)
			}
			destination := sanitizeInline(string(n.Destination))
			if destination != "" && plainSpans(spans[start:]) != destination {
				spans = append(spans, markdownSpan{text: " (" + destination + ")", style: ansiDim + ansiCyan})
			}
			return
		case *ast.AutoLink:
			spans = append(spans, markdownSpan{text: string(n.Label(r.source)), style: style + ansiUnderline + ansiCyan})
			return
		case *ast.Image:
			spans = append(spans, markdownSpan{text: "image: ", style: style + ansiDim})
			for child := current.FirstChild(); child != nil; child = child.NextSibling() {
				walk(child, style+ansiItalic)
			}
			if destination := sanitizeInline(string(n.Destination)); destination != "" {
				spans = append(spans, markdownSpan{text: " (" + destination + ")", style: ansiDim + ansiCyan})
			}
			return
		case *ast.RawHTML:
			spans = append(spans, markdownSpan{text: string(n.Text(r.source)), style: style + ansiDim + ansiGray})
			return
		case *extast.Strikethrough:
			style += ansiStrike
		case *extast.TaskCheckBox:
			marker := "[ ] "
			if n.IsChecked {
				marker = "[✓] "
			}
			spans = append(spans, markdownSpan{text: marker, style: style + ansiCyan})
			return
		}
		for child := current.FirstChild(); child != nil; child = child.NextSibling() {
			walk(child, style)
		}
	}
	walk(node, inherited)
	return spans
}

func (r *terminalMarkdown) blank() {
	if len(r.lines) > 0 && r.lines[len(r.lines)-1] != "" {
		r.lines = append(r.lines, "")
	}
}

func (r *terminalMarkdown) trimBlankLines() {
	for len(r.lines) > 0 && r.lines[len(r.lines)-1] == "" {
		r.lines = r.lines[:len(r.lines)-1]
	}
}

func renderSpans(spans []markdownSpan, colors palette) string {
	var output strings.Builder
	for _, span := range spans {
		output.WriteString(colors.wrap(span.style, span.text))
	}
	return output.String()
}

func plainSpans(spans []markdownSpan) string {
	var output strings.Builder
	for _, span := range spans {
		output.WriteString(span.text)
	}
	return output.String()
}
