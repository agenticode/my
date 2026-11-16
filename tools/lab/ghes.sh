#!/bin/bash
# ghes.sh image|boot|prepare|run|clock-step|multi|stop
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
#   clock-step  set the clock back by 10 seconds after rasdaemon started,
#             inject fewer errors, capture into $LAB/cap/ghes-clock-step
#   multi     another guest with six DIMMs, booted three times a day apart,
#             a different fault on four of the DIMMs; capture into
#             $LAB/cap/ghes-multi (see "What multi injects" below)
#   stop      power the guest off
#
# What "run" injects, all on the one DIMM of the guest:
#
#   row-*     15 corrected errors in row 0x1a2f of one device and bank, on five
#             columns and two bits, 3 of them found by the scrubber; then 2 in
#             the row next to it
#   burst     6 corrected errors at one cell of another rank within a second
#   thr       1 corrected error with the flag "error threshold exceeded": the
#             kernel takes the page offline soft, and nothing records that
#   ue-1      1 uncorrected recoverable error (multi-bit ECC) on a page in use
#   ue-2      1 uncorrected recoverable error found by the scrubber
#
# What "clock-step" injects: 6 corrected errors in the row, two of the
# addresses twice; 4 at one cell within a second; 1 uncorrected recoverable
# error on a page in use. This guest has no machine check records, so
# nothing in the rasdaemon database carries the kernel's time.
#
# What "multi" injects. The guest is a copy of the prepared disk with its
# own name (lab-arm6), its own machine ID, an empty journal and no rasdaemon
# database, and 96 GiB of memory: QEMU describes that as six DIMMs of 16 GiB.
# QEMU's own type 17 entries would give all six one serial number, so
# smbios17.py writes them with a serial each. Slot CPU0_A1 to CPU0_F1 are on
# channels 0 to 5 of socket 0; every record names its DIMM by the SMBIOS
# handle, which is what ghes_edac and the kernel's "DIMM location" go by.
# The time of a firmware record is set by the guest's clock when the kernel
# handles it: the CPER time stamp is printed and stored nowhere else. So the
# guest is booted three times, its clock two days, one day and zero days
# behind the lab host's, chronyd disabled, and each boot gets the errors of
# its day (the table multi_plan below):
#
#   CPU0_A1   3 corrected errors at one cell, one a day
#   CPU0_B1   10 corrected errors on one column of one bank, 9 rows
#   CPU0_C1   nothing
#   CPU0_D1   10 corrected errors on one row of one bank, 8 columns, then
#             1 uncorrected error found by the scrubber on that row; its
#             page is free and the kernel takes it offline
#   CPU0_E1   12 corrected errors scattered over one bank, no two on a row
#             or a column
#   CPU0_F1   nothing
#
# The addresses are made up from the location (ghes.py --address-base); the
# page of the uncorrected error is checked to be free before.
set -eu
LAB=${LAB:-/root/my-lab}
HERE=$(cd "$(dirname "$0")" && pwd)
A64=$LAB/a64
PORT=${PORT:-2222}
KEY=$A64/id_ed25519
QMP=$A64/qmp.sock
STEP=${1:?usage: ghes.sh image|boot|prepare|run|clock-step|multi|stop}
WORK=$LAB/tmp

guest() { ssh -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=20 -i "$KEY" -p "$PORT" root@127.0.0.1 "$@"; }
push() { scp -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i "$KEY" -P "$PORT" "$@"; }

# What the steps that inject share.
ghes() { python3 "$HERE/ghes.py" --qmp "$QMP" "$@" | tee -a "$WORK/injected.jsonl"; }
# Where the errors sit: the one DIMM, named by its SMBIOS handle.
dimm=(--node 0 --card 0 --module 0 --handle 0x1100 --fru-text "DIMM 0" --label "DIMM 0")
cell=(--rank 0 --device 7 --bank-group 1 --bank-address 2)
addr() { printf '0x%x' $(($1 * 4096 + $2)); }

# begin: an empty list of injections, and rasdaemon at work.
begin() {
	mkdir -p "$WORK"
	: >"$WORK/injected.jsonl"
	guest 'systemctl is-active rasdaemon; dmidecode -t 17 | grep -E "^Handle|Locator|Serial"'
}

