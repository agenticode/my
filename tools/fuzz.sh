#!/usr/bin/env bash
# Runs fuzz targets one after the other and stops at the first that fails.
# "make fuzz" calls it; docs/fuzzing.md says what the targets are.
#
#   tools/fuzz.sh <fuzztime> <minimize> <workers> <package:function>...
#
# <fuzztime> is what go test takes as -fuzztime: a duration ("10s", "10m")
# or a number of calls ("20000x"). <minimize> is -fuzzminimizetime.
#
# go test can end a run that is limited by a duration with the one line
# "context deadline exceeded" and no failing input: the time ran out while
# a worker was at work on an input. That is no finding. Such a run is made
# once more. A finding comes with a failing input, which go test writes to
# testdata/fuzz and names in its output; a second run would fail on it at
# once. A run limited by a number of calls has no deadline to hit.
set -u -o pipefail

if [ $# -lt 4 ]; then
	echo "usage: tools/fuzz.sh <fuzztime> <minimize> <workers> <package:function>..." >&2
	exit 64
fi
fuzztime=$1 minimize=$2 workers=$3
shift 3
go=${GO:-go}
log=$(mktemp)
trap 'rm -f "$log"' EXIT

for target in "$@"; do
	pkg=${target%%:*} fn=${target##*:}
	for try in 1 2; do
		echo "fuzz $fn ($fuzztime)"
		if "$go" test -run '^$' -fuzz "^$fn\$" -fuzztime "$fuzztime" -fuzzminimizetime "$minimize" -parallel "$workers" "$pkg" 2>&1 | tee "$log"; then
			break
		fi
		if [ "$try" = 1 ] && grep -q '^    context deadline exceeded$' "$log" && ! grep -q 'Failing input written to' "$log"; then
			echo "fuzz $fn: go test ended the run at its time limit without a failing input; running it once more"
			continue
		fi
		exit 1
	done
done
