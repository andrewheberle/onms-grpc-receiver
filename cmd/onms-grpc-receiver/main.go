package main

import (
	"fmt"
	"os"

	"github.com/andrewheberle/onms-grpc-receiver/pkg/cmd"
)

func main() {
	if err := cmd.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error during execution: %s\n", err)
		os.Exit(1)
	}
}
