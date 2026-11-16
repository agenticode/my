#!/bin/bash
# capture.sh NAME [--formats] [--all-boots]
#
# Collects from the running host what a memory error investigation asks for:
# the kernel log, the rasdaemon database, the EDAC counters, the memory
# devices of SMBIOS, and what "my" itself says about the host. The result is
# laid out as a copy of the host's files, so "my --root DIR" and
# "my import DIR" read it.
#
# It only reads. Run it as root: the SMBIOS tables, the kernel log and the
# rasdaemon database are closed to other users on most hosts.
#
#   LAB        where captures go (default /root/my-lab); the capture is $LAB/cap/NAME
#   MY         the my binary to run (default $LAB/bin/my); "-" leaves its output out
#   RASDAEMON  the rasdaemon program that is recording, when it is not the one in the path
#
# --formats adds the kernel log in every form dmesg, /dev/kmsg and journalctl
# print it, under formats/.
#
# --all-boots adds journal-all.json, the kernel messages of every boot the
# journal holds (journalctl -k reads the running boot only).
set -u
LAB=${LAB:-/root/my-lab}
MY=${MY:-$LAB/bin/my}
NAME=${1:?usage: capture.sh NAME [--formats] [--all-boots]}
shift
FORMATS=0
ALLBOOTS=0
for opt in "$@"; do
	case "$opt" in
	--formats) FORMATS=1 ;;
	--all-boots) ALLBOOTS=1 ;;
	*)
		echo "capture.sh: unknown option $opt" >&2
		exit 2
		;;
	esac
done

OUT=$LAB/cap/$NAME
rm -rf "$OUT"
mkdir -p "$OUT"
cd "$OUT" || exit 1

have() { command -v "$1" >/dev/null 2>&1; }
# save FILE CMD...: the output of a command; what it wrote to stderr goes to capture.log.
save() {
	local f=$1
	shift
	mkdir -p "$(dirname "$f")"
	if ! "$@" >"$f" 2>>capture.log; then
		echo "capture: \"$*\" failed, see capture.log" >&2
	fi
}
# copy FILE: a file of the host to the same path below the capture.
copy() {
	local f=$1
	[ -r "$f" ] || return 0
	mkdir -p ".$(dirname "$f")"
	cat "$f" >".$f" 2>>capture.log
}

NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# Host facts: what "my" reads from /proc and /sys to name the host and its boot.
for f in /proc/stat /proc/uptime /proc/version /proc/cmdline /proc/meminfo \
	/proc/sys/kernel/hostname /proc/sys/kernel/osrelease /proc/sys/kernel/arch \
	/proc/sys/kernel/random/boot_id \
	/sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_name \
	/sys/class/dmi/id/board_vendor /sys/class/dmi/id/board_name \
	/sys/class/dmi/id/bios_vendor /sys/class/dmi/id/bios_version /sys/class/dmi/id/bios_date; do
	copy "$f"
done

# Kernel log of the running boot, and the journal's view of it.
save dmesg dmesg
if have journalctl && journalctl -k -n 1 >/dev/null 2>&1; then
	save journal-k.json journalctl --no-pager -k -o json
	# A host without a journal answers with nothing.
	[ -s journal-k.json ] || rm -f journal-k.json
	if [ "$ALLBOOTS" = 1 ] && [ -s journal-k.json ]; then
		save journal-all.json journalctl --no-pager -o json _TRANSPORT=kernel
	fi
fi
if [ "$FORMATS" = 1 ]; then
	save formats/dmesg-T.txt dmesg -T
	save formats/dmesg-iso.txt dmesg --time-format=iso
	save formats/dmesg-raw.txt dmesg -r
	# Reading /dev/kmsg without blocking ends with "resource temporarily
	# unavailable" when the last record was read: that is the end, not a failure.
	dd if=/dev/kmsg iflag=nonblock status=none >formats/kmsg.txt 2>/dev/null || true
	if [ -s journal-k.json ]; then
		save formats/journal-short.txt journalctl --no-pager -k
		save formats/journal-short-iso.txt journalctl --no-pager -k -o short-iso-precise
		save formats/journal-short-monotonic.txt journalctl --no-pager -k -o short-monotonic
	fi
fi

# rasdaemon database. rasdaemon keeps it open; a rollback journal next to it
# belongs to the copy.
for db in /var/lib/rasdaemon/ras-mc_event.db /usr/local/var/lib/rasdaemon/ras-mc_event.db; do
	[ -r "$db" ] || continue
	mkdir -p ".$(dirname "$db")"
	cp "$db" ".$db"
	[ -e "$db-journal" ] && cp "$db-journal" ".$db-journal"
	break
done
if have ras-mc-ctl; then
	save extra/ras-mc-ctl-errors.txt ras-mc-ctl --errors
	save extra/ras-mc-ctl-summary.txt ras-mc-ctl --summary
fi

