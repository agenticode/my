-- MemorY ledger, schema version 4.
--
-- Counter readings of more than one source. The EDAC counters of the kernel
-- were the only per-DIMM counters so far; the mcelog daemon keeps counts
-- per DIMM as well, and a host can have both. A reading now belongs to its
-- source, so that two sources read in the same second stay two readings and
-- the growth of a counter is worked out against the earlier readings of
-- the same source.
--
-- A source that knows no EDAC position names the DIMM by socket, channel
-- and DIMM number and by the SMBIOS bank locator. A source that keeps a
-- rate window beside its totals says how many of the errors fell into the
-- time before the reading (recent_ce within recent_ce_seconds).

-- SQLite cannot change a UNIQUE constraint in place: the table is rebuilt.
CREATE TABLE counter_sample_v4 (
  id                INTEGER PRIMARY KEY,
  host              TEXT    NOT NULL,
  observed_at       TEXT    NOT NULL,
  source            TEXT    NOT NULL DEFAULT '',
  mc                INTEGER NOT NULL,
  mc_name           TEXT    NOT NULL DEFAULT '',
  dimm_index        INTEGER NOT NULL,
  location          TEXT    NOT NULL DEFAULT '',
  label             TEXT    NOT NULL DEFAULT '',
  bank_locator      TEXT    NOT NULL DEFAULT '',
  "SocketId"        INTEGER,
  "ChannelId"       INTEGER,
  "ChannelSlotId"   INTEGER,
  ce                INTEGER NOT NULL,
  ue                INTEGER NOT NULL,
  recent_ce         INTEGER,
  recent_ce_seconds INTEGER,
  recent_ue         INTEGER,
  recent_ue_seconds INTEGER,
  reset_at          TEXT,
  size_mb           INTEGER,
  mem_type          TEXT    NOT NULL DEFAULT '',
  dev_type          TEXT    NOT NULL DEFAULT '',
  edac_mode         TEXT    NOT NULL DEFAULT '',
  raw_ref           TEXT    NOT NULL DEFAULT '',
  UNIQUE (host, source, mc, dimm_index, observed_at)
);
INSERT INTO counter_sample_v4 (id, host, observed_at, source, mc, mc_name, dimm_index, location, label, ce, ue,
  reset_at, size_mb, mem_type, dev_type, edac_mode, raw_ref)
SELECT id, host, observed_at, source, mc, mc_name, dimm_index, location, label, ce, ue,
  reset_at, size_mb, mem_type, dev_type, edac_mode, raw_ref
FROM counter_sample;
DROP TABLE counter_sample;
ALTER TABLE counter_sample_v4 RENAME TO counter_sample;
