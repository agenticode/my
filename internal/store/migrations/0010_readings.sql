-- MemorY ledger, schema version 10.
--
-- Readings of the Memory Explorer: the text of a file or of a read-only
-- command about the memory of a host at the time of a scan (meminfo,
-- vmstat, slab caches, NUMA nodes, page statistics, CPU caches). They hold
-- no error record and no finding reads them.
--
-- The host gets the number of processor records of its SMBIOS tables
-- (the sockets of the board, 0 when not known) and the name of the first.
ALTER TABLE host ADD COLUMN sockets INTEGER NOT NULL DEFAULT 0;
ALTER TABLE host ADD COLUMN cpu TEXT NOT NULL DEFAULT '';

CREATE TABLE host_reading (
  id        INTEGER PRIMARY KEY,
  host      TEXT    NOT NULL,
  scan_id   INTEGER REFERENCES scan(id),
  kind      TEXT    NOT NULL,
  key       TEXT    NOT NULL DEFAULT '',
  taken_at  TEXT    NOT NULL,
  origin    TEXT    NOT NULL DEFAULT '',
  text      TEXT    NOT NULL,
  cut       INTEGER NOT NULL DEFAULT 0,
  UNIQUE (host, kind, key, taken_at)
);
CREATE INDEX host_reading_host ON host_reading (host, kind, taken_at);
