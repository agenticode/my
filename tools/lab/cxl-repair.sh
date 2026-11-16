#!/bin/bash
# cxl-repair.sh
#
# A memory repair on CXL memory devices, recorded by the kernel and rasdaemon:
# errors in one row of each of two devices, a soft post package repair (PPR)
# of that row through the kernel's EDAC memory repair interface, then errors
# again in the repaired row of one device while the other stays quiet.
#
# QEMU 11.1 or later is needed: it emulates the PPR and sparing maintenance of
# a CXL type 3 device and answers a repair with a memory sparing event record
# (QEMU 10.2 has neither). Build it once on the lab host:
#   curl -fsSLO https://download.qemu.org/qemu-11.1.3.tar.xz   (and check the .sig)
#   ./configure --target-list=x86_64-softmmu --prefix=$LAB/qemu && ninja -C build install
# The guest runs the kernel of the lab host (7.3 with CONFIG_CXL_EDAC_MEM_REPAIR)
# and rasdaemon 1.0.0 built from source, as cxl.sh does. MY is the my binary
# capture.sh runs in the guest (default $LAB/bin/my).
#
# Outside the guest, cxl-repair.py puts DRAM event records into the devices
# through QMP; inside, this script makes the repairs. The two sides wait for
# each other through files in $LAB/tmp. The capture is $LAB/cap/cxl-repair.
set -eu
LAB=${LAB:-/root/my-lab}
HERE=$(cd "$(dirname "$0")" && pwd)
RASDAEMON=${RASDAEMON:-$LAB/src/rasdaemon/build/rasdaemon}
QEMU=${QEMU:-$LAB/qemu/bin/qemu-system-x86_64}
WORK=$LAB/tmp
QMP=$WORK/cxlr-qmp.sock
name=cxl-repair

# wait_for FILE SECONDS
wait_for() {
	for _ in $(seq 1 "$2"); do
		[ -e "$1" ] && return 0
		sleep 1
	done
	return 1
}

if [ "${1:-}" != "--inside" ]; then
	mkdir -p "$WORK" "$LAB/cap"
	rm -rf "$LAB/cap/$name" "$WORK"/cxlr-* "$QMP"
	: >"$WORK/cxlr-injected.jsonl"
	# Outside the guest: errors before the repair, wait for the repair, errors after.
	(
		if wait_for "$WORK/cxlr-ready" 900; then
			python3 "$HERE/cxl-repair.py" --qmp "$QMP" --phase before >>"$WORK/cxlr-injected.jsonl" 2>>"$WORK/cxlr-inject.err" || true
			touch "$WORK/cxlr-before"
			if wait_for "$WORK/cxlr-repaired" 300; then
				python3 "$HERE/cxl-repair.py" --qmp "$QMP" --phase after >>"$WORK/cxlr-injected.jsonl" 2>>"$WORK/cxlr-inject.err" || true
			fi
		fi
		touch "$WORK/cxlr-before" "$WORK/cxlr-after"
	) &
	vng --verbose --run "/boot/vmlinuz-$(uname -r)" --qemu "$QEMU" --disable-kvm --disable-microvm --cpus 2 --memory 4G --user root --name lab-guest \
		--rwdir="$LAB" \
		--qemu-opts="-cpu Skylake-Server -machine cxl=on \
-object memory-backend-ram,id=vmem0,share=on,size=256M -object memory-backend-ram,id=vmem1,share=on,size=256M \
-device pxb-cxl,bus_nr=12,bus=pcie.0,id=cxl.1 \
-device cxl-rp,port=0,bus=cxl.1,id=root_port13,chassis=0,slot=2 -device cxl-rp,port=1,bus=cxl.1,id=root_port14,chassis=0,slot=3 \
-device cxl-type3,bus=root_port13,volatile-memdev=vmem0,id=cxl-vmem0,sn=0x4c41420011 \
-device cxl-type3,bus=root_port14,volatile-memdev=vmem1,id=cxl-vmem1,sn=0x4c41420012 \
-M cxl-fmw.0.targets.0=cxl.1,cxl-fmw.0.size=4G -qmp unix:$QMP,server=on,wait=off" \
		--exec "RASDAEMON=$RASDAEMON MY=${MY:-$LAB/bin/my} QEMU_VERSION=$("$QEMU" --version | awk '{print $4; exit}') $HERE/cxl-repair.sh --inside" >"$WORK/$name-console.log" 2>&1 || true
	wait
	if [ -d "$LAB/cap/$name" ]; then
		echo "captured: $LAB/cap/$name"
	else
		echo "cxl-repair.sh: no capture; see $WORK/$name-console.log and $WORK/$name.log" >&2
		exit 1
	fi
	exit 0
fi

# From here on: inside the guest.
exec 2>"$WORK/$name.log"
set -x
export RASDAEMON
rm -f /var/lib/rasdaemon/ras-mc_event.db
"$RASDAEMON" -f -r >"$WORK/$name-rasdaemon.log" 2>&1 &
for _ in $(seq 1 30); do
	grep -q "Listening to events" "$WORK/$name-rasdaemon.log" && break
	sleep 1
