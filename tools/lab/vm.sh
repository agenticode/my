#!/bin/bash
# vm.sh STEP
#
# The injections that run on the lab host itself, where no machine check bank
# and no memory controller is visible: the kernel is told directly that a page
# is bad. Each step leaves a capture under $LAB/cap (see capture.sh).
#
#   baseline   capture without injecting anything
#   hwpoison   four pages taken offline hard through madvise(MADV_HWPOISON)
#   soft       two pages taken offline soft through madvise(MADV_SOFT_OFFLINE), and one
#              by the corrected error collector after it was told of as many errors on
#              that page as its threshold asks for; a second page stays below the threshold
#
# Needs root, CONFIG_MEMORY_FAILURE, and for the collector CONFIG_RAS_CEC_DEBUG.
set -eu
LAB=${LAB:-/root/my-lab}
HERE=$(cd "$(dirname "$0")" && pwd)
STEP=${1:?usage: vm.sh baseline|hwpoison|soft}
WORK=$LAB/tmp
mkdir -p "$WORK" "$LAB/cap"

versions() {
	echo "--set kernel=$(uname -r)"
	if command -v rasdaemon >/dev/null; then
		echo "--set rasdaemon=$(rasdaemon --version | awk '{print $2; exit}')"
	fi
}

finish() { # NAME SUMMARY METHOD [capture options]
	local name=$1 summary=$2 method=$3
	shift 3
	sleep 3 # rasdaemon writes a record when it has read it from the trace buffer
	# shellcheck disable=SC2046
	python3 "$HERE/plan.py" --scenario "$name" --summary "$summary" --method "$method" $(versions) <"$WORK/injected.jsonl" >"$WORK/plan.yaml"
	"$HERE/capture.sh" "$name" "$@"
	cp "$WORK/plan.yaml" "$LAB/cap/$name/plan.yaml"
	cp "$WORK/injected.jsonl" "$LAB/cap/$name/extra/injected.jsonl"
}

case "$STEP" in
baseline)
	: >"$WORK/injected.jsonl"
	finish baseline "no injection: the host as it is after a boot" "none"
	;;

hwpoison)
	"$LAB/bin/hwpoison" -d "$WORK" -t anon:2 mlocked:1 file:1 | tee "$WORK/injected.jsonl"
	finish hwpoison "four pages of a test process taken offline hard" "madvise(MADV_HWPOISON)" --formats
	;;

soft)
	CEC=/sys/kernel/debug/ras/cec
	"$LAB/bin/hwpoison" -d "$WORK" soft:2 | tee "$WORK/injected.jsonl"
	if [ -w "$CEC/pfn" ]; then
		threshold=$(cat "$CEC/action_threshold")
		below=5
		# Two pages in use by a process that waits: one gets the full count, one stays below it.
		coproc HOLD { "$LAB/bin/hwpoison" hold:2; }
		# bash forgets the descriptors of a coprocess when it ends: keep copies.
		exec {hold_out}<&"${HOLD[0]}" {hold_in}>&"${HOLD[1]}"
		hold_pid=$HOLD_PID
		read -r first <&"$hold_out"
		read -r second <&"$hold_out"
		pfn1=$(sed 's/.*"pfn":"\(0x[0-9a-f]*\)".*/\1/' <<<"$first")
		pfn2=$(sed 's/.*"pfn":"\(0x[0-9a-f]*\)".*/\1/' <<<"$second")
		when=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
		for _ in $(seq 1 "$threshold"); do echo "$pfn1" >"$CEC/pfn"; done
		for _ in $(seq 1 "$below"); do echo "$pfn2" >"$CEC/pfn"; done
		sleep 2 # the collector hands the page to a worker
		echo >&"$hold_in"
		read -r held1 <&"$hold_out"
		read -r held2 <&"$hold_out"
		wait "$hold_pid" 2>/dev/null || true
		{
			printf '{"id":"cec-1","time":"%s","inject":"page-offline","how":"soft","count":1,"address":"%s000","note":"%s corrected errors reported to the corrected error collector for this page through debugfs: its threshold"}\n' "$when" "$pfn1" "$threshold"
			printf '{"id":"cec-below","time":"%s","inject":"page-offline","how":"soft","count":0,"address":"%s000","note":"%s corrected errors reported for this page, below the threshold of %s: the page stays in use"}\n' "$when" "$pfn2" "$below" "$threshold"
		} >>"$WORK/injected.jsonl"
		printf '%s\n%s\n' "$held1" "$held2" >"$WORK/held.jsonl"
	else
		echo "vm.sh: $CEC/pfn is not writable: the collector part is left out" >&2
	fi
	finish soft-offline "pages taken offline soft: two through madvise, one by the corrected error collector" \
		"madvise(MADV_SOFT_OFFLINE); debugfs ras/cec/pfn"
	[ -f "$WORK/held.jsonl" ] && cp "$WORK/held.jsonl" "$LAB/cap/soft-offline/extra/held.jsonl"
	;;

*)
	echo "usage: vm.sh baseline|hwpoison|soft" >&2
	exit 2
	;;
esac
