-- rasdaemon database of an AMD EPYC host (amd64_edac), created by rasdaemon
-- 0.6.8 and later opened by 0.8.4. The upgrade added columns with
-- ALTER TABLE ... ADD, so ppin and microcode sit at the end of mce_record
-- (a fresh 0.8.4 database has them after walltime and cpuvendor), and rows
-- stored before the upgrade hold NULL there. The CXL tables were created by
-- 0.8.4.

CREATE TABLE IF NOT EXISTS mc_event (id INTEGER PRIMARY KEY, timestamp TEXT, err_count INTEGER, err_type TEXT, err_msg TEXT, label TEXT, mc INTEGER, top_layer INTEGER, middle_layer INTEGER, lower_layer INTEGER, address INTEGER, grain INTEGER, syndrome INTEGER, driver_detail TEXT);
CREATE TABLE IF NOT EXISTS aer_event (id INTEGER PRIMARY KEY, timestamp TEXT, dev_name TEXT, err_type TEXT, err_msg TEXT);
CREATE TABLE IF NOT EXISTS mce_record (id INTEGER PRIMARY KEY, timestamp TEXT, mcgcap INTEGER, mcgstatus INTEGER, status INTEGER, addr INTEGER, misc INTEGER, ip INTEGER, tsc INTEGER, walltime INTEGER, cpu INTEGER, cpuid INTEGER, apicid INTEGER, socketid INTEGER, cs INTEGER, bank INTEGER, cpuvendor INTEGER, bank_name TEXT, error_msg TEXT, mcgstatus_msg TEXT, mcistatus_msg TEXT, mcastatus_msg TEXT, user_action TEXT, mc_location TEXT);
CREATE TABLE IF NOT EXISTS devlink_event (id INTEGER PRIMARY KEY, timestamp TEXT, bus_name TEXT, dev_name TEXT, driver_name TEXT, reporter_name TEXT, msg TEXT);
CREATE TABLE IF NOT EXISTS disk_errors (id INTEGER PRIMARY KEY, timestamp TEXT, dev TEXT, sector INTEGER, nr_sector INTEGER, error TEXT, rwbs TEXT, cmd TEXT);
CREATE TABLE IF NOT EXISTS memory_failure_event (id INTEGER PRIMARY KEY, timestamp TEXT, pfn TEXT, page_type TEXT, action_result TEXT);
ALTER TABLE mce_record ADD ppin INTEGER;
ALTER TABLE mce_record ADD microcode INTEGER;
CREATE TABLE IF NOT EXISTS cxl_aer_ce_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, error_status INTEGER);
CREATE TABLE IF NOT EXISTS cxl_general_media_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, log_type TEXT, hdr_uuid TEXT, hdr_flags INTEGER, hdr_handle INTEGER, hdr_related_handle INTEGER, hdr_ts TEXT, hdr_length INTEGER, hdr_maint_op_class INTEGER, hdr_maint_op_sub_class INTEGER, hdr_ld_id INTEGER, hdr_head_id INTEGER, dpa INTEGER, dpa_flags INTEGER, descriptor INTEGER, type INTEGER, transaction_type INTEGER, channel INTEGER, rank INTEGER, device INTEGER, comp_id BLOB, hpa INTEGER, region TEXT, region_uuid TEXT, pldm_entity_id BLOB, pldm_resource_id BLOB, sub_type INTEGER, cme_threshold_ev_flags INTEGER, cme_count INTEGER, hpa_alias0 INTEGER);
CREATE TABLE IF NOT EXISTS cxl_dram_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, log_type TEXT, hdr_uuid TEXT, hdr_flags INTEGER, hdr_handle INTEGER, hdr_related_handle INTEGER, hdr_ts TEXT, hdr_length INTEGER, hdr_maint_op_class INTEGER, hdr_maint_op_sub_class INTEGER, hdr_ld_id INTEGER, hdr_head_id INTEGER, dpa INTEGER, dpa_flags INTEGER, descriptor INTEGER, type INTEGER, transaction_type INTEGER, channel INTEGER, rank INTEGER, nibble_mask INTEGER, bank_group INTEGER, bank INTEGER, row INTEGER, column INTEGER, cor_mask BLOB, hpa INTEGER, region TEXT, region_uuid TEXT, comp_id BLOB, pldm_entity_id BLOB, pldm_resource_id BLOB, sub_type INTEGER, sub_channel INTEGER, cme_threshold_ev_flags INTEGER, cvme_count INTEGER, hpa_alias0 INTEGER);

