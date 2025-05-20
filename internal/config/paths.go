package config

import (
	"log"
	"os"
  "path/filepath"
)

const (
  CONFIG_DIR = ".godo"
  CONFIG_FILE = "config.json"
  JOB_DIR = ".hash"
)

// NOTE CliConfig is a skeleton struct for later work.
type CliConfig struct {}

// InitConfig is an idempotent scaffolding function to ensure that a config
// exists on the user's system, then use those values to configure the CLI.
func InitConfig() *CliConfig {
  
  // TODO
  // 1. Assert config directories exist.
  // 2. Assert files exist.
  // 3. Read file contents and UnMarshall to CliConfig

  return &CliConfig {}
}

func GetConfigPath() string {
  home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
  return filepath.Join(home, CONFIG_DIR)
}

func GetConfigFile() string {
  return filepath.Join(GetConfigPath(), JOB_DIR)
}

func GetJobDir() string {
  return filepath.Join(GetConfigPath(), JOB_DIR)
}
