package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/config"
)

var (
	forceTemplate  bool
	noEditTemplate bool
)

var templateCmd = &cobra.Command{
	Use:   "template <name>",
	Short: "Scaffold a new service file in ~/.godo/services/ and open it in $EDITOR",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		cfg := config.InitConfig()
		path := filepath.Join(config.GetServiceDir(), name+".toml")

		if !forceTemplate {
			if _, err := os.Stat(path); err == nil {
				fmt.Fprintf(os.Stderr, "godo: %s already exists (use --force to overwrite)\n", path)
				os.Exit(1)
			}
		}

		if err := os.WriteFile(path, renderTemplate(name), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "godo:", err)
			os.Exit(1)
		}

		if !noEditTemplate {
			if err := launchEditor(cfg, path); err != nil {
				fmt.Fprintln(os.Stderr, "godo: editor exited with error:", err)
				// Don't bail — the file was written; user can edit manually.
			}
		}

		fmt.Printf("wrote %s\n", path)
		fmt.Println("run 'godo reload' to register it")
	},
}

func init() {
	templateCmd.Flags().BoolVarP(&forceTemplate, "force", "f", false, "overwrite an existing service file")
	templateCmd.Flags().BoolVar(&noEditTemplate, "no-edit", false, "skip launching the editor after writing the file")
}

const templateBody = `# godo service file — edit, then activate with: godo reload

# Display name; defaults to the filename without .toml.
# name = "<NAME>"

# REQUIRED. The executable to run.
command = "echo"

# Arguments passed to the command.
args = ["hello"]

# Working directory. Defaults to the daemon's cwd.
# working_dir = "/path/to/cwd"

# Environment variables.
# [env]
# PORT = "8080"
# LOG_LEVEL = "info"

# Start automatically when the daemon boots.
# autostart = false

# Restart policy: "no" (default), "on-failure", "always".
# restart = "on-failure"

# POSIX nice value (-20 highest priority .. 19 lowest). Default 0.
# nice = 0

# Cron scheduling.
# [cron]
# schedule = "*/5 * * * *"   # 5-field cron, or @hourly, @daily, @every 1m
# overlap = false             # if true, allow concurrent runs
`

func renderTemplate(name string) []byte {
	return []byte(strings.ReplaceAll(templateBody, "<NAME>", name))
}

func launchEditor(cfg *config.CliConfig, path string) error {
	parts := strings.Fields(cfg.ResolveEditor())
	if len(parts) == 0 {
		return fmt.Errorf("editor command is empty")
	}
	editorArgs := append(parts[1:], path)
	c := exec.Command(parts[0], editorArgs...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
