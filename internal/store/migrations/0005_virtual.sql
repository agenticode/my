-- MemorY ledger, schema version 5.
--
-- Whether a host is a virtual machine. A guest is not shown the corrected
-- errors of the machine it runs on, and of the uncorrected ones at most
-- those that hit its own memory, so "no errors on record" means less for a
-- guest than for a physical host. The column holds the name of the
-- hypervisor as the machine or its kernel gives it ("KVM", "VMware",
-- "Hyper-V"), "hypervisor" where one is known to be there and not known by
-- name, and '' for a host that is not known to be a virtual machine.
ALTER TABLE host ADD COLUMN virtual TEXT NOT NULL DEFAULT '';
