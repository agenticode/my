package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
)

// synthCommand generates synthetic memory error histories. It is hidden
// from the command list: it exists for tests, demos and development.
func synthCommand() *cli.Command {
	return &cli.Command{
		Name:    "synth",
		Summary: "generate a synthetic memory error history",
		Hidden:  true,
		Run:     runSynth,
	}
}

func runSynth(ctx context.Context, env *cli.Env, args []string) int {
	fl := flag.NewFlagSet("my synth", flag.ContinueOnError)
	fl.SetOutput(env.Stderr)
	scenario := fl.String("scenario", "demo", "scenario `name`")
	list := fl.Bool("list", false, "list the scenario names and exit")
	seed := fl.Uint64("seed", 1, "seed; the same seed gives the same data")
	out := fl.String("out", "", "write the files of a host with this history below `dir`")
	flavor := fl.String("flavor", string(synth.FlavorIntel), "platform the host tree imitates: intel, amd or ghes")
	ledger := fl.String("ledger", "", "write a ledger holding the history to `file`")
	fl.Usage = func() {
		fmt.Fprintf(env.Stderr, "Usage: my synth [-scenario name] [-seed n] [-out dir [-flavor f]] [-ledger file]\n\n"+
			"Generates a synthetic host with memory faults. Nothing here comes from real hardware.\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cli.ExitOK
		}
		return cli.ExitUsage
	}
	if fl.NArg() > 0 {
		fmt.Fprintf(env.Stderr, "my synth: unexpected argument %q\n", fl.Arg(0))
		return cli.ExitUsage
	}
	if *list {
		for _, n := range synth.Names() {
			fmt.Fprintln(env.Stdout, n)
		}
		return cli.ExitOK
	}
	if *out == "" && *ledger == "" {
		fmt.Fprintln(env.Stderr, "my synth: give -out, -ledger or both")
		return cli.ExitUsage
	}
	sc, err := synth.Named(*scenario, *seed)
	if err != nil {
		fmt.Fprintf(env.Stderr, "my synth: %v\n", err)
		return cli.ExitUsage
	}
	// Never write into something that is already there.
	if *out != "" {
		if entries, err := os.ReadDir(*out); err == nil && len(entries) > 0 {
			fmt.Fprintf(env.Stderr, "my synth: %s exists and is not empty\n", *out)
			return cli.ExitUsage
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(env.Stderr, "my synth: %v\n", err)
			return cli.ExitFailure
		}
	}
	if *ledger != "" {
		if _, err := os.Stat(*ledger); err == nil {
			fmt.Fprintf(env.Stderr, "my synth: %s exists\n", *ledger)
			return cli.ExitUsage
		}
	}

	ds, err := synth.Generate(sc)
	if err != nil {
		fmt.Fprintf(env.Stderr, "my synth: %v\n", err)
		return cli.ExitFailure
	}
	fmt.Fprintf(env.Stdout, "scenario %s, seed %d: host %s, %d DIMMs, %d boots, %d events, %d actions\n",
		sc.Name, sc.Seed, sc.Host.Name, len(ds.Modules), len(ds.Boots), len(ds.Events), len(ds.Actions))
	for _, t := range ds.Truth {
		fmt.Fprintf(env.Stdout, "  fault %d: %s in %s (serial %s), %d corrected, %d uncorrected\n", t.Fault, t.Mode, t.Slot, t.Serial, t.CE, t.UE)
	}

	if *out != "" {
		if err := ds.WriteHostTree(*out, synth.Flavor(strings.ToLower(*flavor))); err != nil {
			fmt.Fprintf(env.Stderr, "my synth: %v\n", err)
			return cli.ExitFailure
		}
		fmt.Fprintf(env.Stdout, "host tree (%s) written below %s\n", *flavor, *out)
	}
	if *ledger != "" {
		s, err := store.Open(ctx, *ledger, store.Create)
		if err != nil {
			fmt.Fprintf(env.Stderr, "my synth: %v\n", err)
			return cli.ExitFailure
		}
		res, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: sc.End,
			Note: fmt.Sprintf("synthetic scenario %s, seed %d", sc.Name, sc.Seed)})
		cerr := s.Close()
		if err == nil {
			err = cerr
		}
		if err != nil {
			fmt.Fprintf(env.Stderr, "my synth: %v\n", err)
			return cli.ExitFailure
		}
		fmt.Fprintf(env.Stdout, "ledger written to %s: %d events, %d actions\n", *ledger, res.Events, res.Actions)
	}
	return cli.ExitOK
}
