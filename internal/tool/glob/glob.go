package glob

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
	"path"
	"path/filepath"
	"sort"
	"strings"

	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/model"
)

const (
	defaultResultLimit = 100
	maxPathBytes       = 1024 * 1024
	maxStderrBytes     = 64 * 1024
)

//go:embed glob.txt
var globDescription string

type globTool struct {
	lookPath func(string) (string, error)
}

type globParams struct {
	pattern globPattern
	path    string
}

type globPattern struct {
	segments      []string
	basenameOnly  bool
	originalValue string
}

func New() model.Tool {
	return globTool{lookPath: exec.LookPath}
}

func (globTool) Definition() llm.ToolDefinition {
	additionalProperties := false
	return llm.ToolDefinition{
		Name:        "glob",
		Description: strings.TrimSpace(globDescription),
		Parameters: llm.Schema{
			Type: "object",
			Properties: map[string]llm.Schema{
				"pattern": {
					Type:        "string",
					Description: "The glob pattern to match, such as *.go or internal/**/*.go",
				},
				"path": {
					Type:        "string",
					Description: "The absolute directory path to search (defaults to the current working directory)",
				},
			},
			Required:             []string{"pattern"},
			AdditionalProperties: &additionalProperties,
		},
	}
}

func (globTool) Status(args json.RawMessage) string {
	params, err := decodeGlobParams(args)
	if err != nil {
		return "Finding files"
	}
	return "Finding files matching " + params.pattern.originalValue
}

func (tool globTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	params, err := decodeGlobParams(args)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	info, err := os.Stat(params.path)
	if err != nil {
		return "", fmt.Errorf("inspect glob path %q: %w", params.path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("glob path %q must be a directory", params.path)
	}
	root, err := filepath.EvalSymlinks(params.path)
	if err != nil {
		return "", fmt.Errorf("resolve glob path %q: %w", params.path, err)
	}
	root = filepath.Clean(root)

	executable, backend, arguments, err := tool.command(root)
	if err != nil {
		return "", err
	}

	command := exec.CommandContext(ctx, executable, arguments...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("prepare %s output: %w", backend, err)
	}
	stderr := &limitedBuffer{limit: maxStderrBytes}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("start %s: %w", backend, err)
	}

	matches := make([]string, 0, defaultResultLimit+1)
	scanner := bufio.NewScanner(stdout)
	scanner.Split(splitNUL)
	scanner.Buffer(make([]byte, 4096), maxPathBytes)
	for scanner.Scan() {
		candidate := filepath.Clean(scanner.Text())
		relative, err := filepath.Rel(root, candidate)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if params.pattern.matches(filepath.ToSlash(relative)) {
			matches = insertMatch(matches, candidate, defaultResultLimit+1)
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()

	if err := ctx.Err(); err != nil {
		return "", err
	}
	if scanErr != nil {
		return "", fmt.Errorf("read %s output: %w", backend, scanErr)
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return "", fmt.Errorf("%s: %s: %w", backend, message, waitErr)
		}
		return "", fmt.Errorf("run %s: %w", backend, waitErr)
	}
	if len(matches) == 0 {
		return "No files found", nil
	}
	if len(matches) > defaultResultLimit {
		matches = matches[:defaultResultLimit]
		matches = append(matches, fmt.Sprintf("(Results truncated after %d files. Narrow path or pattern.)", defaultResultLimit))
	}
	return strings.Join(matches, "\n"), nil
}

func (tool globTool) command(root string) (string, string, []string, error) {
	lookPath := tool.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	fd, err := lookPath("fd")
	if err == nil {
		return fd, "fd", []string{
			"--type", "file",
			"--absolute-path",
			"--print0",
			"--color", "never",
			"--search-path", root,
		}, nil
	}
	if !errors.Is(err, exec.ErrNotFound) {
		return "", "", nil, fmt.Errorf("locate fd: %w", err)
	}

	find, err := lookPath("find")
	if err != nil {
		return "", "", nil, fmt.Errorf("locate find after fd was unavailable: %w", err)
	}
	return find, "find", []string{
		root,
		"-mindepth", "1",
		"-name", ".*", "-prune",
		"-o", "-type", "f", "-print0",
	}, nil
}

func decodeGlobParams(args json.RawMessage) (globParams, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		return globParams{}, errors.New("glob arguments must be a JSON object")
	}

	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(args))
	if err := decoder.Decode(&raw); err != nil {
		return globParams{}, fmt.Errorf("decode glob arguments: %w", err)
	}
	if raw == nil {
		return globParams{}, errors.New("glob arguments must be a JSON object")
	}
	for name := range raw {
		switch name {
		case "pattern", "path":
		default:
			return globParams{}, fmt.Errorf("glob argument %q is not supported", name)
		}
	}

	value, err := requiredString(raw, "pattern")
	if err != nil {
		return globParams{}, err
	}
	pattern, err := parseGlobPattern(value)
	if err != nil {
		return globParams{}, err
	}

	root, err := optionalString(raw, "path")
	if err != nil {
		return globParams{}, err
	}
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return globParams{}, fmt.Errorf("get current working directory: %w", err)
		}
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return globParams{}, errors.New("path must be absolute")
	}

	return globParams{pattern: pattern, path: root}, nil
}