# hold N: a process of the guest takes N pages in use and waits for a line
# on a named pipe that a sleeping process keeps open. pfn[1..N] are the
# page frames.
hold() {
	guest "cd /root/my-lab && rm -f tmp/hold.* && mkfifo tmp/hold.in &&
		(nohup sh -c 'sleep 900 >tmp/hold.in' >/dev/null 2>&1 &) &&
		(nohup sh -c './bin/hwpoison hold:$1 <tmp/hold.in >tmp/hold.out 2>tmp/hold.err' >/dev/null 2>&1 &)
		sleep 4; cat tmp/hold.out" >"$WORK/hold.jsonl"
	pfn=()
	n=0
	while read -r line; do
		n=$((n + 1))
		pfn[n]=$(sed 's/.*"pfn":"\(0x[0-9a-f]*\)".*/\1/' <<<"$line")
	done <"$WORK/hold.jsonl"
	[ "$n" = "$1" ] || { echo "ghes.sh: the guest holds $n pages, not $1" >&2; exit 1; }
}

# held: the holder reads its pages once more and says what became of them.
held() {
	guest 'cd /root/my-lab && echo >tmp/hold.in; sleep 3; grep held tmp/hold.out; pkill -f "sleep 900" || true' >"$WORK/held.jsonl" || true
}

# finish NAME SUMMARY [capture.sh options]: the plan, the capture in the
# guest and its copy on the lab host.
finish() {
	name=$1
	summary=$2
	shift 2
	python3 "$HERE/plan.py" --scenario "$name" --summary "$summary" \
		--method "QMP inject-ghes-v2-error of QEMU: CPER memory error sections through the generic hardware error source" \
		--set "kernel=$(guest uname -r)" --set "rasdaemon=$(guest 'rasdaemon --version' | awk '{print $2; exit}')" \
		--set "machine=QEMU $(qemu-system-aarch64 --version | awk '{print $4; exit}') virt, ras=on, emulated without hardware virtualization" \
		<"$WORK/injected.jsonl" >"$WORK/plan.yaml"
	guest "cd /root/my-lab && LAB=/root/my-lab MY_KEY=/root/my-lab/anon.key ./tools/capture.sh $name $* >tmp/capture.out 2>&1; tail -3 tmp/capture.out"
	rm -rf "$LAB/cap/$name"
	mkdir -p "$LAB/cap"
	guest "cd /root/my-lab/cap && tar cf - $name" | tar xf - -C "$LAB/cap"
	cp "$WORK/plan.yaml" "$LAB/cap/$name/plan.yaml"
	for f in injected.jsonl held.jsonl; do
		if [ -f "$WORK/$f" ]; then cp "$WORK/$f" "$LAB/cap/$name/extra/"; fi
	done
	echo "captured: $LAB/cap/$name"
}

# The guest of "multi" (see the top of this file): a directory, a port, a
# QMP socket and a web server for its cloud-init data of its own, so that
# the disk of the guest above is never touched.
M=$LAB/a64-multi
MPORT=${MPORT:-2223}
MHTTP=${MHTTP:-8001}
MYBIN=${MYBIN:-$LAB/bin/my-arm64}
MNAME=lab-arm6
slots=(A1 B1 C1 D1 E1 F1)

