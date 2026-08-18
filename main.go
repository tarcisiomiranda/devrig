package main

import (
	"os"

	"github.com/tarcisiomiranda/devrig/cmd"
)

// version is overridden at link time: -ldflags "-X main.version=..."
var version = "0.1.1"

func main() {
	cmd.SetVersion(version)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
