package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"asteria/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.New(os.Stdin, os.Stdout, os.Stderr).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
