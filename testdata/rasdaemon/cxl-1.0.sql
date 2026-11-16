-- A rasdaemon 1.0 database of a host with two CXL memory devices. The tables
-- have the columns rasdaemon 1.0.0 creates (events/ras-cxl-handler.c).
CREATE TABLE cxl_dram_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, log_type TEXT, hdr_uuid TEXT, hdr_flags INTEGER, hdr_handle INTEGER, hdr_related_handle INTEGER, hdr_ts TEXT, hdr_length INTEGER, hdr_maint_op_class INTEGER, hdr_maint_op_sub_class INTEGER, hdr_ld_id INTEGER, hdr_head_id INTEGER, dpa INTEGER, dpa_flags INTEGER, descriptor INTEGER, type INTEGER, transaction_type INTEGER, channel INTEGER, rank INTEGER, nibble_mask INTEGER, bank_group INTEGER, bank INTEGER, row INTEGER, column INTEGER, cor_mask BLOB, hpa INTEGER, region TEXT, region_uuid TEXT, comp_id BLOB, pldm_entity_id BLOB, pldm_resource_id BLOB, sub_type INTEGER, sub_channel INTEGER, cme_threshold_ev_flags INTEGER, cvme_count INTEGER, hpa_alias0 INTEGER);
CREATE TABLE cxl_general_media_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, log_type TEXT, hdr_uuid TEXT, hdr_flags INTEGER, hdr_handle INTEGER, hdr_related_handle INTEGER, hdr_ts TEXT, hdr_length INTEGER, hdr_maint_op_class INTEGER, hdr_maint_op_sub_class INTEGER, hdr_ld_id INTEGER, hdr_head_id INTEGER, dpa INTEGER, dpa_flags INTEGER, descriptor INTEGER, type INTEGER, transaction_type INTEGER, channel INTEGER, rank INTEGER, device INTEGER, comp_id BLOB, hpa INTEGER, region TEXT, region_uuid TEXT, pldm_entity_id BLOB, pldm_resource_id BLOB, sub_type INTEGER, cme_threshold_ev_flags INTEGER, cme_count INTEGER, hpa_alias0 INTEGER);
CREATE TABLE cxl_memory_module_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, log_type TEXT, hdr_uuid TEXT, hdr_flags INTEGER, hdr_handle INTEGER, hdr_related_handle INTEGER, hdr_ts TEXT, hdr_length INTEGER, hdr_maint_op_class INTEGER, hdr_maint_op_sub_class INTEGER, hdr_ld_id INTEGER, hdr_head_id INTEGER, event_type INTEGER, health_status INTEGER, media_status INTEGER, life_used INTEGER, dirty_shutdown_cnt INTEGER, cor_vol_err_cnt INTEGER, cor_per_err_cnt INTEGER, device_temp INTEGER, add_status INTEGER, event_sub_type INTEGER, comp_id BLOB, pldm_entity_id BLOB, pldm_resource_id BLOB);
CREATE TABLE cxl_memory_sparing_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, log_type TEXT, hdr_uuid TEXT, hdr_flags INTEGER, hdr_handle INTEGER, hdr_related_handle INTEGER, hdr_ts TEXT, hdr_length INTEGER, hdr_maint_op_class INTEGER, hdr_maint_op_sub_class INTEGER, hdr_ld_id INTEGER, hdr_head_id INTEGER, flags INTEGER, result INTEGER, validity_flags INTEGER, res_avail INTEGER, channel INTEGER, sub_channel INTEGER, rank INTEGER, nibble_mask INTEGER, bank_group INTEGER, bank INTEGER, row INTEGER, column INTEGER, comp_id BLOB, pldm_entity_id BLOB, pldm_resource_id BLOB);
CREATE TABLE cxl_poison_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, trace_type TEXT, region TEXT, region_uuid TEXT, hpa INTEGER, dpa INTEGER, dpa_length INTEGER, source TEXT, flags INTEGER, overflow_ts TEXT, hpa_alias0 INTEGER);
CREATE TABLE cxl_overflow_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, log_type TEXT, count INTEGER, first_ts TEXT, last_ts TEXT);
CREATE TABLE cxl_generic_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, log_type TEXT, hdr_uuid TEXT, hdr_flags INTEGER, hdr_handle INTEGER, hdr_related_handle INTEGER, hdr_ts TEXT, hdr_length INTEGER, hdr_maint_op_class INTEGER, hdr_maint_op_sub_class INTEGER, hdr_ld_id INTEGER, hdr_head_id INTEGER, data BLOB);
CREATE TABLE cxl_aer_ue_event (id INTEGER PRIMARY KEY, timestamp TEXT, memdev TEXT, host TEXT, serial INTEGER, error_status INTEGER, first_error INTEGER, header_log BLOB);

