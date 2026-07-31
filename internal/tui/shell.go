package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	localShellTimeout          = 30 * time.Second
	localShellMaxInsertedBytes = 16 * 1024
	localShellMaxBlockBytes    = 4 * 1024
)

type localShellResult struct {
	Command          string
	WorkingDirectory string
	ExitCode         int
	TimedOut         bool
	Stdout           string
	Stderr           string
}

func runLocalShellCommand(ctx context.Context, command string, workingDirectory string) (localShellResult, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return localShellResult{}, errors.New("command must not be empty")
	}

	commandCtx, cancel := context.WithTimeout(ctx, localShellTimeout)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, "/bin/bash", "-lc", command)
	cmd.Dir = workingDirectory
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	timedOut := errors.Is(commandCtx.Err(), context.DeadlineExceeded)
	if err := ctx.Err(); err != nil && !timedOut {
		return localShellResult{}, err
	}

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if timedOut {
			exitCode = -1
		} else {
			return localShellResult{}, fmt.Errorf("start shell command: %w", runErr)
		}
	}
	if timedOut {
		exitCode = -1
	}

	return localShellResult{
		Command:          command,
		WorkingDirectory: workingDirectory,
		ExitCode:         exitCode,
		TimedOut:         timedOut,
		Stdout:           stdout.String(),
		Stderr:           stderr.String(),
	}, nil
}

func formatLocalShellForMessage(result localShellResult) string {
	formatted := formatLocalShell(result)
	truncated, ok := truncateUTF8Bytes(formatted, localShellMaxInsertedBytes)
	if ok {
		return fmt.Sprintf("%s\n\n(Output truncated to %d bytes.)", strings.TrimRight(truncated, "\n"), localShellMaxInsertedBytes)
	}
	return formatted
}

func formatLocalShellForBlock(result localShellResult) string {
	formatted := formatLocalShell(result)
	truncated, ok := truncateUTF8Bytes(formatted, localShellMaxBlockBytes)
	if ok {
		return fmt.Sprintf("%s\n\n(Output truncated to %d bytes.)", strings.TrimRight(truncated, "\n"), localShellMaxBlockBytes)
	}
	return formatted
}

func formatLocalShell(result localShellResult) string {
	var output strings.Builder
	fmt.Fprintf(&output, "Command: %s\n", result.Command)
	fmt.Fprintf(&output, "Working directory: %s\n", result.WorkingDirectory)
	fmt.Fprintf(&output, "Exit code: %d\n", result.ExitCode)
	if result.TimedOut {
		output.WriteString("Timed out: true\n")
	}
	if result.Stdout != "" {
		output.WriteString("\nStdout:\n")
		output.WriteString(result.Stdout)
		if !strings.HasSuffix(result.Stdout, "\n") {
			output.WriteByte('\n')
		}
	}
	if result.Stderr != "" {
		output.WriteString("\nStderr:\n")
		output.WriteString(result.Stderr)
		if !strings.HasSuffix(result.Stderr, "\n") {
			output.WriteByte('\n')
		}
	}
	if result.Stdout == "" && result.Stderr == "" {
		output.WriteString("\n(no output)\n")
	}
	return strings.TrimRight(output.String(), "\n")
}

func truncateUTF8Bytes(value string, limit int) (string, bool) {
	if limit <= 0 {
		return "", value != ""
	}
	if len(value) <= limit {
		return value, false
	}
	truncated := value[:limit]
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated, true
}
