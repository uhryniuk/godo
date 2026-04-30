package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all jobs",
	Long:  "TODO",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(cmd, args)
	},
}
