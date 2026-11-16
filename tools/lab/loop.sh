#!/bin/bash
# loop.sh image|boot|prepare|wait|inject|run|capture|stop|status
#
# Boots that end in a fatal memory error, one after another, in an x86 guest
# with a disk of its own. The lab host has no machine check banks, so the
# errors are put into the banks of an emulated processor: QEMU emulates a
# Xeon (Skylake-SP) without hardware virtualization, and its monitor command
# "mce" writes a status, an address and a MISC value into a bank of a
# virtual CPU and raises a machine check (target/i386/helper.c,
# do_inject_x86_mce). With an uncorrected status that has the processor
# context corrupt the guest kernel panics, and 30 seconds later it restarts
# the guest (mce_panic). QEMU keeps the bank contents over that reset:
# mce_banks lies after end_reset_fields in CPUX86State (target/i386/cpu.h),
# which x86_cpu_reset_hold leaves alone. The next boot polls the banks and
# finds the record (arch/x86/kernel/cpu/mce/core.c,
# __mcheck_cpu_init_prepare_banks).
#
# The guest has an ACPI error record store (-device acpi-erst, QEMU
# docs/specs/acpi_erst.rst) with a file behind it, so the kernel registers
# "erst" as its pstore backend and mce_panic has a store for the record. With
# FIRMWARE=efi the guest boots through OVMF instead, without ERST, and the
# panic text goes to EFI variables (efi_pstore).
#
#   image     make the guest disk from the Fedora cloud image at $LOOP/img
#   boot      start the guest; the console of every boot goes to $X86/console.log
#   prepare   once per disk: packages, kernel parameters, this tool
#   wait      wait until the guest answers
#   kdump     once: a crash kernel in the guest, then a reboot
#   inject ID BANK STATUS ADDR MISC [MCGSTATUS]
#             write one record into a bank of CPU 0 and raise the machine check
#   run PLAN  one boot per line of PLAN: wait for the harvest, put the record
#             into the banks of every CPU, wait for the next boot
#   capture NAME  the files of the guest after a loop, as a copy of a host
#   stop      power the guest off
#   status    what runs, and the free memory of the lab host
#
# Ports and names are this lane's own: the guest is lab-x86, ssh on
# 127.0.0.1:2232, the seed on 127.0.0.1:8002.
set -eu
LAB=${LAB:-/root/my-lab}
LOOP=${LOOP:-$LAB/loop}
X86=$LOOP/x86
HERE=$(cd "$(dirname "$0")" && pwd)
PORT=${PORT:-2232}
SEEDPORT=${SEEDPORT:-8002}
FIRMWARE=${FIRMWARE:-bios}
KEY=$X86/id_ed25519
MON=$X86/mon.sock
QMP=$X86/qmp.sock
STEP=${1:?usage: loop.sh image|boot|prepare|wait|inject|run|clean|capture|stop|status}
guest() { ssh -n -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=20 -i "$KEY" -p "$PORT" root@127.0.0.1 "$@"; }
push() { scp -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i "$KEY" -P "$PORT" "$@"; }
# hmp CMD: one command of the human monitor, through QMP.
hmp() {
	python3 - "$QMP" "$1" <<'PYEOF'
import json, socket, sys
s = socket.socket(socket.AF_UNIX)
s.connect(sys.argv[1])
f = s.makefile("rw")
json.loads(f.readline())
for cmd in ({"execute": "qmp_capabilities"}, {"execute": "human-monitor-command", "arguments": {"command-line": sys.argv[2]}}):
    f.write(json.dumps(cmd) + "\n"); f.flush()
    while True:
        r = json.loads(f.readline())
        if "event" in r:
            continue
        break
    if "error" in r:
        sys.exit("qmp: " + r["error"]["desc"])
print(r.get("return", ""), end="")
PYEOF
}

# waitharvest: wait until the harvest of this boot has finished; print the
# boot ID.
waitharvest() {
	local k=0
	while :; do
		state=$(guest 'systemctl show my-harvest.service -p ActiveState --value; systemctl show my-harvest.service -p ExecMainExitTimestampMonotonic --value' 2>/dev/null | tr '\n' ' ')
		case "$state" in
		"inactive "[1-9]*) break ;;
		esac
		k=$((k + 1))
		[ $k -lt 90 ] || { echo "loop.sh: the harvest did not finish: $state" >&2; return 1; }
		sleep 10
	done
	guest cat /proc/sys/kernel/random/boot_id | tr -d -
}

case "$STEP" in
image)
	mkdir -p "$X86/seed"
	base=$(ls "$LOOP"/img/Fedora-Cloud-Base-Generic-*.x86_64.qcow2 | tail -1)
	rm -f "$X86/disk.qcow2"
	qemu-img create -q -f qcow2 -F qcow2 -b "$base" "$X86/disk.qcow2" 16G
	[ -f "$KEY" ] || ssh-keygen -q -t ed25519 -N "" -C lab-x86 -f "$KEY"
	cat >"$X86/seed/user-data" <<EOF
