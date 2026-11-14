// Command my is the MemorY command line tool.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/agenticode/my/internal/buildinfo"
	"github.com/agenticode/my/internal/cli"
)

func app() *cli.App {
	return &cli.App{
		Name:     "my",
		Synopsis: "MemorY reads memory error records from a Linux server. It never changes system state.",
		Commands: []*cli.Command{
			{
				Name:    "version",
				Summary: "print version and build information",
				Run: func(_ context.Context, env *cli.Env, _ []string) int {
					fmt.Fprintln(env.Stdout, buildinfo.String())
					return cli.ExitOK
				},
			},
			synthCommand(),
			analyzeCommand(),
		},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app().Run(ctx, cli.OSEnv(), os.Args[1:])
	stop()
	os.Exit(code)
}
