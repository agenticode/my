-- MemorY ledger, schema version 9.
--
-- What the BIOS and the BMC say about memory, in one vocabulary for every
-- vendor. Each firmware record keeps a row in fw_record with the vendor's
-- own words beside the event or action made from it; a record that makes
-- neither (a module detected, the RAS mode in effect) has the row alone.

-- SQLite cannot drop a CHECK constraint in place: the action table is
-- rebuilt once more, as in schema 2, without the check on kind (the program
-- checks the kind, so later kinds need no rebuild), and with two columns:
-- dup_of names the action that reports the same step through another log
-- or reader of the BMC, withdrawn_by the informational event that took a
-- mark back.
CREATE TABLE action_v9 (
  id                  INTEGER PRIMARY KEY,
  host                TEXT    NOT NULL,
  at                  TEXT    NOT NULL,
  time_quality        TEXT    NOT NULL DEFAULT 'exact',
  kind                TEXT    NOT NULL,
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
  scan_id             INTEGER REFERENCES scan(id),
  dup_of              INTEGER,
  withdrawn_by        INTEGER
);
INSERT INTO action_v9 (id, host, at, time_quality, kind, method, result, "OsPhysicalAddress", "Rank", "DeviceId", "BankGroup", "Bank", "Row",
  label, slot, dimm_uid, count, boot_id, detail, source, raw_ref, raw, fingerprint, scan_id)
SELECT id, host, at, time_quality, kind, method, result, "OsPhysicalAddress", "Rank", "DeviceId", "BankGroup", "Bank", "Row",
  label, slot, dimm_uid, count, boot_id, detail, source, raw_ref, raw, fingerprint, scan_id
FROM action;
DROP TABLE action;
ALTER TABLE action_v9 RENAME TO action;
CREATE INDEX action_time ON action(host, at);
CREATE INDEX action_dimm ON action(dimm_uid, at);

-- One row per firmware record about memory. event_id or action_id ties it
-- to what was made of it; a row with neither carries the fingerprint and
-- the raw text of its record. common is the common event, mapping says how
-- it was found (table, bmc-registry, wording, ipmi, none) and map_ref which
-- line of which table, basis how far that table was checked.
CREATE TABLE fw_record (
  id               INTEGER PRIMARY KEY,
  host             TEXT    NOT NULL,
  scan_id          INTEGER REFERENCES scan(id),
  event_id         INTEGER REFERENCES event(id) ON DELETE CASCADE,
  action_id        INTEGER REFERENCES action(id) ON DELETE CASCADE,
  at               TEXT    NOT NULL,
  common           TEXT    NOT NULL,
  flags            TEXT    NOT NULL DEFAULT '',
  clears           TEXT    NOT NULL DEFAULT '',
  vendor           TEXT    NOT NULL DEFAULT '',
  vendor_id        TEXT    NOT NULL DEFAULT '',
  registry         TEXT    NOT NULL DEFAULT '',
  registry_version TEXT    NOT NULL DEFAULT '',
  vendor_severity  TEXT    NOT NULL DEFAULT '',
  origin           TEXT    NOT NULL DEFAULT '',
  log              TEXT    NOT NULL DEFAULT '',
  location         TEXT    NOT NULL DEFAULT '',
  args             TEXT    NOT NULL DEFAULT '[]',
  text             TEXT    NOT NULL DEFAULT '',
  repeat           INTEGER NOT NULL DEFAULT 1,
  last_at          TEXT,
  resolved         INTEGER NOT NULL DEFAULT 0,
  entry_key        TEXT    NOT NULL DEFAULT '',
  mapping          TEXT    NOT NULL DEFAULT 'none',
  map_ref          TEXT    NOT NULL DEFAULT '',
  basis            TEXT    NOT NULL DEFAULT '',
  source           TEXT    NOT NULL DEFAULT '',
  raw_ref          TEXT    NOT NULL DEFAULT '',
  fingerprint      TEXT    UNIQUE,
  raw              TEXT    NOT NULL DEFAULT '',
  CHECK (event_id IS NULL OR action_id IS NULL)
);
CREATE INDEX fw_record_common ON fw_record(host, common);
CREATE INDEX fw_record_entry ON fw_record(entry_key);
CREATE INDEX fw_record_event ON fw_record(event_id);
CREATE INDEX fw_record_action ON fw_record(action_id);

-- Records a reader could not use and whose text names memory: at most 50
-- per source, each cut at 4 KiB. Records that do not name memory are only
-- counted (source.unread).
CREATE TABLE unread_record (
  id        INTEGER PRIMARY KEY,
  source_id INTEGER NOT NULL REFERENCES source(id) ON DELETE CASCADE,
  ref       TEXT    NOT NULL DEFAULT '',
  reason    TEXT    NOT NULL DEFAULT '',
  text      TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX unread_record_source ON unread_record(source_id);

-- Message registries a BMC published, one row per registry of a host, with
-- how much of it this version classes. unmapped is a JSON list of the keys
-- of memory messages nothing classes, at most 200.
CREATE TABLE registry_seen (
  id        INTEGER PRIMARY KEY,
  host      TEXT    NOT NULL,
  scan_id   INTEGER REFERENCES scan(id),
  seen_at   TEXT    NOT NULL,
  prefix    TEXT    NOT NULL,
  version   TEXT    NOT NULL DEFAULT '',
  owner     TEXT    NOT NULL DEFAULT '',
  language  TEXT    NOT NULL DEFAULT '',
  messages  INTEGER NOT NULL DEFAULT 0,
  memory    INTEGER NOT NULL DEFAULT 0,
  mapped    INTEGER NOT NULL DEFAULT 0,
  wording   INTEGER NOT NULL DEFAULT 0,
  unmapped  TEXT    NOT NULL DEFAULT '[]',
  source    TEXT    NOT NULL DEFAULT '',
  UNIQUE (host, prefix, version, language)
);

-- unread counts the records of a source the reader did not understand,
-- faults the faults of the reader that were caught.
ALTER TABLE source ADD COLUMN unread INTEGER NOT NULL DEFAULT 0;
ALTER TABLE source ADD COLUMN faults INTEGER NOT NULL DEFAULT 0;
