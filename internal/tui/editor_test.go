package tui

import "testing"

func TestEditorEditsMultilineUnicodeText(t *testing.T) {
	var editor editor
	editor.insert("hello\n世界")
	editor.moveLeft()
	editor.backspace()
	editor.insert("界")
	if got := editor.value(); got != "hello\n界界" {
		t.Fatalf("value = %q", got)
	}
	editor.home()
	editor.insert("> ")
	if got := editor.value(); got != "hello\n> 界界" {
		t.Fatalf("value after home = %q", got)
	}
}

func TestEditorHistoryAvoidsDuplicates(t *testing.T) {
	var editor editor
	editor.remember("first")
	editor.remember("first")
	editor.remember("second")
	editor.reset()
	editor.historyMove(-1)
	if got := editor.value(); got != "second" {
		t.Fatalf("latest history = %q", got)
	}
	editor.historyMove(-1)
	if got := editor.value(); got != "first" {
		t.Fatalf("earlier history = %q", got)
	}
	if len(editor.history) != 2 {
		t.Fatalf("history = %#v", editor.history)
	}
}

func TestEditorDeleteWord(t *testing.T) {
	var editor editor
	editor.insert("one two  ")
	editor.deleteWord()
	if got := editor.value(); got != "one " {
		t.Fatalf("value = %q", got)
	}
}