#cloud-config
disable_root: false
ssh_pwauth: false
users:
  - name: root
    ssh_authorized_keys:
      - $(cat "$KEY.pub")
runcmd:
  - [ sh, -c, "echo LAB-X86-READY > /dev/ttyS0" ]
EOF
	printf 'instance-id: lab-x86-1\nlocal-hostname: lab-x86\n' >"$X86/seed/meta-data"
	: >"$X86/seed/vendor-data"
	cp /usr/share/OVMF/OVMF_VARS.fd "$X86/vars.fd"
	# The error record store: a file that keeps what the guest writes into it.
	rm -f "$X86/erst.backing"
	echo "disk $X86/disk.qcow2 on $base"
	;;

boot)
	if pgrep -f "[q]emu-system-x86_64 -name lab-x86" >/dev/null; then
		echo "lab-x86 runs already"
		exit 0
	fi
	avail=$(awk '/MemAvailable/ {print int($2/1048576)}' /proc/meminfo)
	if [ "$avail" -lt 14 ]; then
		echo "loop.sh: only $avail GiB available; the lab host keeps 8 GiB and the guest takes 4" >&2
		exit 1
	fi
	pgrep -f "[h]ttp.server $SEEDPORT" >/dev/null || (cd "$X86/seed" && nohup python3 -m http.server "$SEEDPORT" --bind 127.0.0.1 </dev/null >"$X86/http.log" 2>&1 &)
	fw=()
	if [ "$FIRMWARE" = efi ]; then
		fw=(-drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE.fd -drive if=pflash,format=raw,file="$X86/vars.fd")
	else
		fw=(-object memory-backend-file,id=erstnvram,mem-path="$X86/erst.backing",size=0x10000,share=on -device acpi-erst,memdev=erstnvram)
	fi
	rm -f "$MON" "$QMP"
	# No -no-reboot: the guest restarts itself after a panic, inside the
	# same QEMU process, so that the banks keep what was put into them.
	nohup qemu-system-x86_64 -name lab-x86 -machine q35 -accel tcg,thread=multi -cpu Skylake-Server -smp 4 -m 4G \
		"${fw[@]}" \
		-drive if=virtio,format=qcow2,file="$X86/disk.qcow2" \
		-netdev user,id=n0,hostfwd=tcp:127.0.0.1:$PORT-:22 -device virtio-net-pci,netdev=n0 \
		-device virtio-rng-pci \
		-smbios "type=1,manufacturer=QEMU,product=lab-x86,serial=ds=nocloud;s=http://10.0.2.2:$SEEDPORT/" \
		-smbios "type=17,loc_pfx=DIMM,bank=BANK 0,manufacturer=Lab Memory,serial=LAB00X86,part=LABDDR4-4G,speed=3200" \
		-qmp unix:"$QMP",server=on,wait=off \
		-serial file:"$X86/console.log" -display none -monitor none </dev/null >"$X86/qemu.log" 2>&1 &
	echo "qemu pid $!"
	;;

wait)
	n=0
	until guest true 2>/dev/null; do
		n=$((n + 1))
		if [ $n -ge 90 ]; then
			echo "loop.sh: the guest does not answer" >&2
			exit 1
		fi
		sleep 10
	done
	guest 'uname -r; cat /proc/cmdline'
	;;

prepare)
	guest 'dnf -y -q install rasdaemon sqlite dmidecode >/dev/null && systemctl enable --now rasdaemon.service'
	# mce=print_all: a record left in a bank is printed by the next boot also
	# when /dev/mcelog would take it. panic=0 keeps the restart of mce_panic
	# (30 s); the console goes to the serial port.
	# systemd.default_device_timeout_sec: under emulation udev can take
	# longer than the 90 s systemd waits for the disks of /var and /boot.
	guest 'grubby --update-kernel=ALL --args="mce=print_all console=tty0 console=ttyS0,115200 systemd.default_device_timeout_sec=900" && systemctl enable systemd-pstore.service'
	guest 'mkdir -p /root/my-lab/bin /var/log/journal'
	push "$LOOP/bin/my" root@127.0.0.1:/usr/local/bin/my
	guest 'chown root:root /usr/local/bin/my && chmod 0755 /usr/local/bin/my && my version'
	guest 'reboot' || true
	;;

kdump)
	# kdump: a crash kernel in the guest (kdump-utils), with 256 MiB
	# reserved for it; then a reboot. A panic then boots the crash kernel,
	# which saves vmcore-dmesg.txt below /var/crash and restarts the guest.
	guest 'dnf -y -q install kdump-utils makedumpfile >/dev/null && grubby --update-kernel=ALL --args=crashkernel=256M && systemctl enable kdump.service'
	guest 'systemctl reboot' || true
	;;

