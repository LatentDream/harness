package edit

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

const defaultExpectedReplacements = 1

//go:embed edit.txt
var editDescription string

type editTool struct{}

type editParams struct {
	filePath             string
	oldString            string
	newString            string
	expectedReplacements int
}

func New() model.Tool {
	return editTool{}
}

func (editTool) Definition() llm.ToolDefinition {
	additionalProperties := false
	return llm.ToolDefinition{
		Name:        "edit",
		Description: strings.TrimSpace(editDescription),
		Parameters: llm.Schema{
			Type: "object",
			Properties: map[string]llm.Schema{
				"filePath": {
					Type:        "string",
					Description: "The absolute path to the existing file to edit",
				},
				"oldString": {
					Type:        "string",
					Description: "The exact text to replace. Must match the current file contents exactly.",
				},
				"newString": {
					Type:        "string",
					Description: "The replacement text. May be empty.",
				},
				"expectedReplacements": {
					Type:        "number",
					Description: "Expected number of replacements. Defaults to 1 and must be a positive integer.",
				},
			},
			Required:             []string{"filePath", "oldString", "newString"},
			AdditionalProperties: &additionalProperties,
		},
	}
}

func (editTool) Capability() model.Capability {
	return model.CapabilityMutating
}

func (editTool) Status(args json.RawMessage) string {
	params, err := decodeEditParams(args)
	if err != nil {
		return "Editing file"
	}
	return "Editing file " + params.filePath
}

func (editTool) Present(args json.RawMessage, _ string, _ error) model.Activity {
	params, err := decodeEditParams(args)
	if err != nil {
		return model.Activity{}
	}
	return model.Activity{Target: params.filePath}
}

func (editTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	params, err := decodeEditParams(args)
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
		return "", fmt.Errorf("stat file %q: %w", params.filePath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("filePath must be a regular file")
	}

	contents, err := os.ReadFile(params.filePath)
	if err != nil {
		return "", fmt.Errorf("read file %q: %w", params.filePath, err)
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	current := string(contents)
	matches := strings.Count(current, params.oldString)
	if matches != params.expectedReplacements {
		return "", fmt.Errorf("oldString matched %d time(s), expected %d", matches, params.expectedReplacements)
	}

	updated := strings.Replace(current, params.oldString, params.newString, params.expectedReplacements)
	if err := atomicWriteFile(params.filePath, []byte(updated), info.Mode().Perm()); err != nil {
		return "", err
	}

	return fmt.Sprintf("Edited file successfully: %d replacement(s).", matches), nil
}

func decodeEditParams(args json.RawMessage) (editParams, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		return editParams{}, errors.New("edit arguments must be a JSON object")
	}

	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return editParams{}, fmt.Errorf("decode edit arguments: %w", err)
	}
	if raw == nil {
		return editParams{}, errors.New("edit arguments must be a JSON object")
	}
	for name := range raw {
		switch name {
		case "filePath", "oldString", "newString", "expectedReplacements":
		default:
			return editParams{}, fmt.Errorf("edit argument %q is not supported", name)
		}
	}

	filePath, err := utils.ParseRequiredString(raw, "filePath")
	if err != nil {
		return editParams{}, err
	}
	filePath = filepath.Clean(filePath)
	if !filepath.IsAbs(filePath) {
		return editParams{}, errors.New("filePath must be absolute")
	}

	oldString, err := parseRequiredEditString(raw, "oldString", false)
	if err != nil {
		return editParams{}, err
	}
	newString, err := parseRequiredEditString(raw, "newString", true)
	if err != nil {
		return editParams{}, err
	}

	expectedReplacements := defaultExpectedReplacements
	parsedExpected, ok, err := utils.ParseOptionalPositiveInt(raw, "expectedReplacements")
	if err != nil {
		return editParams{}, err
	}
	if ok {
		expectedReplacements = parsedExpected
	}

	return editParams{
		filePath:             filePath,
		oldString:            oldString,
		newString:            newString,
		expectedReplacements: expectedReplacements,
	}, nil
}

func parseRequiredEditString(raw map[string]json.RawMessage, name string, allowEmpty bool) (string, error) {
	value, ok := raw[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return "", fmt.Errorf("%s must be a string", name)
	}

	var parsed string
	if err := json.Unmarshal(value, &parsed); err != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	if !allowEmpty && parsed == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}

	return parsed, nil
}

func atomicWriteFile(path string, contents []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", path, err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set temporary file permissions for %q: %w", path, err)
	}
	if _, err := temp.Write(contents); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary file for %q: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace file %q: %w", path, err)
	}

	return nil
}
