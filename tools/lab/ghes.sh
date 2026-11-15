#!/bin/bash
# ghes.sh image|boot|prepare|run|stop
#
# Firmware error records in an aarch64 guest. Firmware-first error handling
# cannot be driven on the lab host: it has no EINJ table and its hypervisor
# hands it no firmware error source. QEMU does build one for an aarch64
# guest ("-machine virt,ras=on"), and its QMP command inject-ghes-v2-error
# puts a record into that source. The guest kernel then does what it does
# on a server whose firmware reports memory errors: ghes prints the record,
# ghes_edac reports it through EDAC with the DIMM the record names, and the
# page is taken offline when the record asks for it.
#
# The guest is the Fedora cloud image for aarch64, emulated without
# hardware virtualization, with the kernel and the rasdaemon package of the
# distribution.
#
#   image     make the guest disk from the cloud image at $LAB/img/*.qcow2
#   boot      start the guest and wait until it answers
#   prepare   once per disk: install rasdaemon, sqlite, dmidecode and a compiler
#   run       inject (see below), capture into $LAB/cap/ghes
#   stop      power the guest off
#
# What "run" injects, all on the one DIMM of the guest:
#
#   row-*     15 corrected errors in row 0x1a2f of one device and bank, on five
#             columns and two bits, 3 of them found by the scrubber; then 2 in
#             the row next to it
#   burst     6 corrected errors at one cell of another rank within a second
#   thr       1 corrected error with the flag "error threshold exceeded": the
#             kernel moves the page elsewhere
#   ue-1      1 uncorrected recoverable error (multi-bit ECC) on a page in use
#   ue-2      1 uncorrected recoverable error found by the scrubber
set -eu
LAB=${LAB:-/root/my-lab}
HERE=$(cd "$(dirname "$0")" && pwd)
A64=$LAB/a64
PORT=${PORT:-2222}
KEY=$A64/id_ed25519
QMP=$A64/qmp.sock
STEP=${1:?usage: ghes.sh image|boot|prepare|run|stop}

guest() { ssh -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=20 -i "$KEY" -p "$PORT" root@127.0.0.1 "$@"; }
push() { scp -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i "$KEY" -P "$PORT" "$@"; }

