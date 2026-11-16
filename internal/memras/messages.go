package memras

import (
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// Messages are the words of the settings check, in both languages.
var Messages = i18n.Catalog{
	// The settings.
	"setting.patrol-scrub":     {EN: "patrol scrub", KO: "패트롤 스크럽"},
	"setting.adddc":            {EN: "ADDDC", KO: "ADDDC"},
	"setting.memory-mode":      {EN: "memory mode", KO: "메모리 모드"},
	"setting.ce-logging":       {EN: "CE logging", KO: "CE 로깅"},
	"setting.ce-threshold":     {EN: "CE threshold event", KO: "CE 임계값 이벤트"},
	"setting.leaky-bucket":     {EN: "leaky bucket", KO: "leaky bucket"},
	"setting.ppr":              {EN: "post package repair", KO: "PPR"},
	"setting.memory-frequency": {EN: "memory frequency", KO: "메모리 주파수"},
	"setting.refresh-rate":     {EN: "refresh rate", KO: "리프레시 속도"},

	// What a value means.
	"state.on":              {EN: "on", KO: "켜짐"},
	"state.off":             {EN: "off", KO: "꺼짐"},
	"state.independent":     {EN: "independent", KO: "독립 채널"},
	"state.advanced-ecc":    {EN: "advanced ECC", KO: "Advanced ECC"},
	"state.adddc":           {EN: "ADDDC", KO: "ADDDC"},
	"state.rank-sparing":    {EN: "rank sparing", KO: "rank 스페어링"},
	"state.mirroring":       {EN: "mirroring", KO: "미러링"},
	"state.fault-resilient": {EN: "fault resilient", KO: "fault resilient"},
	"state.auto":            {EN: "not limited", KO: "제한 없음"},
	"state.limited":         {EN: "limited", KO: "제한됨"},
	"state.1x":              {EN: "1x", KO: "1x"},
	"state.2x":              {EN: "2x", KO: "2x"},

	// What became of a setting.
	"status.ok":            {EN: "as expected", KO: "기대와 일치"},
	"status.mismatch":      {EN: "not as expected", KO: "기대와 다름"},
	"status.no-fit":        {EN: "does not fit", KO: "맞지 않음"},
	"status.reported":      {EN: "reported", KO: "보고"},
	"status.unknown-value": {EN: "unknown value", KO: "모르는 값"},
	"status.absent":        {EN: "not in this BIOS", KO: "이 BIOS에 없음"},
	"status.unmapped":      {EN: "not mapped", KO: "매핑 없음"},

	// Why a setting has no mapping.
	"reason.not-documented": {EN: "no public document that was read gives a BIOS attribute for it", KO: "읽은 공개 문서에 해당 BIOS 속성이 없음"},
	"reason.no-vendor":      {EN: "the profile has no mapping for the BIOS of this vendor", KO: "프로필에 이 벤더 BIOS의 매핑이 없음"},

	// Why the built-in profile expects what it expects.
	"why.patrol-scrub": {EN: "with patrol scrub off, memory that is not read is not checked, and an error in it stays until an access finds it", KO: "패트롤 스크럽이 꺼져 있으면 읽지 않는 메모리는 검사되지 않고, 그 안의 오류는 접근할 때까지 남음"},
	"why.ce-logging":   {EN: "with it off the firmware logs no corrected error to the BMC, and the BMC log says nothing about corrected errors", KO: "꺼져 있으면 펌웨어가 CE를 BMC에 기록하지 않아 BMC 로그로는 CE를 알 수 없음"},

	// Lines about one setting.
	"line.pending":       {EN: "pending: {value} ({state}); it takes effect after a reboot", KO: "대기 중: {value} ({state}). 재부팅 뒤에 적용"},
	"line.pending_plain": {EN: "pending: {value}; it takes effect after a reboot", KO: "대기 중: {value}. 재부팅 뒤에 적용"},
	"line.pending_ok":    {EN: "the pending value is what the profile expects", KO: "대기 중인 값은 프로필의 기대와 일치"},
	"line.pending_bad":   {EN: "the pending value is not what the profile expects ({expected})", KO: "대기 중인 값은 프로필의 기대({expected})와 다름"},
	"line.read_only":     {EN: "the registry marks the attribute read-only: it cannot be changed as it stands", KO: "레지스트리에 읽기 전용으로 표시된 속성: 현재 상태로는 바꿀 수 없음"},
	"line.no_registry":   {EN: "the attribute registry was not read: whether this BIOS allows the expected value is not known", KO: "속성 레지스트리를 읽지 못해 이 BIOS가 기대값을 허용하는지 알 수 없음"},
	"line.allowed":       {EN: "the expectation does not fit this BIOS version: it allows {values}", KO: "기대값이 이 BIOS 버전에 맞지 않음: 허용하는 값은 {values}"},
	"line.absent":        {EN: "the settings of this BIOS on record do not list the attribute", KO: "기록된 이 BIOS의 설정에 이 속성이 없음"},
	"line.unknown_value": {EN: "the profile does not say what {value} means", KO: "프로필에 {value} 값의 의미가 없음"},
	"line.why":           {EN: "expected {expected}: {why}", KO: "기대값 {expected}: {why}"},

	// What the DIMMs of the host mean for a setting.
	"note.adddc_x8":            {EN: "{n} of {total} DIMMs are x8 ({slots}): ADDDC is on and has no effect on them. ADDDC works with x4 DIMMs only; it is a feature of Intel Xeon Scalable platforms", KO: "DIMM {total}개 중 {n}개가 x8({slots}): ADDDC가 켜져 있어도 이 DIMM에는 효과 없음. ADDDC는 x4 DIMM에서만 동작하며 Intel Xeon Scalable 플랫폼의 기능임"},
	"note.adddc_width_unknown": {EN: "the device width (x4 or x8) of {n} of {total} DIMMs is not known: ADDDC has an effect on x4 DIMMs only", KO: "DIMM {total}개 중 {n}개의 device width(x4, x8)를 알 수 없음: ADDDC는 x4 DIMM에만 효과 있음"},
	"note.adddc_one_rank":      {EN: "{n} {n|DIMM is|DIMMs are} on a channel with one rank ({slots}): ADDDC needs two or more ranks on a channel", KO: "DIMM {n}개가 rank가 하나뿐인 채널에 있음({slots}): ADDDC에는 채널당 rank가 2개 이상 필요"},
	"note.sddc_x8":             {EN: "{n} of {total} DIMMs are x8 ({slots}): device correction covers a nibble of an x8 device, and the failure of a whole x8 device is not corrected. A whole device is corrected on x4 DIMMs (stated for Intel Xeon Scalable and AMD EPYC servers)", KO: "DIMM {total}개 중 {n}개가 x8({slots}): 장치 정정은 x8 device의 nibble까지만 정정하고 x8 device 전체 고장은 정정하지 못함. device 전체 정정은 x4 DIMM에서 가능(Intel Xeon Scalable, AMD EPYC 서버 기준)"},

	// Speed.
	"speed.lead":        {EN: "{n} of {known} {known|DIMM runs|DIMMs run} below the speed {known|it is|they are} rated for: {list}", KO: "DIMM {known}개 중 {n}개가 정격 속도에 못 미치는 속도로 동작: {list}"},
	"speed.item":        {EN: "{slots} at {operating}, rated {rated}", KO: "{slots} 동작 {operating}, 정격 {rated}"},
	"speed.all_rated":   {EN: "all {known} {known|DIMM|DIMMs} with a known speed run at the speed they are rated for", KO: "속도를 아는 DIMM {known}개 모두 정격 속도로 동작"},
	"speed.candidates":  {EN: "What the records show that can be the reason. These are candidates; the records prove none of them:", KO: "기록에서 보이는 원인 후보. 후보일 뿐이며 이 기록으로 확정되는 것은 없음:"},
	"speed.none":        {EN: "the records show nothing that explains it", KO: "기록에는 원인으로 볼 만한 것이 없음"},
	"speed.bios":        {EN: "the BIOS limits the memory frequency ({attribute} = {value})", KO: "BIOS가 메모리 주파수를 제한함 ({attribute} = {value})"},
	"speed.per_channel": {EN: "{n} of these DIMMs share a channel with another DIMM ({slots}); the number of DIMMs on a channel is one of the things that set the memory frequency", KO: "이 중 DIMM {n}개가 다른 DIMM과 채널을 함께 씀({slots}). 채널당 DIMM 수는 메모리 주파수를 정하는 요소 중 하나임"},
	"speed.mixed":       {EN: "another module of the host is rated {rated}; the rated frequency of the DIMMs is one of the things that set the memory frequency", KO: "호스트의 다른 모듈이 정격 {rated}임. DIMM의 정격 주파수는 메모리 주파수를 정하는 요소 중 하나임"},
	"speed.platform":    {EN: "every DIMM with a known speed runs at {speed} MT/s: the processor and the board are what is left, and the ledger holds no processor model to check that against", KO: "속도를 아는 모든 DIMM이 {speed} MT/s로 동작: 남는 후보는 프로세서와 보드이며, 원장에는 이를 확인할 프로세서 모델이 없음"},

	// The check as a whole.
	"check.title":    {EN: "BIOS memory RAS settings", KO: "BIOS 메모리 RAS 설정"},
	"check.none":     {EN: "No BIOS settings are on record for this host. A BMC reports them over Redfish: \"my collect redfish --bmc ADDRESS -o DIR\", then \"my import DIR\".", KO: "이 호스트의 BIOS 설정 기록이 없음. BIOS 설정은 BMC가 Redfish로 알려 줌: \"my collect redfish --bmc ADDRESS -o DIR\" 실행 뒤 \"my import DIR\"."},
	"check.none_bmc": {EN: "No BIOS settings are on record for this host. The BMC at {address} was read, and its Bios resource came back as {state}.", KO: "이 호스트의 BIOS 설정 기록이 없음. BMC {address}를 읽었고 Bios 리소스의 결과는 {state}."},
	"check.builtin":  {EN: "built-in", KO: "기본 제공"},
	// What became of the Bios resource at a BMC (model.BMCClass.State).
	"check.state.unsupported": {EN: "unsupported", KO: "미지원"},
	"check.state.denied":      {EN: "denied", KO: "거부"},
	"check.state.timeout":     {EN: "timeout", KO: "시간 초과"},
	"check.state.error":       {EN: "error", KO: "오류"},
	"check.state.skipped":     {EN: "skipped", KO: "건너뜀"},
	"check.state.not offered": {EN: "not offered", KO: "제공 안 함"},
	"check.state.not reached": {EN: "not reached", KO: "도달 못 함"},
	"check.none_memory":       {EN: "The BMC reported the BIOS settings of this host, and none of them bears on memory, by its name or by its entry in the attribute registry.", KO: "BMC가 이 호스트의 BIOS 설정을 보고했으나 메모리와 관련된 설정은 없음. 이름으로도, 속성 레지스트리의 항목으로도 메모리 설정에 해당하는 속성이 없음."},
	"check.read":              {EN: "{n} memory {n|setting|settings} of the BIOS on record, read {when}", KO: "BIOS 메모리 설정 {n}개 기록됨, 읽은 시각 {when}"},
	"check.read_from":         {EN: "{n} memory {n|setting|settings} of the BIOS on record, read {when} from the BMC at {address}", KO: "BIOS 메모리 설정 {n}개 기록됨, {when}에 BMC {address}에서 읽음"},
	"check.registry":          {EN: "attribute registry {registry}", KO: "속성 레지스트리 {registry}"},
	"check.no_registry":       {EN: "the attribute registry was not read", KO: "속성 레지스트리를 읽지 못함"},
	"check.profile":           {EN: "profile {profile}, mapping for {vendor} (by {by})", KO: "프로필 {profile}, {vendor} 매핑 적용 ({by} 기준)"},
	"check.no_vendor":         {EN: "profile {profile}: it has no mapping for this BIOS (system manufacturer: {manufacturer})", KO: "프로필 {profile}: 이 BIOS의 매핑이 없음 (시스템 제조사: {manufacturer})"},
	"check.by.manufacturer":   {EN: "the system manufacturer", KO: "시스템 제조사"},
	"check.by.attributes":     {EN: "the attribute names of the BIOS", KO: "BIOS 속성 이름"},
	"check.unknown":           {EN: "unknown", KO: "알 수 없음"},
	"check.related":           {EN: "{n} other memory {n|setting|settings} on record that the profile maps to nothing:", KO: "프로필이 매핑하지 않는 다른 메모리 설정 {n}개:"},
	"check.unmapped":          {EN: "Not mapped ({settings}): {reason}.", KO: "매핑 없음 ({settings}): {reason}."},
	"check.sources":           {EN: "Where the mappings and rules in use come from:", KO: "적용한 매핑과 규칙의 출처:"},
	"check.summary_ok":        {EN: "{expected} {expected|expectation|expectations} of the profile, all met", KO: "프로필의 기대 {expected}개 모두 충족"},
	"check.summary_bad":       {EN: "{n} of {expected} {expected|expectation|expectations} of the profile {n|is|are} not met", KO: "프로필의 기대 {expected}개 중 {n}개 미충족"},
	"check.summary_pending":   {EN: "{n} pending {n|value|values} will not be as expected after the next reboot", KO: "대기 중인 값 {n}개는 다음 재부팅 뒤 기대와 달라짐"},
	"check.summary_none":      {EN: "none of the settings the profile expects something of could be read", KO: "프로필이 기대값을 둔 설정 중 읽은 것이 없음"},
	"check.col.setting":       {EN: "SETTING", KO: "설정"},
	"check.col.state":         {EN: "STATE", KO: "상태"},
	"check.col.value":         {EN: "BIOS ATTRIBUTE = VALUE", KO: "BIOS 속성 = 값"},
	"check.col.expected":      {EN: "EXPECTED", KO: "기대"},
	"check.col.check":         {EN: "CHECK", KO: "점검"},
}

// Text renders a statement of this package.
func Text(lang i18n.Lang, st model.Statement) string {
	if st.Code == "" {
		return st.Text
	}
	return Messages.Text(lang, st.Code, st.Args)
}

// SettingText names a setting.
func SettingText(lang i18n.Lang, setting string) string {
	return Messages.Text(lang, "setting."+setting, nil)
}

// StateText names what a value means. An empty state is "".
func StateText(lang i18n.Lang, state string) string {
	if state == "" {
		return ""
	}
	return Messages.Text(lang, "state."+state, nil)
}

// StatusText names what became of a setting.
func StatusText(lang i18n.Lang, status string) string {
	return Messages.Text(lang, "status."+status, nil)
}

// ReasonText says why a setting has no mapping: the text of a reason
// code, or the words of a profile as they are.
func ReasonText(lang i18n.Lang, reason string) string {
	if Messages.Has("reason." + reason) {
		return Messages.Text(lang, "reason."+reason, nil)
	}
	return reason
}

// WhyText says why the profile expects what it expects of a setting: the
// words of the profile, or for the built-in profile the text of the
// catalog. It is "" when nothing is said.
func WhyText(lang i18n.Lang, it *Item) string {
	if it.Why != "" {
		return it.Why
	}
	// The reasons of the catalog are those of the built-in expectations.
	if it.Expected != "" && Builtin().Expect[it.Setting] == it.Expected && Messages.Has("why."+it.Setting) {
		return Messages.Text(lang, "why."+it.Setting, nil)
	}
	return ""
}
