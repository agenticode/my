#!/bin/bash
# loop-arm.sh disk|boot|prepare|wait|run|capture|stop|status
#
# Boots that end in a fatal firmware error record, one after another, in an
# aarch64 guest with six DIMMs. QEMU puts a CPER memory error section of
# severity fatal into the guest's generic hardware error source (QMP
# inject-ghes-v2-error, hw/acpi/ghes.c). The kernel prints the record with
# its DIMM, row and column and panics (drivers/acpi/apei/ghes.c,
# __ghes_panic), and restarts ghes_panic_timeout (30 s) later; the guest
# restarts inside the same QEMU process. The panic text goes to EFI
# variables (efi_pstore, drivers/firmware/efi/efi-pstore.c, which the
# Fedora kernel registers only with efi_pstore.pstore_disable=0), the next
# boot's systemd-pstore archives it, and my harvest reads the archive.
#
#   disk      a copy of the prepared disk of ghes.sh ($LAB/a64) with a host
#             name of its own, and the SMBIOS tables of six DIMMs
#   boot      start the guest; the console of every boot goes to $ARM/console.log
#   prepare   once per disk: efi_pstore on, systemd-pstore, the tool and
#             my harvest install; then a reboot
#   wait      wait until the guest answers
#   run PLAN  one boot per line of PLAN ("slot rank device bank_group bank
#             row column"): wait for the harvest, send the fatal record, wait
#             for the next boot
#   capture NAME  the files of the guest after a loop, as a copy of a host
#   stop      power the guest off
#   status    what runs, and the free memory of the lab host
#
# Ports and names are this lane's own: the guest is lab-arm-loop, ssh on
# 127.0.0.1:2242, its seed on 127.0.0.1:8012.
set -eu
LAB=${LAB:-/root/my-lab}
LOOP=${LOOP:-$LAB/loop}
ARM=$LOOP/arm
A64=$LAB/a64
HERE=$(cd "$(dirname "$0")" && pwd)
PORT=${PORT:-2242}
HTTP=${HTTP:-8012}
NAME=lab-arm-loop
QMP=$ARM/qmp.sock
KEY=$A64/id_ed25519
MYBIN=${MYBIN:-$LOOP/bin/my-arm64}
slots=(A1 B1 C1 D1 E1 F1)
STEP=${1:?usage: loop-arm.sh disk|boot|prepare|wait|run|capture|stop|status}
guest() { ssh -n -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=20 -i "$KEY" -p "$PORT" root@127.0.0.1 "$@"; }
push() { scp -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i "$KEY" -P "$PORT" "$@"; }
running() { pgrep -f "[q]emu-system-aarch64 -name $NAME" >/dev/null; }

# waitguest: wait until the guest answers; an emulated boot takes ten
# minutes or more.
waitguest() {
	for _ in $(seq 1 240); do
		guest true 2>/dev/null && return 0
		running || { echo "loop-arm.sh: qemu ended; see $ARM/qemu.log" >&2; return 1; }
		sleep 10
	done
	echo "loop-arm.sh: the guest does not answer" >&2
	return 1
}

# waitharvest: wait until the harvest of this boot has finished; print the
# boot ID.
waitharvest() {
	local k=0 state
	while :; do
		state=$(guest 'systemctl show my-harvest.service -p ActiveState --value; systemctl show my-harvest.service -p ExecMainExitTimestampMonotonic --value' 2>/dev/null | tr '\n' ' ')
		case "$state" in
		"inactive "[1-9]*) break ;;
		esac
		k=$((k + 1))
		[ $k -lt 180 ] || { echo "loop-arm.sh: the harvest did not finish: $state" >&2; return 1; }
		sleep 10
	done
	guest cat /proc/sys/kernel/random/boot_id | tr -d -
}