-- DRAM events of mem0: a corrected error on a host read with the full
-- location, and an access to an invalid address, which is no memory error.
INSERT INTO cxl_dram_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, dpa, dpa_flags, descriptor, type, transaction_type, channel, rank, nibble_mask, bank_group, bank, row, "column", cor_mask, hpa, region, sub_type, sub_channel, cme_threshold_ev_flags, cvme_count)
  VALUES (1, '2026-11-10 08:00:00 +0000', 'mem0', '0000:35:00.0', 0x1A2B3C4D5E, 'Warning', '601dcbb3-9c06-4eab-b8af-4e9bfb5c9624', 0x18680, 1, 0, 0, 1, 0, 1, 0x4, 1, 2, 0x1a2f, 0x3f8, X'0400000000000000000000000000000000000000000000000000000000000000', 0x2c0018680, 'region0', 0, 1, 0, 3);
INSERT INTO cxl_dram_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, dpa, dpa_flags, descriptor, type, transaction_type, channel, rank, nibble_mask, bank_group, bank, row, "column", cor_mask, hpa, region)
  VALUES (2, '2026-11-10 08:05:00 +0000', 'mem0', '0000:35:00.0', 0x1A2B3C4D5E, 'Informational', '601dcbb3-9c06-4eab-b8af-4e9bfb5c9624', 0x99000, 1, 0, 2, 1, 0, 0, 0, 0, 0, 0, 0, NULL, -1, '');

-- General media events of mem1. Their event types are numbered differently:
-- 1 is an invalid address here, 4 the scrub media ECC error. The second row
-- is an uncorrected error the device's own error check scrub found.
INSERT INTO cxl_general_media_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, dpa, dpa_flags, descriptor, type, transaction_type, channel, rank, device, hpa, region)
  VALUES (1, '2026-11-10 09:00:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'Informational', 'fbcd0a77-c260-417f-85a9-088b1621eba6', 0x2000, 1, 0, 1, 1, 0, 0, 0, -1, '');
INSERT INTO cxl_general_media_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, dpa, dpa_flags, descriptor, type, transaction_type, channel, rank, device, hpa, region)
  VALUES (2, '2026-11-11 02:30:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'Failure', 'fbcd0a77-c260-417f-85a9-088b1621eba6', 0x7f000, 3, 1, 4, 7, 1, 0, 3, 0x3c007f000, 'region1');

-- Health of mem1: it asks to be replaced, twice, and mem0 reports a
-- temperature change.
INSERT INTO cxl_memory_module_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, event_type, health_status, media_status, life_used, dirty_shutdown_cnt, cor_vol_err_cnt, cor_per_err_cnt, device_temp, add_status, event_sub_type)
  VALUES (1, '2026-11-11 02:31:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'Failure', 'fe927475-dd59-4339-a586-79bab113b774', 0, 0x5, 0, 97, 2, 4012, 0, 61, 0x12, 0);
INSERT INTO cxl_memory_module_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, event_type, health_status, media_status, life_used, dirty_shutdown_cnt, cor_vol_err_cnt, cor_per_err_cnt, device_temp, add_status, event_sub_type)
  VALUES (2, '2026-11-12 02:31:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'Failure', 'fe927475-dd59-4339-a586-79bab113b774', 1, 0x4, 3, 97, 2, 4100, 0, 60, 0x12, 0);
INSERT INTO cxl_memory_module_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, event_type, health_status, media_status, life_used, dirty_shutdown_cnt, cor_vol_err_cnt, cor_per_err_cnt, device_temp, add_status, event_sub_type)
  VALUES (3, '2026-11-12 03:00:00 +0000', 'mem0', '0000:35:00.0', 0x1A2B3C4D5E, 'Warning', 'fe927475-dd59-4339-a586-79bab113b774', 3, 0, 0, 12, 0, 0, 0, 78, 0x4, 0);

