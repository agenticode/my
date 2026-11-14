-- rasdaemon 0.6.0 database of a two-socket Intel Xeon (skx_edac, Linux 4.18).
-- Schema as rasdaemon 0.6.0 creates it: no dev_name in aer_event, no
-- memory_failure_event, no CXL tables. The 4.18 driver prints row and column
-- as bare hexadecimal.

CREATE TABLE IF NOT EXISTS mc_event (id INTEGER PRIMARY KEY, timestamp TEXT, err_count INTEGER, err_type TEXT, err_msg TEXT, label TEXT, mc INTEGER, top_layer INTEGER, middle_layer INTEGER, lower_layer INTEGER, address INTEGER, grain INTEGER, syndrome INTEGER, driver_detail TEXT);
CREATE TABLE IF NOT EXISTS aer_event (id INTEGER PRIMARY KEY, timestamp TEXT, err_type TEXT, err_msg TEXT);
CREATE TABLE IF NOT EXISTS non_standard_event (id INTEGER PRIMARY KEY, timestamp TEXT, sec_type BLOB, fru_id BLOB, fru_text TEXT, severity TEXT, error BLOB);
CREATE TABLE IF NOT EXISTS arm_event (id INTEGER PRIMARY KEY, timestamp TEXT, error_count INTEGER, affinity INTEGER, mpidr INTEGER, running_state INTEGER, psci_state INTEGER);
CREATE TABLE IF NOT EXISTS extlog_event (id INTEGER PRIMARY KEY, timestamp TEXT, etype INTEGER, error_count INTEGER, severity INTEGER, address INTEGER, fru_id BLOB, fru_text TEXT, cper_data BLOB);
CREATE TABLE IF NOT EXISTS mce_record (id INTEGER PRIMARY KEY, timestamp TEXT, mcgcap INTEGER, mcgstatus INTEGER, status INTEGER, addr INTEGER, misc INTEGER, ip INTEGER, tsc INTEGER, walltime INTEGER, cpu INTEGER, cpuid INTEGER, apicid INTEGER, socketid INTEGER, cs INTEGER, bank INTEGER, cpuvendor INTEGER, bank_name TEXT, error_msg TEXT, mcgstatus_msg TEXT, mcistatus_msg TEXT, mcastatus_msg TEXT, user_action TEXT, mc_location TEXT);

-- Three corrected read errors in one row of one DIMM (socket 0, IMC 0, channel 1, slot 0).
INSERT INTO mc_event VALUES (1, '2023-02-27 14:36:26 +0800', 1, 'Corrected', 'memory read error', 'CPU_SrcID#0_MC#0_Chan#1_DIMM#0', 0, 1, 0, -1, 0x1f8bd19c0, 5, 0, ' err_code:0101:0091 socket:0 imc:0 rank:1 bg:1 ba:3 row:1b8b col:338');
INSERT INTO mc_event VALUES (2, '2023-02-27 14:36:26 +0800', 1, 'Corrected', 'memory read error', 'CPU_SrcID#0_MC#0_Chan#1_DIMM#0', 0, 1, 0, -1, 0x1f8bd1a40, 5, 0, ' err_code:0101:0091 socket:0 imc:0 rank:1 bg:1 ba:3 row:1b8b col:348');
INSERT INTO mc_event VALUES (3, '2023-02-27 19:02:11 +0800', 3, 'Corrected', 'memory read error', 'CPU_SrcID#0_MC#0_Chan#1_DIMM#0', 0, 1, 0, -1, 0x1f8bd1000, 5, 0, ' OVERFLOW err_code:0101:0091 socket:0 imc:0 rank:1 bg:1 ba:3 row:1b8b col:200');
-- The patrol scrubber finds an uncorrected error in the same row.
INSERT INTO mc_event VALUES (4, '2023-03-01 03:14:07 +0800', 1, 'Uncorrected', 'memory scrubbing error', 'CPU_SrcID#0_MC#0_Chan#1_DIMM#0', 0, 1, 0, -1, 0x1f8bd19c0, 5, 0, ' recoverable err_code:0008:00c1 socket:0 imc:0 rank:1 bg:1 ba:3 row:1b8b col:338');
-- Another DIMM on socket 1, after the host moved to UTC.
INSERT INTO mc_event VALUES (5, '2023-03-05 08:00:00 +0000', 1, 'Corrected', 'memory read error', 'CPU_SrcID#1_MC#1_Chan#2_DIMM#1', 3, 2, 1, -1, 0x5c7f01240, 5, 0, ' err_code:0101:0092 socket:1 imc:1 rank:1 bg:0 ba:2 row:1000 col:10');
-- Rows that cannot be used: localtime() failed and left the timestamp empty; an unknown error type.
INSERT INTO mc_event VALUES (6, '', 1, 'Corrected', 'memory read error', 'CPU_SrcID#0_MC#0_Chan#1_DIMM#0', 0, 1, 0, -1, 0x1f8bd19c0, 5, 0, ' err_code:0101:0091 socket:0 imc:0 rank:1 bg:1 ba:3 row:1b8b col:338');
INSERT INTO mc_event VALUES (7, '2023-03-05 09:00:00 +0000', 1, 'Unknown', 'memory read error', 'CPU_SrcID#0_MC#0_Chan#1_DIMM#0', 0, 1, 0, -1, 0x1f8bd19c0, 5, 0, '');
-- An informational record.
INSERT INTO mc_event VALUES (8, '2023-03-05 09:30:00 +0000', 1, 'Info', '', 'any memory', 0, -1, -1, -1, 0, 0, 0, '');

