package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUsesEmbeddedDefault(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HOME", t.TempDir())

	config, err := Load()
	if err != nil {
		t.Fatalf("expected embedded default to load, got %v", err)
	}

	if config.UserConfigPath != "~/.harness.json" {
		t.Fatalf("expected default user config path ~/.harness.json, got %q", config.UserConfigPath)
	}
	if config.Logging.Level != "debug" {
		t.Fatalf("expected default log level debug, got %q", config.Logging.Level)
	}
	if config.Logging.Encoding != "json" {
		t.Fatalf("expected default log encoding json, got %q", config.Logging.Encoding)
	}
	if config.Logging.Output != "file" {
		t.Fatalf("expected default output file, got %q", config.Logging.Output)
	}
	if config.Logging.FilePath != "~/.harness/logs/{sessionId}/{sessionId}.log" {
		t.Fatalf("expected default session log file path, got %q", config.Logging.FilePath)
	}
	if len(config.Providers) != 1 {
		t.Fatalf("expected one default provider, got %#v", config.Providers)
	}
	if config.Providers[0].Name != "codex" {
		t.Fatalf("expected default provider codex, got %q", config.Providers[0].Name)
	}
	if len(config.Providers[0].Models) != 1 || config.Providers[0].Models[0].Name != "gpt-5.5" {
		t.Fatalf("expected default codex model gpt-5.5, got %#v", config.Providers[0].Models)
	}
}

func TestConfigEnvNamesAreDerivedFromJSONTags(t *testing.T) {
	tests := []struct {
		goFieldPath []string
		expected    string
	}{
		{[]string{"UserConfigPath"}, "HARNESS_USER_CONFIG_PATH"},
		{[]string{"Logging", "Development"}, "HARNESS_LOGGING_DEVELOPMENT"},
		{[]string{"Logging", "Level"}, "HARNESS_LOGGING_LEVEL"},
		{[]string{"Logging", "Encoding"}, "HARNESS_LOGGING_ENCODING"},
		{[]string{"Logging", "Output"}, "HARNESS_LOGGING_OUTPUT"},
		{[]string{"Logging", "FilePath"}, "HARNESS_LOGGING_FILE_PATH"},
		{[]string{"Logging", "InitialFields"}, "HARNESS_LOGGING_INITIAL_FIELDS"},
		{[]string{"Logging", "DisableCaller"}, "HARNESS_LOGGING_DISABLE_CALLER"},
		{[]string{"Logging", "DisableStacktrace"}, "HARNESS_LOGGING_DISABLE_STACKTRACE"},
		{[]string{"Providers"}, "HARNESS_PROVIDERS"},
	}

	for _, test := range tests {
		envName := configEnv(t, test.goFieldPath...)
		if envName != test.expected {
			t.Fatalf("expected env name %s for %v, got %s", test.expected, test.goFieldPath, envName)
		}
	}
}

func TestLoadReadsProviders(t *testing.T) {
	clearConfigEnv(t)

	configPath := writeConfig(t, t.TempDir(), "harness.json", `{
		"providers": [
			{
				"name": "openai",
				"type": "openai",
				"base_url": "https://api.openai.com/v1",
				"auth_token_env_var": "OPENAI_API_KEY",
				"auth_file": "~/.local/share/opencode/auth.json",
				"auth_provider": "openai",
				"enabled": true,
				"models": [
					{"name": "gpt-4.1"},
					{"name": "gpt-disabled", "enabled": false}
				]
			}
		]
	}`)
	t.Setenv(ConfigPathEnv, configPath)

	config, err := Load()
	if err != nil {
		t.Fatalf("expected config with providers to load, got %v", err)
	}

	if len(config.Providers) != 1 {
		t.Fatalf("expected one provider, got %#v", config.Providers)
	}
	provider := config.Providers[0]
	if provider.Name != "openai" {
		t.Fatalf("expected provider name openai, got %q", provider.Name)
	}
	if provider.Type != "openai" {
		t.Fatalf("expected provider type openai, got %q", provider.Type)
	}
	if provider.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("expected provider base URL, got %q", provider.BaseURL)
	}
	if provider.AuthTokenEnvVar != "OPENAI_API_KEY" {
		t.Fatalf("expected provider auth env var, got %q", provider.AuthTokenEnvVar)
	}
	if provider.AuthFile != "~/.local/share/opencode/auth.json" {
		t.Fatalf("expected provider auth file, got %q", provider.AuthFile)
	}
	if provider.AuthProvider != "openai" {
		t.Fatalf("expected provider auth provider, got %q", provider.AuthProvider)
	}
	if !provider.Enabled {
		t.Fatal("expected provider to be enabled")
	}
	if len(provider.Models) != 2 {
		t.Fatalf("expected two models, got %#v", provider.Models)
	}
	if provider.Models[0].Name != "gpt-4.1" || provider.Models[0].Enabled != nil {
		t.Fatalf("unexpected first model: %#v", provider.Models[0])
	}
	if provider.Models[1].Name != "gpt-disabled" || provider.Models[1].Enabled == nil || *provider.Models[1].Enabled {
		t.Fatalf("unexpected disabled model: %#v", provider.Models[1])
	}
}

