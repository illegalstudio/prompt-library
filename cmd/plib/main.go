package main

import (
	"fmt"
	"os"

	"github.com/illegalstudio/prompt-library/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "plib:", err)
		os.Exit(1)
	}
}
