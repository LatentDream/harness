package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSystemPromptIncludesCurrentWorkingDirectory(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)

	prompt := (&Session{}).BuildSystemPrompt()

	if !strings.Contains(prompt, "Current working directory:\n"+cwd) {
		t.Fatalf("expected prompt to include current working directory %q, got %q", cwd, prompt)
	}
	if strings.Contains(prompt, "Project Context:") {
		t.Fatalf("expected prompt to skip missing project context, got %q", prompt)
	}
}

func TestBuildSystemPromptIncludesProjectContext(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("follow repo instructions\n"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("prefer concise answers\n"), 0o600); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	t.Chdir(cwd)

	prompt := (&Session{}).BuildSystemPrompt()

	for _, want := range []string{
		"Project Context:\n",
		"## AGENTS.md\nfollow repo instructions",
		"## CLAUDE.md\nprefer concise answers",
		"Current working directory:\n" + cwd,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to include %q, got %q", want, prompt)
		}
	}
}
