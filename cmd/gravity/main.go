// Command gravity is the CI/pipeline companion for the Gravity docs platform.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/cli"
)

// version is set via -ldflags "-X main.version=..." at build time.
var version = "0.1.0-dev"

func main() {
	cli.SetVersion(version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Execute(ctx))
}
