package write

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

//go:embed write.txt
var writeDescription string

type writeTool struct{}

type writeParams struct {
	filePath string
	content  string
}

func New() model.Tool {
	return writeTool{}
}

func (writeTool) Definition() llm.ToolDefinition {
	additionalProperties := false
	return llm.ToolDefinition{
		Name:        "write",
		Description: strings.TrimSpace(writeDescription),
		Parameters: llm.Schema{
			Type: "object",
			Properties: map[string]llm.Schema{
				"filePath": {
					Type:        "string",
					Description: "The absolute path to the file to write",
				},
				"content": {
					Type:        "string",
					Description: "The content to write to the file",
				},
			},
			Required:             []string{"filePath", "content"},
			AdditionalProperties: &additionalProperties,
		},
	}
}

func (writeTool) Capability() model.Capability {
	return model.CapabilityMutating
}

func (writeTool) Status(args json.RawMessage) string {
	params, err := decodeWriteParams(args)
	if err != nil {
		return "Writing file"
	}
	return "Writing file " + params.filePath
}

func (writeTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	params, err := decodeWriteParams(args)
	if err != nil {
		return "", err
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	if err := os.MkdirAll(filepath.Dir(params.filePath), 0o755); err != nil {
		return "", fmt.Errorf("create parent directories for %q: %w", params.filePath, err)
	}
	if err := os.WriteFile(params.filePath, []byte(params.content), 0o644); err != nil {
		return "", fmt.Errorf("write file %q: %w", params.filePath, err)
	}

	return "Wrote file successfully.", nil
}

func decodeWriteParams(args json.RawMessage) (writeParams, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		return writeParams{}, errors.New("write arguments must be a JSON object")
	}

	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return writeParams{}, fmt.Errorf("decode write arguments: %w", err)
	}
	if raw == nil {
		return writeParams{}, errors.New("write arguments must be a JSON object")
	}
	for name := range raw {
		switch name {
		case "filePath", "content":
		default:
			return writeParams{}, fmt.Errorf("write argument %q is not supported", name)
		}
	}

	filePath, err := utils.ParseRequiredString(raw, "filePath")
	if err != nil {
		return writeParams{}, err
	}
	filePath = filepath.Clean(filePath)
	if !filepath.IsAbs(filePath) {
		return writeParams{}, errors.New("filePath must be absolute")
	}

	content, err := parseRequiredContent(raw)
	if err != nil {
		return writeParams{}, err
	}

	return writeParams{filePath: filePath, content: content}, nil
}

func parseRequiredContent(raw map[string]json.RawMessage) (string, error) {
	value, ok := raw["content"]
	if !ok {
		return "", errors.New("content is required")
	}
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return "", errors.New("content must be a string")
	}

	var parsed string
	if err := json.Unmarshal(value, &parsed); err != nil {
		return "", errors.New("content must be a string")
	}

	return parsed, nil
}