func TestLoadUsesEnvPath(t *testing.T) {
	clearConfigEnv(t)

	configPath := writeConfig(t, t.TempDir(), "harness.json", `{
		"logging": {
			"development": true,
			"level": "debug",
			"encoding": "console",
			"output": "stderr"
		}
	}`)
	t.Setenv(ConfigPathEnv, configPath)

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
	clearConfigEnv(t)

	configPath := writeConfig(t, t.TempDir(), "harness.json", `{
		"logging": {
			"level": "info",
			"output": "file",
			"filePath": "/tmp/harness.log"
		}
	}`)
	t.Setenv(ConfigPathEnv, configPath)

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

func TestLoadUsesDefaultUserConfigPath(t *testing.T) {
	clearConfigEnv(t)

	homeDir := t.TempDir()
	writeConfig(t, homeDir, ".harness.json", `{
		"logging": {
			"level": "warn"
		}
	}`)
	t.Setenv("HOME", homeDir)

	config, err := Load()
	if err != nil {
		t.Fatalf("expected default user config to load, got %v", err)
	}

	if config.Logging.Level != "warn" {
		t.Fatalf("expected log level warn, got %q", config.Logging.Level)
	}
	if config.Logging.Encoding != "json" {
		t.Fatalf("expected embedded default encoding to be retained, got %q", config.Logging.Encoding)
	}
}

func TestLoadUsesEnvUserConfigPath(t *testing.T) {
	clearConfigEnv(t)

	configPath := writeConfig(t, t.TempDir(), "custom-harness.json", `{
		"logging": {
			"level": "warn"
		}
	}`)
	t.Setenv(configEnv(t, "UserConfigPath"), configPath)

	config, err := Load()
	if err != nil {
		t.Fatalf("expected env user config path to load, got %v", err)
	}

	if config.UserConfigPath != configPath {
		t.Fatalf("expected env user config path %q, got %q", configPath, config.UserConfigPath)
	}
	if config.Logging.Level != "warn" {
		t.Fatalf("expected log level warn, got %q", config.Logging.Level)
	}
}

func TestLoadEnvPathHasPriorityOverDefaultUserConfigPath(t *testing.T) {
	clearConfigEnv(t)

	homeDir := t.TempDir()
	writeConfig(t, homeDir, ".harness.json", `{
		"logging": {
			"level": "warn"
		}
	}`)
	envConfigPath := writeConfig(t, t.TempDir(), "harness.json", `{
		"logging": {
			"level": "debug"
		}
	}`)
	t.Setenv("HOME", homeDir)
	t.Setenv(ConfigPathEnv, envConfigPath)

	config, err := Load()
	if err != nil {
		t.Fatalf("expected env config to load, got %v", err)
	}

	if config.Logging.Level != "debug" {
		t.Fatalf("expected env log level debug, got %q", config.Logging.Level)
	}
}

func TestLoadEnvValuesOverrideEmbeddedDefault(t *testing.T) {
	clearConfigEnv(t)

	logPath := filepath.Join(t.TempDir(), "harness.log")
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configEnv(t, "UserConfigPath"), filepath.Join(t.TempDir(), "missing-harness.json"))
	t.Setenv(configEnv(t, "Logging", "Development"), "true")
	t.Setenv(configEnv(t, "Logging", "Level"), "debug")
	t.Setenv(configEnv(t, "Logging", "Encoding"), "console")
	t.Setenv(configEnv(t, "Logging", "Output"), "file")
	t.Setenv(configEnv(t, "Logging", "FilePath"), logPath)
	t.Setenv(configEnv(t, "Logging", "InitialFields"), `{"service":"harness","version":1}`)
	t.Setenv(configEnv(t, "Logging", "DisableCaller"), "true")
	t.Setenv(configEnv(t, "Logging", "DisableStacktrace"), "true")

	config, err := Load()
	if err != nil {
		t.Fatalf("expected env overrides to load, got %v", err)
	}

	if !config.Logging.Development {
		t.Fatal("expected env development true")
	}
	if config.Logging.Level != "debug" {
		t.Fatalf("expected env level debug, got %q", config.Logging.Level)
	}
	if config.Logging.Encoding != "console" {
		t.Fatalf("expected env encoding console, got %q", config.Logging.Encoding)
	}
	if config.Logging.Output != "file" {
		t.Fatalf("expected env output file, got %q", config.Logging.Output)
	}
	if config.Logging.FilePath != logPath {
		t.Fatalf("expected env file path %q, got %q", logPath, config.Logging.FilePath)
	}
	if config.Logging.InitialFields["service"] != "harness" {
		t.Fatalf("expected env initial field service harness, got %v", config.Logging.InitialFields["service"])
	}
	if config.Logging.InitialFields["version"] != float64(1) {
		t.Fatalf("expected env initial field version 1, got %v", config.Logging.InitialFields["version"])
	}
	if !config.Logging.DisableCaller {
		t.Fatal("expected env disable caller true")
	}
	if !config.Logging.DisableStacktrace {
		t.Fatal("expected env disable stacktrace true")
	}
}

