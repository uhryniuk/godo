package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var supervisorCmd = &cobra.Command{
  Use: "supervisor",
  Hidden: true,
  Run: func(cmd *cobra.Command, args []string) {
    fmt.Println(cmd, args)
  },
}
