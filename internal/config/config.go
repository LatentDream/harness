package config

import (
	"latentdream/harness/internal/logging"
)

const envPrefix = "HARNESS_"
const ConfigPathEnv = envPrefix + "CONFIG_PATH"

type Config struct {
	UserConfigPath string         `json:"userConfigPath"`
	Logging        logging.Config `json:"logging"`
	Providers      []Provider     `json:"providers"`
}

type Provider struct {
	Name            string          `json:"name"`
	Type            string          `json:"type"`
	BaseURL         string          `json:"base_url"`
	AuthTokenEnvVar string          `json:"auth_token_env_var"`
	Models          []ProviderModel `json:"models"`
	Enabled         bool            `json:"enabled"`
}

type ProviderModel struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled"`
}
