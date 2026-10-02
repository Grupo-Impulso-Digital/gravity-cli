// Command gravity is the CI/pipeline companion for the Gravity docs platform.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return cli.Execute(ctx)
}
