package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/endigma/tescoctl/internal/cmd"
)

func main() {
	// Cancel in-flight requests on Ctrl-C rather than leaving a socket open.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := cmd.New().Run(ctx, os.Args); err != nil {
		// Command bodies render their own errors; anything reaching here is a
		// usage or parse failure that urfave has not already printed.
		if msg := err.Error(); msg != "" {
			fmt.Fprintln(os.Stderr, "grosh:", msg)
		}
		os.Exit(1)
	}
}
