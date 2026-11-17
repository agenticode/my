-- MemorY ledger, schema version 11.
--
-- What the SMBIOS processor records of a host say beside the name of the
-- processor: the CPUID signature of the record the name comes from
-- ("family/model/stepping", empty when not known), and per processor record
-- whether its socket holds a processor ("1" populated, "0" not; one
-- character per record in table order, empty when not known). The board
-- reads the memory channels of a processor from its name or, for a blank or
-- generic name, from the signature.
ALTER TABLE host ADD COLUMN cpu_signature TEXT NOT NULL DEFAULT '';
ALTER TABLE host ADD COLUMN cpu_present TEXT NOT NULL DEFAULT '';
