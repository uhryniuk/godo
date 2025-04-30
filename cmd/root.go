package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	// "log/slog"
)	

var rootCmd = &cobra.Command{
  Use:   "godo",
  Short: "Godo is a better way to manage jobs from the command-line",
  Long: `An alternative to 'job' that improves the ergnomics of 
    creating and managing jobs from the command-line.`,
  Run: func(cmd *cobra.Command, args []string) {
    // Do Stuff Here
    fmt.Println("This is the root command")
    command := exec.Command("ls", "-la")

    // Create a file, then just pass that to the Stdout reference.
    f, err := os.OpenFile("some-file", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
    if err != nil {
      panic(err)
    }
    defer f.Close()

    // Create the buffers to capture all of the logs.
    var stdout, stderr bytes.Buffer
    command.Stdout = f
    command.Stderr = &stderr
    
    exitCode := 0
    if err := command.Run(); err != nil {
      if exitError, ok := err.(*exec.ExitError); ok {
        exitCode = exitError.ExitCode()
      } else {
        fmt.Println("boof failed to run the command")
        panic(err)
      }
      // fmt.Println("WOAH")
      // panic(err)
    }
    fmt.Println(stdout.String())
    fmt.Println("-------------")
    fmt.Println(stderr.String())
    fmt.Println(exitCode)
  },
}

func Execute() {
  if err := rootCmd.Execute(); err != nil {
    fmt.Println(err)
    os.Exit(1)
  }
}
