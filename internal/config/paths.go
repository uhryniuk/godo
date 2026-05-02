package config

import (
	"log"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

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

// CliConfig is the parsed contents of ~/.godo/config.toml. Fields are
// optional; missing keys keep their zero value.
type CliConfig struct {
	// Editor is the command to launch when an interactive edit is needed
	// (e.g. `godo template`). Overridden by $EDITOR at resolution time.
	Editor string `toml:"editor"`
}

// InitConfig is an idempotent scaffolding function: it ensures every
// directory under ~/.godo exists, touches the config file, and parses
// any user values from it.
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
	file.Close()

	var cfg CliConfig
	if _, err := toml.DecodeFile(GetConfigFile(), &cfg); err != nil {
		log.Fatalf("godo: parse %s: %v", GetConfigFile(), err)
	}
	return &cfg
}

// ResolveEditor returns the editor command to launch for interactive
// editing. Precedence: $EDITOR env var > config.Editor > "vim".
func (c *CliConfig) ResolveEditor() string {
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	if c != nil && c.Editor != "" {
		return c.Editor
	}
	return "vim"
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
