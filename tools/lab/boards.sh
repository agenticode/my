#!/bin/bash
# boards.sh image|prepare|boot|collect|stop|clean|status [NAME]
#
# x86 guests whose SMBIOS memory devices carry slot names that no public
# dump under testdata/explorer has, one of them with two sockets and two
# NUMA nodes. In each guest capture.sh runs "my collect host" and the rest
# of a capture; nothing is injected. The board and the tabs of the memory
# readings are drawn from what the guest recorded.
#
# QEMU describes the memory of a guest as one type 17 entry per 16 GiB.
# smbios17.py writes one structure per slot instead: as many populated
# slots of 16 GiB as QEMU would make for the guest's memory, then the empty
# slots (size 0). With a type 17 structure from a file QEMU makes none of
# its own; its type 16 array keeps the device count QEMU gives it.
#
#   image         a disk for the guests on the Fedora cloud image at $LAB/loop/img
#   prepare       once: numactl, sysstat, lshw and dmidecode on that disk
#   boot NAME     start guest NAME on a disk of its own over the prepared one
#   collect NAME  sar samples, then capture.sh in the guest; the result goes to $ME/cap/NAME
#   stop NAME     power the guest off
#   clean         stop every guest, remove the disks and the captures
#   status        what runs, and the free memory of the lab host
#
# The guests; ports are this tool's own (ssh 2261 to 2264, seed 8061 to 8064):
#
#   two-sockets  2 sockets of 2 cores, 2 NUMA nodes of 32 GiB; slots CPU0_CH0_DIMM0 to CPU1_CH1_DIMM1
#   digits       1 socket, 32 GiB; slots DIMM000, DIMM010 (populated), DIMM001, DIMM011
#   letters      1 socket, 32 GiB; slots DIMMA1, DIMMB1 (populated), DIMMA2, DIMMB2
#
# Guest memory is allocated as the guest touches it: a guest takes 2 to 3
# GiB of the lab host. The lab host keeps 8 GiB free. One guest runs at a
# time: three guests booted together under emulation were so slow that
# systemd stopped waiting for the disk of /home and two of them stayed in
# emergency mode.
set -eu
LAB=${LAB:-/root/my-lab}
ME=${ME:-$LAB/me}
HERE=$(cd "$(dirname "$0")" && pwd)
MY=${MY:-$LAB/bin/my}
KEY=$ME/id_ed25519
STEP=${1:?usage: boards.sh image|prepare|boot|collect|stop|clean|status [NAME]}
NAMES="two-sockets digits letters"

