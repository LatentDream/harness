package grep

import (
	"bufio"
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

	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/model"
	"latentdream/harness/internal/tool/utils"
)

const (
	defaultResultLimit = 100
	maxMatchColumns    = 2000
)

//go:embed grep.txt
var grepDescription string

type grepTool struct {
	executable string
}

type grepParams struct {
	pattern string
	path    string
	include string
}

func New() model.Tool {
	return grepTool{executable: "rg"}
}

func (grepTool) Definition() llm.ToolDefinition {
	additionalProperties := false
	return llm.ToolDefinition{
		Name:        "grep",
		Description: strings.TrimSpace(grepDescription),
		Parameters: llm.Schema{
			Type: "object",
			Properties: map[string]llm.Schema{
				"pattern": {
					Type:        "string",
					Description: "The regular expression to search for",
				},
				"path": {
					Type:        "string",
					Description: "The absolute file or directory path to search (defaults to the current working directory)",
				},
				"include": {
					Type:        "string",
					Description: "A glob that matching files must satisfy, such as *.go",
				},
			},
			Required:             []string{"pattern"},
			AdditionalProperties: &additionalProperties,
		},
	}
}

func (grepTool) Capability() model.Capability {
	return model.CapabilityReadOnly
}

func (grepTool) Status(args json.RawMessage) string {
	params, err := decodeGrepParams(args)
	if err != nil {
		return "Searching files"
	}
	return "Searching for " + params.pattern
}

func (tool grepTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	params, err := decodeGrepParams(args)
	if err != nil {
		return "", err
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	arguments := []string{
		"--line-number",
		"--with-filename",
		"--no-heading",
		"--color=never",
		"--max-columns", fmt.Sprint(maxMatchColumns),
		"--max-columns-preview",
	}
	if params.include != "" {
		arguments = append(arguments, "--glob", params.include)
	}
	arguments = append(arguments, "--", params.pattern, params.path)

	command := exec.CommandContext(ctx, tool.executable, arguments...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("prepare ripgrep output: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("start ripgrep: %w", err)
	}

	lines := make([]string, 0, defaultResultLimit)
	truncated := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if len(lines) == defaultResultLimit {
			truncated = true
			_ = command.Process.Kill()
			break
		}
		lines = append(lines, scanner.Text())
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()

	if err := ctx.Err(); err != nil {
		return "", err
	}
	if scanErr != nil {
		return "", fmt.Errorf("read ripgrep output: %w", scanErr)
	}
	if truncated {
		lines = append(lines, fmt.Sprintf("(Results truncated after %d matching lines. Narrow path, pattern, or include.)", defaultResultLimit))
		return strings.Join(lines, "\n"), nil
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			return "No matches found", nil
		}
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return "", fmt.Errorf("ripgrep: %s: %w", message, waitErr)
		}
		return "", fmt.Errorf("run ripgrep: %w", waitErr)
	}
	if len(lines) == 0 {
		return "No matches found", nil
	}
	return strings.Join(lines, "\n"), nil
}

func decodeGrepParams(args json.RawMessage) (grepParams, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		return grepParams{}, errors.New("grep arguments must be a JSON object")
	}

	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(args))
	if err := decoder.Decode(&raw); err != nil {
		return grepParams{}, fmt.Errorf("decode grep arguments: %w", err)
	}
	if raw == nil {
		return grepParams{}, errors.New("grep arguments must be a JSON object")
	}
	for name := range raw {
		switch name {
		case "pattern", "path", "include":
		default:
			return grepParams{}, fmt.Errorf("grep argument %q is not supported", name)
		}
	}

	pattern, err := utils.ParseRequiredString(raw, "pattern")
	if err != nil {
		return grepParams{}, err
	}

	path, err := optionalString(raw, "path")
	if err != nil {
		return grepParams{}, err
	}
	if path == "" {
		path, err = os.Getwd()
		if err != nil {
			return grepParams{}, fmt.Errorf("get current working directory: %w", err)
		}
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return grepParams{}, errors.New("path must be absolute")
	}

	include, err := optionalString(raw, "include")
	if err != nil {
		return grepParams{}, err
	}

	return grepParams{pattern: pattern, path: path, include: include}, nil
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
