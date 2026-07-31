package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (u *UI) editPromptExternally(ctx context.Context, state *model) (runErr error) {
	editorCommand := externalEditorCommand()
	if strings.TrimSpace(editorCommand) == "" {
		state.notice = "no editor found; set $VISUAL or $EDITOR"
		return nil
	}

	file, err := os.CreateTemp("", "harness-prompt-*.md")
	if err != nil {
		return fmt.Errorf("create prompt editor file: %w", err)
	}
	path := file.Name()
	defer func() { runErr = errors.Join(runErr, os.Remove(path)) }()

	if _, err := file.WriteString(state.editor.value()); err != nil {
		_ = file.Close()
		return fmt.Errorf("write prompt editor file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close prompt editor file: %w", err)
	}

	if err := u.terminal.Suspend(); err != nil {
		return err
	}
	runErr = runExternalEditor(ctx, editorCommand, path, u.in, u.out, u.err, state.workingDirectory)
	resumeErr := u.terminal.Resume()
	if runErr != nil {
		state.notice = fmt.Sprintf("editor failed: %v", runErr)
		return resumeErr
	}
	if resumeErr != nil {
		return resumeErr
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read prompt editor file: %w", err)
	}
	state.editor.set(strings.TrimRight(string(data), "\n"))
	return nil
}

func externalEditorCommand() string {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	for _, candidate := range []string{"nvim", "vim", "vi"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

func runExternalEditor(ctx context.Context, editorCommand, path string, stdin, stdout, stderr *os.File, workingDirectory string) error {
	command := editorCommand + " " + shellQuote(path)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	if workingDirectory != "" {
		if absolute, err := filepath.Abs(workingDirectory); err == nil {
			cmd.Dir = absolute
		}
	}
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