done
sleep 2

# The kernel's name of each device, by serial number.
declare -A memdev
for d in /sys/bus/cxl/devices/mem*; do
	memdev[$(cat "$d/serial")]=$(basename "$d")
done

# repair_dir MEMDEV: the soft PPR control of the device.
repair_dir() {
	local r
	for r in /sys/bus/edac/devices/cxl_"$1"/mem_repair*; do
		[ "$(cat "$r/repair_type" 2>/dev/null)" = ppr ] && echo "$r" && return 0
	done
	return 1
}

# The controls as the kernel shows them before any repair.
{
	echo "# ls /sys/bus/edac/devices"
	ls /sys/bus/edac/devices/
	for s in "${!memdev[@]}"; do
		m=${memdev[$s]}
		echo "# $m serial $s"
		for r in /sys/bus/edac/devices/cxl_"$m"/*/; do
			echo "## $r"
			for f in "$r"*; do
				[ -f "$f" ] && [ -r "$f" ] && echo "$(basename "$f"): $(cat "$f" 2>&1)"
			done
		done
	done
} >"$WORK/cxlr-edac-before.txt" 2>>"$WORK/$name.log"

touch "$WORK/cxlr-ready"
wait_for "$WORK/cxlr-before" 300 || true
sleep 3

# A soft PPR of the failing row of each device, at the address of the first
# error in it. The guest has not mapped the devices into a region, so the
# kernel lets the repair through without a matching error record of this boot
# (drivers/cxl/core/edac.c, cxl_mem_perform_ppr).
: >"$WORK/cxlr-repairs.jsonl"
for pair in 0x4c41420011:0x1002000 0x4c41420012:0x1001000; do
	serial=${pair%%:*}
	dpa=${pair#*:}
	m=${memdev[$serial]:-}
	r=$(repair_dir "$m") || r=
	at=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
	result=ok
	if [ -z "$r" ]; then
		result="no soft PPR control for $serial"
	else
		echo "$dpa" >"$r/dpa" || result="dpa refused"
		if [ "$result" = ok ]; then
			echo 1 >"$r/repair" 2>"$WORK/cxlr-repair.err" || result="repair refused: $(cat "$WORK/cxlr-repair.err")"
		fi
	fi
	printf '{"id": "ppr-%s", "time": "%s", "repair": "soft PPR", "dpa": "%s", "device_name": "%s", "serial": "%s", "control": "%s", "persist_mode": "%s", "result": "%s"}\n' \
		"$m" "$at" "$dpa" "$m" "$serial" "$r" "$(cat "$r/persist_mode" 2>/dev/null)" "$result" >>"$WORK/cxlr-repairs.jsonl"
	sleep 2
done
sleep 3
touch "$WORK/cxlr-repaired"
wait_for "$WORK/cxlr-after" 300 || true
sleep 3

{
	echo "# ls -l /sys/bus/cxl/devices"
	ls -l /sys/bus/cxl/devices/
	for d in /sys/bus/cxl/devices/mem*; do
		echo "# $d"
		for f in serial firmware_version payload_max ram/size pmem/size numa_node; do
			[ -r "$d/$f" ] && echo "$f: $(cat "$d/$f")"
		done
	done
} >"$WORK/cxlr-sysfs.txt" 2>/dev/null

# The plan names each device as the kernel does.
sed_args=()
for s in "${!memdev[@]}"; do
	sed_args+=(-e "s/\"serial $s\"/\"${memdev[$s]}\"/")
done
sed "${sed_args[@]}" "$WORK/cxlr-injected.jsonl" >"$WORK/cxlr-plan-in.jsonl"
python3 "$HERE/plan.py" --scenario "$name" \
	--summary "errors in one row of two emulated CXL memory devices, a soft post package repair of that row on each, then errors again in the repaired row of one device" \
	--method "QMP of QEMU: cxl-inject-dram-event with maintenance needed; the repair through /sys/bus/edac/devices/cxl_memN/mem_repairX in the guest" \
	--set "kernel=$(uname -r)" --set "rasdaemon=$("$RASDAEMON" --version | awk '{print $2; exit}')" \
	--set "qemu=${QEMU_VERSION:-unknown}" \
	--set "device=two QEMU CXL type 3 memory devices, 256 MB volatile each, behind one emulated CXL host bridge with two root ports" \
	<"$WORK/cxlr-plan-in.jsonl" >"$WORK/cxlr-plan.yaml"
"$HERE/capture.sh" "$name"
cp "$WORK/cxlr-plan.yaml" "$LAB/cap/$name/plan.yaml"
cp "$WORK/cxlr-injected.jsonl" "$WORK/cxlr-repairs.jsonl" "$WORK/cxlr-sysfs.txt" "$WORK/cxlr-edac-before.txt" "$LAB/cap/$name/extra/"
cp "$WORK/$name-rasdaemon.log" "$LAB/cap/$name/extra/rasdaemon.log"