-- Sparing on mem0: a row spared for good, a query for spare resources, and
-- an operation the device started that did not complete.
-- flags: 1 query, 2 hard, 4 started by the device. validity_flags: channel 1, rank 2, bank group 8, bank 16, row 32.
INSERT INTO cxl_memory_sparing_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, hdr_maint_op_class, hdr_maint_op_sub_class, flags, result, validity_flags, res_avail, channel, sub_channel, rank, nibble_mask, bank_group, bank, row, "column")
  VALUES (1, '2026-11-10 08:10:00 +0000', 'mem0', '0000:35:00.0', 0x1A2B3C4D5E, 'Informational', 'e71f3a40-2d29-4092-8a39-4d1c966c7c65', 2, 1, 2, 0, 59, 3, 0, 0, 1, 0, 1, 2, 0x1a2f, 0);
INSERT INTO cxl_memory_sparing_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, hdr_maint_op_class, hdr_maint_op_sub_class, flags, result, validity_flags, res_avail, channel, sub_channel, rank, nibble_mask, bank_group, bank, row, "column")
  VALUES (2, '2026-11-10 08:11:00 +0000', 'mem0', '0000:35:00.0', 0x1A2B3C4D5E, 'Informational', 'e71f3a40-2d29-4092-8a39-4d1c966c7c65', 2, 1, 1, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0);
INSERT INTO cxl_memory_sparing_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, hdr_maint_op_class, hdr_maint_op_sub_class, flags, result, validity_flags, res_avail, channel, sub_channel, rank, nibble_mask, bank_group, bank, row, "column")
  VALUES (3, '2026-11-11 02:35:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'Warning', 'e71f3a40-2d29-4092-8a39-4d1c966c7c65', 2, 2, 4, 1, 24, 0, 0, 0, 0, 0, 0, 5, 0, 0);
INSERT INTO cxl_memory_sparing_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, flags, result, validity_flags, res_avail)
  VALUES (4, '', 'mem1', '0000:36:00.0', 0x5678, 'Warning', 'e71f3a40-2d29-4092-8a39-4d1c966c7c65', 2, 0, 0, 0);
-- A soft post package repair of a row of mem1, reported in the class of PPR.
INSERT INTO cxl_memory_sparing_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, hdr_maint_op_class, hdr_maint_op_sub_class, flags, result, validity_flags, res_avail, channel, sub_channel, rank, nibble_mask, bank_group, bank, row, "column")
  VALUES (5, '2026-11-12 05:00:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'Informational', 'e71f3a40-2d29-4092-8a39-4d1c966c7c65', 1, 0, 0, 0, 59, 1, 0, 0, 0, 0, 3, 1, 0x0c11, 0);

-- The poison list of mem1, read out twice, and an injection that was cleared.
INSERT INTO cxl_poison_event VALUES (1, '2026-11-11 03:00:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'List', 'region1', '5b0c3f2e-0000-4000-8000-000000000001', 0x3c007f000, 0x7f000, 64, 'Internal', 0, '1970-01-01 00:00:00 +0000', -1);
INSERT INTO cxl_poison_event VALUES (2, '2026-11-11 03:00:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'List', '', '', -1, 0x100000, 128, 'Unknown', 0, '1970-01-01 00:00:00 +0000', -1);
INSERT INTO cxl_poison_event VALUES (3, '2026-11-12 03:00:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'List', 'region1', '5b0c3f2e-0000-4000-8000-000000000001', 0x3c007f000, 0x7f000, 64, 'Internal', 0, '1970-01-01 00:00:00 +0000', -1);
INSERT INTO cxl_poison_event VALUES (4, '2026-11-12 04:00:00 +0000', 'mem0', '0000:35:00.0', 0x1A2B3C4D5E, 'Inject', '', '', -1, 0x40, 64, 'Injected', 0, '1970-01-01 00:00:00 +0000', -1);
INSERT INTO cxl_poison_event VALUES (5, '2026-11-12 04:01:00 +0000', 'mem0', '0000:35:00.0', 0x1A2B3C4D5E, 'Clear', '', '', -1, 0x40, 64, 'Injected', 0, '1970-01-01 00:00:00 +0000', -1);

INSERT INTO cxl_overflow_event VALUES (1, '2026-11-11 02:40:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'Failure', 7, '2026-11-11 02:30:05 +0000', '2026-11-11 02:39:59 +0000');
INSERT INTO cxl_generic_event (id, timestamp, memdev, host, serial, log_type, hdr_uuid, data) VALUES (1, '2026-11-11 02:41:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 'Informational', '00000000-0000-0000-0000-000000000000', X'00');
INSERT INTO cxl_aer_ue_event VALUES (1, '2026-11-11 02:42:00 +0000', 'mem1', '0000:36:00.0', 0x5678, 0x80, 7, X'00');
