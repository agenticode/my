#!/bin/bash
# mce.sh [print-all]
#
# Machine checks in a nested guest. The lab host has no machine check banks,
# so the errors are injected one level down: QEMU emulates a Xeon (Skylake-SP,
# family 6 model 85, ten machine check banks) without hardware virtualization,
# the guest runs the kernel of the lab host on a copy-on-write view of its
# file system (virtme-ng), and the mce-inject module hands the kernel machine
# check records as if it had read them from the banks.
#
#   mce.sh              the kernel as it boots by default
#   mce.sh print-all    the same with mce=print_all, which makes the kernel print
#                       every machine check record it logs
#   mce.sh clock-step   as print-all, and the clock is set back by 10 seconds after
#                       rasdaemon started. NTP does that at boot on a host whose
#                       hardware clock runs ahead (the lab host: 6.7 and 10.5 seconds
#                       on two boots); rasdaemon keeps the offset it measured when it
#                       started, so its times stay ahead of the kernel's
#   mce.sh fatal        one uncorrected error with the processor context corrupt: the
#                       kernel panics. Only the console of the guest tells; it is
#                       kept as $LAB/cap/mce-fatal/console.log
#
# What is injected, at addresses of pages a waiting process holds:
#
#   ce-1  1 corrected read error, bank 7, channel 0
#   ce-2  1 corrected read error, bank 7, channel 1
#   ce-3  2 corrected read errors on one page, bank 8: the corrected error collector
#         of the kernel takes a page offline at the second error on Intel
#   ce-4  3 corrected read errors on one page, bank 7: offline at the second
#   ue-1  1 uncorrected error found by the patrol scrubber, no action required (UCNA);
#         the kernel takes the page offline all the same
#   ue-2  1 uncorrected error found by the patrol scrubber, action optional (SRAO),
#         raised as a machine check exception
#   ue-3  1 uncorrected error on a data load, action required (SRAR), bank 1,
#         raised as a machine check exception in user context
#
# The capture is $LAB/cap/mce or $LAB/cap/mce-print-all.
set -eu
LAB=${LAB:-/root/my-lab}
HERE=$(cd "$(dirname "$0")" && pwd)

if [ "${1:-}" != "--inside" ]; then
	name=mce
	append=()
	case "${1:-}" in
	"") ;;
	print-all)
		name=mce-print-all
		append=(--append "mce=print_all")
		;;
	clock-step)
		name=mce-clock-step
		append=(--append "mce=print_all")
		;;
	fatal) name=mce-fatal ;;
	*)
		echo "usage: mce.sh [print-all|clock-step|fatal]" >&2
		exit 2
		;;
	esac
	mkdir -p "$LAB/tmp" "$LAB/cap"
	rm -rf "$LAB/cap/$name"
	# The console of the guest is kept: when the guest dies of a machine
	# check, the console is the only place that says so.
	vng --verbose --run "/boot/vmlinuz-$(uname -r)" --disable-kvm --cpus 2 --memory 4G --user root --name lab-guest \
		--rwdir="$LAB" "${append[@]}" --qemu-opts="-cpu Skylake-Server" \
		--exec "$HERE/mce.sh --inside $name" >"$LAB/tmp/$name-console.log" 2>&1 || true
	if [ "$name" = mce-fatal ]; then
		# The guest is gone. What is left is what a serial console server or
		# the console log of a BMC would hold: the kernel's lines.
		mkdir -p "$LAB/cap/$name/extra"
		grep -a -E '^\[ *[0-9]+\.[0-9]{6}\] ' "$LAB/tmp/$name-console.log" | tr -d '\r' >"$LAB/cap/$name/console.log"
		cp "$LAB/tmp/plan.yaml" "$LAB/cap/$name/plan.yaml"
		cp "$LAB/tmp/injected.jsonl" "$LAB/cap/$name/extra/"
		cp "$LAB/tmp/$name.env" "$LAB/cap/$name/capture.env"
		echo "captured: $LAB/cap/$name"
	elif [ -d "$LAB/cap/$name" ]; then
		echo "captured: $LAB/cap/$name"
	else
		echo "mce.sh: no capture; see $LAB/tmp/$name-console.log and $LAB/tmp/$name.log" >&2
		exit 1
	fi
	exit 0
fi

# From here on: inside the guest.
name=$2
WORK=$LAB/tmp
exec 2>"$WORK/$name.log"
set -x
modprobe mce-inject

# rasdaemon with a database of its own: the guest sees the host's file
# system, the database of the host included.
rm -f /var/lib/rasdaemon/ras-mc_event.db
rasdaemon -f -r >"$WORK/$name-rasdaemon.log" 2>&1 &
for _ in $(seq 1 30); do
	grep -q "Listening to events" "$WORK/$name-rasdaemon.log" && break
	sleep 1
