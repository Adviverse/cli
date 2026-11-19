package main

import (
	"os"

	"github.com/Adviverse/cli/internal/cmd"
)

func main() {
	if err := cmd.NewRoot().Execute(); err != nil {
		os.Exit(1)
	}
}
