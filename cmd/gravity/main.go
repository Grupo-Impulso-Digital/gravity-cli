// Command gravity is the CI/pipeline companion for the Gravity docs platform.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/impulso/gravity-cli/internal/cli"
)

// version is set via -ldflags "-X main.version=..." at build time.
var version = "0.1.0-dev"

func main() {
	os.Exit(run())
}

func run() int {
	cli.SetVersion(version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return cli.Execute(ctx)
}
