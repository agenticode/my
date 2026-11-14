-- MemorY ledger, schema version 2.
--
-- Findings carry their statements as message codes with arguments, a finding
-- can be about the memory of a GPU, and the marks firmware puts on a module
-- (taken out of use, reported as failed) are kept as actions.

-- SQLite cannot change a CHECK constraint in place: the action table is
-- rebuilt with the two new kinds, 'disable' and 'mark-failed'.
CREATE TABLE action_v2 (
  id                  INTEGER PRIMARY KEY,
  host                TEXT    NOT NULL,
  at                  TEXT    NOT NULL,
  time_quality        TEXT    NOT NULL DEFAULT 'exact',
  kind                TEXT    NOT NULL CHECK (kind IN ('page-offline', 'sppr', 'hppr', 'sparing', 'row-remap', 'replacement', 'reseat', 'disable', 'mark-failed')),
  method              TEXT    NOT NULL DEFAULT '',
  result              TEXT    NOT NULL DEFAULT 'unknown' CHECK (result IN ('ok', 'failed', 'pending', 'unknown')),
  "OsPhysicalAddress" INTEGER,
  "Rank"              INTEGER,
  "DeviceId"          INTEGER,
  "BankGroup"         INTEGER,
  "Bank"              INTEGER,
  "Row"               INTEGER,
  label               TEXT    NOT NULL DEFAULT '',
  slot                TEXT    NOT NULL DEFAULT '',
  dimm_uid            TEXT    REFERENCES dimm(dimm_uid),
  count               INTEGER NOT NULL DEFAULT 1,
  boot_id             TEXT,
  detail              TEXT    NOT NULL DEFAULT '',
  source              TEXT    NOT NULL,
  raw_ref             TEXT    NOT NULL,
  raw                 TEXT    NOT NULL DEFAULT '',
  fingerprint         TEXT    NOT NULL UNIQUE,
  scan_id             INTEGER REFERENCES scan(id)
);
INSERT INTO action_v2 (id, host, at, time_quality, kind, method, result, "OsPhysicalAddress", "Rank", "DeviceId", "BankGroup", "Bank", "Row",
  label, slot, dimm_uid, count, boot_id, detail, source, raw_ref, raw, fingerprint, scan_id)
SELECT id, host, at, time_quality, kind, method, result, "OsPhysicalAddress", "Rank", "DeviceId", "BankGroup", "Bank", "Row",
  label, slot, dimm_uid, count, boot_id, detail, source, raw_ref, raw, fingerprint, scan_id
FROM action;
DROP TABLE action;
ALTER TABLE action_v2 RENAME TO action;
CREATE INDEX action_time ON action(host, at);
CREATE INDEX action_dimm ON action(dimm_uid, at);

-- Faults and findings are derived from the events. Rows of an earlier
-- engine have another shape; they are dropped and computed again.
DELETE FROM verdict;
DELETE FROM fault;

-- gpu is set on findings about GPU memory: the serial number or the UUID.
-- evidence, counter_evidence and missing hold statements (code, args and
-- English text); next_action holds one statement, or is empty.
ALTER TABLE verdict ADD COLUMN gpu TEXT NOT NULL DEFAULT '';
ALTER TABLE verdict ADD COLUMN confidence_factors TEXT NOT NULL DEFAULT '[]';
