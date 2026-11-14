package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/dcbrain"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/eval"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// evalCommand measures the analysis engine on the dcbrain DRAM error
// dataset. It is a development command and is left out of the command
// list: it needs a copy of that dataset, which is not part of this
// repository.
func evalCommand() *cli.Command {
	return &cli.Command{
		Name:    "eval",
		Summary: "measure the analysis engine on the dcbrain DRAM error dataset",
		Hidden:  true,
		Run:     runEval,
	}
}

const evalUsage = `Usage: my eval -data dir|zip [-format text|md|json] [-config file] [-set key=value]
               [-cutoffs days] [-horizon days] [-split day] [-leads times] [-degrade B|C]
               [-server sid] [-spool dir] [-scratch dir] [-workers n] [-max-records n] [-quiet]
       my eval -data dir|zip -extract sid -ledger file
       my eval -generate dir [-seed n] [-servers n]

Replays the DRAM error dataset of github.com/alibaba-edu/dcbrain (directory dramdata) through the
analysis engine: for every server the engine sees the errors up to a cut-off day and its finding is
compared with the failure tickets of the days after it. -data names the directory that holds
mcelog, inventory and trouble_tickets (as .csv or .tar.gz), or the zip archive of the dataset.

-extract imports one server into a ledger, for a look at it with "my analyze".
-generate writes a made-up fleet in the format of the dataset, to try the command without it.

`

func runEval(ctx context.Context, env *cli.Env, args []string) int {
	fl := flag.NewFlagSet("my eval", flag.ContinueOnError)
	fl.SetOutput(env.Stderr)
	data := fl.String("data", "", "the dataset: a `directory` with the three tables, or the zip archive")
	format := fl.String("format", eval.FormatText, "output `format`: text, md or json")
	cutoffs := fl.String("cutoffs", "", "cut-off `days` of the dataset calendar, separated by commas (default 60,90,120,150,180,210)")
	horizon := fl.Int("horizon", eval.DefaultHorizonDays, "length of the outcome window after a cut-off in `days`")
	split := fl.Int("split", eval.DefaultSplitDay, "cut-offs from this `day` on are the held-out period")
	leads := fl.String("leads", "", "`times` before a failure at which the finding is taken, separated by commas (default 1s,5m,1h,24h,168h,720h)")
	degrade := fl.String("degrade", "", "remove location detail to observation `grade` B or C before the engine sees the records")
	spool := fl.String("spool", "", "keep the error table, split by server, in this `directory` and use it again on the next run")
	scratch := fl.String("scratch", "", "`directory` for temporary files (default: the system's)")
	workers := fl.Int("workers", 0, "servers analysed at a time (default 2)")
	maxRecords := fl.Int("max-records", eval.DefaultMaxRecords, "largest number of records of one server that is analysed")
	quiet := fl.Bool("quiet", false, "do not print progress to standard error")
	extract := fl.String("extract", "", "import the server with this `sid` into the ledger given with -ledger")
	ledger := fl.String("ledger", "", "ledger `file` for -extract")
	generate := fl.String("generate", "", "write a made-up fleet in the format of the dataset into this `directory`")
	seed := fl.Uint64("seed", 1, "seed of the made-up fleet")
	servers := fl.Int("servers", 300, "servers with errors in the made-up fleet")
	var only multiFlag
	fl.Var(&only, "server", "analyse only the server with this `sid`; may be given several times")
	var cf engine.ConfigFlags
	cf.Register(fl)
	fl.Usage = func() {
		fmt.Fprint(env.Stderr, evalUsage)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cli.ExitOK
		}
		return cli.ExitUsage
	}
	usage := func(format string, a ...any) int {
		fmt.Fprintf(env.Stderr, "my eval: "+format+"\n", a...)
		return cli.ExitUsage
	}
	fail := func(err error) int {
		fmt.Fprintf(env.Stderr, "my eval: %v\n", err)
		return cli.ExitFailure
	}
	if fl.NArg() > 0 {
		return usage("unexpected argument %q", fl.Arg(0))
	}

	if *generate != "" {
		if *data != "" || *extract != "" {
			return usage("-generate goes with neither -data nor -extract")
		}
		if *servers < 1 || *servers > 1_000_000 {
			return usage("-servers %d: want 1 to 1000000", *servers)
		}
		g := eval.Generate(eval.GenOptions{Seed: *seed, Servers: *servers})
		if err := g.WriteDir(*generate); err != nil {
			return fail(err)
		}
		fmt.Fprintf(env.Stdout, "wrote %d rows of %d servers and %d tickets to %s\n", len(g.Rows), len(g.Servers), len(g.Tickets), *generate)
		return cli.ExitOK
	}
	if *data == "" {
		return usage("give the dataset with -data, or -generate a made-up one")
	}
	ds, err := dcbrain.Open(*data)
	if err != nil {
		return fail(err)
	}

	if *extract != "" {
		if *ledger == "" {
			return usage("-extract needs the ledger to write, given with -ledger")
		}
		return extractServer(ctx, env, ds, *extract, *ledger)
	}
	if *ledger != "" {
		return usage("-ledger goes with -extract")
	}

	cfg, err := cf.Config()
	if err != nil {
		return usage("%v", err)
	}
	opts := eval.Options{Config: &cfg, HorizonDays: *horizon, SplitDay: *split, Workers: *workers, MaxRecords: *maxRecords,
		Spool: *spool, Scratch: *scratch, Servers: only}
	if *horizon < 1 {
		return usage("-horizon %d: want at least 1 day", *horizon)
	}
	if *split < 1 {
		return usage("-split %d: want a day of the dataset calendar", *split)
	}
	if *maxRecords < 1 {
		return usage("-max-records %d: want at least 1", *maxRecords)
	}
	if *cutoffs != "" {
		for _, s := range strings.Split(*cutoffs, ",") {
			d, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				return usage("-cutoffs %s: %q is not a day number", *cutoffs, s)
			}
			opts.Cutoffs = append(opts.Cutoffs, d)
		}
	}
	if *leads != "" {
		opts.Leads = []time.Duration{}
		for _, s := range strings.Split(*leads, ",") {
			d, err := time.ParseDuration(strings.TrimSpace(s))
			if err != nil {
				return usage("-leads %s: %q is not a duration such as 5m or 24h", *leads, s)
			}
			opts.Leads = append(opts.Leads, d)
		}
	}
	switch strings.ToUpper(*degrade) {
	case "":
	case "B":
		opts.Degrade = model.GradeB
	case "C":
		opts.Degrade = model.GradeC
	default:
		return usage("-degrade %s: want B or C", *degrade)
	}
	switch *format {
	case eval.FormatText, eval.FormatMarkdown, eval.FormatJSON:
	default:
		return usage("-format %s: want text, md or json", *format)
	}
	if !*quiet {
		opts.Progress = func(format string, a ...any) {
			fmt.Fprintf(env.Stderr, "my eval: "+format+"\n", a...)
		}
	}
	rep, err := eval.Run(ctx, ds, opts)
	if err != nil {
		// Settings the harness refuses are usage errors like the ones above.
		if strings.HasPrefix(err.Error(), "eval: cut-off") || strings.HasPrefix(err.Error(), "eval: lead time") {
			return usage("%v", strings.TrimPrefix(err.Error(), "eval: "))
		}
		return fail(err)
	}
	if err := rep.Write(env.Stdout, *format); err != nil {
		return fail(err)
	}
	return cli.ExitOK
}

