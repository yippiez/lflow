package cmd

import (
	"fmt"
	"os"
)

func rootCmd() {
	fmt.Printf(`Dnote server - a simple command line notebook

Usage:
  lflow-server [command] [flags]

Available commands:
  start: Start the server (use 'lflow-server start --help' for flags)
  user: Manage users (use 'lflow-server user' for subcommands)
  version: Print the version
`)
}

// Execute is the main entry point for the CLI
func Execute() {
	if len(os.Args) < 2 {
		rootCmd()
		return
	}

	cmd := os.Args[1]

	switch cmd {
	case "start":
		startCmd(os.Args[2:])
	case "user":
		userCmd(os.Args[2:])
	case "version":
		versionCmd()
	default:
		fmt.Printf("Unknown command %s\n", cmd)
		rootCmd()
		os.Exit(1)
	}
}
