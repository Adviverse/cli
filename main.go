package main

import (
	"errors"
	"os"

	"github.com/Adviverse/cli/internal/cmd"
)

func main() {
	if err := cmd.NewRoot().Execute(); err != nil {
		var ec cmd.ExitCodeError
		if errors.As(err, &ec) {
			os.Exit(ec.Code)
		}
		os.Exit(1)
	}
}
