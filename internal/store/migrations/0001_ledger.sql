-- MemorY ledger, schema version 1.
--
-- Times are UTC text in the fixed form 2006-01-02T15:04:05.000000Z, which
-- sorts correctly as text. Columns that carry FMFM field names keep the FMFM
-- spelling and are quoted. 64-bit unsigned values (physical addresses,
-- syndromes) are stored as their two's complement signed bit pattern.

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE host (
  name           TEXT PRIMARY KEY,
  arch           TEXT NOT NULL DEFAULT '',
  system_vendor  TEXT NOT NULL DEFAULT '',
  system_product TEXT NOT NULL DEFAULT '',
  board          TEXT NOT NULL DEFAULT '',
  first_seen     TEXT,
  last_seen      TEXT
) WITHOUT ROWID;

-- One collection run.
CREATE TABLE scan (
  id           INTEGER PRIMARY KEY,
  host         TEXT    NOT NULL,
  kind         TEXT    NOT NULL,
  started_at   TEXT    NOT NULL,
  finished_at  TEXT,
  tool_version TEXT    NOT NULL DEFAULT '',
  privileged   INTEGER NOT NULL DEFAULT 0,
  note         TEXT    NOT NULL DEFAULT ''
);

-- Source inventory of a scan: every source that was looked for, including
-- the ones that could not be read.
CREATE TABLE source (
  id       INTEGER PRIMARY KEY,
  scan_id  INTEGER NOT NULL REFERENCES scan(id) ON DELETE CASCADE,
  kind     TEXT    NOT NULL,
  origin   TEXT    NOT NULL,
  status   TEXT    NOT NULL,
  detail   TEXT    NOT NULL DEFAULT '',
  records  INTEGER NOT NULL DEFAULT 0,
  events   INTEGER NOT NULL DEFAULT 0,
  actions  INTEGER NOT NULL DEFAULT 0,
  skipped  INTEGER NOT NULL DEFAULT 0,
  grade_a  INTEGER NOT NULL DEFAULT 0,
  grade_b  INTEGER NOT NULL DEFAULT 0,
  grade_c  INTEGER NOT NULL DEFAULT 0,
  first_ts TEXT,
  last_ts  TEXT,
  notes    TEXT    NOT NULL DEFAULT '[]'
);
CREATE INDEX source_scan ON source(scan_id);

CREATE TABLE boot (
  host          TEXT    NOT NULL,
  boot_id       TEXT    NOT NULL,
  derived       INTEGER NOT NULL DEFAULT 0,
  started_at    TEXT    NOT NULL,
  start_quality TEXT    NOT NULL DEFAULT 'exact',
  last_seen     TEXT,
  kernel        TEXT    NOT NULL DEFAULT '',
  bios          TEXT    NOT NULL DEFAULT '',
  bmc           TEXT    NOT NULL DEFAULT '',
  cmdline       TEXT    NOT NULL DEFAULT '',
  source        TEXT    NOT NULL DEFAULT '',
  raw_ref       TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (host, boot_id)
) WITHOUT ROWID;
CREATE INDEX boot_start ON boot(host, started_at);

-- DIMM identity. dimm_uid is a hash of manufacturer, part number and serial
-- number, or a slot placeholder when no serial number could be read.
CREATE TABLE dimm (
  dimm_uid             TEXT PRIMARY KEY,
  identity             TEXT NOT NULL CHECK (identity IN ('serial', 'slot')),
  manufacturer         TEXT NOT NULL DEFAULT '',
  part_number          TEXT NOT NULL DEFAULT '',
  serial               TEXT NOT NULL DEFAULT '',
  manufacturer_id      TEXT NOT NULL DEFAULT '',
  size_bytes           INTEGER,
  memory_type          TEXT NOT NULL DEFAULT '',
  form_factor          TEXT NOT NULL DEFAULT '',
  module_type          TEXT NOT NULL DEFAULT '',
  technology           TEXT NOT NULL DEFAULT '',
  ranks                INTEGER,
  device_width         INTEGER,
  data_width           INTEGER,
  total_width          INTEGER,
  speed_mts            INTEGER,
  configured_speed_mts INTEGER,
  mfg_date             TEXT NOT NULL DEFAULT '',
  first_seen           TEXT,
  last_seen            TEXT
) WITHOUT ROWID;

-- Physical slots of a host, with the names different sources use for them.
CREATE TABLE slot (
  host          TEXT NOT NULL,
  name          TEXT NOT NULL,
  locator       TEXT NOT NULL DEFAULT '',
  bank_locator  TEXT NOT NULL DEFAULT '',
  smbios_handle INTEGER,
  socket        INTEGER,
  imc           INTEGER,
  channel       INTEGER,
  position      INTEGER,
  edac_mc       INTEGER,
  edac_location TEXT NOT NULL DEFAULT '',
  edac_label    TEXT NOT NULL DEFAULT '',
  mapping       TEXT NOT NULL DEFAULT '',
  populated     INTEGER,
  PRIMARY KEY (host, name)
) WITHOUT ROWID;

