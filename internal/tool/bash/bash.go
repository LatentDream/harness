package bash

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/model"
	"latentdream/harness/internal/tool/utils"
)

const (
	defaultTimeoutSeconds = 30
	maxTimeoutSeconds     = 120
	maxOutputBytes        = 64 * 1024
	maxActivityBytes      = 4 * 1024
	maxActivityLines      = 8
)

//go:embed bash.txt
var bashDescription string

type bashTool struct {
	executable     string
	defaultTimeout time.Duration
	maxTimeout     time.Duration
}

type bashParams struct {
	command          string
	workingDirectory string
	timeout          time.Duration
}

func New() model.Tool {
	return bashTool{
		executable:     "/bin/bash",
		defaultTimeout: defaultTimeoutSeconds * time.Second,
		maxTimeout:     maxTimeoutSeconds * time.Second,
	}
}

func (bashTool) Definition() llm.ToolDefinition {
	additionalProperties := false
	return llm.ToolDefinition{
		Name:        "bash",
		Description: strings.TrimSpace(bashDescription),
		Parameters: llm.Schema{
			Type: "object",
			Properties: map[string]llm.Schema{
				"command": {
					Type:        "string",
					Description: "The bash command to execute",
				},
				"workingDirectory": {
					Type:        "string",
					Description: "The absolute directory path to run the command in. Defaults to the current working directory.",
				},
				"timeoutSeconds": {
					Type:        "number",
					Description: "Maximum command runtime in seconds. Defaults to 30 and is capped at 120.",
				},
			},
			Required:             []string{"command"},
			AdditionalProperties: &additionalProperties,
		},
	}
}

func (bashTool) Capability() model.Capability {
	return model.CapabilityMutating
}

func (bashTool) Status(args json.RawMessage) string {
	params, err := decodeBashParams(args, defaultTimeoutSeconds*time.Second, maxTimeoutSeconds*time.Second)
	if err != nil {
		return "Running bash command"
	}
	return "Running bash command: " + params.command
}

func (bashTool) Present(args json.RawMessage, result string, executionErr error) model.Activity {
	var values struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &values); err != nil {
		return model.Activity{}
	}
	activity := model.Activity{Command: strings.TrimSpace(values.Command)}
	if result != "" && executionErr == nil {
		activity.Output = summarizeOutput(result)
	}
	return activity
}

func (tool bashTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	params, err := decodeBashParams(args, tool.defaultTimeout, tool.maxTimeout)
	if err != nil {
		return "", err
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	commandCtx, cancel := context.WithTimeout(ctx, params.timeout)
	defer cancel()

	command := exec.CommandContext(commandCtx, tool.executable, "-lc", params.command)
	command.Dir = params.workingDirectory
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	runErr := command.Run()
	timedOut := errors.Is(commandCtx.Err(), context.DeadlineExceeded)
	if err := ctx.Err(); err != nil && !timedOut {
		return "", err
	}

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if timedOut {
			exitCode = -1
		} else {
			return "", fmt.Errorf("start bash: %w", runErr)
		}
	}
	if timedOut {
		exitCode = -1
	}

	return formatOutput(params, exitCode, timedOut, stdout.String(), stderr.String()), nil
}

func decodeBashParams(args json.RawMessage, defaultTimeout time.Duration, maxTimeout time.Duration) (bashParams, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		return bashParams{}, errors.New("bash arguments must be a JSON object")
	}

	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return bashParams{}, fmt.Errorf("decode bash arguments: %w", err)
	}
	if raw == nil {
		return bashParams{}, errors.New("bash arguments must be a JSON object")
	}
	for name := range raw {
		switch name {
		case "command", "workingDirectory", "timeoutSeconds":
		default:
			return bashParams{}, fmt.Errorf("bash argument %q is not supported", name)
		}
	}

	command, err := utils.ParseRequiredString(raw, "command")
	if err != nil {
		return bashParams{}, err
	}

	workingDirectory, err := optionalString(raw, "workingDirectory")
	if err != nil {
		return bashParams{}, err
	}
	if workingDirectory == "" {
		workingDirectory, err = os.Getwd()
		if err != nil {
			return bashParams{}, fmt.Errorf("get current working directory: %w", err)
		}
	}
	workingDirectory = filepath.Clean(workingDirectory)
	if !filepath.IsAbs(workingDirectory) {
		return bashParams{}, errors.New("workingDirectory must be absolute")
	}
	info, err := os.Stat(workingDirectory)
	if err != nil {
		return bashParams{}, fmt.Errorf("stat workingDirectory %q: %w", workingDirectory, err)
	}
	if !info.IsDir() {
		return bashParams{}, errors.New("workingDirectory must be a directory")
	}

	timeout := defaultTimeout
	if timeout <= 0 {
		timeout = defaultTimeoutSeconds * time.Second
	}
	if maxTimeout <= 0 {
		maxTimeout = maxTimeoutSeconds * time.Second
	}
	timeoutSeconds, ok, err := utils.ParseOptionalPositiveInt(raw, "timeoutSeconds")
	if err != nil {
		return bashParams{}, err
	}
	if ok {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}

	return bashParams{command: command, workingDirectory: workingDirectory, timeout: timeout}, nil
}