-- The machine check records behind events 1, 2 and 4 (bank 7 is IMC 0 on this model).
INSERT INTO mce_record VALUES (1, '2023-02-27 14:36:26 +0800', 0x7000c14, 0, 0x8c00004000010091, 0x1f8bd19c0, 0x200000c0a0001086, 0, 0, 1677479786, 12, 0x50654, 24, 0, 0, 7, 0, 'iMC0', 'MEMORY CONTROLLER RD_CHANNEL1_ERR', '', 'Corrected_error', 'Transaction: Memory read error', '', 'memory_channel=1');
INSERT INTO mce_record VALUES (2, '2023-02-27 14:36:26 +0800', 0x7000c14, 0, 0x8c00004000010091, 0x1f8bd1a40, 0x200000c0a0001086, 0, 0, 1677479786, 12, 0x50654, 24, 0, 0, 7, 0, 'iMC0', 'MEMORY CONTROLLER RD_CHANNEL1_ERR', '', 'Corrected_error', 'Transaction: Memory read error', '', 'memory_channel=1');
INSERT INTO mce_record VALUES (3, '2023-03-01 03:14:07 +0800', 0x7000c14, 0, 0xbd000000000800c1, 0x1f8bd19c0, 0x200000c0a0001086, 0, 0, 1677611647, 12, 0x50654, 24, 0, 0, 7, 0, 'iMC0', 'MEMORY CONTROLLER MS_CHANNEL1_ERR', '', 'Uncorrected_error Action optional', 'Transaction: Memory scrubbing error', '', 'memory_channel=1');
-- A memory machine check without an EDAC record: skx_edac was not loaded yet.
INSERT INTO mce_record VALUES (4, '2023-02-20 10:00:00 +0800', 0x7000c14, 0, 0x8c00004000010090, 0x2a0000040, 0x200000c0a0001086, 0, 0, 1676858400, 3, 0x50654, 6, 0, 0, 7, 0, 'iMC0', 'MEMORY CONTROLLER RD_CHANNEL0_ERR', '', 'Corrected_error', 'Transaction: Memory read error', '', 'memory_channel=0');
-- A machine check that is not a memory error.
INSERT INTO mce_record VALUES (5, '2023-03-02 11:11:11 +0800', 0x7000c14, 0, 0x9000004000000135, 0, 0, 0, 0, 1677726671, 5, 0x50654, 10, 0, 0, 1, 0, 'L1 cache', 'Data CACHE Level-1 Data-Read Error', '', 'Corrected_error', 'Data CACHE Level-1 Data-Read Error', '', '');
-- A row whose status register is not valid.
INSERT INTO mce_record VALUES (6, '2023-03-02 12:00:00 +0800', 0x7000c14, 0, 0, 0, 0, 0, 0, 1677729600, 5, 0x50654, 10, 0, 0, 7, 0, '', '', '', '', '', '', '');

INSERT INTO aer_event VALUES (1, '2023-02-28 01:00:00 +0800', 'Corrected', 'Receiver Error');
INSERT INTO aer_event VALUES (2, '2023-02-28 01:00:05 +0800', 'Corrected', 'Bad TLP');
