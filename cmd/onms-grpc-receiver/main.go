package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/andrewheberle/onms-grpc-receiver/internal/cmd"
)

func main() {
	// shut down gracefully on SIGINT or SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.Execute(ctx, os.Args[1:]); err != nil {
		stop()
		fmt.Fprintf(os.Stderr, "error during execution: %s\n", err)
		os.Exit(1)
	}
}
