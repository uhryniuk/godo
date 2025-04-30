package main

// import "fmt"

import (
	"log/slog"
	"os"
  "github.com/uhryniuk/godo/cmd"

	// "github.com/spf13/cobra"
)

func main() {
  logFile, err := os.OpenFile("godo.jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

  if err != nil {
    panic(err)
  }
  defer logFile.Close()

  jsonFileHandler := slog.NewJSONHandler(logFile, nil)

  logger := slog.New(jsonFileHandler)
  logger.Info("User logged in", slog.String("user", "alice"))
  cmd.Execute()
}