-- Which DIMM was seen in which slot, and when.
CREATE TABLE placement (
  id         INTEGER PRIMARY KEY,
  dimm_uid   TEXT NOT NULL REFERENCES dimm(dimm_uid),
  host       TEXT NOT NULL,
  slot       TEXT NOT NULL,
  first_seen TEXT NOT NULL,
  last_seen  TEXT NOT NULL,
  source     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX placement_slot ON placement(host, slot, first_seen);
CREATE INDEX placement_dimm ON placement(dimm_uid, first_seen);

-- The event ledger. All totals are computed from this table:
--   errors = SUM(count - covered) over rows with dup_of IS NULL.
CREATE TABLE event (
  id                  INTEGER PRIMARY KEY,
  host                TEXT    NOT NULL,
  kind                TEXT    NOT NULL DEFAULT 'error' CHECK (kind IN ('error', 'counter')),
  "ErrorDT"           TEXT    NOT NULL,
  "ErrorSeverity"     TEXT    NOT NULL CHECK ("ErrorSeverity" IN ('Informational', 'CE', 'Recoverable', 'UE', 'Fatal')),
  "ErrorScrubType"    TEXT    CHECK ("ErrorScrubType" IN ('Demand', 'Patrol', 'Other')),
  "SocketId"          INTEGER,
  "ChannelId"         INTEGER,
  "ChannelSlotId"     INTEGER,
  "SubChannel"        INTEGER,
  "Rank"              INTEGER,
  "DeviceId"          INTEGER,
  "BankGroup"         INTEGER,
  "Bank"              INTEGER,
  "Row"               INTEGER,
  "Column"            INTEGER,
  "ErrorVector"       TEXT    NOT NULL DEFAULT '',
  "OsPhysicalAddress" INTEGER,
  source              TEXT    NOT NULL,
  raw_ref             TEXT    NOT NULL,
  grade               TEXT    NOT NULL CHECK (grade IN ('A', 'B', 'C')),
  boot_id             TEXT,
  dimm_uid            TEXT    REFERENCES dimm(dimm_uid),
  dimm_link           TEXT    NOT NULL DEFAULT '',
  count               INTEGER NOT NULL DEFAULT 1 CHECK (count >= 1),
  covered             INTEGER NOT NULL DEFAULT 0 CHECK (covered >= 0),
  dup_of              INTEGER REFERENCES event(id),
  dup_fingerprint     TEXT    NOT NULL DEFAULT '',
  time_quality        TEXT    NOT NULL DEFAULT 'exact' CHECK (time_quality IN ('exact', 'estimated', 'relative')),
  window_start        TEXT,
  utc_offset          INTEGER,
  imc                 INTEGER,
  edac_mc             INTEGER,
  edac_location       TEXT    NOT NULL DEFAULT '',
  smbios_handle       INTEGER,
  label               TEXT    NOT NULL DEFAULT '',
  slot                TEXT    NOT NULL DEFAULT '',
  syndrome            INTEGER,
  error_type          TEXT    NOT NULL DEFAULT '',
  decoder             TEXT    NOT NULL DEFAULT '',
  fingerprint         TEXT    NOT NULL UNIQUE,
  scan_id             INTEGER REFERENCES scan(id)
);
CREATE INDEX event_time ON event(host, "ErrorDT");
CREATE INDEX event_dimm ON event(dimm_uid, "ErrorDT");
CREATE INDEX event_slot ON event(host, slot, "ErrorDT");
CREATE INDEX event_dup  ON event(dup_of) WHERE dup_of IS NOT NULL;

-- The original record behind an event, kept apart so that scans of the
-- event table stay small.
CREATE TABLE event_raw (
  event_id INTEGER PRIMARY KEY REFERENCES event(id) ON DELETE CASCADE,
  text     TEXT NOT NULL
);

-- Readings of per-DIMM error counters. Counter events in the ledger are the
-- growth between two readings; the readings themselves are kept here.
CREATE TABLE counter_sample (
  id          INTEGER PRIMARY KEY,
  host        TEXT    NOT NULL,
  observed_at TEXT    NOT NULL,
  mc          INTEGER NOT NULL,
  mc_name     TEXT    NOT NULL DEFAULT '',
  dimm_index  INTEGER NOT NULL,
  location    TEXT    NOT NULL DEFAULT '',
  label       TEXT    NOT NULL DEFAULT '',
  ce          INTEGER NOT NULL,
  ue          INTEGER NOT NULL,
  reset_at    TEXT,
  size_mb     INTEGER,
  mem_type    TEXT    NOT NULL DEFAULT '',
  dev_type    TEXT    NOT NULL DEFAULT '',
  edac_mode   TEXT    NOT NULL DEFAULT '',
  source      TEXT    NOT NULL DEFAULT '',
  raw_ref     TEXT    NOT NULL DEFAULT '',
  UNIQUE (host, mc, dimm_index, observed_at)
);

-- Repair and isolation history. MemorY records these; it never performs them.
CREATE TABLE action (
  id                  INTEGER PRIMARY KEY,
  host                TEXT    NOT NULL,
  at                  TEXT    NOT NULL,
  time_quality        TEXT    NOT NULL DEFAULT 'exact',
  kind                TEXT    NOT NULL CHECK (kind IN ('page-offline', 'sppr', 'hppr', 'sparing', 'row-remap', 'replacement', 'reseat')),
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
CREATE INDEX action_time ON action(host, at);
CREATE INDEX action_dimm ON action(dimm_uid, at);

-- Clustered faults, written by the analysis engine.
CREATE TABLE fault (
  id             INTEGER PRIMARY KEY,
  host           TEXT    NOT NULL,
  dimm_uid       TEXT    REFERENCES dimm(dimm_uid),
  slot           TEXT    NOT NULL DEFAULT '',
  mode           TEXT    NOT NULL,
  estimated      INTEGER NOT NULL DEFAULT 0,
  grade          TEXT    NOT NULL CHECK (grade IN ('A', 'B', 'C')),
  "Rank"         INTEGER,
  "DeviceId"     INTEGER,
  "BankGroup"    INTEGER,
  "Bank"         INTEGER,
  row_min        INTEGER,
  row_max        INTEGER,
  column_min     INTEGER,
  column_max     INTEGER,
  address_min    INTEGER,
  address_max    INTEGER,
  first_seen     TEXT    NOT NULL,
  last_seen      TEXT    NOT NULL,
  event_count    INTEGER NOT NULL DEFAULT 0,
  error_count    INTEGER NOT NULL DEFAULT 0,
  confidence     INTEGER NOT NULL DEFAULT 0 CHECK (confidence BETWEEN 0 AND 100),
  engine_version TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX fault_dimm ON fault(host, dimm_uid);

CREATE TABLE fault_event (
  fault_id INTEGER NOT NULL REFERENCES fault(id) ON DELETE CASCADE,
  event_id INTEGER NOT NULL REFERENCES event(id) ON DELETE CASCADE,
  PRIMARY KEY (fault_id, event_id)
) WITHOUT ROWID;

-- Findings per DIMM, written by the analysis engine. rules, evidence,
-- counter_evidence and missing are JSON.
CREATE TABLE verdict (
  id               INTEGER PRIMARY KEY,
  host             TEXT    NOT NULL,
  dimm_uid         TEXT    REFERENCES dimm(dimm_uid),
  slot             TEXT    NOT NULL DEFAULT '',
  grade            TEXT    NOT NULL CHECK (grade IN ('ok', 'observe', 'consider-replacement', 'replace-now', 'hold-replacement')),
  rules            TEXT    NOT NULL DEFAULT '[]',
  evidence         TEXT    NOT NULL DEFAULT '[]',
  counter_evidence TEXT    NOT NULL DEFAULT '[]',
  missing          TEXT    NOT NULL DEFAULT '[]',
  next_action      TEXT    NOT NULL DEFAULT '',
  confidence       INTEGER NOT NULL DEFAULT 0 CHECK (confidence BETWEEN 0 AND 100),
  engine_version   TEXT    NOT NULL DEFAULT '',
  computed_at      TEXT    NOT NULL
);
CREATE INDEX verdict_dimm ON verdict(host, dimm_uid);

-- GPU memory health: row remapping snapshots and XID events.
CREATE TABLE gpu_mem (
  id                  INTEGER PRIMARY KEY,
  host                TEXT    NOT NULL,
  observed_at         TEXT    NOT NULL,
  kind                TEXT    NOT NULL CHECK (kind IN ('snapshot', 'xid')),
  gpu_serial          TEXT    NOT NULL DEFAULT '',
  gpu_uuid            TEXT    NOT NULL DEFAULT '',
  gpu_index           INTEGER,
  pci_bus             TEXT    NOT NULL DEFAULT '',
  model               TEXT    NOT NULL DEFAULT '',
  xid                 INTEGER,
  xid_text            TEXT    NOT NULL DEFAULT '',
  remap_correctable   INTEGER,
  remap_uncorrectable INTEGER,
  remap_pending       INTEGER,
  remap_failure       INTEGER,
  bank_max            INTEGER,
  bank_high           INTEGER,
  bank_partial        INTEGER,
  bank_low            INTEGER,
  bank_none           INTEGER,
  retired_sbe         INTEGER,
  retired_dbe         INTEGER,
  retired_pending     INTEGER,
  boot_id             TEXT,
  source              TEXT    NOT NULL,
  raw_ref             TEXT    NOT NULL,
  raw                 TEXT    NOT NULL DEFAULT '',
  fingerprint         TEXT    NOT NULL UNIQUE
);
CREATE INDEX gpu_mem_time ON gpu_mem(host, gpu_serial, observed_at);