func optionalString(raw map[string]json.RawMessage, name string) (string, error) {
	value, ok := raw[name]
	if !ok {
		return "", nil
	}
	var parsed string
	if err := json.Unmarshal(value, &parsed); err != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	parsed = strings.TrimSpace(parsed)
	if parsed == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	return parsed, nil
}

func formatOutput(params bashParams, exitCode int, timedOut bool, stdout string, stderr string) string {
	truncatedStdout, stdoutTruncated := truncateOutput(stdout)
	truncatedStderr, stderrTruncated := truncateOutput(stderr)

	var output strings.Builder
	fmt.Fprintf(&output, "<command>%s</command>\n", params.command)
	fmt.Fprintf(&output, "<workingDirectory>%s</workingDirectory>\n", params.workingDirectory)
	fmt.Fprintf(&output, "<exitCode>%d</exitCode>\n", exitCode)
	if timedOut {
		fmt.Fprintf(&output, "<timedOut>true</timedOut>\n")
	}
	output.WriteString("<stdout>\n")
	output.WriteString(truncatedStdout)
	if stdoutTruncated {
		fmt.Fprintf(&output, "\n(Output truncated to %d bytes.)", maxOutputBytes)
	}
	output.WriteString("\n</stdout>\n")
	output.WriteString("<stderr>\n")
	output.WriteString(truncatedStderr)
	if stderrTruncated {
		fmt.Fprintf(&output, "\n(Output truncated to %d bytes.)", maxOutputBytes)
	}
	output.WriteString("\n</stderr>")
	return output.String()
}

// TODO: we might want a better way to truncate output, but this is a start
func truncateOutput(value string) (string, bool) {
	if len(value) <= maxOutputBytes {
		return value, false
	}
	return value[:maxOutputBytes], true
}

func summarizeOutput(result string) string {
	exitCode := extractElement(result, "exitCode")
	stdout := extractElement(result, "stdout")
	stderr := extractElement(result, "stderr")
	timedOut := extractElement(result, "timedOut") == "true"

	lines := make([]string, 0, 4)
	if timedOut {
		lines = append(lines, "timed out")
	} else if exitCode != "" && exitCode != "0" {
		lines = append(lines, "exit code "+exitCode)
	}
	if stdout != "" {
		lines = append(lines, stdout)
	}
	if stderr != "" {
		lines = append(lines, "stderr:", stderr)
	}
	return cropActivity(strings.Join(lines, "\n"))
}

func extractElement(value, name string) string {
	startMarker := "<" + name + ">"
	endMarker := "</" + name + ">"
	start := strings.Index(value, startMarker)
	end := strings.LastIndex(value, endMarker)
	if start < 0 || end < start {
		return ""
	}
	start += len(startMarker)
	return strings.TrimSpace(value[start:end])
}

func cropActivity(value string) string {
	value = strings.ToValidUTF8(value, "?")
	truncated := false
	if len(value) > maxActivityBytes {
		end := maxActivityBytes
		for end > 0 && !utf8.ValidString(value[:end]) {
			end--
		}
		value = value[:end]
		truncated = true
	}
	lines := strings.Split(value, "\n")
	if len(lines) > maxActivityLines {
		lines = lines[:maxActivityLines]
		truncated = true
	}
	value = strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if truncated {
		value += "\n... output cropped ..."
	}
	return value
}