done
sleep 2

if [ "$name" = mce-clock-step ]; then
	# What NTP does at boot when the hardware clock runs ahead.
	date -u -s "@$(($(date +%s) - 10))" >/dev/null
	echo "clock set back by 10 seconds at $(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$WORK/clock-step.txt"
	sleep 1
fi

: >"$WORK/injected.jsonl"
now() { date -u +%Y-%m-%dT%H:%M:%S.%3NZ; }
plan() { # SUMMARY: writes the plan from what was injected
	python3 "$HERE/plan.py" --scenario "$name" --summary "$1" \
		--method "mce-inject module, records handed to the machine check poller and exception handler" \
		--set "kernel=$(uname -r)" --set "rasdaemon=$(rasdaemon --version | awk '{print $2; exit}')" \
		--set "cpu=Skylake-SP (family 6 model 85) emulated by QEMU without hardware virtualization" \
		<"$WORK/injected.jsonl" >"$WORK/plan.yaml"
}

# inject ID BANK STATUS ADDR COUNT [extra lines of the mce-inject file...]
inject() {
	local id=$1 bank=$2 status=$3 addr=$4 count=$5
	shift 5
	{
		echo "CPU 0 BANK $bank"
		echo "STATUS $status"
		echo "MISC 0x86"
		echo "ADDR $addr"
		for line in "$@"; do echo "$line"; done
	} >"$WORK/$id.inj"
	for _ in $(seq 1 "$count"); do
		mce-inject "$WORK/$id.inj" || echo "mce.sh: mce-inject failed for $id" >&2
		sleep 1.2
	done
}

if [ "$name" = mce-fatal ]; then
	# Uncorrected, processor context corrupt: nothing can be recovered.
	{
		echo "captured_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
		echo "kernel=$(uname -r)"
		echo "arch=$(uname -m)"
	} >"$WORK/$name.env"
	t=$(now)
	printf '{"id":"fatal-1","time":"%s","inject":"error","severity":"Fatal","count":1,"address":"0x12345040","socket":0,"channel":0,"note":"machine check exception, bank 7, status 0xbe00000000010090 (uncorrected memory read error, processor context corrupt); the kernel panics"}\n' "$t" >"$WORK/injected.jsonl"
	plan "a fatal machine check in a nested guest: the kernel panics"
	sync
	inject fatal-1 7 0xbe00000000010090 0x12345040 1 "MCGSTATUS MCIP"
	sleep 5
	echo "mce.sh: the guest survived a fatal machine check" >&2
	exit 1
fi

coproc HOLD { "$LAB/bin/hwpoison" hold:7; }
# bash forgets the descriptors of a coprocess when it ends: keep copies.
exec {hold_out}<&"${HOLD[0]}" {hold_in}>&"${HOLD[1]}"
hold_pid=$HOLD_PID
pfn=()
for i in 1 2 3 4 5 6 7; do
	read -r line <&"$hold_out"
	pfn[i]=$(sed 's/.*"pfn":"\(0x[0-9a-f]*\)".*/\1/' <<<"$line")
done
addr() { printf '0x%x' $(($1 * 4096 + $2)); }

# Corrected read errors as the memory controller banks of a Skylake-SP report
# them: MCACOD 0x009c (c = channel), one error counted, address valid to the
# cache line (MISC 0x86).
t=$(now)
inject ce-1 7 0x9c00004000010090 "$(addr "${pfn[1]}" 0x040)" 1
printf '{"id":"ce-1","time":"%s","inject":"error","severity":"CE","scrub":"Demand","count":1,"address":"%s","socket":0,"channel":0,"note":"machine check bank 7, status 0x9c00004000010090 (corrected memory read error, channel 0)"}\n' "$t" "$(addr "${pfn[1]}" 0x040)" >>"$WORK/injected.jsonl"

t=$(now)
inject ce-2 7 0x9c00004000010091 "$(addr "${pfn[2]}" 0x980)" 1
printf '{"id":"ce-2","time":"%s","inject":"error","severity":"CE","scrub":"Demand","count":1,"address":"%s","socket":0,"channel":1,"note":"machine check bank 7, status 0x9c00004000010091 (corrected memory read error, channel 1)"}\n' "$t" "$(addr "${pfn[2]}" 0x980)" >>"$WORK/injected.jsonl"

t=$(now)
inject ce-3 8 0x9c00004000010090 "$(addr "${pfn[3]}" 0x100)" 2
printf '{"id":"ce-3","time":"%s","inject":"error","severity":"CE","scrub":"Demand","count":2,"address":"%s","socket":0,"channel":0,"note":"machine check bank 8, status 0x9c00004000010090, twice on one page"}\n' "$t" "$(addr "${pfn[3]}" 0x100)" >>"$WORK/injected.jsonl"
printf '{"id":"ce-3-offline","time":"%s","inject":"page-offline","how":"soft","count":1,"address":"%s","note":"by the corrected error collector at the second error of ce-3"}\n' "$t" "$(addr "${pfn[3]}" 0)" >>"$WORK/injected.jsonl"