// extractServer imports one server of the dataset into a ledger.
func extractServer(ctx context.Context, env *cli.Env, ds *dcbrain.Dataset, sid, ledger string) int {
	fail := func(err error) int {
		fmt.Fprintf(env.Stderr, "my eval: %v\n", err)
		return cli.ExitFailure
	}
	s, err := store.Open(ctx, ledger, store.Create)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	res, err := ingest.Run(ctx, s, []adapter.Adapter{dcbrain.NewServer(ds, sid, dcbrain.Options{})}, ingest.Options{})
	if err != nil {
		return fail(err)
	}
	for _, src := range res.Sources {
		fmt.Fprintf(env.Stdout, "%s %s: %s, %d rows, %d records", src.Kind, filepath.Base(src.Origin), src.Status, src.Records, src.Events)
		if src.Detail != "" {
			fmt.Fprintf(env.Stdout, " (%s)", src.Detail)
		}
		fmt.Fprintln(env.Stdout)
		for _, n := range src.Notes {
			fmt.Fprintf(env.Stdout, "  %s\n", n)
		}
	}
	if res.Events == 0 {
		return fail(fmt.Errorf("no record of server %s was added to %s", sid, ledger))
	}
	fmt.Fprintf(env.Stdout, "ledger %s holds server %s; look at it with: my analyze -ledger %s -as-of <date>\n", ledger, sid, ledger)
	return cli.ExitOK
}

// multiFlag collects the values of a flag that may be given several times.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
