-- rasdaemon 1.0.0 database of a firmware-first platform: errors arrive as
-- CPER records through GHES and ghes_edac repeats the firmware's fields in
-- the driver detail. A fresh 1.0.0 database indexes the timestamp column and
-- has ppin and microcode inside mce_record. The extlog table holds the
-- compact CPER copy (55 bytes) in cper_data.

CREATE TABLE IF NOT EXISTS mc_event (id INTEGER PRIMARY KEY, timestamp TEXT, err_count INTEGER, err_type TEXT, err_msg TEXT, label TEXT, mc INTEGER, top_layer INTEGER, middle_layer INTEGER, lower_layer INTEGER, address INTEGER, grain INTEGER, syndrome INTEGER, driver_detail TEXT);
CREATE INDEX IF NOT EXISTS mc_event_timestamp_idx ON mc_event (timestamp);
CREATE TABLE IF NOT EXISTS extlog_event (id INTEGER PRIMARY KEY, timestamp TEXT, etype INTEGER, error_count INTEGER, severity INTEGER, address INTEGER, fru_id BLOB, fru_text TEXT, cper_data BLOB);
CREATE INDEX IF NOT EXISTS extlog_event_timestamp_idx ON extlog_event (timestamp);
CREATE TABLE IF NOT EXISTS mce_record (id INTEGER PRIMARY KEY, timestamp TEXT, mcgcap INTEGER, mcgstatus INTEGER, status INTEGER, addr INTEGER, misc INTEGER, ip INTEGER, tsc INTEGER, walltime INTEGER, ppin INTEGER, cpu INTEGER, cpuid INTEGER, apicid INTEGER, socketid INTEGER, cs INTEGER, bank INTEGER, cpuvendor INTEGER, microcode INTEGER, bank_name TEXT, error_msg TEXT, mcgstatus_msg TEXT, mcistatus_msg TEXT, mcastatus_msg TEXT, user_action TEXT, mc_location TEXT);
CREATE INDEX IF NOT EXISTS mce_record_timestamp_idx ON mce_record (timestamp);
CREATE TABLE IF NOT EXISTS memory_failure_event (id INTEGER PRIMARY KEY, timestamp TEXT, pfn TEXT, page_type TEXT, action_result TEXT);
CREATE TABLE IF NOT EXISTS signal_event (id INTEGER PRIMARY KEY, timestamp TEXT, sig INTEGER, errorno INTEGER, code INTEGER, comm TEXT, pid INTEGER, grp INTEGER, res INTEGER);

-- Firmware reported the full location: grade A, in decimal.
INSERT INTO mc_event VALUES (1, '2026-10-02 09:15:30 +0900', 1, 'Corrected', 'single-bit ECC', 'NODE 1 CPU1_DIMM_B1', 0, 9, -1, -1, 0xb5689dc40, 6, 0, 'APEI location: node:0 card:1 module:0 rank:1 bank_group:1 bank_address:3 device:7 row:6703 column:1016 bit_position:5 DIMM location: NODE 1 CPU1_DIMM_B1 status(0x0000000000000400): Storage error in DRAM memory');
INSERT INTO mc_event VALUES (2, '2026-10-02 09:15:31 +0900', 1, 'Corrected', 'scrub corrected error', 'NODE 1 CPU1_DIMM_B1', 0, 9, -1, -1, 0xb5689dd00, 6, 0, 'APEI location: node:0 card:1 module:0 rank:1 bank_group:1 bank_address:3 device:7 row:6703 column:1040 bit_position:5 DIMM location: NODE 1 CPU1_DIMM_B1 status(0x0000000000000400): Storage error in DRAM memory');
-- Firmware reported only the module: grade B. The kernel could not resolve the DMI handle.
INSERT INTO mc_event VALUES (3, '2026-10-03 01:02:03 +0900', 1, 'Corrected', 'single-bit ECC', 'unknown memory', 0, -1, -1, -1, 0x12f4a6000, 12, 0, 'APEI location: node:1 card:2 module:1 DIMM location: not present. DMI handle: 0x001a status(0x0000000000000400): Storage error in DRAM memory');
-- No address and no location: grade C.
INSERT INTO mc_event VALUES (4, '2026-10-03 01:02:04 +0900', 2, 'Corrected', 'unknown error', 'unknown memory', 0, -1, -1, -1, 0, 0, 0, 'APEI location: ');
-- An uncorrected error found by the scrubber, and a fatal one.
INSERT INTO mc_event VALUES (5, '2026-10-05 17:00:00 +0900', 1, 'Uncorrected', 'scrub uncorrected error', 'NODE 1 CPU1_DIMM_B1', 0, 9, -1, -1, 0xb5689dc40, 6, 0, 'APEI location: node:0 card:1 module:0 rank:1 bank_group:1 bank_address:3 device:7 row:6703 column:1016 bit_position:5 DIMM location: NODE 1 CPU1_DIMM_B1 status(0x0000000000000400): Storage error in DRAM memory');
INSERT INTO mc_event VALUES (6, '2026-10-06 00:00:00 +0900', 1, 'Fatal', 'multi-bit ECC', 'NODE 1 CPU1_DIMM_B1', 0, 9, -1, -1, 0xb5689dc40, 6, 0, 'APEI location: node:0 card:1 module:0 rank:1 bank_group:1 bank_address:3 device:7 row:6703 column:1016 DIMM location: NODE 1 CPU1_DIMM_B1 status(0x0000000000000400): Storage error in DRAM memory');

-- extlog records. The first is the firmware record of mc_event 1 (same address, same second).
-- error_count holds the kernel's sequence number, severity the CPER severity (2 = corrected, 0 = recoverable).
INSERT INTO extlog_event VALUES (1, '2026-10-02 09:15:30 +0900', 2, 17, 2, 0xb5689dc40, X'00112233445566778899AABBCCDDEEFF', 'CPU1_DIMM_B1', X'B8871A0000000000000001000000030107002F1AF803050000000000000000000000000000000000000000000000000001000000001100');
INSERT INTO extlog_event VALUES (2, '2026-10-04 12:00:00 +0900', 3, 18, 0, 0x12f4a6040, X'00112233445566778899AABBCCDDEEFF', 'CPU2_DIMM_C2', X'380002000000000001000200010000000000000000000000000000000000000000000000000000000000000000000000000000001A0000');
-- A record with a truncated CPER blob and one whose address was not valid (all ones).
INSERT INTO extlog_event VALUES (3, '2026-10-04 12:00:01 +0900', 2, 19, 2, 0x12f4a6080, NULL, '', X'3800020000');
INSERT INTO extlog_event VALUES (4, '2026-10-04 12:00:02 +0900', 255, 20, 2, -1, NULL, 'CPU2_DIMM_C2', X'380002000000000001000200010000000000000000000000000000000000000000000000000000000000000000000000000000001A0000');

INSERT INTO memory_failure_event VALUES (1, '2026-10-05 17:00:00 +0900', '0xb5689d', 'clean LRU page', 'Recovered');
INSERT INTO signal_event VALUES (1, '2026-10-05 17:00:00 +0900', 7, 0, 4, 'postgres', 4242, 0, 0);
