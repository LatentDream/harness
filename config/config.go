package config

import (
	"latentdream/harness/logging"
)

const envPrefix = "HARNESS_"
const ConfigPathEnv = envPrefix + "CONFIG_PATH"


type Config struct {
	UserConfigPath string         `json:"userConfigPath"`
	Logging        logging.Config `json:"logging"`
}
