#!/bin/bash
# cxl.sh
#
# Event records of a CXL memory device in a nested guest. QEMU emulates a CXL
# host bridge, a root port and a type 3 device with 256 MB of volatile memory;
# the guest runs the kernel of the lab host, whose CXL driver reads the event
# log of the device and traces every record. rasdaemon 1.0.0, built from
# source, stores the traces: the rasdaemon package of Fedora 44 is built
# without the CXL tables.
#
# The records are put into the device from outside the guest, through QMP
# (cxl.py). The capture is $LAB/cap/cxl.
set -eu
LAB=${LAB:-/root/my-lab}
HERE=$(cd "$(dirname "$0")" && pwd)
RASDAEMON=${RASDAEMON:-$LAB/src/rasdaemon/build/rasdaemon}
WORK=$LAB/tmp
QMP=$WORK/cxl-qmp.sock
name=cxl

if [ "${1:-}" != "--inside" ]; then
	mkdir -p "$WORK" "$LAB/cap"
	rm -rf "$LAB/cap/$name" "$WORK/cxl-ready" "$WORK/cxl-injected" "$QMP"
	: >"$WORK/injected.jsonl"
	# Outside the guest: wait until it is ready, inject, say so.
	(
		for _ in $(seq 1 600); do
			[ -e "$WORK/cxl-ready" ] && break
			sleep 1
		done
		if [ -e "$WORK/cxl-ready" ]; then
			python3 "$HERE/cxl.py" --qmp "$QMP" >"$WORK/injected.jsonl" 2>"$WORK/cxl-inject.err" || true
		fi
		touch "$WORK/cxl-injected"
	) &
	vng --verbose --run "/boot/vmlinuz-$(uname -r)" --disable-kvm --disable-microvm --cpus 2 --memory 4G --user root --name lab-guest \
		--rwdir="$LAB" \
		--qemu-opts="-cpu Skylake-Server -machine cxl=on -object memory-backend-ram,id=vmem0,share=on,size=256M -device pxb-cxl,bus_nr=12,bus=pcie.0,id=cxl.1 -device cxl-rp,port=0,bus=cxl.1,id=root_port13,chassis=0,slot=2 -device cxl-type3,bus=root_port13,volatile-memdev=vmem0,id=cxl-vmem0,sn=0x4c41420001 -M cxl-fmw.0.targets.0=cxl.1,cxl-fmw.0.size=4G -qmp unix:$QMP,server=on,wait=off" \
		--exec "RASDAEMON=$RASDAEMON $HERE/cxl.sh --inside" >"$WORK/$name-console.log" 2>&1 || true
	wait
	if [ -d "$LAB/cap/$name" ]; then
		echo "captured: $LAB/cap/$name"
	else
		echo "cxl.sh: no capture; see $WORK/$name-console.log and $WORK/$name.log" >&2
		exit 1
	fi
	exit 0
fi

# From here on: inside the guest.
exec 2>"$WORK/$name.log"
set -x
export RASDAEMON
ls /sys/bus/cxl/devices/ >&2 || true
rm -f /var/lib/rasdaemon/ras-mc_event.db
"$RASDAEMON" -f -r >"$WORK/$name-rasdaemon.log" 2>&1 &
for _ in $(seq 1 30); do
	grep -q "Listening to events" "$WORK/$name-rasdaemon.log" && break
	sleep 1
done
sleep 2
touch "$WORK/cxl-ready"
for _ in $(seq 1 300); do
	[ -e "$WORK/cxl-injected" ] && break
	sleep 1
done
sleep 2
# The kernel reads the poison list of a device when it is asked to.
for d in /sys/bus/cxl/devices/mem*; do
	[ -w "$d/trigger_poison_list" ] && echo 1 >"$d/trigger_poison_list"
done
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
	echo "# ls /sys/bus/edac/devices"
	ls /sys/bus/edac/devices/ 2>/dev/null
	for d in /sys/bus/edac/devices/cxl_*; do
		[ -d "$d" ] && find "$d" -maxdepth 2 -type f -perm -u+r | sort | while read -r f; do echo "$f: $(cat "$f" 2>/dev/null)"; done
	done
} >"$WORK/cxl-sysfs.txt" 2>&1

python3 "$HERE/plan.py" --scenario "$name" \
	--summary "event records of an emulated CXL memory device: 11 corrected and 1 uncorrected error, a health record, 2 poison ranges" \
	--method "QMP of QEMU: cxl-inject-dram-event, cxl-inject-general-media-event, cxl-inject-memory-module-event, cxl-inject-poison" \
	--set "kernel=$(uname -r)" --set "rasdaemon=$("$RASDAEMON" --version | awk '{print $2; exit}')" \
	--set "device=QEMU CXL type 3 memory device, 256 MB volatile, behind an emulated CXL host bridge and root port" \
	<"$WORK/injected.jsonl" >"$WORK/plan.yaml"
"$HERE/capture.sh" "$name"
cp "$WORK/plan.yaml" "$LAB/cap/$name/plan.yaml"
cp "$WORK/injected.jsonl" "$WORK/cxl-sysfs.txt" "$LAB/cap/$name/extra/"
cp "$WORK/$name-rasdaemon.log" "$LAB/cap/$name/extra/rasdaemon.log"