-- amd64_edac reports chip-select row and channel, a system address and a syndrome. No row or column.
INSERT INTO mc_event VALUES (1, '2024-06-03 22:10:41 -0500', 1, 'Corrected', '', 'mc#0csrow#2channel#1', 0, 2, 1, -1, 0x7f5d2c9c0, 6, 0x4, '');
INSERT INTO mc_event VALUES (2, '2024-06-03 22:10:41 -0500', 1, 'Corrected', '', 'mc#0csrow#2channel#1', 0, 2, 1, -1, 0x7f5d2ca00, 6, 0x4, '');
-- After ras-mc-ctl --register-labels the label is the silk-screen name.
INSERT INTO mc_event VALUES (3, '2024-06-09 04:00:12 -0500', 1, 'Corrected', '', 'P0_DIMM_F1', 0, 5, 3, -1, 0x1a3c001c0, 6, 0x2a01, '');
-- A deferred error, an error whose address could not be mapped, and a fatal error.
INSERT INTO mc_event VALUES (4, '2024-06-10 13:45:00 -0500', 1, 'Deferred', '', 'mc#0csrow#2channel#1', 0, 2, 1, -1, 0x7f5d2c9c0, 6, 0, '');
INSERT INTO mc_event VALUES (5, '2024-06-10 13:50:00 -0500', 1, 'Corrected', 'Failed to map error addr to a csrow', 'any memory', 0, -1, -1, -1, 0, 0, 0, '');
INSERT INTO mc_event VALUES (6, '2024-06-11 00:00:01 -0500', 1, 'Fatal', '', 'mc#1csrow#0channel#0', 1, 0, 0, -1, 0x9e0040000, 6, 0, '');

-- Machine check records of events 1 and 2 (stored before the upgrade: ppin and microcode are NULL).
-- MCA_ADDR of the unified memory controller is a normalized address, not the system address above.
INSERT INTO mce_record (id, timestamp, mcgcap, mcgstatus, status, addr, misc, ip, tsc, walltime, cpu, cpuid, apicid, socketid, cs, bank, cpuvendor, bank_name, error_msg, mcgstatus_msg, mcistatus_msg, mcastatus_msg, user_action, mc_location)
  VALUES (1, '2024-06-03 22:10:41 -0500', 0x117, 0, 0x9c2040000000011b, 0x1d2c9c0, 0xd01a000101000000, 0, 0, 1717470641, 2, 0xa00f11, 4, 0, 0, 17, 2, 'Unified Memory Controller (bank=17)', 'Corrected error, no action required.', '', 'Error_overflow', 'DRAM ECC error.', '', 'memory_channel=1,csrow=2');
INSERT INTO mce_record (id, timestamp, mcgcap, mcgstatus, status, addr, misc, ip, tsc, walltime, cpu, cpuid, apicid, socketid, cs, bank, cpuvendor, bank_name, error_msg, mcgstatus_msg, mcistatus_msg, mcastatus_msg, user_action, mc_location)
  VALUES (2, '2024-06-03 22:10:41 -0500', 0x117, 0, 0x9c2040000000011b, 0x1d2ca00, 0xd01a000101000000, 0, 0, 1717470641, 2, 0xa00f11, 4, 0, 0, 17, 2, 'Unified Memory Controller (bank=17)', 'Corrected error, no action required.', '', '', 'DRAM ECC error.', '', 'memory_channel=1,csrow=2');
-- Stored after the upgrade. A DRAM error with no EDAC record at the same time, and an interface
-- error of the memory controller, which is not an error of the memory cells.
INSERT INTO mce_record (id, timestamp, mcgcap, mcgstatus, status, addr, misc, ip, tsc, walltime, cpu, cpuid, apicid, socketid, cs, bank, cpuvendor, bank_name, error_msg, mcgstatus_msg, mcistatus_msg, mcastatus_msg, user_action, mc_location, ppin, microcode)
  VALUES (3, '2024-06-12 06:30:00 -0500', 0x117, 0, 0x9c2040000000011b, 0x2e40080, 0xd01a000101000000, 0, 0, 1718191800, 2, 0xa00f11, 4, 0, 0, 18, 2, 'Unified Memory Controller (bank=18)', 'Corrected error, no action required.', '', '', 'DRAM ECC error.', '', 'memory_channel=3,csrow=1', 0x2b4c1a00f1d2e3, 0xa0011d1);
