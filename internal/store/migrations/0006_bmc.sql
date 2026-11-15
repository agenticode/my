-- MemorY ledger, schema version 6.
--
-- What a BMC says about a host beyond events, actions and the inventory:
-- what the BMC is and how complete it was read, the settings of the system
-- firmware that bear on memory errors, and each memory module as the BMC
-- describes it, with the error counts the BMC keeps for it.

-- One reading of a management controller. address is the name or IP
-- address it was asked at. classes and logs are JSON: what became of each
-- class of resources, and the event logs with the period each covers.
CREATE TABLE bmc (
  id              INTEGER PRIMARY KEY,
  host            TEXT    NOT NULL,
  observed_at     TEXT    NOT NULL,
  address         TEXT    NOT NULL DEFAULT '',
  manager         TEXT    NOT NULL DEFAULT '',
  firmware        TEXT    NOT NULL DEFAULT '',
  bios_version    TEXT    NOT NULL DEFAULT '',
  redfish_version TEXT    NOT NULL DEFAULT '',
  bios_registry   TEXT    NOT NULL DEFAULT '',
  live            INTEGER NOT NULL DEFAULT 0,
  complete        INTEGER,
  tls             TEXT    NOT NULL DEFAULT '',
  auth            TEXT    NOT NULL DEFAULT '',
  clock_offset    INTEGER,
  last_reset      TEXT,
  classes         TEXT    NOT NULL DEFAULT '[]',
  logs            TEXT    NOT NULL DEFAULT '[]',
  notes           TEXT    NOT NULL DEFAULT '[]',
  source          TEXT    NOT NULL,
  raw_ref         TEXT    NOT NULL DEFAULT '',
  fingerprint     TEXT    NOT NULL UNIQUE,
  scan_id         INTEGER REFERENCES scan(id)
);
CREATE INDEX bmc_host ON bmc(host, observed_at);

-- Settings of the system firmware as a BMC reports them: the value in
-- effect, the value that waits for the next boot when it differs, and
-- what the attribute registry of that firmware says about the setting.
-- A ledger holds the settings that bear on memory, not the whole BIOS.
-- has_value and has_pending tell an empty value from none.
CREATE TABLE bios_setting (
  id             INTEGER PRIMARY KEY,
  host           TEXT    NOT NULL,
  observed_at    TEXT    NOT NULL,
  name           TEXT    NOT NULL,
  value          TEXT    NOT NULL DEFAULT '',
  has_value      INTEGER NOT NULL DEFAULT 1,
  pending        TEXT    NOT NULL DEFAULT '',
  has_pending    INTEGER NOT NULL DEFAULT 0,
  value_type     TEXT    NOT NULL DEFAULT '',
  registry       TEXT    NOT NULL DEFAULT '',
  display_name   TEXT    NOT NULL DEFAULT '',
  menu_path      TEXT    NOT NULL DEFAULT '',
  read_only      INTEGER,
  reset_required INTEGER,
  allowed        TEXT    NOT NULL DEFAULT '[]',
  lower_bound    INTEGER,
  upper_bound    INTEGER,
  default_value  TEXT    NOT NULL DEFAULT '',
  has_default    INTEGER NOT NULL DEFAULT 0,
  source         TEXT    NOT NULL,
  raw_ref        TEXT    NOT NULL DEFAULT '',
  scan_id        INTEGER REFERENCES scan(id),
  UNIQUE (host, observed_at, name)
);

-- A memory module as a BMC describes it at one moment. label is the name
-- the source uses for the slot, slot the slot of the ledger it was tied
-- to. The counts are the BMC's own: "current" since the last reset of the
-- system or of the counters, "lifetime" for the life of the module. They
-- are readings, not events: no total of the ledger includes them.
CREATE TABLE bmc_memory (
  id                  INTEGER PRIMARY KEY,
  host                TEXT    NOT NULL,
  observed_at         TEXT    NOT NULL,
  label               TEXT    NOT NULL,
  slot                TEXT    NOT NULL DEFAULT '',
  device_locator      TEXT    NOT NULL DEFAULT '',
  service_label       TEXT    NOT NULL DEFAULT '',
  resource_id         TEXT    NOT NULL DEFAULT '',
  state               TEXT    NOT NULL DEFAULT '',
  health              TEXT    NOT NULL DEFAULT '',
  error_correction    TEXT    NOT NULL DEFAULT '',
  operating_speed     INTEGER,
  allowed_speeds      TEXT    NOT NULL DEFAULT '[]',
  rank_spare          INTEGER,
  spare_device        INTEGER,
  device_width        INTEGER,
  device_width_source TEXT    NOT NULL DEFAULT '',
  metrics             TEXT    NOT NULL DEFAULT '',
  ce_current          INTEGER,
  ue_current          INTEGER,
  ce_lifetime         INTEGER,
  ue_lifetime         INTEGER,
  alarm_ce            INTEGER,
  alarm_ue            INTEGER,
  current_since       TEXT,
  source              TEXT    NOT NULL,
  raw_ref             TEXT    NOT NULL DEFAULT '',
  raw                 TEXT    NOT NULL DEFAULT '',
  fingerprint         TEXT    NOT NULL UNIQUE,
  scan_id             INTEGER REFERENCES scan(id)
);
CREATE INDEX bmc_memory_slot ON bmc_memory(host, slot, observed_at);
