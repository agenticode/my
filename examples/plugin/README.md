# Example vendor analyzer

`analyzer.py` is a vendor analyzer for MemorY: a program that gets the error records of each DIMM on its standard input and answers with an analysis on its standard output. [docs/plugin-contract.md](../../docs/plugin-contract.md) is the reference for the contract.

```
my open ../demo.mycase dimm CPU0_C1 --plugin ./analyzer.py
my report ../demo.mycase -o fa.html --audience fa --plugin ./analyzer.py
```

It needs Python 3 and nothing else.

The analyzer knows no real device. Its three answers are there to show what the contract carries, and its rules are made up:

| Answer | What the example does | What it changes |
| --- | --- | --- |
| `Level2` | counts the rows and the data lines with errors in the records it got | a block in the report for failure analysis (`--audience fa`) |
| `AdjacentRows` | names rows 2k and 2k+1 a pair, and gives a row no other neighbour | rows `0x1a2f` and `0x1a30` of the demo DIMM are neighbours by their numbers and one fault; with the example they are in different pairs and two faults |
| `Baseline` | a percentile from a table of three steps | the line "vendor reference for the part number" on the finding |

| File | Content |
| --- | --- |
| `analyzer.py` | the analyzer |
| `request.json` | a request as the tool sends it: the two DIMMs of the demo host that have errors, with the newest record of each and two before it (`--plugin-logs 2`) |
| `response.json` | what `analyzer.py` answers to that request |

Both JSON files are written by a test (`cmd/my`, `TestExampleRequestAndResponse`), which fails when they no longer match what the tool sends and the analyzer answers. `make golden` writes them anew.

To try an analyzer by hand:

```
./analyzer.py < request.json
```