INSERT INTO mce_record (id, timestamp, mcgcap, mcgstatus, status, addr, misc, ip, tsc, walltime, cpu, cpuid, apicid, socketid, cs, bank, cpuvendor, bank_name, error_msg, mcgstatus_msg, mcistatus_msg, mcastatus_msg, user_action, mc_location, ppin, microcode)
  VALUES (4, '2024-06-12 06:31:00 -0500', 0x117, 0, 0xb00000000002010b, 0, 0, 0, 0, 1718191860, 2, 0xa00f11, 4, 0, 0, 18, 2, 'Unified Memory Controller (bank=18)', 'Uncorrected, software restartable error.', '', '', 'Write data parity error.', '', '', 0x2b4c1a00f1d2e3, 0xa0011d1);

-- Pages the kernel took offline after uncorrected errors, and one row with an unreadable page number.
INSERT INTO memory_failure_event VALUES (1, '2024-06-10 13:45:00 -0500', '0x7f5d2c', 'dirty LRU page', 'Recovered');
INSERT INTO memory_failure_event VALUES (2, '2024-06-10 13:45:02 -0500', '0x7f5d2d', 'free buddy page', 'Delayed');
INSERT INTO memory_failure_event VALUES (3, '2024-06-11 00:00:01 -0500', '0x9e0040', 'kernel slab page', 'Failed');
INSERT INTO memory_failure_event VALUES (4, '2024-06-11 00:00:02 -0500', 'n/a', 'unknown page', 'Ignored');

-- CXL memory device events. The second DRAM event has no valid location fields (stored as zeros).
INSERT INTO cxl_dram_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, dpa, dpa_flags, descriptor, type, transaction_type, channel, rank, nibble_mask, bank_group, bank, row, "column", cor_mask, hpa, region)
  VALUES (1, '2024-06-15 10:00:00 -0500', 'mem0', '0000:35:00.0', 0x1234, 'Warning', '601dcbb3-9c06-4eab-b8af-4e9bfb5c9624', 0x18680, 0, 0, 0, 1, 0, 1, 0x4, 1, 2, 0x1a2f, 0x3f8, X'0400000000000000000000000000000000000000000000000000000000000000', 0x2c0018680, 'region0');
INSERT INTO cxl_dram_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, dpa, dpa_flags, descriptor, type, transaction_type, channel, rank, nibble_mask, bank_group, bank, row, "column", cor_mask, hpa, region)
  VALUES (2, '2024-06-15 11:00:00 -0500', 'mem0', '0000:35:00.0', 0x1234, 'Failure', '601dcbb3-9c06-4eab-b8af-4e9bfb5c9624', 0x40000, 0, 1, 1, 5, 0, 0, 0, 0, 0, 0, 0, NULL, 0x2c0040000, 'region0');
INSERT INTO cxl_general_media_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, dpa, dpa_flags, descriptor, type, transaction_type, channel, rank, device, hpa, region)
  VALUES (1, '2024-06-15 12:00:00 -0500', 'mem1', '0000:36:00.0', 0x5678, 'Warning', 'fbcd0a77-c260-417f-85a9-088b1621eba6', 0x2000, 0, 0, 0, 1, 1, 0, 3, -1, '');
INSERT INTO cxl_aer_ce_event VALUES (1, '2024-06-15 12:30:00 -0500', 'mem0', '0000:35:00.0', 0x1234, 0x40);

INSERT INTO aer_event VALUES (1, '2024-06-04 00:00:00 -0500', '0000:c1:00.0', 'Corrected', 'Receiver Error');
INSERT INTO disk_errors VALUES (1, '2024-06-04 00:00:01 -0500', '8:0', 12345678, 8, 'critical medium', 'R', '28 00 00 bc 61 4e 00 00 08 00');
