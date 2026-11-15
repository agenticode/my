package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"

	"github.com/agenticode/my/internal/buildinfo"
	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/store"
)

// versionInfo is what "my version --json" prints: what the binary was
// built from, and the versions of what it reads and writes. A report of a
// problem can quote it as it is.
type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	// Date is the time of the commit the binary was built from.
	Date string `json:"date,omitzero"`
	Go   string `json:"go"`
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// EngineVersion changes when the engine can give another finding for
	// the same ledger.
	EngineVersion string `json:"engine_version"`
	// LedgerSchema is the schema version of the ledgers this binary
	// writes; BundleFormat the format version of its case bundles;
	// JSONVersion the version of the --json output of the commands.
	LedgerSchema int `json:"ledger_schema"`
	BundleFormat int `json:"bundle_format"`
	JSONVersion  int `json:"json_version"`
}

// runVersion is "my version": one line that says what this binary is, or
// the same and more as JSON.
func runVersion(_ context.Context, env *cli.Env, args []string) int {
	fs := flag.NewFlagSet("my version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "")
	lang := cli.Lang(env)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if err == flag.ErrHelp {
			fmt.Fprintln(env.Stdout, say(lang, "help.version"))
			return cli.ExitOK
		}
		fmt.Fprintf(env.Stderr, "my version: %s\n", say(lang, "err.version"))
		return cli.ExitUsage
	}
	if !*asJSON {
		fmt.Fprintln(env.Stdout, buildinfo.String())
		return cli.ExitOK
	}
	enc := json.NewEncoder(env.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(versionInfo{
		Version: buildinfo.Version, Commit: buildinfo.Revision(), Date: buildinfo.Date,
		Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		EngineVersion: engine.Version, LedgerSchema: store.SchemaVersion(), BundleFormat: bundle.Version, JSONVersion: jsonVersion,
	}); err != nil {
		fmt.Fprintf(env.Stderr, "my version: %v\n", err)
		return cli.ExitFailure
	}
	return cli.ExitOK
}
