package config

import (
	"log"
	"os"
	"path/filepath"

	"github.com/uhryniuk/godo/internal/utils"
)

const (
	CONFIG_DIR  = ".godo"
	CONFIG_FILE = "config.toml"
	JOB_DIR     = ".hash"
	SERVICE_DIR = "services"
	STATE_DIR   = "state"
	SOCKET_FILE = "godo.sock"
)

// NOTE CliConfig is a skeleton struct for later work.
type CliConfig struct{}

// InitConfig is an idempotent scaffolding function to ensure that a config
// exists on the user's system, then use those values to configure the CLI.
func InitConfig() *CliConfig {

	// Ensure all config paths exist
	dirs := []string{GetConfigPath(), GetJobDir(), GetServiceDir(), GetStateDir()}
	for _, dir := range dirs {
		if !utils.FileExists(dir) {
			os.MkdirAll(dir, 0755)
		}
	}

	// Touch the config file so subsequent reads find it.
	file, err := os.OpenFile(GetConfigFile(), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	// TODO UnMarshall into the config.

	return &CliConfig{}
}

func GetConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	return filepath.Join(home, CONFIG_DIR)
}

func GetConfigFile() string {
	return filepath.Join(GetConfigPath(), CONFIG_FILE)
}

func GetJobDir() string {
	return filepath.Join(GetConfigPath(), JOB_DIR)
}

func GetServiceDir() string {
	return filepath.Join(GetConfigPath(), SERVICE_DIR)
}

func GetStateDir() string {
	return filepath.Join(GetConfigPath(), STATE_DIR)
}

func GetLogDir(jobID string) string {
	return filepath.Join(GetStateDir(), jobID)
}

func GetSocketPath() string {
	return filepath.Join(GetStateDir(), SOCKET_FILE)
}
