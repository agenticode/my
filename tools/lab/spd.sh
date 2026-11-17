#!/bin/bash
# spd.sh image|boot|run|stop|clean|status [ddr4|ddr5]
#
# The SPD probe and read of MemorY against a real kernel: an x86 guest of
# the q35 machine, whose ICH9 SMBus controller the kernel drives with
# i2c_i801, and whose SMBus carries the eight EEPROMs QEMU emulates at
# 0x50 to 0x57 (hw/i386/pc_q35.c, smbus_eeprom_init). QEMU writes no SPD
# data into them. smbios17.py gives the guest SMBIOS type 17 structures of
# two populated and two empty slots of DDR4 or DDR5, so that the kernel's
# i2c_register_spd creates ee1004 or spd5118 clients on that bus. The
# guest runs the Fedora cloud kernel, which has the SMBus and SPD drivers
# as modules. MemorY runs with MY_DEBUG=spd-vm, without which it does not
# probe on a virtual machine. Nothing is written to the bus.
#
#   image       a disk for the guest on the Fedora cloud image at $LAB/loop/img
#   boot TYPE   start the guest with SMBIOS memory devices of TYPE
#   run TYPE    in the guest: the I2C devices and drivers as sysfs shows them,
#               my spd as root and as nobody, my collect host; the result goes
#               to $SPD/out/TYPE
#   stop        power the guest off
#   clean       stop the guest, remove the disk, the key and the results
#   status      what runs, and the free memory of the lab host
#
# The guest takes 2 to 3 GiB of the lab host, which keeps 8 GiB free.
set -eu
LAB=${LAB:-/root/my-lab}
SPD=${SPD:-$LAB/spd/vm}
HERE=$(cd "$(dirname "$0")" && pwd)
MY=${MY:-$LAB/bin/my}
KEY=$SPD/id_ed25519
PORT=2271 SEEDPORT=8071 NAME=spd
STEP=${1:?usage: spd.sh image|boot|run|stop|clean|status [ddr4|ddr5]}

guest() { ssh -n -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=20 -i "$KEY" -p "$PORT" root@127.0.0.1 "$@"; }
push() { scp -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i "$KEY" -P "$PORT" "$@"; }
running() { pgrep -f "[q]emu-system-x86_64 -name lab-$NAME " >/dev/null; }
memtype() {
	case "${1:-}" in
	ddr4) TYPE=DDR4 PART=LABDDR4-16G ;;
	ddr5) TYPE=DDR5 PART=LABDDR5-16G ;;
	*)
		echo "spd.sh: TYPE is ddr4 or ddr5" >&2
		exit 2
		;;
	esac
}
waitguest() {
	local n=0
	until guest true 2>/dev/null; do
		n=$((n + 1))
		if [ $n -ge 90 ]; then
			echo "spd.sh: the guest does not answer" >&2
			return 1
		fi
		sleep 10
	done
}
halt() {
	local n=0
	if running; then
		guest poweroff 2>/dev/null || true
		while running && [ $n -lt 30 ]; do
			sleep 5
			n=$((n + 1))
		done
		running && pkill -f "[q]emu-system-x86_64 -name lab-$NAME "
	fi
	pkill -f "[h]ttp.server $SEEDPORT" || true
}

case "$STEP" in
image)
	mkdir -p "$SPD"
	base=$(find "$LAB/loop/img" -name "Fedora-Cloud-Base-Generic-*.x86_64.qcow2" | sort | tail -1)
	rm -f "$SPD/disk.qcow2"
	qemu-img create -q -f qcow2 -F qcow2 -b "$base" "$SPD/disk.qcow2" 16G
	[ -f "$KEY" ] || ssh-keygen -q -t ed25519 -N "" -C lab-spd -f "$KEY"
	mkdir -p "$SPD/seed"
	cat >"$SPD/seed/user-data" <<USERDATA
#cloud-config
disable_root: false
ssh_pwauth: false
users:
  - name: root
    ssh_authorized_keys:
      - $(cat "$KEY.pub")
USERDATA
	printf 'instance-id: lab-spd-1\nlocal-hostname: lab-spd\n' >"$SPD/seed/meta-data"
	: >"$SPD/seed/vendor-data"
	echo "disk $SPD/disk.qcow2 on $base"
	;;
