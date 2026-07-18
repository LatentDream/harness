package read

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/model"
	"latentdream/harness/internal/tool/utils"
)

const defaultReadLimit = 2000

//go:embed read.txt
var readDescription string

type readTool struct{}

type readParams struct {
	filePath string
	offset   int
	limit    int
}

func New() model.Tool {
	return readTool{}
}

func (readTool) Definition() llm.ToolDefinition {
	additionalProperties := false
	return llm.ToolDefinition{
		Name:        "read",
		Description: strings.TrimSpace(readDescription),
		Parameters: llm.Schema{
			Type: "object",
			Properties: map[string]llm.Schema{
				"filePath": {
					Type:        "string",
					Description: "The absolute path to the file or directory to read",
				},
				"offset": {
					Type:        "number",
					Description: "The line number to start reading from (1-indexed)",
				},
				"limit": {
					Type:        "number",
					Description: "The maximum number of lines to read (defaults to 2000)",
				},
			},
			Required:             []string{"filePath"},
			AdditionalProperties: &additionalProperties,
		},
	}
}

func (readTool) Status(args json.RawMessage) string {
	params, err := decodeReadParams(args)
	if err != nil {
		return "Reading file"
	}
	return "Reading file " + params.filePath
}

func (readTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	params, err := decodeReadParams(args)
	if err != nil {
		return "", err
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	info, err := os.Stat(params.filePath)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", params.filePath, err)
	}
	if info.IsDir() {
		return readDirectory(params.filePath)
	}

	return readFile(params.filePath, params.offset, params.limit)
}

func decodeReadParams(args json.RawMessage) (readParams, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		return readParams{}, errors.New("read arguments must be a JSON object")
	}

	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return readParams{}, fmt.Errorf("decode read arguments: %w", err)
	}
	if raw == nil {
		return readParams{}, errors.New("read arguments must be a JSON object")
	}
	for name := range raw {
		switch name {
		case "filePath", "offset", "limit":
		default:
			return readParams{}, fmt.Errorf("read argument %q is not supported", name)
		}
	}

	filePath, err := utils.ParseRequiredString(raw, "filePath")
	if err != nil {
		return readParams{}, err
	}
	filePath = filepath.Clean(filePath)
	if !filepath.IsAbs(filePath) {
		return readParams{}, errors.New("filePath must be absolute")
	}

	offset, ok, err := utils.ParseOptionalPositiveInt(raw, "offset")
	if err != nil {
		return readParams{}, err
	}
	if !ok {
		offset = 1
	}

	limit, ok, err := utils.ParseOptionalPositiveInt(raw, "limit")
	if err != nil {
		return readParams{}, err
	}
	if !ok {
		limit = defaultReadLimit
	}

	return readParams{filePath: filePath, offset: offset, limit: limit}, nil
}

func readDirectory(path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("read directory %q: %w", path, err)
	}

	var output strings.Builder
	fmt.Fprintf(&output, "<path>%s</path>\n", path)
	output.WriteString("<type>directory</type>\n")
	output.WriteString("<entries>\n")
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		output.WriteString(name)
		output.WriteByte('\n')
	}
	output.WriteString("</entries>")
	return output.String(), nil
}

func readFile(path string, offset int, limit int) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file %q: %w", path, err)
	}

	lines := splitLines(contents)
	start := offset - 1
	if start > len(lines) {
		start = len(lines)
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	var output strings.Builder
	fmt.Fprintf(&output, "<path>%s</path>\n", path)
	output.WriteString("<type>file</type>\n")
	output.WriteString("<content>\n")
	for index := start; index < end; index++ {
		fmt.Fprintf(&output, "%d: %s\n", index+1, lines[index])
	}
	output.WriteByte('\n')
	if end < len(lines) {
		fmt.Fprintf(&output, "(Showing lines %d-%d of %d total lines. Use offset %d to read more.)\n", offset, end, len(lines), end+1)
	} else {
		fmt.Fprintf(&output, "(End of file - total %d lines)\n", len(lines))
	}
	output.WriteString("</content>")
	return output.String(), nil
}

func splitLines(contents []byte) []string {
	if len(contents) == 0 {
		return nil
	}

	text := strings.ReplaceAll(string(contents), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for index := range lines {
		lines[index] = strings.TrimSuffix(lines[index], "\r")
	}
	return lines
}
