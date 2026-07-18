package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUsesEmbeddedDefault(t *testing.T) {
	t.Setenv(EnvPath, "")

	config, err := Load()
	if err != nil {
		t.Fatalf("expected embedded default to load, got %v", err)
	}

	if config.Logging.Level != "info" {
		t.Fatalf("expected default log level info, got %q", config.Logging.Level)
	}
	if config.Logging.Encoding != "json" {
		t.Fatalf("expected default log encoding json, got %q", config.Logging.Encoding)
	}
	if config.Logging.Output != "stdout" {
		t.Fatalf("expected default output stdout, got %q", config.Logging.Output)
	}
}

func TestLoadUsesEnvPath(t *testing.T) {
	configPath := writeConfig(t, t.TempDir(), "harness.json", `{
		"logging": {
			"development": true,
			"level": "debug",
			"encoding": "console",
			"output": "stderr"
		}
	}`)
	t.Setenv(EnvPath, configPath)

	config, err := Load()
	if err != nil {
		t.Fatalf("expected env config to load, got %v", err)
	}

	if !config.Logging.Development {
		t.Fatal("expected development logging to be true")
	}
	if config.Logging.Level != "debug" {
		t.Fatalf("expected log level debug, got %q", config.Logging.Level)
	}
	if config.Logging.Encoding != "console" {
		t.Fatalf("expected log encoding console, got %q", config.Logging.Encoding)
	}
	if config.Logging.Output != "stderr" {
		t.Fatalf("expected output stderr, got %q", config.Logging.Output)
	}
}

func TestLoadReadsFileLoggingOutput(t *testing.T) {
	configPath := writeConfig(t, t.TempDir(), "harness.json", `{
		"logging": {
			"level": "info",
			"output": "file",
			"filePath": "/tmp/harness.log"
		}
	}`)
	t.Setenv(EnvPath, configPath)

	config, err := Load()
	if err != nil {
		t.Fatalf("expected env config to load, got %v", err)
	}

	if config.Logging.Output != "file" {
		t.Fatalf("expected output file, got %q", config.Logging.Output)
	}
	if config.Logging.FilePath != "/tmp/harness.log" {
		t.Fatalf("expected filePath /tmp/harness.log, got %q", config.Logging.FilePath)
	}
}

func TestLoadExpandsHomePath(t *testing.T) {
	homeDir := t.TempDir()
	writeConfig(t, homeDir, ".harness.json", `{
		"logging": {
			"level": "warn"
		}
	}`)
	t.Setenv("HOME", homeDir)
	t.Setenv(EnvPath, "~/.harness.json")

	config, err := Load()
	if err != nil {
		t.Fatalf("expected home-relative config to load, got %v", err)
	}

	if config.Logging.Level != "warn" {
		t.Fatalf("expected log level warn, got %q", config.Logging.Level)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	configPath := writeConfig(t, t.TempDir(), "harness.json", `{
		"logging": {},
		"unknown": true
	}`)
	t.Setenv(EnvPath, configPath)

	_, err := Load()
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func writeConfig(t *testing.T, dir string, name string, contents string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}
