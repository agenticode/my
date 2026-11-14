-- MemorY ledger, schema version 3.
--
-- Records of the inputs beyond the host's own logs: ECC counters of GPU
-- memory, readings of the DDR5 error check and scrub (ECS) registers, and a
-- count of both in the source inventory.

-- A GPU memory record from a kernel log without wall-clock times is only
-- placed relative to boot, like an event. Snapshots carry the ECC state and
-- counters next to the row remapping state: of the frame buffer memory
-- (dram) and of the SRAM in the GPU, kept for the life of the GPU and, with
-- the suffix _volatile, since the driver was loaded.
ALTER TABLE gpu_mem ADD COLUMN time_quality TEXT NOT NULL DEFAULT 'exact';
ALTER TABLE gpu_mem ADD COLUMN ecc_enabled INTEGER;
ALTER TABLE gpu_mem ADD COLUMN dram_correctable INTEGER;
ALTER TABLE gpu_mem ADD COLUMN dram_uncorrectable INTEGER;
ALTER TABLE gpu_mem ADD COLUMN dram_correctable_volatile INTEGER;
ALTER TABLE gpu_mem ADD COLUMN dram_uncorrectable_volatile INTEGER;
ALTER TABLE gpu_mem ADD COLUMN sram_correctable INTEGER;
ALTER TABLE gpu_mem ADD COLUMN sram_uncorrectable INTEGER;
ALTER TABLE gpu_mem ADD COLUMN sram_correctable_volatile INTEGER;
ALTER TABLE gpu_mem ADD COLUMN sram_uncorrectable_volatile INTEGER;
ALTER TABLE gpu_mem ADD COLUMN sram_threshold_exceeded INTEGER;
ALTER TABLE gpu_mem ADD COLUMN scan_id INTEGER REFERENCES scan(id);
CREATE INDEX gpu_mem_uuid ON gpu_mem(host, gpu_uuid);

-- Readings of the DDR5 ECS mode registers MR14 to MR20, one row per module
-- or device and reading. A reading is not an error event: the errors a DDR5
-- device corrects itself are only visible as these counts. The registers
-- are stored as read; decoding is done by the reader.
CREATE TABLE ecs_sample (
  id              INTEGER PRIMARY KEY,
  host            TEXT    NOT NULL,
  observed_at     TEXT    NOT NULL,
  time_quality    TEXT    NOT NULL DEFAULT 'exact' CHECK (time_quality IN ('exact', 'estimated', 'relative')),
  "SocketId"      INTEGER,
  "ChannelId"     INTEGER,
  "ChannelSlotId" INTEGER,
  "SubChannel"    INTEGER,
  "Rank"          INTEGER,
  "DeviceId"      INTEGER,
  label           TEXT    NOT NULL DEFAULT '',
  slot            TEXT    NOT NULL DEFAULT '',
  dimm_uid        TEXT    REFERENCES dimm(dimm_uid),
  mr14            INTEGER CHECK (mr14 BETWEEN 0 AND 255),
  mr15            INTEGER CHECK (mr15 BETWEEN 0 AND 255),
  mr16            INTEGER CHECK (mr16 BETWEEN 0 AND 255),
  mr17            INTEGER CHECK (mr17 BETWEEN 0 AND 255),
  mr18            INTEGER CHECK (mr18 BETWEEN 0 AND 255),
  mr19            INTEGER CHECK (mr19 BETWEEN 0 AND 255),
  mr20            INTEGER CHECK (mr20 BETWEEN 0 AND 255),
  boot_id         TEXT,
  source          TEXT    NOT NULL,
  raw_ref         TEXT    NOT NULL,
  raw             TEXT    NOT NULL DEFAULT '',
  fingerprint     TEXT    NOT NULL UNIQUE,
  scan_id         INTEGER REFERENCES scan(id)
);
CREATE INDEX ecs_sample_slot ON ecs_sample(host, slot, observed_at);
CREATE INDEX ecs_sample_dimm ON ecs_sample(dimm_uid, observed_at);

-- GPU memory records and ECS readings a source produced.
ALTER TABLE source ADD COLUMN samples INTEGER NOT NULL DEFAULT 0;