case "$STEP" in
disk)
	running && { echo "loop-arm.sh: the guest runs; stop it first" >&2; exit 1; }
	rm -rf "$ARM"
	mkdir -p "$ARM/seed" "$ARM/mnt"
	cp --reflink=auto --sparse=always "$A64/disk.raw" "$ARM/disk.raw"
	cp "$A64/vars.raw" "$ARM/vars.raw"
	loop=$(losetup -fP --show "$ARM/disk.raw")
	mount -o subvol=root "${loop}p2" "$ARM/mnt"
	mount -o subvol=var "${loop}p2" "$ARM/mnt/var"
	printf '%s\n' "$NAME" >"$ARM/mnt/etc/hostname"
	tr -d '-' </proc/sys/kernel/random/uuid >"$ARM/mnt/etc/machine-id"
	find "$ARM/mnt/var/log/journal" -mindepth 1 -delete
	rm -f "$ARM"/mnt/var/lib/rasdaemon/ras-mc_event.db*
	rm -rf "$ARM/mnt/var/lib/my" "$ARM/mnt/var/lib/systemd/pstore"/*
	umount "$ARM/mnt/var"
	umount "$ARM/mnt"
	losetup -d "$loop"
	sed "s/^local-hostname: .*/local-hostname: $NAME/" "$A64/seed/meta-data" >"$ARM/seed/meta-data"
	cp "$A64/seed/user-data" "$A64/seed/vendor-data" "$ARM/seed/"
	# Socket 0, channels 0 to 5, one DIMM each, as in ghes.sh multi.
	python3 "$HERE/smbios17.py" --registered --ecc "$ARM/smbios" \
		CPU0_A1,P0_Node0_Channel0_Dimm0,LABL0001 CPU0_B1,P0_Node0_Channel1_Dimm0,LABL0002 \
		CPU0_C1,P0_Node0_Channel2_Dimm0,LABL0003 CPU0_D1,P0_Node0_Channel3_Dimm0,LABL0004 \
		CPU0_E1,P0_Node0_Channel4_Dimm0,LABL0005 CPU0_F1,P0_Node0_Channel5_Dimm0,LABL0006
	echo "disk $ARM/disk.raw"
	;;

boot)
	if running; then
		echo "$NAME runs already"
		exit 0
	fi
	avail=$(awk '/MemAvailable/ {print int($2/1048576)}' /proc/meminfo)
	if [ "$avail" -lt 20 ]; then
		echo "loop-arm.sh: only $avail GiB available; the lab host keeps 8 GiB and the guest may take 12" >&2
		exit 1
	fi
	pgrep -f "[h]ttp.server $HTTP" >/dev/null || (cd "$ARM/seed" && nohup python3 -m http.server "$HTTP" --bind 127.0.0.1 </dev/null >"$ARM/http.log" 2>&1 &)
	rm -f "$QMP"
	dimms=()
	for f in "$ARM"/smbios/dimm*.bin; do dimms+=(-smbios "file=$f"); done
	# 96 GiB as six DIMMs of 16 GiB, allocated as the guest touches it.
	setsid nohup qemu-system-aarch64 -name "$NAME" -machine virt,ras=on,gic-version=3,memory-backend=ram0 -cpu neoverse-n1 -accel tcg,thread=multi -smp 8 -m 96G \
		-object memory-backend-ram,id=ram0,size=96G,reserve=off \
		-drive if=pflash,format=raw,readonly=on,file=/usr/share/edk2/aarch64/QEMU_EFI-pflash.raw \
		-drive if=pflash,format=raw,file="$ARM/vars.raw" \
		-drive if=virtio,format=raw,file="$ARM/disk.raw" \
		-netdev user,id=n0,hostfwd=tcp:127.0.0.1:"$PORT"-:22 -device virtio-net-pci,netdev=n0 \
		-device virtio-rng-pci \
		-smbios "type=1,manufacturer=QEMU,product=$NAME,serial=ds=nocloud;s=http://10.0.2.2:$HTTP/" \
		"${dimms[@]}" \
		-qmp unix:"$QMP",server=on,wait=off \
		-serial file:"$ARM/console.log" -display none -monitor none </dev/null >"$ARM/qemu.log" 2>&1 &
	echo "qemu started"
	;;

wait)
	waitguest
	guest 'uname -r; cat /proc/cmdline'
	;;

prepare)
	waitguest
	push "$MYBIN" root@127.0.0.1:/usr/local/bin/my
	guest 'chown root:root /usr/local/bin/my && chmod 0755 /usr/local/bin/my && my version'
	guest 'grubby --update-kernel=ALL --args="efi_pstore.pstore_disable=0" && systemctl enable systemd-pstore.service && my harvest install'
	guest 'systemctl reboot' || true
	;;