# EDAC: every readable file as a tree (what "my" reads), and as one listing.
if [ -d /sys/devices/system/edac ]; then
	(
		cd / || exit 1
		find sys/devices/system/edac -type d -print
	) | while read -r d; do mkdir -p "$d"; done
	(
		cd / || exit 1
		find sys/devices/system/edac -type f -perm -u+r -print
	) | while read -r f; do cat "/$f" >"$f" 2>/dev/null || rm -f "$f"; done
	{
		echo "# find /sys/devices/system/edac /sys/bus/edac/devices, with the content of each readable file"
		find /sys/devices/system/edac -mindepth 1 \( -type d -printf '%p/\n' \) -o \( -type l -printf '%p -> %l\n' \) -o \( -type f -print \) | sort |
			while read -r p rest; do
				case "$p$rest" in
				*/ | *"->"*) echo "$p${rest:+ $rest}" ;;
				*) if v=$(cat "$p" 2>/dev/null); then echo "$p: $v"; else echo "$p: (not readable)"; fi ;;
				esac
			done
		if [ -d /sys/bus/edac/devices ]; then
			find /sys/bus/edac/devices -mindepth 1 -maxdepth 1 -printf '%p -> %l\n' | sort
		fi
	} >edac-sysfs.txt 2>>capture.log
fi

# SMBIOS memory devices.
if have dmidecode; then
	save dmidecode.txt dmidecode -t 17
fi

# What the kernel itself counts: poisoned memory, and the corrected error
# collector's table when debugfs shows it.
mkdir -p extra
grep -E 'HardwareCorrupted|MemTotal' /proc/meminfo >extra/meminfo.txt 2>/dev/null
[ -r /sys/kernel/debug/ras/cec/array ] && cat /sys/kernel/debug/ras/cec/array >extra/cec-array.txt 2>/dev/null

# Versions of everything that took part.
{
	echo "captured_at=$NOW"
	echo "kernel=$(uname -r)"
	echo "arch=$(uname -m)"
	echo "uptime_seconds=$(cut -d' ' -f1 /proc/uptime)"
	if [ -n "${RASDAEMON:-}" ] && [ "$RASDAEMON" != rasdaemon ]; then
		echo "rasdaemon=$("$RASDAEMON" --version 2>/dev/null | head -1)"
		echo "rasdaemon_package=built from source"
	else
		have rasdaemon && echo "rasdaemon=$(rasdaemon --version 2>/dev/null | head -1)"
		have rpm && rpm -q rasdaemon >/dev/null 2>&1 && echo "rasdaemon_package=$(rpm -q rasdaemon)"
	fi
	have sqlite3 && echo "sqlite=$(sqlite3 --version | cut -d' ' -f1)"
	have dmidecode && echo "dmidecode=$(dmidecode --version)"
	have journalctl && echo "systemd=$(journalctl --version | head -1 | cut -d' ' -f2)"
	[ -r /etc/os-release ] && (. /etc/os-release && echo "os=$PRETTY_NAME")
	[ -x "$MY" ] && echo "my=$("$MY" version)"
} >capture.env

# What names this host: for sanitize.py, which takes these values out of
# the capture before it is shown to anyone. The directory is not part of
# what is published.
mkdir -p private
{
	echo "hostname=$(cat /proc/sys/kernel/hostname)"
	have hostname && echo "fqdn=$(hostname -f 2>/dev/null)"
	[ -r /etc/machine-id ] && echo "machine_id=$(cat /etc/machine-id)"
	for f in product_uuid product_serial board_serial chassis_serial; do
		[ -r "/sys/class/dmi/id/$f" ] && echo "dmi_$f=$(cat "/sys/class/dmi/id/$f")"
	done
	for dev in /sys/class/net/*; do
		[ -r "$dev/address" ] && echo "mac=$(cat "$dev/address")"
	done
	if have ip; then
		ip -o addr show 2>/dev/null | awk '{print "ip=" $4}' | sed 's|/.*||'
		ip -o route show default 2>/dev/null | awk '{print "ip=" $3}'
	fi
	if have blkid; then
		blkid -o export 2>/dev/null | grep -E '^(UUID|PARTUUID|PTUUID|UUID_SUB)=' | sed 's/^[A-Z_]*=/fs_uuid=/'
	fi
	grep -h -E '^nameserver' /etc/resolv.conf 2>/dev/null | awk '{print "ip=" $2}'
} >private/ident.txt 2>/dev/null

# What "my" says about the live host, taken last so that it sees what the
# files above hold.
if [ "$MY" != "-" ] && [ -x "$MY" ]; then
	mkdir -p my
	"$MY" scan --json >my/scan.json 2>my/scan.stderr
	echo "exit=$?" >my/scan.exit
	"$MY" scan --lang en --color never --width 100 >my/scan.txt 2>>my/scan.stderr
	"$MY" timeline --lang en --color never --width 100 >my/timeline.txt 2>>my/scan.stderr
	"$MY" bundle --anonymize ${MY_KEY:+--key-file "$MY_KEY"} --force -o my/case.mycase >my/bundle.txt 2>&1
	echo "exit=$?" >my/bundle.exit
	[ -s my/scan.stderr ] || rm -f my/scan.stderr
fi

[ -s capture.log ] || rm -f capture.log
echo "captured $NAME at $NOW into $OUT"
find . -type f | sort | sed 's/^/  /'
