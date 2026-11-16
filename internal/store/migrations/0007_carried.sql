-- MemorY ledger, schema version 7.
--
-- Records that outlived a reset, how a boot ended, and what a reset would
-- lose on the host.
--
-- A record that hardware or firmware kept over a reset and a later boot
-- logged (the contents of a machine check bank, a boot error record of the
-- firmware) is "carried". It belongs to the boot before the one that read
-- it: boot_id is that boot, read_boot_id the boot whose log or firmware
-- table held it, "ErrorDT" the latest time it can have happened and
-- not_before the earliest, when known. Its time_quality is 'estimated'; no
-- value is added to the CHECK of that column, which would rebuild the
-- table.
ALTER TABLE event ADD COLUMN carried INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event ADD COLUMN read_boot_id TEXT;
ALTER TABLE event ADD COLUMN not_before TEXT;

-- How a boot ended, when a record shows it: 'stop' (a hardware error
-- stopped it) or 'panic' (a panic and no hardware error record); '' when
-- nothing tells. A clean end is not claimed: no record tells a shutdown
-- from a power loss. end_ref names the record that shows it, end_source
-- its source, end_at the time.
ALTER TABLE boot ADD COLUMN "end" TEXT NOT NULL DEFAULT '';
ALTER TABLE boot ADD COLUMN end_at TEXT;
ALTER TABLE boot ADD COLUMN end_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE boot ADD COLUMN end_source TEXT NOT NULL DEFAULT '';

-- What "my preflight" found when a live scan ran: per check, whether a
-- reset right after a fatal memory error would keep the records. detail
-- is the statement as JSON (code, arguments, English text), evidence a
-- JSON list of what was read.
CREATE TABLE capture_check (
  scan_id  INTEGER NOT NULL REFERENCES scan(id) ON DELETE CASCADE,
  host     TEXT    NOT NULL,
  name     TEXT    NOT NULL,
  state    TEXT    NOT NULL CHECK (state IN ('kept', 'lost', 'none', 'unknown', 'info')),
  detail   TEXT    NOT NULL DEFAULT '',
  evidence TEXT    NOT NULL DEFAULT '[]',
  PRIMARY KEY (scan_id, name)
);
CREATE INDEX capture_check_host ON capture_check(host, scan_id);