func TestLoadEnvValuesOverrideConfigFile(t *testing.T) {
	clearConfigEnv(t)

	configPath := writeConfig(t, t.TempDir(), "harness.json", `{
		"logging": {
			"level": "warn",
			"output": "stderr",
			"disableCaller": false
		}
	}`)
	t.Setenv(ConfigPathEnv, configPath)
	t.Setenv(configEnv(t, "Logging", "Level"), "error")
	t.Setenv(configEnv(t, "Logging", "Output"), "stdout")
	t.Setenv(configEnv(t, "Logging", "DisableCaller"), "true")

	config, err := Load()
	if err != nil {
		t.Fatalf("expected env overrides to load, got %v", err)
	}

	if config.Logging.Level != "error" {
		t.Fatalf("expected env log level error, got %q", config.Logging.Level)
	}
	if config.Logging.Output != "stdout" {
		t.Fatalf("expected env output stdout, got %q", config.Logging.Output)
	}
	if !config.Logging.DisableCaller {
		t.Fatal("expected env disableCaller true")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	clearConfigEnv(t)

	configPath := writeConfig(t, t.TempDir(), "harness.json", `{
		"logging": {},
		"unknown": true
	}`)
	t.Setenv(ConfigPathEnv, configPath)

	_, err := Load()
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadRejectsWrongType(t *testing.T) {
	clearConfigEnv(t)

	configPath := writeConfig(t, t.TempDir(), "harness.json", `{
		"logging": {
			"level": 42
		}
	}`)
	t.Setenv(ConfigPathEnv, configPath)

	_, err := Load()
	if err == nil {
		t.Fatal("expected wrong type error")
	}
	if !strings.Contains(err.Error(), `field "logging.level" has invalid type: got number, want string`) {
		t.Fatalf("expected clear wrong type error, got %v", err)
	}
}

func TestLoadRejectsInvalidBoolEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configEnv(t, "Logging", "Development"), "sometimes")

	_, err := Load()
	if err == nil {
		t.Fatal("expected invalid bool env error")
	}
	if !strings.Contains(err.Error(), `parse env HARNESS_LOGGING_DEVELOPMENT: invalid boolean "sometimes"`) {
		t.Fatalf("expected clear bool env error, got %v", err)
	}
}

func TestLoadRejectsInvalidInitialFieldsEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configEnv(t, "Logging", "InitialFields"), `[]`)

	_, err := Load()
	if err == nil {
		t.Fatal("expected invalid initial fields env error")
	}
	if !strings.Contains(err.Error(), `parse env HARNESS_LOGGING_INITIAL_FIELDS: invalid type: got array, want object`) {
		t.Fatalf("expected clear initial fields env error, got %v", err)
	}
}

func TestLoadRejectsEmptyUserConfigPathEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(configEnv(t, "UserConfigPath"), "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected empty user config path env error")
	}
	if !strings.Contains(err.Error(), `parse env HARNESS_USER_CONFIG_PATH: value must not be empty`) {
		t.Fatalf("expected clear user config path env error, got %v", err)
	}
}

func TestLoadReturnsErrorWhenEnvPathIsMissing(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "missing.json")
	t.Setenv(ConfigPathEnv, configPath)

	_, err := Load()
	if err == nil {
		t.Fatal("expected missing env config error")
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Fatalf("expected read config error, got %v", err)
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()

	envNames, err := configValueEnvNames()
	if err != nil {
		t.Fatalf("config value env names: %v", err)
	}
	envNames = append(envNames, ConfigPathEnv)

	for _, envName := range envNames {
		envName := envName
		previous, existed := os.LookupEnv(envName)
		if err := os.Unsetenv(envName); err != nil {
			t.Fatalf("unset %s: %v", envName, err)
		}

		t.Cleanup(func() {
			if existed {
				if err := os.Setenv(envName, previous); err != nil {
					t.Fatalf("restore %s: %v", envName, err)
				}
				return
			}

			if err := os.Unsetenv(envName); err != nil {
				t.Fatalf("restore unset %s: %v", envName, err)
			}
		})
	}
}

func configEnv(t *testing.T, goFieldPath ...string) string {
	t.Helper()

	envName, err := configValueEnvName(goFieldPath...)
	if err != nil {
		t.Fatalf("config env name for %v: %v", goFieldPath, err)
	}

	return envName
}

func writeConfig(t *testing.T, dir string, name string, contents string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}
