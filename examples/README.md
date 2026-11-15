# Example case

A case of a synthetic host, and what `my report` makes of it. The host, `r12-n034`, has 16 DIMMs. One of them, in slot `CPU0_C1`, has a row fault that grew over seven weeks, came back after a hard PPR and ended in an uncorrected error; another, in `CPU1_D1`, has one cell that fails now and then.

| File | What it is |
| --- | --- |
| `demo.mycase` | the case bundle, with `CPU0_C1` as its subject |
| `demo-report.html` | the report of the case; download it and open it in a browser |
| `figures/*.svg` | the figures of the report as files of their own |
| `plugin/` | an example vendor analyzer, with a request and its response ([plugin/README.md](plugin/README.md)) |

Look at the case with the commands of the tool:

```
my open examples/demo.mycase
my open examples/demo.mycase dimm CPU0_C1
```

The files are made by the tool. `make examples` writes the report and the figures again from the case, and a test fails when they differ from what the command writes:

```
my report examples/demo.mycase -o examples/demo-report.html --figures examples/figures
```

The case itself comes from the synthetic generator (`make examples-case`):

```
my synth -scenario demo -ledger demo.db
my open demo.db bundle CPU0_C1 -o examples/demo.mycase
```

No real host and no real DIMM is behind these records: the names, serial numbers and part numbers are made up.
