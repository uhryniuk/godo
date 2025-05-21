package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/uhryniuk/godo/internal/utils"
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
  
  // Ensure all config paths exist
  dirs := []string{GetConfigPath(), GetJobDir()}
  for _, dir := range dirs {
    if !utils.FileExists(dir) {
      os.Mkdir(dir, 0644)
    }
  }

  // Read the config file if exists, otherwise creating it
  file, err := os.OpenFile(GetConfigFile(), os.O_RDWR|os.O_CREATE, 0644)
  if err != nil {
    log.Fatal(err)
  } else {
    fmt.Println("successfully read config")
  }
  defer file.Close()

  // TODO UnMarshall into the config.

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