run)
	plan=${2:?plan file}
	out=$ARM/runs/$(basename "$plan" .plan)
	mkdir -p "$out"
	cp "$plan" "$out/plan.txt"
	: >"$out/boots.jsonl"
	: >"$out/injected.jsonl"
	n=0
	grep -v '^#' "$plan" | grep -v '^$' | while read -r slot rank dev bg bank row col; do
		n=$((n + 1))
		waitguest
		boot=$(waitharvest)
		i=0
		for s in "${slots[@]}"; do
			[ "$s" = "$slot" ] && break
			i=$((i + 1))
		done
		base=$((0x40000000 + i * (16 << 30)))
		line=$(python3 "$HERE/ghes.py" --qmp "$QMP" --id "boot-$n" --severity fatal --type 3 --node 0 --card "$i" --module 0 \
			--handle "$((0x1100 + i))" --fru-text "CPU0_$slot" --label "CPU0_$slot" --rank "$rank" --device "$dev" \
			--bank-group "$bg" --bank-address "$bank" --row "$row" --column "$col" --address-base "$base" \
			--note "a fatal multi-bit ECC error; the kernel panics")
		printf '%s\n' "$line" >>"$out/injected.jsonl"
		printf '{"boot":%d,"boot_id":"%s","slot":"CPU0_%s","record":%s}\n' "$n" "$boot" "$slot" "$line" >>"$out/boots.jsonl"
		k=0
		while [ "$(guest cat /proc/sys/kernel/random/boot_id 2>/dev/null | tr -d -)" = "$boot" ] || ! guest true 2>/dev/null; do
			k=$((k + 1))
			[ $k -lt 240 ] || { echo "loop-arm.sh: no next boot after boot $n" >&2; exit 1; }
			sleep 10
		done
		echo "boot $n: CPU0_$slot row $row column $col, next boot up"
	done
	waitguest
	waitharvest >/dev/null
	echo "done: $out"
	;;

capture)
	name=${2:?name}
	dest=$ARM/cap/$name
	rm -rf "$dest"
	mkdir -p "$dest/proc/sys/kernel/random" "$dest/etc"
	guest 'cd / && tar -cf - --ignore-failed-read var/lib/my var/lib/systemd/pstore var/lib/rasdaemon 2>/dev/null' | tar -xf - -C "$dest" || true
	# Files of /proc report the size 0, so tar would copy them empty.
	for f in cmdline version stat uptime cpuinfo sys/kernel/hostname sys/kernel/osrelease sys/kernel/arch sys/kernel/random/boot_id; do
		guest "cat /proc/$f" >"$dest/proc/$f"
	done
	guest 'cat /etc/os-release' >"$dest/etc/os-release"
	guest 'journalctl -k -o json --no-pager' >"$dest/journal-k.json"
	guest 'journalctl -o json --no-pager _TRANSPORT=kernel' >"$dest/journal-all.json"
	guest 'dmesg' >"$dest/dmesg"
	guest 'my preflight; my harvest status' >"$dest/my-status.txt" 2>&1 || true
	guest 'dmidecode -t 17' >"$dest/dmidecode.txt" 2>/dev/null || true
	cp "$ARM/console.log" "$dest/console.log"
	echo "captured: $dest"
	;;

stop)
	guest 'systemctl poweroff' 2>/dev/null || true
	for _ in $(seq 1 60); do
		running || break
		sleep 5
	done
	pkill -f "[q]emu-system-aarch64 -name $NAME" 2>/dev/null || true
	pkill -f "[h]ttp.server $HTTP" 2>/dev/null || true
	;;

status)
	pgrep -af "[q]emu-system-aarch64 -name $NAME" | cut -c1-160 || echo "$NAME does not run"
	awk '/MemAvailable/ {printf "%d GiB available\n", $2/1048576}' /proc/meminfo
	;;

*)
	echo "usage: loop-arm.sh disk|boot|prepare|wait|run|capture|stop|status" >&2
	exit 2
	;;
esac