# spec NAME: the port, memory, processors and slots of a guest.
spec() {
	NUMA=()
	case "$1" in
	prep)
		PORT=2264 SEEDPORT=8064 SMP=4 MEM=4G
		POP=() EMPTY=()
		;;
	two-sockets)
		PORT=2261 SEEDPORT=8061 SMP=4,sockets=2,cores=2,threads=1 MEM=64G
		NUMA=(-object "memory-backend-ram,id=m0,size=32G,reserve=off" -object "memory-backend-ram,id=m1,size=32G,reserve=off"
			-numa "node,nodeid=0,cpus=0-1,memdev=m0" -numa "node,nodeid=1,cpus=2-3,memdev=m1")
		POP=(CPU0_CH0_DIMM0 CPU0_CH1_DIMM0 CPU1_CH0_DIMM0 CPU1_CH1_DIMM0)
		EMPTY=(CPU0_CH0_DIMM1 CPU0_CH1_DIMM1 CPU1_CH0_DIMM1 CPU1_CH1_DIMM1)
		;;
	digits)
		PORT=2262 SEEDPORT=8062 SMP=2 MEM=32G
		POP=(DIMM000 DIMM010) EMPTY=(DIMM001 DIMM011)
		;;
	letters)
		PORT=2263 SEEDPORT=8063 SMP=2 MEM=32G
		POP=(DIMMA1 DIMMB1) EMPTY=(DIMMA2 DIMMB2)
		;;
	*)
		echo "boards.sh: no guest $1 (guests: $NAMES)" >&2
		exit 2
		;;
	esac
	G=$ME/$1
}
guest() { ssh -n -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=20 -i "$KEY" -p "$PORT" root@127.0.0.1 "$@"; }
push() { scp -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i "$KEY" -P "$PORT" "$@"; }
running() { pgrep -f "[q]emu-system-x86_64 -name lab-$1 " >/dev/null; }
waitguest() {
	local n=0
	until guest true 2>/dev/null; do
		n=$((n + 1))
		if [ $n -ge 90 ]; then
			echo "boards.sh: guest $1 does not answer" >&2
			return 1
		fi
		sleep 10
	done
}
# start NAME DISK: boot a guest with the slots of its spec.
start() {
	local name=$1 disk=$2 i
	if running "$name"; then
		echo "lab-$name runs already"
		return 0
	fi
	if pgrep -f "[q]emu-system-x86_64 -name lab-(prep|two-sockets|digits|letters) " >/dev/null; then
		echo "boards.sh: another guest of this tool runs; stop it first (boards.sh status)" >&2
		exit 1
	fi
	avail=$(awk '/MemAvailable/ {print int($2/1048576)}' /proc/meminfo)
	if [ "$avail" -lt 12 ]; then
		echo "boards.sh: only $avail GiB available; the lab host keeps 8 GiB and the guest takes up to 4" >&2
		exit 1
	fi
	mkdir -p "$G/seed"
	sed "s/@NAME@/$name/" "$ME/user-data" >"$G/seed/user-data"
	printf 'instance-id: lab-%s-1\nlocal-hostname: lab-%s\n' "$name" "$name" >"$G/seed/meta-data"
	: >"$G/seed/vendor-data"
	pgrep -f "[h]ttp.server $SEEDPORT" >/dev/null || (cd "$G/seed" && nohup python3 -m http.server "$SEEDPORT" --bind 127.0.0.1 </dev/null >"$G/http.log" 2>&1 &)
	dimms=()
	if [ ${#POP[@]} -gt 0 ]; then
		rm -rf "$G/smbios"
		args=()
		for i in "${!POP[@]}"; do args+=("${POP[$i]},Not Specified,LABB$(printf %04d $((i + 1)))"); done
		python3 "$HERE/smbios17.py" --registered --ecc "$G/smbios/pop" "${args[@]}" >/dev/null
		args=()
		for i in "${!EMPTY[@]}"; do args+=("${EMPTY[$i]},Not Specified,Not Specified"); done
		python3 "$HERE/smbios17.py" --first-handle $((0x1100 + ${#POP[@]})) --size-mib 0 --speed 0 --ranks 0 --millivolts 0 \
			--manufacturer "Not Specified" --part "Not Specified" "$G/smbios/empty" "${args[@]}" >/dev/null
		for f in "$G"/smbios/pop/dimm*.bin "$G"/smbios/empty/dimm*.bin; do dimms+=(-smbios "file=$f"); done
	fi
	ram=(-machine q35)
	if [ ${#NUMA[@]} -eq 0 ]; then
		ram=(-machine "q35,memory-backend=ram0" -object "memory-backend-ram,id=ram0,size=$MEM,reserve=off")
	fi
	nohup qemu-system-x86_64 -name "lab-$name" "${ram[@]}" -accel tcg,thread=multi -cpu Skylake-Server -smp "$SMP" -m "$MEM" \
		"${NUMA[@]}" \
		-drive if=virtio,format=qcow2,file="$disk" \
		-netdev user,id=n0,hostfwd=tcp:127.0.0.1:$PORT-:22 -device virtio-net-pci,netdev=n0 \
		-device virtio-rng-pci \
		-smbios "type=1,manufacturer=QEMU,product=lab-$name,serial=ds=nocloud;s=http://10.0.2.2:$SEEDPORT/" \
		"${dimms[@]}" \
		-serial file:"$G/console.log" -display none -monitor none </dev/null >"$G/qemu.log" 2>&1 &
	echo "lab-$name: qemu pid $!, ssh on 127.0.0.1:$PORT"
}
# halt NAME: power the guest off, and stop its seed server.
halt() {
	local name=$1 n=0
	if running "$name"; then
		guest poweroff 2>/dev/null || true
		while running "$name" && [ $n -lt 30 ]; do
			sleep 5
			n=$((n + 1))
		done
		running "$name" && pkill -f "[q]emu-system-x86_64 -name lab-$name "
	fi
	pkill -f "[h]ttp.server $SEEDPORT" || true
}

case "$STEP" in
image)
	mkdir -p "$ME"
	base=$(find "$LAB/loop/img" -name "Fedora-Cloud-Base-Generic-*.x86_64.qcow2" | sort | tail -1)
	rm -f "$ME/prep.qcow2"
	qemu-img create -q -f qcow2 -F qcow2 -b "$base" "$ME/prep.qcow2" 16G
	[ -f "$KEY" ] || ssh-keygen -q -t ed25519 -N "" -C lab-boards -f "$KEY"
	cat >"$ME/user-data" <<EOF
#cloud-config
disable_root: false
ssh_pwauth: false
users:
  - name: root
    ssh_authorized_keys:
      - $(cat "$KEY.pub")
runcmd:
  - [ sh, -c, "echo LAB-@NAME@-READY > /dev/ttyS0" ]
EOF
	echo "disk $ME/prep.qcow2 on $base"
	;;
prepare)
	spec prep
	start prep "$ME/prep.qcow2"
	waitguest prep
	guest 'dnf -y -q install numactl sysstat lshw dmidecode && systemctl disable --now sysstat.service sysstat-collect.timer sysstat-summary.timer 2>/dev/null; true'
	guest 'mkdir -p /root/my-lab/bin /root/my-lab/tools'
	push "$HERE/capture.sh" "$HERE/plan.py" root@127.0.0.1:/root/my-lab/tools/
	# Every guest on this disk gets a machine ID and host keys of its own.
	guest 'cloud-init clean --logs --machine-id; rm -f /etc/ssh/ssh_host_*; sync'
	halt prep
	echo "prepared $ME/prep.qcow2"
	;;
boot)
	spec "${2:?usage: boards.sh boot NAME}"
	mkdir -p "$G"
	[ -f "$G/disk.qcow2" ] || qemu-img create -q -f qcow2 -F qcow2 -b "$ME/prep.qcow2" "$G/disk.qcow2"
	start "$2" "$G/disk.qcow2"
	waitguest "$2"
	echo "lab-$2 answers"
	;;
collect)
	spec "${2:?usage: boards.sh collect NAME}"
	push "$MY" root@127.0.0.1:/root/my-lab/bin/my
	# sar -B reads the day's file of sysstat: six samples ten seconds apart.
	guest '/usr/lib64/sa/sa1 10 6'
	summary="no injection: a board of ${#POP[@]} populated and ${#EMPTY[@]} empty slots"
	guest "cd /root/my-lab/tools && python3 plan.py --scenario board-$2 --summary '$summary' --method none --set kernel=\$(uname -r) </dev/null >/root/my-lab/plan.yaml"
	guest "/root/my-lab/tools/capture.sh board-$2 >/root/my-lab/capture.out 2>&1; cp /root/my-lab/plan.yaml /root/my-lab/cap/board-$2/"
	# QEMU's user network puts every guest behind the gateway 10.0.2.2, and
	# the system serial number is the seed URL on it. They name no machine,
	# and the raw SMBIOS table, which sanitize.py copies as it is, holds the
	# URL: both are left out of what sanitize.py replaces.
	guest "sed -i '/^ip=10\\.0\\.2\\.2\$/d; /^dmi_product_serial=ds=nocloud;/d' /root/my-lab/cap/board-$2/private/ident.txt"
	# Every SMBIOS type as dmidecode prints it: the processor records count
	# the sockets of the board, and the explorer's fixtures read this text.
	guest "dmidecode >/root/my-lab/cap/board-$2/extra/dmidecode-all.txt"
	# my collect host stops lshw after 15 seconds, which lshw does not
	# meet in an emulated guest. The fixtures take its text from a run
	# without that limit; lshw-seconds.txt says how long it took.
	guest "cd /root/my-lab/cap/board-$2/extra && { TIMEFORMAT=%R; time lshw -c memory -c processor >lshw.txt 2>/dev/null; } 2>lshw-seconds.txt"
	mkdir -p "$ME/cap"
	rm -rf "$ME/cap/board-$2"
	guest "tar -C /root/my-lab/cap -cf - board-$2" | tar -C "$ME/cap" -xf -
	echo "capture in $ME/cap/board-$2"
	;;
stop)
	spec "${2:?usage: boards.sh stop NAME}"
	halt "$2"
	;;
clean)
	for n in prep $NAMES; do
		spec "$n"
		halt "$n"
	done
	rm -rf "$ME/prep.qcow2" "$ME/user-data" "$ME/cap" "$KEY" "$KEY.pub"
	for n in $NAMES; do rm -rf "${ME:?}/$n"; done
	rm -rf "${ME:?}/prep"
	;;
status)
	pgrep -af "[q]emu-system-x86_64 -name lab-(prep|two-sockets|digits|letters) " | cut -c1-100 || echo "no guest runs"
	awk '/MemAvailable/ {printf "lab host: %d GiB available\n", $2/1048576}' /proc/meminfo
	;;
*)
	echo "usage: boards.sh image|prepare|boot|collect|stop|clean|status [NAME]" >&2
	exit 2
	;;
esac