case "$STEP" in
image)
	mkdir -p "$A64/seed"
	[ -f "$KEY" ] || ssh-keygen -q -t ed25519 -N "" -f "$KEY" -C lab-a64
	base=$(ls "$LAB"/img/Fedora-Cloud-Base-Generic-*.aarch64.qcow2 | head -1)
	qemu-img convert -O raw "$base" "$A64/disk.raw"
	qemu-img resize -q -f raw "$A64/disk.raw" 20G
	# Emulated, the guest needs minutes to find its disks: longer than
	# systemd waits by default. SELinux only reports, so that files copied
	# in from outside can be run.
	loop=$(losetup -fP --show "$A64/disk.raw")
	mkdir -p "$A64/mnt"
	mount -o subvol=boot "${loop}p2" "$A64/mnt"
	for f in "$A64"/mnt/loader/entries/*.conf; do
		sed 's|^options \(.*\)$|options \1 systemd.default_device_timeout_sec=900 systemd.default_timeout_start_sec=900 enforcing=0|' "$f" >"$A64/entry.conf"
		cat "$A64/entry.conf" >"$f" # in place: the file keeps its SELinux label
	done
	umount "$A64/mnt"
	losetup -d "$loop"
	printf 'instance-id: lab-a64-1\nlocal-hostname: lab-arm\n' >"$A64/seed/meta-data"
	: >"$A64/seed/vendor-data"
	{
		echo "#cloud-config"
		echo "disable_root: false"
		echo "ssh_pwauth: false"
		echo "users:"
		echo "  - name: root"
		echo "    ssh_authorized_keys:"
		echo "      - $(cat "$KEY.pub")"
	} >"$A64/seed/user-data"
	cp /usr/share/edk2/aarch64/vars-template-pflash.raw "$A64/vars.raw"
	;;

boot)
	cd "$A64"
	# cloud-init fetches its configuration from the lab host.
	pgrep -f "http.server 8000" >/dev/null || (cd seed && nohup python3 -m http.server 8000 --bind 127.0.0.1 >../http.log 2>&1 </dev/null &)
	rm -f "$QMP" console.log
	# One DIMM of 8 GB with a serial number of its own, a firmware error
	# source (ras=on), and the QMP socket the records are sent through.
	nohup qemu-system-aarch64 -name lab-arm -machine virt,ras=on,gic-version=3 -cpu neoverse-n1 -accel tcg,thread=multi -smp 8 -m 8G \
		-drive if=pflash,format=raw,readonly=on,file=/usr/share/edk2/aarch64/QEMU_EFI-pflash.raw \
		-drive if=pflash,format=raw,file=vars.raw \
		-drive if=virtio,format=raw,file=disk.raw \
		-netdev user,id=n0,hostfwd=tcp:127.0.0.1:"$PORT"-:22 -device virtio-net-pci,netdev=n0 \
		-device virtio-rng-pci \
		-smbios "type=1,manufacturer=QEMU,product=lab-arm,serial=ds=nocloud;s=http://10.0.2.2:8000/" \
		-smbios "type=17,loc_pfx=DIMM,bank=BANK 0,manufacturer=Lab Memory,serial=LAB00A64,part=LABDDR4-8G,speed=3200" \
		-qmp unix:"$QMP",server=on,wait=off \
		-serial file:console.log -display none -monitor none >qemu.log 2>&1 </dev/null &
	echo "qemu started; waiting for the guest (emulated: about ten minutes)"
	for _ in $(seq 1 240); do
		if guest true 2>/dev/null; then
			guest 'uname -r; uptime'
			exit 0
		fi
		sleep 10
	done
	echo "ghes.sh: the guest did not answer; see $A64/console.log" >&2
	exit 1
	;;

prepare)
	guest 'dnf -y -q install rasdaemon sqlite dmidecode gcc >/root/dnf.log 2>&1; rpm -q rasdaemon sqlite dmidecode gcc; systemctl enable rasdaemon; mkdir -p /root/my-lab/tools /root/my-lab/bin /root/my-lab/tmp /root/my-lab/cap'
	push "$HERE"/capture.sh "$HERE"/plan.py "$HERE"/hwpoison.c root@127.0.0.1:/root/my-lab/tools/
	push "$LAB/bin/my-arm64" root@127.0.0.1:/root/my-lab/bin/my
	[ -f "$LAB/anon.key" ] && push "$LAB/anon.key" root@127.0.0.1:/root/my-lab/anon.key
	guest 'cd /root/my-lab && gcc -O2 -o bin/hwpoison tools/hwpoison.c && ./bin/my version && sync'
	;;

run)
	name=ghes
	WORK=$LAB/tmp
	mkdir -p "$WORK"
	: >"$WORK/injected.jsonl"
	guest 'systemctl is-active rasdaemon; dmidecode -t 17 | grep -E "^Handle|Locator|Serial"'
	ghes() { python3 "$HERE/ghes.py" --qmp "$QMP" "$@" | tee -a "$WORK/injected.jsonl"; }
	# Where the errors sit: the one DIMM, named by its SMBIOS handle.
	dimm=(--node 0 --card 0 --module 0 --handle 0x1100 --fru-text "DIMM 0" --label "DIMM 0")
	cell=(--rank 0 --device 7 --bank-group 1 --bank-address 2)

	# A row fault: one row, five columns, two bits of one device. The
	# addresses are made up; nothing is done to their pages.
	i=0
	for spec in "0x010 5 2" "0x118 5 2" "0x238 6 2" "0x2a0 5 2" "0x3f0 6 2" "0x118 5 2" "0x238 6 2" "0x010 5 13" "0x2a0 5 2" \
		"0x238 6 13" "0x3f0 6 2" "0x118 5 2" "0x238 6 2" "0x010 5 13" "0x2a0 5 2"; do
		set -- $spec
		i=$((i + 1))
		ghes --id "row-$i" --severity corrected --type "$3" "${dimm[@]}" "${cell[@]}" --row 0x1a2f --column "$1" --bit "$2" \
			--address "$(printf '0x%x' $((0x100000000 + $1 * 0x1000 + $2 * 0x40)))"
		sleep 3
	done
	for col in 0x118 0x238; do
		i=$((i + 1))
		ghes --id "row-$i" --severity corrected --type 2 "${dimm[@]}" "${cell[@]}" --row 0x1a30 --column "$col" --bit 5 \
			--address "$(printf '0x%x' $((0x100400000 + col * 0x1000)))"
		sleep 3
	done

	# Six times the same cell within a second: the kernel prints a firmware
	# error record of a corrected error at most twice in five seconds.
	ghes --id burst --count 6 --gap 0.15 --severity corrected --type 2 "${dimm[@]}" --rank 1 --device 3 --bank-group 0 --bank-address 1 \
		--row 0x0456 --column 0x0c8 --bit 2 --address 0x180045640
	sleep 6

	# Three pages in use by a process of the guest. It waits for a line on
	# a named pipe that a sleeping process keeps open.
	guest 'cd /root/my-lab && rm -f tmp/hold.* && mkfifo tmp/hold.in &&
		(nohup sh -c "sleep 900 >tmp/hold.in" >/dev/null 2>&1 &) &&
		(nohup sh -c "./bin/hwpoison hold:3 <tmp/hold.in >tmp/hold.out 2>tmp/hold.err" >/dev/null 2>&1 &)
		sleep 4; cat tmp/hold.out' >"$WORK/hold.jsonl"
	pfn=()
	n=0
	while read -r line; do
		n=$((n + 1))
		pfn[n]=$(sed 's/.*"pfn":"\(0x[0-9a-f]*\)".*/\1/' <<<"$line")
	done <"$WORK/hold.jsonl"
	[ "$n" = 3 ] || { echo "ghes.sh: the guest holds $n pages, not 3" >&2; exit 1; }
	addr() { printf '0x%x' $(($1 * 4096 + $2)); }

	ghes --id thr --severity corrected --threshold --type 2 "${dimm[@]}" --rank 0 --device 2 --bank-group 0 --bank-address 3 \
		--row 0x0777 --column 0x040 --bit 1 --address "$(addr "${pfn[1]}" 0x100)" --note "the kernel takes the page offline soft"
	printf '{"id":"thr-offline","inject":"page-offline","how":"soft","count":1,"address":"%s","note":"asked for by the record of thr"}\n' "$(addr "${pfn[1]}" 0)" >>"$WORK/injected.jsonl"
	sleep 4
	ghes --id ue-1 --severity recoverable --type 3 "${dimm[@]}" --rank 0 --device 9 --bank-group 2 --bank-address 0 \
		--row 0x2001 --column 0x1f0 --address "$(addr "${pfn[2]}" 0x200)"
	printf '{"id":"ue-1-offline","inject":"page-offline","how":"hard","count":1,"address":"%s","note":"by the kernel after ue-1"}\n' "$(addr "${pfn[2]}" 0)" >>"$WORK/injected.jsonl"
	sleep 4
	ghes --id ue-2 --severity recoverable --type 14 "${dimm[@]}" --rank 1 --device 11 --bank-group 3 --bank-address 1 \
		--row 0x3ffe --column 0x008 --address "$(addr "${pfn[3]}" 0x300)"
	printf '{"id":"ue-2-offline","inject":"page-offline","how":"hard","count":1,"address":"%s","note":"by the kernel after ue-2"}\n' "$(addr "${pfn[3]}" 0)" >>"$WORK/injected.jsonl"
	sleep 5

	# Let the holder read its pages once more and say what became of them.
	guest 'cd /root/my-lab && echo >tmp/hold.in; sleep 3; grep held tmp/hold.out; pkill -f "sleep 900" || true' >"$WORK/held.jsonl" || true

	python3 "$HERE/plan.py" --scenario "$name" \
		--summary "firmware error records in an aarch64 guest: 24 corrected and 2 uncorrected memory errors on one DIMM" \
		--method "QMP inject-ghes-v2-error of QEMU: CPER memory error sections through the generic hardware error source" \
		--set "kernel=$(guest uname -r)" --set "rasdaemon=$(guest 'rasdaemon --version' | awk '{print $2; exit}')" \
		--set "machine=QEMU $(qemu-system-aarch64 --version | awk '{print $4; exit}') virt, ras=on, emulated without hardware virtualization" \
		<"$WORK/injected.jsonl" >"$WORK/plan.yaml"
	guest 'cd /root/my-lab && LAB=/root/my-lab MY_KEY=/root/my-lab/anon.key ./tools/capture.sh ghes --formats >tmp/capture.out 2>&1; tail -3 tmp/capture.out'
	rm -rf "$LAB/cap/$name"
	mkdir -p "$LAB/cap"
	guest 'cd /root/my-lab/cap && tar cf - ghes' | tar xf - -C "$LAB/cap"
	cp "$WORK/plan.yaml" "$LAB/cap/$name/plan.yaml"
	cp "$WORK/injected.jsonl" "$WORK/held.jsonl" "$LAB/cap/$name/extra/"
	echo "captured: $LAB/cap/$name"
	;;

stop)
	guest 'systemctl poweroff' || true
	;;

*)
	echo "usage: ghes.sh image|boot|prepare|run|stop" >&2
	exit 2
	;;
esac
