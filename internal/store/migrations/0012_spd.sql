-- MemorY ledger, schema version 12.
--
-- The SPD EEPROM of the memory modules: one row per SPD as a scan or an
-- import got it, with the bytes read (raw, at most 2,048) or, for the text
-- of decode-dimms, the fields it printed as JSON. The fields of the bytes
-- are decoded each time a view is built. A reading with the bytes, state
-- and detail of the latest row of its origin moves that row's last_seen
-- and scan. The join sets dimm_uid and tie (serial, serial-reversed) when
-- the serial number of the SPD is that of a DIMM of SMBIOS. What the scan
-- saw of the path to the SPD is a host_reading of kind spd-probe.
CREATE TABLE spd_reading (
  id         INTEGER PRIMARY KEY,
  host       TEXT    NOT NULL,
  scan_id    INTEGER REFERENCES scan(id),
  origin     TEXT    NOT NULL,
  bus        TEXT    NOT NULL DEFAULT '',
  address    INTEGER NOT NULL DEFAULT 0,
  driver     TEXT    NOT NULL DEFAULT '',
  form       TEXT    NOT NULL,
  state      TEXT    NOT NULL,
  detail     TEXT    NOT NULL DEFAULT '',
  raw        BLOB,
  fields     TEXT    NOT NULL DEFAULT '',
  sha256     TEXT    NOT NULL,
  first_seen TEXT    NOT NULL,
  last_seen  TEXT    NOT NULL,
  dimm_uid   TEXT    NOT NULL DEFAULT '',
  tie        TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX spd_reading_host ON spd_reading (host, origin, last_seen);
