-- rasdaemon 0.8.0 (Fedora 44 package) database of the lab guest of
-- testdata/real/7.3.0-mce-clock-step, as "sqlite3 .dump" prints it.
--
-- The clock of the host was set back by 10 seconds after rasdaemon had
-- started, as NTP does at boot when the hardware clock runs ahead. rasdaemon
-- keeps the offset between wall clock and uptime that it measured at its
-- start: "timestamp" is 9 to 10 seconds ahead of "walltime", the time the
-- kernel gave each machine check. The kernel log dates the same errors by
-- the kernel's clock.
--
-- Ten machine checks injected with mce-inject on an emulated Skylake-SP: 7
-- corrected read errors, an uncorrected error found by the scrubber (UCNA),
-- one with action optional (SRAO) and one with action required (SRAR). The
-- three uncorrected errors each cost a page (memory_failure_event).
-- rasdaemon stores the status register as a signed number.

CREATE TABLE mc_event (id INTEGER PRIMARY KEY, timestamp TEXT, err_count INTEGER, err_type TEXT, err_msg TEXT, label TEXT, mc INTEGER, top_layer INTEGER, middle_layer INTEGER, lower_layer INTEGER, address INTEGER, grain INTEGER, syndrome INTEGER, driver_detail TEXT);
CREATE TABLE aer_event (id INTEGER PRIMARY KEY, timestamp TEXT, dev_name TEXT, err_type TEXT, err_msg TEXT);
CREATE TABLE extlog_event (id INTEGER PRIMARY KEY, timestamp TEXT, etype INTEGER, error_count INTEGER, severity INTEGER, address INTEGER, fru_id BLOB, fru_text TEXT, cper_data BLOB);
CREATE TABLE mce_record (id INTEGER PRIMARY KEY, timestamp TEXT, mcgcap INTEGER, mcgstatus INTEGER, status INTEGER, addr INTEGER, misc INTEGER, ip INTEGER, tsc INTEGER, walltime INTEGER, cpu INTEGER, cpuid INTEGER, apicid INTEGER, socketid INTEGER, cs INTEGER, bank INTEGER, cpuvendor INTEGER, bank_name TEXT, error_msg TEXT, mcgstatus_msg TEXT, mcistatus_msg TEXT, mcastatus_msg TEXT, user_action TEXT, mc_location TEXT);
INSERT INTO mce_record VALUES(1,'2026-11-15 05:24:50 +0000',16777482,0,-7205759128914820976,4363522112,134,0,0,1794720280,0,329300,0,0,0,7,0,'','MEMORY CONTROLLER RD_CHANNEL0_ERR Transaction: Memory read error MscodDataRdErr','mcgstatus=0','Corrected_error Error_enabled','M2M: ','','n_errors=1');
INSERT INTO mce_record VALUES(2,'2026-11-15 05:24:51 +0000',16777482,0,-7205759128914820975,4367264128,134,0,0,1794720281,0,329300,0,0,0,7,0,'','MEMORY CONTROLLER RD_CHANNEL1_ERR Transaction: Memory read error MscodDataRdErr','mcgstatus=0','Corrected_error Error_enabled','M2M: ','','n_errors=1');
INSERT INTO mce_record VALUES(3,'2026-11-15 05:24:53 +0000',16777482,0,-7205759128914820976,4367360256,134,0,0,1794720283,0,329300,0,0,0,8,0,'','MEMORY CONTROLLER RD_CHANNEL0_ERR Transaction: Memory read error MscodDataRdErr','mcgstatus=0','Corrected_error Error_enabled','M2M: ','','n_errors=1');
INSERT INTO mce_record VALUES(4,'2026-11-15 05:24:54 +0000',16777482,0,-7205759128914820976,4367360256,134,0,0,1794720284,0,329300,0,0,0,8,0,'','MEMORY CONTROLLER RD_CHANNEL0_ERR Transaction: Memory read error MscodDataRdErr','mcgstatus=0','Corrected_error Error_enabled','M2M: ','','n_errors=1');
INSERT INTO mce_record VALUES(5,'2026-11-15 05:24:56 +0000',16777482,0,-7205759128914820976,4363661248,134,0,0,1794720286,0,329300,0,0,0,7,0,'','MEMORY CONTROLLER RD_CHANNEL0_ERR Transaction: Memory read error MscodDataRdErr','mcgstatus=0','Corrected_error Error_enabled','M2M: ','','n_errors=1');
INSERT INTO mce_record VALUES(6,'2026-11-15 05:24:57 +0000',16777482,0,-7205759128914820976,4363661248,134,0,0,1794720287,0,329300,0,0,0,7,0,'','MEMORY CONTROLLER RD_CHANNEL0_ERR Transaction: Memory read error MscodDataRdErr','mcgstatus=0','Corrected_error Error_enabled','M2M: ','','n_errors=1');
INSERT INTO mce_record VALUES(7,'2026-11-15 05:24:58 +0000',16777482,0,-7205759128914820976,4363661248,134,0,0,1794720288,0,329300,0,0,0,7,0,'','MEMORY CONTROLLER RD_CHANNEL0_ERR Transaction: Memory read error MscodDataRdErr','mcgstatus=0','Corrected_error Error_enabled','M2M: ','','n_errors=1');
INSERT INTO mce_record VALUES(8,'2026-11-15 05:25:00 +0000',16777482,0,-4899916394579033920,4363653632,134,0,0,1794720290,0,329300,0,0,0,7,0,'','MEMORY CONTROLLER MS_CHANNEL0_ERR Transaction: Memory scrubbing error MscodDataRdErr','mcgstatus=0','Uncorrected_error Error_enabled','M2M: ','','n_errors=0');
INSERT INTO mce_record VALUES(9,'2026-11-15 05:25:01 +0000',16777482,5,-4827858800541105984,4363576320,134,0,194331079995,1794720292,0,329300,0,0,0,7,0,'','MEMORY CONTROLLER MS_CHANNEL0_ERR Transaction: Memory scrubbing error MscodDataRdErr','mcgstatus=5 RIPV MCIP','Uncorrected_error Error_enabled SRAO','M2M: ','','n_errors=0');
INSERT INTO mce_record VALUES(10,'2026-11-15 05:25:03 +0000',16777482,7,-4791830003521158860,4355994432,134,4198400,199702640417,1794720293,0,329300,0,0,51,1,0,'','Data CACHE Level-0 Data-Read Error','mcgstatus=7 RIPV EIPV MCIP','Uncorrected_error Error_enabled SRAR','Data CACHE Level-0 Data-Read Error','','');
CREATE TABLE devlink_event (id INTEGER PRIMARY KEY, timestamp TEXT, bus_name TEXT, dev_name TEXT, driver_name TEXT, reporter_name TEXT, msg TEXT);
CREATE TABLE disk_errors (id INTEGER PRIMARY KEY, timestamp TEXT, dev TEXT, sector INTEGER, nr_sector INTEGER, error TEXT, rwbs TEXT, cmd TEXT);
CREATE TABLE memory_failure_event (id INTEGER PRIMARY KEY, timestamp TEXT, pfn TEXT, page_type TEXT, action_result TEXT);
INSERT INTO memory_failure_event VALUES(1,'2026-11-15 05:25:00 +0000','0x104181','dirty unevictable LRU page','Recovered');
INSERT INTO memory_failure_event VALUES(2,'2026-11-15 05:25:01 +0000','0x10416e','dirty unevictable LRU page','Recovered');
INSERT INTO memory_failure_event VALUES(3,'2026-11-15 05:25:03 +0000','0x103a33','dirty unevictable LRU page','Recovered');