t=$(now)
inject ce-4 7 0x9c00004000010090 "$(addr "${pfn[4]}" 0xfc0)" 3
printf '{"id":"ce-4","time":"%s","inject":"error","severity":"CE","scrub":"Demand","count":3,"address":"%s","socket":0,"channel":0,"note":"machine check bank 7, status 0x9c00004000010090, three times on one page"}\n' "$t" "$(addr "${pfn[4]}" 0xfc0)" >>"$WORK/injected.jsonl"
printf '{"id":"ce-4-offline","time":"%s","inject":"page-offline","how":"soft","count":1,"address":"%s","note":"by the corrected error collector at the second error of ce-4"}\n' "$t" "$(addr "${pfn[4]}" 0)" >>"$WORK/injected.jsonl"

# Uncorrected, found by the patrol scrubber, no action required: logged by the poller.
t=$(now)
inject ue-1 7 0xbc000000000100c0 "$(addr "${pfn[5]}" 0x200)" 1 "POLL"
printf '{"id":"ue-1","time":"%s","inject":"error","severity":"Recoverable","scrub":"Patrol","count":1,"address":"%s","socket":0,"channel":0,"note":"machine check bank 7, status 0xbc000000000100c0 (uncorrected, no action required, memory scrubbing error)"}\n' "$t" "$(addr "${pfn[5]}" 0x200)" >>"$WORK/injected.jsonl"
printf '{"id":"ue-1-offline","time":"%s","inject":"page-offline","how":"hard","count":1,"address":"%s","note":"by the kernel after ue-1: an uncorrected error with a usable address costs its page even when no action is required"}\n' "$t" "$(addr "${pfn[5]}" 0)" >>"$WORK/injected.jsonl"

# Uncorrected, action optional, as a machine check exception.
t=$(now)
inject ue-2 7 0xbd000000000100c0 "$(addr "${pfn[6]}" 0x400)" 1 "MCGSTATUS RIPV MCIP"
printf '{"id":"ue-2","time":"%s","inject":"error","severity":"Recoverable","scrub":"Patrol","count":1,"address":"%s","socket":0,"channel":0,"note":"machine check exception, bank 7, status 0xbd000000000100c0 (uncorrected, action optional, memory scrubbing error)"}\n' "$t" "$(addr "${pfn[6]}" 0x400)" >>"$WORK/injected.jsonl"
printf '{"id":"ue-2-offline","time":"%s","inject":"page-offline","how":"hard","count":1,"address":"%s","note":"by the kernel after ue-2"}\n' "$t" "$(addr "${pfn[6]}" 0)" >>"$WORK/injected.jsonl"

# Uncorrected, action required: a data load of a program hit the error.
t=$(now)
inject ue-3 1 0xbd80000000100134 "$(addr "${pfn[7]}" 0x340)" 1 "MCGSTATUS RIPV EIPV MCIP" "RIP 0x33:0x401000"
printf '{"id":"ue-3","time":"%s","inject":"error","severity":"Recoverable","count":1,"address":"%s","socket":0,"note":"machine check exception in user context, bank 1, status 0xbd80000000100134 (uncorrected, action required, data load)"}\n' "$t" "$(addr "${pfn[7]}" 0x340)" >>"$WORK/injected.jsonl"
printf '{"id":"ue-3-offline","time":"%s","inject":"page-offline","how":"hard","count":1,"address":"%s","note":"by the kernel after ue-3"}\n' "$t" "$(addr "${pfn[7]}" 0)" >>"$WORK/injected.jsonl"

sleep 2
echo >&"$hold_in"
: >"$WORK/held.jsonl"
for _ in 1 2 3 4 5 6 7; do
	read -r line <&"$hold_out" || break
	echo "$line" >>"$WORK/held.jsonl"
done
wait "$hold_pid" 2>/dev/null || true
sleep 3

plan "machine checks in a nested guest: 7 corrected and 3 uncorrected memory errors"
"$HERE/capture.sh" "$name"
[ "$name" = mce-clock-step ] && cp "$WORK/clock-step.txt" "$LAB/cap/$name/extra/"
cp "$WORK/plan.yaml" "$LAB/cap/$name/plan.yaml"
cp "$WORK/injected.jsonl" "$WORK/held.jsonl" "$LAB/cap/$name/extra/"
cp "$WORK/$name-rasdaemon.log" "$LAB/cap/$name/extra/rasdaemon.log"
