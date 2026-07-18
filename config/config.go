package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"latentdream/harness/logging"
)

const ConfigPathEnv = "HARNESS_CONFIG_PATH"

//go:embed default.json
var defaultConfigJSON []byte

type Config struct {
	UserConfigPath string         `json:"userConfigPath"`
	Logging        logging.Config `json:"logging"`
}

func Load() (Config, error) {
	defaultConfig, err := decode("embedded default", defaultConfigJSON, Config{})
	if err != nil {
		return Config{}, err
	}

	if configPath := os.Getenv(ConfigPathEnv); configPath != "" {
		return loadFile(configPath, defaultConfig)
	}

	contents, expandedPath, err := readConfigFile(defaultConfig.UserConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig, nil
	}
	if err != nil {
		return Config{}, err
	}

	return decode(expandedPath, contents, defaultConfig)
}

func loadFile(configPath string, base Config) (Config, error) {
	contents, expandedPath, err := readConfigFile(configPath)
	if err != nil {
		return Config{}, err
	}

	return decode(expandedPath, contents, base)
}

func readConfigFile(configPath string) ([]byte, string, error) {
	expandedPath, err := expandPath(configPath)
	if err != nil {
		return nil, "", err
	}

	contents, err := os.ReadFile(expandedPath)
	if err != nil {
		return nil, expandedPath, fmt.Errorf("read config %q: %w", expandedPath, err)
	}

	return contents, expandedPath, nil
}

func decode(source string, contents []byte, base Config) (Config, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()

	config := base
	if err := decoder.Decode(&config); err != nil {
		return Config{}, formatDecodeError(source, err)
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Config{}, fmt.Errorf("decode config %s: multiple JSON values", source)
	}

	if err := validate(config); err != nil {
		return Config{}, fmt.Errorf("validate config %s: %w", source, err)
	}

	return config, nil
}

func validate(config Config) error {
	if config.UserConfigPath == "" {
		return errors.New("userConfigPath must not be empty")
	}

	return nil
}

func formatDecodeError(source string, err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Errorf(
			"decode config %s: field %q has invalid type: got %s, want %s",
			source,
			fieldName(typeErr),
			typeErr.Value,
			expectedJSONType(typeErr.Type),
		)
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("decode config %s: invalid JSON at byte %d: %w", source, syntaxErr.Offset, err)
	}

	if errors.Is(err, io.EOF) {
		return fmt.Errorf("decode config %s: empty JSON", source)
	}

	const unknownFieldPrefix = "json: unknown field "
	if strings.HasPrefix(err.Error(), unknownFieldPrefix) {
		return fmt.Errorf("decode config %s: unknown field %s", source, strings.TrimPrefix(err.Error(), unknownFieldPrefix))
	}

	return fmt.Errorf("decode config %s: %w", source, err)
}

func fieldName(typeErr *json.UnmarshalTypeError) string {
	if typeErr.Field != "" {
		return typeErr.Field
	}

	return fmt.Sprintf("byte offset %d", typeErr.Offset)
}

func expectedJSONType(configType reflect.Type) string {
	if configType == nil {
		return "valid JSON"
	}

	switch configType.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.Interface:
		return "any JSON value"
	default:
		return configType.String()
	}
}

func expandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}

		if path == "~" {
			return homeDir, nil
		}

		return filepath.Join(homeDir, strings.TrimPrefix(path, "~/")), nil
	}

	return path, nil
}
