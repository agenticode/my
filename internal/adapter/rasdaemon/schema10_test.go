package rasdaemon

// schema10 lists the tables rasdaemon 1.0.0 creates for the records this
// package reads, with the type of every column: the table descriptors of
// its event handlers. A store on a server has these columns and, after the
// first of them, the hostname column.
//
// The types are the ones rasdaemon maps to SQL per backend:
//
//	serial  BIGINT AUTO_INCREMENT (MySQL), BIGSERIAL (PostgreSQL)
//	int32   INT, INTEGER
//	int64   BIGINT, BIGINT
//	stamp   DATETIME(6) filled in UTC, TIMESTAMPTZ
//	text    TEXT, TEXT
//	blob    BLOB, BYTEA
type column10 struct{ name, typ string }

var schema10 = map[string][]column10{
	"mc_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"err_count", "int32"}, {"err_type", "text"}, {"err_msg", "text"}, {"label", "text"}, {"mc", "int32"},
		{"top_layer", "int32"}, {"middle_layer", "int32"}, {"lower_layer", "int32"}, {"address", "int64"}, {"grain", "int64"}, {"syndrome", "int64"},
		{"driver_detail", "text"},
	},
	"mce_record": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"mcgcap", "int64"}, {"mcgstatus", "int64"}, {"status", "int64"}, {"addr", "int64"}, {"misc", "int64"},
		{"ip", "int64"}, {"tsc", "int64"}, {"walltime", "int64"}, {"ppin", "int64"}, {"cpu", "int32"}, {"cpuid", "int32"}, {"apicid", "int32"},
		{"socketid", "int32"}, {"cs", "int32"}, {"bank", "int32"}, {"cpuvendor", "int32"}, {"microcode", "int32"}, {"bank_name", "text"},
		{"error_msg", "text"}, {"mcgstatus_msg", "text"}, {"mcistatus_msg", "text"}, {"mcastatus_msg", "text"}, {"user_action", "text"},
		{"mc_location", "text"},
	},
	"extlog_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"etype", "int32"}, {"error_count", "int32"}, {"severity", "int32"}, {"address", "int64"},
		{"fru_id", "blob"}, {"fru_text", "text"}, {"cper_data", "blob"},
	},
	"memory_failure_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"pfn", "text"}, {"page_type", "text"}, {"action_result", "text"},
	},
	"cxl_poison_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"memdev", "text"}, {"host", "text"}, {"serial", "int64"}, {"trace_type", "text"}, {"region", "text"},
		{"region_uuid", "text"}, {"hpa", "int64"}, {"dpa", "int64"}, {"dpa_length", "int32"}, {"source", "text"}, {"flags", "int32"},
		{"overflow_ts", "text"}, {"hpa_alias0", "int64"},
	},
	"cxl_aer_ue_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"memdev", "text"}, {"host", "text"}, {"serial", "int64"}, {"error_status", "int32"},
		{"first_error", "int32"}, {"header_log", "blob"},
	},
	"cxl_aer_ce_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"memdev", "text"}, {"host", "text"}, {"serial", "int64"}, {"error_status", "int32"},
	},
	"cxl_overflow_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"memdev", "text"}, {"host", "text"}, {"serial", "int64"}, {"log_type", "text"}, {"count", "int32"},
		{"first_ts", "text"}, {"last_ts", "text"},
	},
	"cxl_generic_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"memdev", "text"}, {"host", "text"}, {"serial", "int64"}, {"log_type", "text"}, {"hdr_uuid", "text"},
		{"hdr_flags", "int32"}, {"hdr_handle", "int32"}, {"hdr_related_handle", "int32"}, {"hdr_ts", "text"}, {"hdr_length", "int32"},
		{"hdr_maint_op_class", "int32"}, {"hdr_maint_op_sub_class", "int32"}, {"hdr_ld_id", "int32"}, {"hdr_head_id", "int32"}, {"data", "blob"},
	},
	"cxl_general_media_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"memdev", "text"}, {"host", "text"}, {"serial", "int64"}, {"log_type", "text"}, {"hdr_uuid", "text"},
		{"hdr_flags", "int32"}, {"hdr_handle", "int32"}, {"hdr_related_handle", "int32"}, {"hdr_ts", "text"}, {"hdr_length", "int32"},
		{"hdr_maint_op_class", "int32"}, {"hdr_maint_op_sub_class", "int32"}, {"hdr_ld_id", "int32"}, {"hdr_head_id", "int32"}, {"dpa", "int64"},
		{"dpa_flags", "int32"}, {"descriptor", "int32"}, {"type", "int32"}, {"transaction_type", "int32"}, {"channel", "int32"}, {"rank", "int32"},
		{"device", "int32"}, {"comp_id", "blob"}, {"hpa", "int64"}, {"region", "text"}, {"region_uuid", "text"}, {"pldm_entity_id", "blob"},
		{"pldm_resource_id", "blob"}, {"sub_type", "int32"}, {"cme_threshold_ev_flags", "int32"}, {"cme_count", "int32"}, {"hpa_alias0", "int64"},
	},
	"cxl_dram_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"memdev", "text"}, {"host", "text"}, {"serial", "int64"}, {"log_type", "text"}, {"hdr_uuid", "text"},
		{"hdr_flags", "int32"}, {"hdr_handle", "int32"}, {"hdr_related_handle", "int32"}, {"hdr_ts", "text"}, {"hdr_length", "int32"},
		{"hdr_maint_op_class", "int32"}, {"hdr_maint_op_sub_class", "int32"}, {"hdr_ld_id", "int32"}, {"hdr_head_id", "int32"}, {"dpa", "int64"},
		{"dpa_flags", "int32"}, {"descriptor", "int32"}, {"type", "int32"}, {"transaction_type", "int32"}, {"channel", "int32"}, {"rank", "int32"},
		{"nibble_mask", "int32"}, {"bank_group", "int32"}, {"bank", "int32"}, {"row", "int32"}, {"column", "int32"}, {"cor_mask", "blob"},
		{"hpa", "int64"}, {"region", "text"}, {"region_uuid", "text"}, {"comp_id", "blob"}, {"pldm_entity_id", "blob"}, {"pldm_resource_id", "blob"},
		{"sub_type", "int32"}, {"sub_channel", "int32"}, {"cme_threshold_ev_flags", "int32"}, {"cvme_count", "int32"}, {"hpa_alias0", "int64"},
	},
	"cxl_memory_module_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"memdev", "text"}, {"host", "text"}, {"serial", "int64"}, {"log_type", "text"}, {"hdr_uuid", "text"},
		{"hdr_flags", "int32"}, {"hdr_handle", "int32"}, {"hdr_related_handle", "int32"}, {"hdr_ts", "text"}, {"hdr_length", "int32"},
		{"hdr_maint_op_class", "int32"}, {"hdr_maint_op_sub_class", "int32"}, {"hdr_ld_id", "int32"}, {"hdr_head_id", "int32"}, {"event_type", "int32"},
		{"health_status", "int32"}, {"media_status", "int32"}, {"life_used", "int32"}, {"dirty_shutdown_cnt", "int32"}, {"cor_vol_err_cnt", "int32"},
		{"cor_per_err_cnt", "int32"}, {"device_temp", "int32"}, {"add_status", "int32"}, {"event_sub_type", "int32"}, {"comp_id", "blob"},
		{"pldm_entity_id", "blob"}, {"pldm_resource_id", "blob"},
	},
	"cxl_memory_sparing_event": {
		{"id", "serial"}, {"timestamp", "stamp"}, {"memdev", "text"}, {"host", "text"}, {"serial", "int64"}, {"log_type", "text"}, {"hdr_uuid", "text"},
		{"hdr_flags", "int32"}, {"hdr_handle", "int32"}, {"hdr_related_handle", "int32"}, {"hdr_ts", "text"}, {"hdr_length", "int32"},
		{"hdr_maint_op_class", "int32"}, {"hdr_maint_op_sub_class", "int32"}, {"hdr_ld_id", "int32"}, {"hdr_head_id", "int32"}, {"flags", "int32"},
		{"result", "int32"}, {"validity_flags", "int32"}, {"res_avail", "int32"}, {"channel", "int32"}, {"sub_channel", "int32"}, {"rank", "int32"},
		{"nibble_mask", "int32"}, {"bank_group", "int32"}, {"bank", "int32"}, {"row", "int32"}, {"column", "int32"}, {"comp_id", "blob"},
		{"pldm_entity_id", "blob"}, {"pldm_resource_id", "blob"},
	},
}