# multi_plan: the errors of "multi" in the order they are sent: day, id,
# slot (index into slots), CPER error type, severity, rank, device, bank
# group, bank, row, column, bit ("-" for none).
multi_plan() {
	cat <<'PLAN'
1 d1-row-1 3 2 corrected 0 3 2 1 0x2b41 0x0a8 2
1 e1-bank-1 4 2 corrected 0 6 3 0 0x07a3 0x038 5
1 b1-col-1 1 2 corrected 1 5 0 3 0x0412 0x1c8 3
1 a1-cell-1 0 2 corrected 1 2 1 2 0x4d2e 0x0f8 4
1 e1-bank-2 4 2 corrected 0 6 3 0 0x1e52 0x2f0 1
1 d1-row-2 3 2 corrected 0 3 2 1 0x2b41 0x2c0 6
1 b1-col-2 1 2 corrected 1 5 0 3 0x2c55 0x1c8 3
1 e1-bank-3 4 13 corrected 0 6 3 0 0x33c8 0x110 7
1 d1-row-3 3 2 corrected 0 3 2 1 0x2b41 0x1f0 2
1 b1-col-3 1 2 corrected 1 5 0 3 0x6b7a 0x1c8 3
1 e1-bank-4 4 2 corrected 0 6 3 0 0x4a19 0x3c8 0
2 b1-col-4 1 2 corrected 1 5 0 3 0x1a07 0x1c8 3
2 d1-row-4 3 2 corrected 0 3 2 1 0x2b41 0x310 4
2 e1-bank-5 4 2 corrected 0 6 3 0 0x5f07 0x0d0 3
2 a1-cell-2 0 2 corrected 1 2 1 2 0x4d2e 0x0f8 4
2 d1-row-5 3 13 corrected 0 3 2 1 0x2b41 0x0a8 2
2 e1-bank-6 4 2 corrected 0 6 3 0 0x6c84 0x268 6
2 b1-col-5 1 2 corrected 1 5 0 3 0x9c31 0x1c8 3
2 e1-bank-7 4 2 corrected 0 6 3 0 0x8a2b 0x1a0 2
2 d1-row-6 3 2 corrected 0 3 2 1 0x2b41 0x3a8 5
2 b1-col-6 1 2 corrected 1 5 0 3 0x2c55 0x1c8 3
2 e1-bank-8 4 2 corrected 0 6 3 0 0x9d10 0x330 4
3 d1-row-7 3 2 corrected 0 3 2 1 0x2b41 0x158 1
3 e1-bank-9 4 2 corrected 0 6 3 0 0xa4e6 0x078 6
3 b1-col-7 1 2 corrected 1 5 0 3 0x3e10 0x1c8 3
3 d1-row-8 3 2 corrected 0 3 2 1 0x2b41 0x238 7
3 a1-cell-3 0 2 corrected 1 2 1 2 0x4d2e 0x0f8 4
3 e1-bank-10 4 2 corrected 0 6 3 0 0xb7f1 0x2a8 1
3 b1-col-8 1 2 corrected 1 5 0 3 0x5203 0x1c8 3
3 d1-row-9 3 2 corrected 0 3 2 1 0x2b41 0x2c0 6
3 e1-bank-11 4 2 corrected 0 6 3 0 0xc93d 0x150 5
3 b1-col-9 1 2 corrected 1 5 0 3 0x8004 0x1c8 3
3 d1-row-10 3 2 corrected 0 3 2 1 0x2b41 0x060 0
3 b1-col-10 1 2 corrected 1 5 0 3 0xd0e9 0x1c8 3
3 e1-bank-12 4 2 corrected 0 6 3 0 0xe0a2 0x3f8 2
3 d1-ue 3 14 recoverable 0 3 2 1 0x2b41 0x1f0 -
PLAN
}

# running: is a guest of this file running?
running() { pgrep -f "qemu-system-aarch64 -name $1" >/dev/null; }

# multi_disk: the guest's disk, a copy of the prepared one, with what makes
# it a host of its own, and its six DIMMs.
multi_disk() {
	if running lab-arm; then
		echo "ghes.sh: a guest is running; stop it first" >&2
		exit 1
	fi
	rm -rf "$M"
	mkdir -p "$M/seed" "$M/tmp" "$M/mnt"
	cp --reflink=auto --sparse=always "$A64/disk.raw" "$M/disk.raw"
	cp "$A64/vars.raw" "$M/vars.raw"
	loop=$(losetup -fP --show "$M/disk.raw")
	mount -o subvol=root "${loop}p2" "$M/mnt"
	mount -o subvol=var "${loop}p2" "$M/mnt/var"
	printf '%s\n' "$MNAME" >"$M/mnt/etc/hostname"
	tr -d '-' </proc/sys/kernel/random/uuid >"$M/mnt/etc/machine-id"
	rm -f "$M/mnt/etc/systemd/system/multi-user.target.wants/chronyd.service"
	find "$M/mnt/var/log/journal" -mindepth 1 -delete
	rm -f "$M"/mnt/var/lib/rasdaemon/ras-mc_event.db*
	find "$M/mnt/root/my-lab/cap" "$M/mnt/root/my-lab/tmp" -mindepth 1 -delete
	umount "$M/mnt/var"
	umount "$M/mnt"
	losetup -d "$loop"
	sed "s/^local-hostname: .*/local-hostname: $MNAME/" "$A64/seed/meta-data" >"$M/seed/meta-data"
	cp "$A64/seed/user-data" "$A64/seed/vendor-data" "$M/seed/"
	# Socket 0, channels 0 to 5, one DIMM each. The EDAC label ghes_edac
	# makes is bank locator and device locator, at most 31 characters.
	python3 "$HERE/smbios17.py" --registered --ecc "$M/smbios" \
		CPU0_A1,P0_Node0_Channel0_Dimm0,LABM0001 CPU0_B1,P0_Node0_Channel1_Dimm0,LABM0002 \
		CPU0_C1,P0_Node0_Channel2_Dimm0,LABM0003 CPU0_D1,P0_Node0_Channel3_Dimm0,LABM0004 \
		CPU0_E1,P0_Node0_Channel4_Dimm0,LABM0005 CPU0_F1,P0_Node0_Channel5_Dimm0,LABM0006
}

