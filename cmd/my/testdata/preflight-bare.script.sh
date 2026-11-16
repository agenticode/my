#!/bin/sh
# my preflight --script for host web-07, 2026-11-16 03:12 UTC. my has run none of these commands.
# Read each block before you run it. A kernel parameter takes effect at the next boot.
set -e

# journal: the directory /var/log/journal does not exist and Storage= is not set: the journal is kept in memory and is gone after a reset
# store the journal on disk (Storage=persistent)
# cost: journal files on disk, by default up to 10% of the file system and at most 4G
# source: journald.conf(5): Storage=, SystemMaxUse=
# undo: rm /etc/systemd/journald.conf.d/90-my-persistent.conf
# undo: systemctl restart systemd-journald.service
mkdir -p /etc/systemd/journald.conf.d
printf '[Journal]\nStorage=persistent\n' > /etc/systemd/journald.conf.d/90-my-persistent.conf
systemctl restart systemd-journald.service

# pstore-backend: no backend is registered: the firmware has no ERST table; efi_pstore has pstore_disable=Y
# let efi_pstore keep the panic text in EFI variables
# cost: each panic writes the last 10240 bytes of the kernel log as EFI variables of 1024 bytes into the variable store of the firmware, where they stay until systemd-pstore or a person removes the files
# source: drivers/firmware/efi/efi-pstore.c (record_size, pstore_disable); fs/pstore/Kconfig (PSTORE_DEFAULT_KMSG_BYTES)
# undo: echo Y > /sys/module/efi_pstore/parameters/pstore_disable
# undo: rm /etc/default/grub.d/90-my-efi-pstore.cfg
# undo: update-grub
# takes effect at the next boot
echo N > /sys/module/efi_pstore/parameters/pstore_disable
mkdir -p /etc/default/grub.d
echo 'GRUB_CMDLINE_LINUX_DEFAULT="$GRUB_CMDLINE_LINUX_DEFAULT efi_pstore.pstore_disable=0"' > /etc/default/grub.d/90-my-efi-pstore.cfg
update-grub

# pstore-archive: systemd-pstore.service is not enabled: the files stay in /sys/fs/pstore, and a full store saves no later panic
# let systemd-pstore move the dumps out of the store at every boot
# cost: a few files per panic below /var/lib/systemd/pstore, each with the end of the kernel log
# source: systemd-pstore.service(8); pstore.conf(5)
# undo: systemctl disable systemd-pstore.service
systemctl enable systemd-pstore.service

# kdump: no crash kernel is loaded and no crashkernel= is on the command line
# reserve memory for a crash kernel and enable the kdump service of the distribution
# cost: the package reserves 192 MB for the crash kernel on hosts with 512 MB or more (crashkernel=512M-:192M)
# source: Debian kdump-tools: debian/kdump-tools.grub.default, debian/rules
# undo: apt purge kdump-tools
# undo: update-grub
# takes effect at the next boot
apt install kdump-tools
update-grub

# mce-print: a record left in a bank goes to /dev/mcelog and is not printed, and no mcelog daemon runs
# print every machine check record to the kernel log (mce=print_all)
# cost: every machine check record, corrected ones included, is printed at emergency level
# source: arch/x86/kernel/cpu/mce/core.c (mce_default_notifier, __print_mce)
# undo: rm /etc/default/grub.d/90-my-print-all.cfg
# undo: update-grub
# takes effect at the next boot
mkdir -p /etc/default/grub.d
echo 'GRUB_CMDLINE_LINUX_DEFAULT="$GRUB_CMDLINE_LINUX_DEFAULT mce=print_all"' > /etc/default/grub.d/90-my-print-all.cfg
update-grub

# console: no console leaves the host (no console= on the command line): the last lines before a reset stay on its screen
# add a serial console (console=tty0 console=ttyS0,115200) and keep what the port prints with the BMC's serial log or a console server, or send the kernel log to another host with netconsole
# cost: netconsole does not capture the earliest panics of a boot (netconsole.rst); a serial console needs something that keeps what it prints: a console server, or a BMC that logs the port
# source: Documentation/admin-guide/serial-console.rst; Documentation/networking/netconsole.rst
# no command for this host; by hand

# rasdaemon: rasdaemon is not installed: the corrected errors of a boot are kept only in its kernel log
# run rasdaemon with --record, so that each error of a running boot is in a database before a reset
# cost: a daemon that stores every error the kernel traces in a SQLite database below /var/lib/rasdaemon
# source: rasdaemon(8): --record
# undo: systemctl disable --now rasdaemon.service
apt install rasdaemon
systemctl enable --now rasdaemon.service

# harvest: my-harvest.service is not installed: what the boot before left is read only when someone runs my
# read what the boot before left into a kept ledger at every boot
# cost: writes /etc/systemd/system/my-harvest.service and enables it; a run at every boot reads the host and adds its records to /var/lib/my/ledger.db
# source: docs/crash-loop.md
# undo: my harvest uninstall
my harvest install
