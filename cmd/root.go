package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	// "github.com/uhryniuk/godo/internal/config"
	// "log/slog"
)	

var rootCmd = &cobra.Command{
  Use:   "godo",
  Short: "Godo is a better way to manage jobs from the command-line",
  Long: `An alternative to 'job' that improves the ergnomics of 
    creating and managing jobs from the command-line.`,
  Run: func(cmd *cobra.Command, args []string) {
    // Do Stuff Here
    // cli := config.InitConfig()
    fmt.Println("This is the root command")
    fmt.Println("Args", args)
    fmt.Println(cmd.Flags().GetBool("stdout"))
    fmt.Println(cmd.Flags().GetBool("stderr"))

    // TODO run strings.Fields(arg) for each arg in args
    // This will split them on their white space
    // Otherwise the command, "la -la", will register as a base command.

    for _, v := range args {
      fmt.Println(v)
    }

    if len(args) == 0 {
      fmt.Println("No command provided")
      os.Exit(1)
    }

    baseCommand := args[0]
    arguments := []string {}

    if len(args) > 1 {
      arguments = args[1:]
    }

    fmt.Println(len(args))
    command := exec.Command(baseCommand, arguments...)
    // FIXME Dirty combine the environments
    command.Env = append(command.Env, os.Environ()...)

    // Create a file, then just pass that to the Stdout reference.
    f, err := os.OpenFile("some-file", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
    if err != nil {
      panic(err)
    }
    defer f.Close()

    // Create the buffers to capture all of the logs.
    var stdout, stderr bytes.Buffer
    command.Stdout = &stdout
    command.Stderr = &stderr

    fmt.Println(command.Env)
    
    exitCode := 0
    if err := command.Run(); err != nil {
      if exitError, ok := err.(*exec.ExitError); ok {
        exitCode = exitError.ExitCode()
      } else {
        fmt.Println("boof failed to run the command")
        panic(err)
      }
    }
    fmt.Println(exitCode)
    // cmd := exec.Command("sleep", "60")
    // cmd.Stdout = nil
    // cmd.Stderr = nil
    // cmd.Stdin = nil
    // cmd.SysProcAttr = &syscall.SysProcAttr{
    //     Setsid: true,
    // }
    // cmd.Start()
  },
}

func Execute() {
  rootCmd.Flags().Bool("stdout", true, "Redirect stdout from child processes to this process")
  rootCmd.Flags().Bool("stderr", true, "Redirect stderr from child processes to this process")
  rootCmd.AddCommand(supervisorCmd, listCmd)
  if err := rootCmd.Execute(); err != nil {
    fmt.Println(err)
    os.Exit(1)
  }
}