boot)
	memtype "${2:-}"
	if running; then
		echo "spd.sh: the guest runs already; stop it first" >&2
		exit 1
	fi
	avail=$(awk '/MemAvailable/ {print int($2/1048576)}' /proc/meminfo)
	if [ "$avail" -lt 12 ]; then
		echo "spd.sh: only $avail GiB available; the lab host keeps 8 GiB and the guest takes up to 4" >&2
		exit 1
	fi
	pgrep -f "[h]ttp.server $SEEDPORT" >/dev/null || (cd "$SPD/seed" && nohup python3 -m http.server "$SEEDPORT" --bind 127.0.0.1 </dev/null >"$SPD/http.log" 2>&1 &)
	rm -rf "$SPD/smbios"
	python3 "$HERE/smbios17.py" --type "$TYPE" --registered --ecc --part "$PART" "$SPD/smbios/pop" \
		"DIMM_A1,Not Specified,LABS0001" "DIMM_B1,Not Specified,LABS0002" >/dev/null
	python3 "$HERE/smbios17.py" --type "$TYPE" --first-handle 0x1102 --size-mib 0 --speed 0 --ranks 0 --millivolts 0 \
		--manufacturer "Not Specified" --part "Not Specified" "$SPD/smbios/empty" \
		"DIMM_A2,Not Specified,Not Specified" "DIMM_B2,Not Specified,Not Specified" >/dev/null
	dimms=()
	for f in "$SPD"/smbios/pop/dimm*.bin "$SPD"/smbios/empty/dimm*.bin; do dimms+=(-smbios "file=$f"); done
	nohup qemu-system-x86_64 -name "lab-$NAME" -machine "q35,memory-backend=ram0" -object "memory-backend-ram,id=ram0,size=32G,reserve=off" \
		-accel tcg,thread=multi -cpu Skylake-Server -smp 2 -m 32G \
		-drive if=virtio,format=qcow2,file="$SPD/disk.qcow2" \
		-netdev user,id=n0,hostfwd=tcp:127.0.0.1:$PORT-:22 -device virtio-net-pci,netdev=n0 \
		-device virtio-rng-pci \
		-smbios "type=1,manufacturer=QEMU,product=lab-$NAME,serial=ds=nocloud;s=http://10.0.2.2:$SEEDPORT/" \
		"${dimms[@]}" \
		-serial file:"$SPD/console-${2}.log" -display none -monitor none </dev/null >"$SPD/qemu.log" 2>&1 &
	echo "lab-$NAME: qemu pid $!, ssh on 127.0.0.1:$PORT"
	waitguest
	echo "lab-$NAME answers"
	;;
run)
	memtype "${2:-}"
	push "$MY" root@127.0.0.1:/usr/local/bin/my
	guest 'rm -rf /root/spd && mkdir -p /root/spd'
	# shellcheck disable=SC2016 # expanded in the guest
	guest 'cd /root/spd && {
		uname -r >kernel.txt
		lspci -nn -k >lspci.txt 2>&1
		dmidecode -t 17 >dmidecode-17.txt 2>&1
		lsmod | grep -E "^(i2c|ee1004|spd5118|at24)" >lsmod.txt || true
		journalctl -k -b --no-pager | grep -iE "i2c|smbus|i801|spd|ee1004|at24" >kernel-log.txt || true
		for d in /sys/bus/i2c/devices/*; do
			printf "%s name=%s driver=%s eeprom=%s\n" "${d##*/}" "$(cat "$d/name" 2>/dev/null)" "$(basename "$(readlink "$d/driver" 2>/dev/null)" 2>/dev/null)" "$(stat -c %s "$d/eeprom" 2>/dev/null || echo -)"
		done >i2c-devices.txt
		ls -l /sys/bus/i2c/devices/*/eeprom >eeprom-files.txt 2>&1 || true
		for f in /sys/bus/i2c/devices/*/eeprom; do
			[ -e "$f" ] || continue
			n=${f%/eeprom}; n=${n##*/}
			{ dd if="$f" of="eeprom-$n.bin" bs=4096 count=1 2>"dd-$n.txt"; echo "exit $?" >>"dd-$n.txt"; } || true
		done
		{ MY_DEBUG=spd-vm my spd --lang en --width 100; echo "exit $?"; } >spd-root.txt 2>&1
		{ MY_DEBUG=spd-vm my spd --json; echo "exit $?"; } >spd-root.json 2>&1
		{ runuser -u nobody -- env MY_DEBUG=spd-vm my spd --lang en --width 100; echo "exit $?"; } >spd-nobody.txt 2>&1
		{ my spd --lang en --width 100; echo "exit $?"; } >spd-no-debug.txt 2>&1
		{ MY_DEBUG=spd-vm my collect host -o /root/spd/collect; echo "exit $?"; } >collect.txt 2>&1
		my version >version.txt 2>&1
	}'
	mkdir -p "$SPD/out"
	rm -rf "$SPD/out/$2"
	mkdir -p "$SPD/out/$2"
	guest 'tar -C /root/spd -cf - .' | tar -C "$SPD/out/$2" -xf -
	echo "result in $SPD/out/$2"
	;;
stop)
	halt
	;;
clean)
	halt
	rm -rf "$SPD"
	;;
status)
	pgrep -af "[q]emu-system-x86_64 -name lab-$NAME " | cut -c1-100 || echo "no guest runs"
	awk '/MemAvailable/ {printf "lab host: %d GiB available\n", $2/1048576}' /proc/meminfo
	;;
*)
	echo "usage: spd.sh image|boot|run|stop|clean|status [ddr4|ddr5]" >&2
	exit 2
	;;
esac