# multi_boot DAY: start the guest with its clock 3 - DAY days behind, and
# wait until it answers. The memory is allocated as the guest touches it.
multi_boot() {
	rtc=$(date -u -d "@$(($(date +%s) - (3 - $1) * 86400))" +%Y-%m-%dT%H:%M:%S)
	pgrep -f "[h]ttp.server $MHTTP" >/dev/null || (cd "$M/seed" && nohup python3 -m http.server "$MHTTP" --bind 127.0.0.1 >../http.log 2>&1 </dev/null &)
	rm -f "$QMP"
	dimms=()
	for f in "$M"/smbios/dimm*.bin; do dimms+=(-smbios "file=$f"); done
	nohup qemu-system-aarch64 -name "$MNAME" -machine virt,ras=on,gic-version=3,memory-backend=ram0 -cpu neoverse-n1 -accel tcg,thread=multi -smp 8 -m 96G \
		-object memory-backend-ram,id=ram0,size=96G,reserve=off \
		-rtc base="$rtc" \
		-drive if=pflash,format=raw,readonly=on,file=/usr/share/edk2/aarch64/QEMU_EFI-pflash.raw \
		-drive if=pflash,format=raw,file="$M/vars.raw" \
		-drive if=virtio,format=raw,file="$M/disk.raw" \
		-netdev user,id=n0,hostfwd=tcp:127.0.0.1:"$PORT"-:22 -device virtio-net-pci,netdev=n0 \
		-device virtio-rng-pci \
		-smbios "type=1,manufacturer=QEMU,product=$MNAME,serial=ds=nocloud;s=http://10.0.2.2:$MHTTP/" \
		"${dimms[@]}" \
		-qmp unix:"$QMP",server=on,wait=off \
		-serial file:"$M/console-$1.log" -display none -monitor none >"$M/qemu-$1.log" 2>&1 </dev/null &
	echo "day $1: qemu started, the guest's clock at $rtc; waiting for it (emulated: ten minutes or more)"
	for _ in $(seq 1 240); do
		if guest true 2>/dev/null; then
			guest 'uname -r; date -u; uptime'
			return 0
		fi
		if ! running "$MNAME"; then
			echo "ghes.sh: qemu ended; see $M/qemu-$1.log" >&2
			exit 1
		fi
		sleep 10
	done
	echo "ghes.sh: the guest did not answer; see $M/console-$1.log" >&2
	exit 1
}

