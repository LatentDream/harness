package tui

import (
	"strings"
	"unicode"
)

type editor struct {
	text         []rune
	cursor       int
	history      []string
	historyIndex int
}

func (e *editor) value() string { return string(e.text) }

func (e *editor) empty() bool { return len(e.text) == 0 }

func (e *editor) reset() {
	e.text = nil
	e.cursor = 0
	e.historyIndex = len(e.history)
}

func (e *editor) set(value string) {
	e.text = []rune(value)
	e.cursor = len(e.text)
}

func (e *editor) insert(value string) {
	runes := []rune(value)
	if len(runes) == 0 {
		return
	}
	e.text = append(e.text, make([]rune, len(runes))...)
	copy(e.text[e.cursor+len(runes):], e.text[e.cursor:len(e.text)-len(runes)])
	copy(e.text[e.cursor:], runes)
	e.cursor += len(runes)
}

func (e *editor) backspace() {
	if e.cursor == 0 {
		return
	}
	copy(e.text[e.cursor-1:], e.text[e.cursor:])
	e.text = e.text[:len(e.text)-1]
	e.cursor--
}

func (e *editor) delete() {
	if e.cursor >= len(e.text) {
		return
	}
	copy(e.text[e.cursor:], e.text[e.cursor+1:])
	e.text = e.text[:len(e.text)-1]
}

func (e *editor) deleteWord() {
	for e.cursor > 0 && unicode.IsSpace(e.text[e.cursor-1]) {
		e.backspace()
	}
	for e.cursor > 0 && !unicode.IsSpace(e.text[e.cursor-1]) {
		e.backspace()
	}
}

func (e *editor) moveLeft() {
	if e.cursor > 0 {
		e.cursor--
	}
}

func (e *editor) moveRight() {
	if e.cursor < len(e.text) {
		e.cursor++
	}
}

func (e *editor) moveWord(direction int) {
	if direction < 0 {
		for e.cursor > 0 && unicode.IsSpace(e.text[e.cursor-1]) {
			e.cursor--
		}
		for e.cursor > 0 && !unicode.IsSpace(e.text[e.cursor-1]) {
			e.cursor--
		}
		return
	}
	for e.cursor < len(e.text) && unicode.IsSpace(e.text[e.cursor]) {
		e.cursor++
	}
	for e.cursor < len(e.text) && !unicode.IsSpace(e.text[e.cursor]) {
		e.cursor++
	}
}

func (e *editor) home() {
	for e.cursor > 0 && e.text[e.cursor-1] != '\n' {
		e.cursor--
	}
}

func (e *editor) end() {
	for e.cursor < len(e.text) && e.text[e.cursor] != '\n' {
		e.cursor++
	}
}

func (e *editor) vertical(direction int) {
	start := e.cursor
	for start > 0 && e.text[start-1] != '\n' {
		start--
	}
	column := e.cursor - start
	if direction < 0 {
		if start == 0 {
			e.historyMove(-1)
			return
		}
		previousEnd := start - 1
		previousStart := previousEnd
		for previousStart > 0 && e.text[previousStart-1] != '\n' {
			previousStart--
		}
		e.cursor = min(previousStart+column, previousEnd)
		return
	}
	lineEnd := e.cursor
	for lineEnd < len(e.text) && e.text[lineEnd] != '\n' {
		lineEnd++
	}
	if lineEnd == len(e.text) {
		e.historyMove(1)
		return
	}
	nextStart := lineEnd + 1
	nextEnd := nextStart
	for nextEnd < len(e.text) && e.text[nextEnd] != '\n' {
		nextEnd++
	}
	e.cursor = min(nextStart+column, nextEnd)
}

func (e *editor) remember(value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if len(e.history) == 0 || e.history[len(e.history)-1] != value {
		e.history = append(e.history, value)
	}
	e.historyIndex = len(e.history)
}

func (e *editor) historyMove(direction int) {
	if len(e.history) == 0 || strings.Contains(e.value(), "\n") {
		return
	}
	index := e.historyIndex + direction
	if index < 0 {
		index = 0
	}
	if index > len(e.history) {
		index = len(e.history)
	}
	e.historyIndex = index
	if index == len(e.history) {
		e.set("")
		return
	}
	e.set(e.history[index])
}
