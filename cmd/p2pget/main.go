package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/qiuxsgit/p2pget/internal/cli"
	"github.com/qiuxsgit/p2pget/internal/progress"
)

func main() {
	progress.ConfigureConsole()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(code)
}
