package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"latentdream/harness/logging"
)

const EnvPath = "HARNESS_CONFIG_PATH"

//go:embed default.json
var defaultConfigJSON []byte

type Config struct {
	Logging logging.Config `json:"logging"`
}

func Load() (Config, error) {
	configPath := os.Getenv(EnvPath)
	if configPath == "" {
		return decode("embedded default", defaultConfigJSON)
	}

	expandedPath, err := expandPath(configPath)
	if err != nil {
		return Config{}, err
	}

	contents, err := os.ReadFile(expandedPath)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", expandedPath, err)
	}

	return decode(expandedPath, contents)
}

func decode(source string, contents []byte) (Config, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()

	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config %s: %w", source, err)
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Config{}, fmt.Errorf("decode config %s: multiple JSON values", source)
	}

	return config, nil
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