inject)
	# inject ID BANK STATUS ADDR MISC [MCGSTATUS]: CPU 0, one bank.
	id=${2:?id}
	bank=${3:?bank}
	status=${4:?status}
	addr=${5:?address}
	misc=${6:?misc}
	mcg=${7:-0x4}
	when=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
	hmp "mce 0 $bank $status $mcg $addr $misc"
	printf '{"id":"%s","time":"%s","inject":"error","bank":%s,"status":"%s","mcg_status":"%s","address":"%s","misc":"%s"}\n' \
		"$id" "$when" "$bank" "$status" "$mcg" "$addr" "$misc" >>"$X86/injected.jsonl"
	;;

run)
	# run PLAN: one boot per line of PLAN ("bank status address misc", "#"
	# comments). Each boot: wait for the guest and for its harvest, note
	# the boot, put the record into the bank of every CPU (-b: the
	# processors broadcast a machine check, as a fatal error of a server
	# does), and wait for the next boot. The kernel panics and restarts the
	# guest 30 s later (mce_panic).
	plan=${2:?plan file}
	out=$X86/runs/$(basename "$plan" .plan)
	mkdir -p "$out"
	cp "$plan" "$out/plan.txt"
	: >"$out/boots.jsonl"
	n=0
	grep -v '^#' "$plan" | grep -v '^$' | while read -r bank status addr misc; do
		n=$((n + 1))
		"$0" wait >/dev/null
		boot=$(waitharvest)
		when=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
		printf '{"boot":%d,"boot_id":"%s","injected":"%s","bank":%s,"status":"%s","address":"%s","misc":"%s"}\n' \
			"$n" "$boot" "$when" "$bank" "$status" "$addr" "$misc" >>"$out/boots.jsonl"
		hmp "mce -b 0 $bank $status 0x4 $addr $misc" >/dev/null
		# The next boot: another boot ID.
		k=0
		while [ "$(guest cat /proc/sys/kernel/random/boot_id 2>/dev/null | tr -d -)" = "$boot" ] || ! guest true 2>/dev/null; do
			k=$((k + 1))
			[ $k -lt 120 ] || { echo "loop.sh: no next boot after boot $n" >&2; exit 1; }
			sleep 10
		done
		echo "boot $n: injected $addr at $when, next boot up"
	done
	"$0" wait >/dev/null
	waitharvest >/dev/null
	echo "done: $out"
	;;

clean)
	# clean: start a loop from nothing kept: no ledger, no archived dumps,
	# no journal of earlier boots. Then reboot.
	guest 'systemctl stop my-harvest.timer; rm -f /var/lib/my/ledger.db /var/lib/my/ledger.db.lock /var/lib/my/ledger.db-wal /var/lib/my/ledger.db-shm;
		rm -rf /var/lib/systemd/pstore/* /var/crash/*; journalctl --rotate >/dev/null; journalctl --vacuum-time=1s >/dev/null; systemctl reboot' || true
	;;

capture)
	# capture NAME: what the guest holds after a loop, laid out as a copy
	# of a host's files, into $X86/cap/NAME.
	name=${2:?name}
	dest=$X86/cap/$name
	rm -rf "$dest"
	mkdir -p "$dest"
	guest 'cd / && tar -cf - --ignore-failed-read --exclude=vmcore var/lib/my var/lib/systemd/pstore var/crash 2>/dev/null' | tar -xf - -C "$dest" || true
	# Files of /proc report the size 0, so tar would copy them empty.
	mkdir -p "$dest/proc/sys/kernel/random" "$dest/etc"
	for f in cmdline version stat uptime cpuinfo sys/kernel/hostname sys/kernel/osrelease sys/kernel/arch sys/kernel/random/boot_id; do
		guest "cat /proc/$f" >"$dest/proc/$f"
	done
	guest 'cat /etc/os-release' >"$dest/etc/os-release"
	guest 'journalctl -o json --no-pager _TRANSPORT=kernel' >"$dest/journal-all.json"
	guest 'journalctl -k -o json --no-pager' >"$dest/journal-k.json"
	guest 'dmesg' >"$dest/dmesg"
	guest 'my preflight; my harvest status' >"$dest/my-status.txt" 2>&1 || true
	guest 'dmidecode -t 17' >"$dest/dmidecode.txt" 2>/dev/null || true
	cp "$X86/console.log" "$dest/console.log"
	echo "captured: $dest"
	;;

stop)
	guest 'systemctl poweroff' 2>/dev/null || true
	n=0
	while pgrep -f "[q]emu-system-x86_64 -name lab-x86" >/dev/null && [ $n -lt 30 ]; do
		n=$((n + 1))
		sleep 5
	done
	pkill -x qemu-system-x86 2>/dev/null || true
	pkill -f "[h]ttp.server $SEEDPORT" 2>/dev/null || true
	;;

status)
	pgrep -af "[q]emu-system-x86_64 -name lab-x86" | cut -c1-160 || echo "lab-x86 does not run"
	awk '/MemAvailable/ {printf "%d GiB available\n", $2/1048576}' /proc/meminfo
	;;

*)
	echo "usage: loop.sh image|boot|prepare|wait|inject|stop|status" >&2
	exit 2
	;;
esac
