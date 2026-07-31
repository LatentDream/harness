package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatLocalShellIncludesCommandMetadataAndOutput(t *testing.T) {
	result := localShellResult{
		Command:          "printf hi",
		WorkingDirectory: "/workspace",
		ExitCode:         0,
		Stdout:           "hi\n",
		Stderr:           "warn\n",
	}

	formatted := formatLocalShellForMessage(result)
	for _, expected := range []string{
		"Command: printf hi",
		"Working directory: /workspace",
		"Exit code: 0",
		"Stdout:\nhi",
		"Stderr:\nwarn",
	} {
		if !strings.Contains(formatted, expected) {
			t.Fatalf("formatted shell output missing %q: %q", expected, formatted)
		}
	}
}

func TestFormatLocalShellTruncatesUTF8Safely(t *testing.T) {
	result := localShellResult{Command: "cmd", WorkingDirectory: "/workspace", Stdout: strings.Repeat("界", localShellMaxInsertedBytes)}

	formatted := formatLocalShellForMessage(result)
	if !strings.Contains(formatted, "Output truncated to") {
		t.Fatalf("formatted shell output was not marked truncated")
	}
	if !strings.Contains(formatted, "界") {
		t.Fatalf("formatted shell output lost utf8 data")
	}
	if strings.ContainsRune(formatted, '\ufffd') {
		t.Fatalf("formatted shell output contains replacement rune: %q", formatted[len(formatted)-128:])
	}
}

func TestRunLocalShellCommandCapturesStdoutStderrAndExitCode(t *testing.T) {
	workspace := t.TempDir()
	result, err := runLocalShellCommand(context.Background(), "printf out; printf err >&2; exit 7", workspace)
	if err != nil {
		t.Fatalf("runLocalShellCommand() error = %v", err)
	}
	if result.WorkingDirectory != workspace || result.Stdout != "out" || result.Stderr != "err" || result.ExitCode != 7 {
		t.Fatalf("unexpected shell result: %#v", result)
	}
}

func TestRunLocalShellCommandUsesWorkingDirectory(t *testing.T) {
	workspace := t.TempDir()
	child := filepath.Join(workspace, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := runLocalShellCommand(context.Background(), "pwd", child)
	if err != nil {
		t.Fatalf("runLocalShellCommand() error = %v", err)
	}
	if strings.TrimSpace(result.Stdout) != child {
		t.Fatalf("pwd = %q, want %q", result.Stdout, child)
	}
}