# multi_offset: the guest's clock minus the lab host's, in seconds, from
# the fastest of a few round trips over one ssh connection.
multi_offset() {
	python3 - "$KEY" "$PORT" <<'PY'
import subprocess, sys, time
ssh = subprocess.Popen(["ssh", "-q", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-i", sys.argv[1],
                        "-p", sys.argv[2], "root@127.0.0.1", "while read -r _; do date +%s.%N; done"],
                       stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
best = None
for _ in range(8):
    t0 = time.time()
    ssh.stdin.write("\n")
    ssh.stdin.flush()
    guest = float(ssh.stdout.readline())
    t1 = time.time()
    if best is None or t1 - t0 < best[0]:
        best = (t1 - t0, guest - (t0 + t1) / 2)
ssh.stdin.close()
ssh.wait()
print("%.3f" % best[1])
PY
}

# mghes ARGS: ghes.py, stopping the run when it fails.
mghes() {
	local line
	line=$(python3 "$HERE/ghes.py" --qmp "$QMP" "$@") || exit 1
	echo "$line" | tee -a "$WORK/injected.jsonl"
}

# pageflags PFN: the flags /proc/kpageflags of the guest has for a page.
pageflags() { guest "dd if=/proc/kpageflags bs=8 skip=$1 count=1 status=none | od -An -tu8" | tr -d ' '; }

# multi_send DAY: the errors of the day.
multi_send() {
	local off day id slot type sev rank dev bg bank row col bit base args addr page flags
	off=$(multi_offset)
	echo "day $1: the guest's clock is $off s from the lab host's"
	printf '{"day":%s,"boot_id":"%s","clock_offset":%s}\n' "$1" "$(guest cat /proc/sys/kernel/random/boot_id)" "$off" >>"$WORK/clock.jsonl"
	while read -r day id slot type sev rank dev bg bank row col bit; do
		[ "$day" = "$1" ] || continue
		base=$((0x40000000 + slot * (16 << 30)))
		args=(--id "$id" --severity "$sev" --type "$type" --node 0 --card "$slot" --module 0 --handle "$((0x1100 + slot))"
			--fru-text "CPU0_${slots[slot]}" --label "CPU0_${slots[slot]}" --rank "$rank" --device "$dev" --bank-group "$bg"
			--bank-address "$bank" --row "$row" --column "$col" --address-base "$base" --clock-offset "$off")
		[ "$bit" = - ] || args+=(--bit "$bit")
		if [ "$sev" = recoverable ]; then
			# The kernel takes the page of an uncorrected error offline. It
			# must be a free page: one in use would end a process or hit
			# the kernel.
			addr=$((base + ((row * 2 + rank) * 16 + bg * 4 + bank) * 8192 + col * 8))
			page=$((addr >> 12))
			flags=$(pageflags "$page")
			echo "page $(printf '0x%x' "$page") before: flags $flags" >>"$WORK/page.txt"
			case "$flags" in
			0 | 1024) ;; # free: no flag, or the first page of a free block
			*)
				echo "ghes.sh: page $(printf '0x%x' "$page") is in use (flags $flags)" >&2
				exit 1
				;;
			esac
			mghes "${args[@]}" --note "the page is free; the kernel takes it offline"
			printf '{"id":"%s-offline","inject":"page-offline","how":"hard","count":1,"address":"0x%x","note":"by the kernel after %s"}\n' \
				"$id" $((page << 12)) "$id" >>"$WORK/injected.jsonl"
			sleep 5
			echo "page $(printf '0x%x' "$page") after: flags $(pageflags "$page")" >>"$WORK/page.txt"
		else
			mghes "${args[@]}"
		fi
		sleep 3
	done < <(multi_plan)
}

# multi_stop: power the guest off and wait until qemu is gone.
multi_stop() {
	guest 'systemctl poweroff' || true
	for _ in $(seq 1 60); do
		running "$MNAME" || return 0
		sleep 5
	done
	echo "ghes.sh: the guest did not power off" >&2
	exit 1
}

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
	# The emulated guest downloads slowly: metadata in the plain form, and a few tries.
	guest 'for try in 1 2 3; do
			dnf -y -q --setopt=zchunk=false --disablerepo="*" --enablerepo=fedora --enablerepo=updates install rasdaemon sqlite dmidecode gcc >/root/dnf.log 2>&1 && break
			dnf clean all >/dev/null 2>&1
		done
		rpm -q rasdaemon sqlite dmidecode gcc; systemctl enable --now rasdaemon; mkdir -p /root/my-lab/tools /root/my-lab/bin /root/my-lab/tmp /root/my-lab/cap'
	push "$HERE"/capture.sh "$HERE"/plan.py "$HERE"/hwpoison.c root@127.0.0.1:/root/my-lab/tools/
	push "$LAB/bin/my-arm64" root@127.0.0.1:/root/my-lab/bin/my
	[ -f "$LAB/anon.key" ] && push "$LAB/anon.key" root@127.0.0.1:/root/my-lab/anon.key
	guest 'cd /root/my-lab && gcc -O2 -o bin/hwpoison tools/hwpoison.c && ./bin/my version && sync'
	;;

run)
	begin

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

	hold 3
	ghes --id thr --severity corrected --threshold --type 2 "${dimm[@]}" --rank 0 --device 2 --bank-group 0 --bank-address 3 \
		--row 0x0777 --column 0x040 --bit 1 --address "$(addr "${pfn[1]}" 0x100)" \
		--note "the kernel moves the content of the page elsewhere and retires it, and leaves no record of that: no line in the kernel log, no trace event (extra/held.jsonl shows the page frame before and after)"
	sleep 4
	ghes --id ue-1 --severity recoverable --type 3 "${dimm[@]}" --rank 0 --device 9 --bank-group 2 --bank-address 0 \
		--row 0x2001 --column 0x1f0 --address "$(addr "${pfn[2]}" 0x200)"
	printf '{"id":"ue-1-offline","inject":"page-offline","how":"hard","count":1,"address":"%s","note":"by the kernel after ue-1"}\n' "$(addr "${pfn[2]}" 0)" >>"$WORK/injected.jsonl"
	sleep 4
	ghes --id ue-2 --severity recoverable --type 14 "${dimm[@]}" --rank 1 --device 11 --bank-group 3 --bank-address 1 \
		--row 0x3ffe --column 0x008 --address "$(addr "${pfn[3]}" 0x300)"
	printf '{"id":"ue-2-offline","inject":"page-offline","how":"hard","count":1,"address":"%s","note":"by the kernel after ue-2"}\n' "$(addr "${pfn[3]}" 0)" >>"$WORK/injected.jsonl"
	sleep 5
	held

	finish ghes "firmware error records in an aarch64 guest: 24 corrected and 2 uncorrected memory errors on one DIMM" --formats
	;;

