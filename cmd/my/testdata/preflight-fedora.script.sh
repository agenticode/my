#!/bin/sh
# my preflight --script for host r12-n034, 2026-11-16 03:12 UTC. my has run none of these commands.
# Read each block before you run it. A kernel parameter takes effect at the next boot.
set -e

# pstore-backend: no backend is registered: the ERST table is there and did not register (the kernel lines that start with "ERST:" say why); efi_pstore is not loaded
# let efi_pstore keep the panic text in EFI variables
# cost: each panic writes the last 10240 bytes of the kernel log as EFI variables of 1024 bytes into the variable store of the firmware, where they stay until systemd-pstore or a person removes the files
# source: drivers/firmware/efi/efi-pstore.c (record_size, pstore_disable); fs/pstore/Kconfig (PSTORE_DEFAULT_KMSG_BYTES)
# undo: grubby --update-kernel=ALL --remove-args="efi_pstore.pstore_disable=0"
# takes effect at the next boot
grubby --update-kernel=ALL --args="efi_pstore.pstore_disable=0"

# kdump: no crash kernel is loaded and no crashkernel= is on the command line
# reserve memory for a crash kernel and enable the kdump service of the distribution
# cost: crashkernel= takes memory away from the running system for the crash kernel; "kdumpctl get-default-crashkernel" prints how much
# source: kdumpctl(8) (kdump-utils); Documentation/admin-guide/kdump/kdump.rst
# undo: systemctl disable kdump.service
# undo: grubby --update-kernel=ALL --remove-args="crashkernel"
# takes effect at the next boot
dnf install /usr/bin/kdumpctl
kdumpctl reset-crashkernel --kernel=ALL
systemctl enable kdump.service

# mce-print: print_all was set through sysfs for this boot only: the next boot reads the banks before it can be set again, and a record left in a bank goes to /dev/mcelog unprinted
# print every machine check record to the kernel log (mce=print_all)
# cost: every machine check record, corrected ones included, is printed at emergency level
# source: arch/x86/kernel/cpu/mce/core.c (mce_default_notifier, __print_mce)
# undo: grubby --update-kernel=ALL --remove-args="mce=print_all"
# takes effect at the next boot
grubby --update-kernel=ALL --args="mce=print_all"
