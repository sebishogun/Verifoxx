// Command verifoxx is the entry point for the Verifoxx policy engine.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/sebishogun/verifoxx/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(app.RunContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