clock-step)
	# rasdaemon starts with an empty database and measures once how far
	# the wall clock is from the uptime. Then the clock is set back, as NTP
	# does at boot when the hardware clock ran ahead. Nothing sets it again.
	guest 'systemctl stop chronyd 2>/dev/null; systemctl stop rasdaemon; rm -f /var/lib/rasdaemon/ras-mc_event.db; systemctl start rasdaemon
		for _ in $(seq 1 30); do [ -s /var/lib/rasdaemon/ras-mc_event.db ] && break; sleep 2; done; sleep 10'
	begin
	guest 'date -s "@$(($(date +%s) - 10))" >/dev/null; echo "clock set back by 10 seconds at $(date -u +%Y-%m-%dT%H:%M:%SZ)"' >"$WORK/clock-step.txt"
	sleep 5

	i=0
	for spec in "0x010 5" "0x118 5" "0x238 6" "0x010 5" "0x2a0 5" "0x118 5"; do
		set -- $spec
		i=$((i + 1))
		ghes --id "row-$i" --severity corrected --type 2 "${dimm[@]}" "${cell[@]}" --row 0x1a2f --column "$1" --bit "$2" \
			--address "$(printf '0x%x' $((0x100000000 + $1 * 0x1000 + $2 * 0x40)))"
		sleep 3
	done
	ghes --id burst --count 4 --gap 0.15 --severity corrected --type 2 "${dimm[@]}" --rank 1 --device 3 --bank-group 0 --bank-address 1 \
		--row 0x0456 --column 0x0c8 --bit 2 --address 0x180045640
	sleep 6

	hold 1
	ghes --id ue-1 --severity recoverable --type 3 "${dimm[@]}" --rank 0 --device 9 --bank-group 2 --bank-address 0 \
		--row 0x2001 --column 0x1f0 --address "$(addr "${pfn[1]}" 0x200)"
	printf '{"id":"ue-1-offline","inject":"page-offline","how":"hard","count":1,"address":"%s","note":"by the kernel after ue-1"}\n' "$(addr "${pfn[1]}" 0)" >>"$WORK/injected.jsonl"
	sleep 5
	held

	finish ghes-clock-step "firmware error records in an aarch64 guest whose clock was set back after rasdaemon started: 10 corrected and 1 uncorrected memory error on one DIMM"
	cp "$WORK/clock-step.txt" "$LAB/cap/ghes-clock-step/extra/"
	;;

multi)
	PORT=$MPORT
	QMP=$M/qmp.sock
	WORK=$M/tmp
	multi_disk
	: >"$WORK/injected.jsonl"
	for today in 1 2 3; do
		multi_boot "$today"
		if [ "$today" = 1 ]; then
			push "$HERE/capture.sh" root@127.0.0.1:/root/my-lab/tools/
			push "$MYBIN" root@127.0.0.1:/root/my-lab/bin/my
			guest 'hostname; systemctl is-enabled chronyd || true; systemctl is-active rasdaemon; /root/my-lab/bin/my version
				grep . /sys/devices/system/edac/mc/mc0/dimm*/dimm_label; dmidecode -t 17 | grep -E "^Handle|Locator|Serial"' | tee "$WORK/guest.txt"
		fi
		multi_send "$today"
		if [ "$today" != 3 ]; then multi_stop; fi
	done
	finish ghes-multi "firmware error records in an aarch64 guest with six DIMMs over three boots a day apart: a row, a column, a bank and a single cell fault on four DIMMs, 35 corrected errors and 1 uncorrected" --all-boots
	cp "$WORK/clock.jsonl" "$WORK/page.txt" "$WORK/guest.txt" "$LAB/cap/ghes-multi/extra/"
	;;

stop)
	guest 'systemctl poweroff' || true
	;;

*)
	echo "usage: ghes.sh image|boot|prepare|run|clock-step|multi|stop" >&2
	exit 2
	;;
esac