func parseGlobPattern(value string) (globPattern, error) {
	normalized := filepath.ToSlash(value)
	if filepath.IsAbs(value) || path.IsAbs(normalized) {
		return globPattern{}, errors.New("pattern must be relative")
	}
	segments := strings.Split(normalized, "/")
	for _, segment := range segments {
		if segment == "" {
			return globPattern{}, errors.New("pattern must not contain empty path segments")
		}
		if segment == "**" {
			continue
		}
		if _, err := path.Match(segment, ""); err != nil {
			return globPattern{}, fmt.Errorf("invalid glob pattern: %w", err)
		}
	}
	return globPattern{
		segments:      segments,
		basenameOnly:  len(segments) == 1,
		originalValue: value,
	}, nil
}

func (pattern globPattern) matches(relative string) bool {
	parts := strings.Split(relative, "/")
	if pattern.basenameOnly {
		matched, _ := path.Match(pattern.segments[0], parts[len(parts)-1])
		return matched
	}

	type state struct {
		pattern int
		path    int
	}
	memo := make(map[state]bool)
	visited := make(map[state]bool)
	var match func(int, int) bool
	match = func(patternIndex int, pathIndex int) bool {
		current := state{pattern: patternIndex, path: pathIndex}
		if visited[current] {
			return memo[current]
		}
		visited[current] = true

		var matched bool
		switch {
		case patternIndex == len(pattern.segments):
			matched = pathIndex == len(parts)
		case pattern.segments[patternIndex] == "**":
			matched = match(patternIndex+1, pathIndex) || pathIndex < len(parts) && match(patternIndex, pathIndex+1)
		case pathIndex < len(parts):
			segmentMatched, _ := path.Match(pattern.segments[patternIndex], parts[pathIndex])
			matched = segmentMatched && match(patternIndex+1, pathIndex+1)
		}
		memo[current] = matched
		return matched
	}
	return match(0, 0)
}

func insertMatch(matches []string, candidate string, limit int) []string {
	index := sort.SearchStrings(matches, candidate)
	if index < len(matches) && matches[index] == candidate {
		return matches
	}
	if len(matches) == limit && index == limit {
		return matches
	}

	matches = append(matches, "")
	copy(matches[index+1:], matches[index:])
	matches[index] = candidate
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func splitNUL(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexByte(data, 0); index >= 0 {
		return index + 1, data[:index], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining > 0 {
		if remaining > len(value) {
			remaining = len(value)
		}
		_, _ = buffer.buffer.Write(value[:remaining])
	}
	if remaining < len(value) {
		buffer.truncated = true
	}
	return written, nil
}

func (buffer *limitedBuffer) String() string {
	value := buffer.buffer.String()
	if buffer.truncated {
		value += " (stderr truncated)"
	}
	return value
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
	if strings.TrimSpace(parsed) == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	return parsed, nil
}

func requiredString(raw map[string]json.RawMessage, name string) (string, error) {
	value, ok := raw[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	var parsed string
	if err := json.Unmarshal(value, &parsed); err != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	if strings.TrimSpace(parsed) == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	return parsed, nil
}
