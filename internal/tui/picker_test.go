package tui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime/command"
)

func TestCommandCandidatesUseRequestedPrefix(t *testing.T) {
	got := commandCandidates(command.DefaultRegistry(), ":")
	want := []string{":q\texit harness", ":help\tshow available commands"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
}

func TestModelCandidatesPreserveModelWhitespace(t *testing.T) {
	got := modelCandidates([]provider.Selection{
		{Provider: "openai", Model: "gpt 4o"},
		{Provider: "anthropic", Model: "claude sonnet 4"},
		{Provider: "", Model: "ignored"},
		{Provider: "local", Model: ""},
	})
	want := []string{"anthropic/claude sonnet 4", "openai/gpt 4o"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
}

func TestFileCandidatesFallsBackOutsideRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "b.go"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := fileCandidates(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.txt", "nested/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
}

func TestCleanCandidatesPreservesFilenameWhitespace(t *testing.T) {
	got := cleanCandidates([]string{" trailing ", "\tname", ""})
	want := []string{"\tname", " trailing "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
}

func TestRunFZFReturnsSelectedField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fzf")
	script := "#!/bin/sh\nIFS= read -r line\nprintf '%s\\n' \"$line\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	selected, ok, err := runFZF(context.Background(), path, []string{"/help\tshow help"}, "Commands > ", "", false, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || selected != "/help" {
		t.Fatalf("selection = %q, %v", selected, ok)
	}
}
