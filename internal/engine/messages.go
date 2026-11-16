package engine

import (
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// Messages holds every sentence the engine can state, in English and
// Korean. A finding carries the code and the arguments of each sentence;
// Text renders it in the reader's language.
//
// The words of a finding are chosen with care: a grade is a finding, and
// the texts say what was seen, what it rests on and what was not checked.
var Messages = i18n.Catalog{
	// Grades, the label of a finding and confidence levels.
	"grade.ok":                   {EN: "ok", KO: "정상"},
	"grade.observe":              {EN: "observe", KO: "관찰"},
	"grade.consider-replacement": {EN: "consider replacement", KO: "교체 고려"},
	"grade.replace-now":          {EN: "replace now", KO: "즉시 교체"},
	"grade.hold-replacement":     {EN: "hold replacement", KO: "교체 보류"},
	"label.finding":              {EN: "finding", KO: "소견"},
	"confidence.high":            {EN: "high", KO: "높음"},
	"confidence.medium":          {EN: "medium", KO: "보통"},
	"confidence.low":             {EN: "low", KO: "낮음"},

	// Thresholds that cannot be set (ConfigError).
	"config.unknown":            {EN: "unknown threshold \"{key}\"", KO: "알 수 없는 임계값 \"{key}\""},
	"config.not_number":         {EN: "{key}: \"{value}\" is not a number", KO: "{key}: \"{value}\": 숫자가 아님"},
	"config.not_whole":          {EN: "{key}: \"{value}\" is not a whole number", KO: "{key}: \"{value}\": 정수가 아님"},
	"config.too_large":          {EN: "{key}: \"{value}\" is larger than {max}", KO: "{key}: \"{value}\": {max} 이하여야 함"},
	"config.not_above_zero":     {EN: "{key}: {value} is not above zero", KO: "{key}: {value}: 0 초과여야 함"},
	"config.below_min":          {EN: "{key}: {value} is below the minimum of {min}", KO: "{key}: {value}: {min} 이상이어야 함"},
	"config.outside_above_zero": {EN: "{key}: {value} is outside the range above 0 to {max}", KO: "{key}: {value}: 0 초과 {max} 이하여야 함"},
	"config.outside":            {EN: "{key}: {value} is outside the range {min} to {max}", KO: "{key}: {value}: {min} 이상 {max} 이하여야 함"},
	"config.syntax":             {EN: "want \"key = value\", found \"{line}\"", KO: "\"key = value\" 형식이어야 함. 읽은 줄: \"{line}\""},
	"config.twice":              {EN: "{key} is already set on line {first}", KO: "{key}: {first}번째 줄에서 이미 정함"},
	"config.set_syntax":         {EN: "{value}: want key=value", KO: "{value}: key=value 형식이어야 함"},

	// Fault modes. The Level 1 names are terms of their own and stay as
	// they are in both languages.
	"mode.single-bit":   {EN: "single-bit", KO: "single bit"},
	"mode.row":          {EN: "row", KO: "row"},
	"mode.multi-row":    {EN: "multi-row", KO: "multi-row"},
	"mode.column":       {EN: "column", KO: "column"},
	"mode.bank":         {EN: "bank", KO: "bank"},
	"mode.device":       {EN: "device", KO: "device"},
	"mode.dimm":         {EN: "DIMM", KO: "DIMM"},
	"mode.system-issue": {EN: "system issue", KO: "system issue"},
	"mode.undetermined": {EN: "undetermined", KO: "판정 불가"},
	"mode.estimated":    {EN: "estimated", KO: "추정"},

	// Words that arguments refer to.
	"word.bios":              {EN: "the BIOS", KO: "BIOS"},
	"word.bmc":               {EN: "the BMC", KO: "BMC"},
	"word.firmware":          {EN: "firmware", KO: "펌웨어"},
	"word.kernel":            {EN: "kernel", KO: "커널"},
	"word.bios-version":      {EN: "BIOS", KO: "BIOS"},
	"word.hypervisor":        {EN: "the CPU reports a hypervisor", KO: "CPU가 하이퍼바이저를 보고함"},
	"kind.page-offline":      {EN: "page offline", KO: "page offline"},
	"kind.sppr":              {EN: "soft PPR", KO: "sPPR"},
	"kind.hppr":              {EN: "hard PPR", KO: "hPPR"},
	"kind.sparing":           {EN: "sparing", KO: "sparing"},
	"kind.row-remap":         {EN: "row remap", KO: "row remap"},
	"kind.reseat":            {EN: "reseat", KO: "reseat"},
	"kind.replacement":       {EN: "replacement", KO: "교체"},
	"kind.disable":           {EN: "disable", KO: "disable"},
	"kind.mark-failed":       {EN: "failure mark", KO: "failed 표시"},
	"name.label":             {EN: "label", KO: "라벨"},
	"name.handle":            {EN: "SMBIOS handle", KO: "SMBIOS 핸들"},
	"name.edac":              {EN: "EDAC position", KO: "EDAC 위치"},
	"name.topology":          {EN: "socket, controller, channel and slot numbers", KO: "소켓, 컨트롤러, 채널, 슬롯 번호"},
	"status.partial":         {EN: "partly read", KO: "일부만 읽음"},
	"word.loop_each_stopped": {EN: "each of these boots ended in a fatal hardware error", KO: "해당 부팅 모두 치명적 하드웨어 오류로 끝남"},
	"word.loop_some_stopped": {EN: "some of these boots ended in a fatal hardware error", KO: "해당 부팅 일부가 치명적 하드웨어 오류로 끝남"},
	"word.loop_none_stopped": {EN: "none of these boots is on record as ended by a fatal hardware error", KO: "해당 부팅 중 치명적 하드웨어 오류로 끝난 기록 없음"},
	"word.loop_ran_unknown":  {EN: "an unknown time", KO: "알 수 없는 시간"},
	"word.loop_no_place":     {EN: "a place not on record", KO: "기록에 없는 위치"},
	"status.empty":           {EN: "no records", KO: "기록 없음"},
	"status.absent":          {EN: "not present", KO: "없음"},
	"status.unreadable":      {EN: "not readable", KO: "읽지 못함"},
	"status.error":           {EN: "read failed", KO: "읽기 실패"},

	// Rule conditions.
	"rule.R-UE-01":    {EN: "an uncorrected error on this DIMM", KO: "이 DIMM에서 UE 발생"},
	"rule.R-FW-01":    {EN: "the BMC or the BIOS marks the DIMM as failed or disabled", KO: "BMC나 BIOS가 DIMM을 failed 또는 disabled로 표시"},
	"rule.R-REP-01":   {EN: "repair resources are used up: a PPR failed, sparing is in use, or a GPU row remap failed (XID 64)", KO: "리페어 자원 소진: PPR 실패, sparing 발동, GPU row remap 실패(XID 64)"},
	"rule.R-ROW-03":   {EN: "the corrected errors of a row or column fault are rising or came in a recent burst, or its extent is spreading; or a fault covers a bank or more", KO: "row, column 고장의 CE가 증가 추세이거나 최근에 폭주했거나 범위가 확산, 또는 뱅크 하나 이상에 걸친 고장"},
	"rule.R-OFF-02":   {EN: "the number of pages taken offline keeps growing", KO: "offline된 페이지 수가 계속 증가"},
	"rule.R-SCRUB-01": {EN: "the patrol scrubber found an uncorrected error", KO: "패트롤 스크럽이 UE를 발견"},
	"rule.R-REP-02":   {EN: "errors came back at the same place after a repair", KO: "리페어 뒤 같은 위치에서 재발"},
	"rule.R-SOFT-01":  {EN: "corrected errors without a growing fault: stray errors at places that did not fail again, or a fault that is neither rising nor spreading", KO: "커지는 고장 없이 CE만 있음: 위치 반복 없는 산발적 CE, 또는 증가도 확산도 없는 고장"},
	"rule.R-QUIET-01": {EN: "a place failed repeatedly and has been quiet since a page offline or a repair", KO: "한 위치에서 반복되다 offline 또는 리페어 뒤 조용해짐"},
	"rule.R-SYS-01":   {EN: "errors on two or more DIMMs of one channel", KO: "같은 채널의 DIMM 2개 이상에서 오류"},
	"rule.R-SYS-02":   {EN: "the DIMM was moved and the errors stayed in the slot", KO: "DIMM을 옮겼는데 오류가 슬롯에 남음"},
	"rule.R-SYS-03":   {EN: "the errors stopped after a reseat", KO: "reseat 뒤 오류 소멸"},
	"rule.R-MAP-01":   {EN: "the slot names of EDAC, SMBIOS and the BMC disagree", KO: "EDAC 라벨, SMBIOS, BMC FRU의 슬롯 매핑이 서로 다름"},
	"rule.R-HOST-01":  {EN: "uncorrected errors of the host that no record ties to a DIMM", KO: "어느 DIMM의 것인지 기록에 없는 UE가 호스트에 있음"},
	"rule.R-HOST-02":  {EN: "corrected errors or page offlines of the host that no record ties to a DIMM", KO: "어느 DIMM의 것인지 기록에 없는 CE나 페이지 offline이 호스트에 있음"},
	"rule.R-LOOP-01":  {EN: "uncorrected errors at one place of the DIMM in several boots", KO: "여러 부팅에서 DIMM의 같은 위치에 UE"},
	"rule.R-LOOP-02":  {EN: "uncorrected errors on the DIMM in several boots, at no place that repeats", KO: "여러 부팅에서 DIMM에 UE, 반복되는 위치 없음"},
	"rule.R-LOOP-03":  {EN: "uncorrected errors in several boots, spread over DIMMs and gathered on none of them", KO: "여러 부팅의 UE가 여러 DIMM에 흩어지고 어느 DIMM에도 몰리지 않음"},

	// R-UE-01 and R-SCRUB-01.
	"ue.located":      {EN: "{n} uncorrected {n|error|errors}, the latest on {when} at {where}", KO: "UE {n}건, 최근 {when}, {where}"},
	"ue.unlocated":    {EN: "{n} uncorrected {n|error|errors}, the latest on {when}; the records carry no location", KO: "UE {n}건, 최근 {when}, 위치 정보 없음"},
	"scrub.located":   {EN: "the patrol scrubber found {n} uncorrected {n|error|errors}, the latest on {when} at {where}", KO: "패트롤 스크럽이 UE {n}건 발견, 최근 {when}, {where}"},
	"scrub.unlocated": {EN: "the patrol scrubber found {n} uncorrected {n|error|errors}, the latest on {when}; the records carry no location", KO: "패트롤 스크럽이 UE {n}건 발견, 최근 {when}, 위치 정보 없음"},
	// A record shows that the uncorrected error stopped the host: the
	// kernel's panic line after it, or the next boot that read it.
	"ue.stopped":      {EN: "the uncorrected error of {when} stopped the host", KO: "{when}의 UE로 호스트가 중단됨"},
	"ue.stopped_many": {EN: "{n} of these uncorrected errors stopped the host, the latest on {when}", KO: "이 UE 중 {n}건으로 호스트가 중단됨, 최근 {when}"},

	// R-FW-01.
	"fw.disabled":        {EN: "{who} took the DIMM out of use on {when}", KO: "{when} {who}가 DIMM 사용 중지"},
	"fw.disabled_detail": {EN: "{who} took the DIMM out of use on {when}: {detail}", KO: "{when} {who}가 DIMM 사용 중지: {detail}"},
	"fw.failed":          {EN: "{who} reports the DIMM as failed since {when}", KO: "{when}부터 {who}가 DIMM을 failed로 보고"},
	"fw.failed_detail":   {EN: "{who} reports the DIMM as failed since {when}: {detail}", KO: "{when}부터 {who}가 DIMM을 failed로 보고: {detail}"},

	// R-REP-01.
	"rep.failed":           {EN: "the {kind} of {where} failed on {when}", KO: "{when} {where} {kind} 실패"},
	"rep.failed_unlocated": {EN: "a {kind} failed on {when}", KO: "{when} {kind} 실패"},
	"rep.sparing":          {EN: "sparing has been in use since {when}: the spare is taken", KO: "{when}부터 sparing 발동: 예비 자원 사용됨"},
	"host.ue_located":      {EN: "{n} uncorrected {n|error|errors} tied to no DIMM, the latest on {when} at {where}", KO: "DIMM 미확인 UE {n}건, 최근 {when}, {where}"},
	"host.ue":              {EN: "{n} uncorrected {n|error|errors} tied to no DIMM, the latest on {when}", KO: "DIMM 미확인 UE {n}건, 최근 {when}"},
	"host.ce":              {EN: "{n} corrected {n|error|errors} tied to no DIMM between {from} and {to}", KO: "DIMM 미확인 CE {n}건, {from}부터 {to}까지"},
	"host.ce_day":          {EN: "{n} corrected {n|error|errors} tied to no DIMM on {from}", KO: "DIMM 미확인 CE {n}건, {from}"},
	"host.ce_undated":      {EN: "{n} corrected {n|error|errors} tied to no DIMM, dated by seconds since boot only", KO: "DIMM 미확인 CE {n}건, 부팅 후 경과 시간만 기록됨"},
	"host.pages":           {EN: "{pages} {pages|page|pages} taken offline and tied to no DIMM, the last on {when}", KO: "DIMM 미확인 페이지 offline {pages}개, 최근 {when}"},
	"host.loop_address":    {EN: "uncorrected errors at the address {where} in {n} of the last {of} boots", KO: "최근 부팅 {of}회 중 {n}회 같은 주소 {where}에서 UE"},
	"host.pages_undated":   {EN: "{pages} {pages|page|pages} taken offline and tied to no DIMM", KO: "DIMM 미확인 페이지 offline {pages}개"},

	"gpu.xid64":         {EN: "row remapping failed: XID 64 on {when}", KO: "row remapping 실패: {when} XID 64"},
	"gpu.remap_failure": {EN: "the driver reports a row remapping failure as of {when}", KO: "{when} 기준 드라이버가 row remapping 실패를 보고"},

	// R-ROW-03.
	"row.rising":         {EN: "{mode} fault at {where}: {recent} errors in the last {days} days, {ratio} times the daily rate of the {base} days before", KO: "{mode} 고장({where}): 최근 {days}일 {recent}건, 그 앞 {base}일 일평균의 {ratio}배"},
	"row.new":            {EN: "{mode} fault at {where}: {recent} errors in the last {days} days, none in the {base} days before", KO: "{mode} 고장({where}): 최근 {days}일 {recent}건, 그 앞 {base}일은 0건"},
	"row.spreading_line": {EN: "{mode} fault at {where}: {new} new {new|place|places} in the last {days} days; it now covers {columns} {columns|column|columns} in {rows} {rows|row|rows}", KO: "{mode} 고장({where}): 최근 {days}일 새 위치 {new}곳, 현재 row {rows}개에 column {columns}개"},
	"row.spreading_area": {EN: "{mode} fault at {where}: {new} new {new|place|places} in the last {days} days, {cells} in total", KO: "{mode} 고장({where}): 최근 {days}일 새 위치 {new}곳, 누적 {cells}곳"},
	"row.storm":          {EN: "{mode} fault at {where}: {peak} errors within {minutes} minutes from {when}", KO: "{mode} 고장({where}): {when}부터 {minutes}분 동안 {peak}건"},
	"row.area":           {EN: "{mode} fault at {where}: {n} errors scattered over {cells} places since {from}", KO: "{mode} 고장({where}): {from}부터 {n}건, {cells}곳에 흩어짐"},
	"row.multi_dq":       {EN: "errors of the fault at {where} involve {lines} data lines of one device", KO: "{where} 고장의 오류에 한 디바이스의 DQ {lines}개가 관여"},

	// R-OFF-02.
	"off.growing": {EN: "{pages} pages were taken offline on {days} different days, the last on {when}", KO: "페이지 {pages}개가 {days}일에 걸쳐 offline, 최근 {when}"},

	// R-REP-02.
	"rep.recurred": {EN: "{n} {n|error|errors} at {where} after the {kind} of {when}", KO: "{when} {kind} 뒤 {where}에서 {n}건 재발"},

	// R-QUIET-01.
	"quiet.after": {EN: "{mode} fault at {where}: {n} errors until {last}, none in the {days} days since the {kind} of {when}", KO: "{mode} 고장({where}): {last}까지 {n}건, {when} {kind} 뒤 {days}일 동안 0건"},

	// R-SOFT-01.
	"soft.stray_once":       {EN: "{n} corrected {n|error|errors} at {places} {places|place|places} on {from}; no place failed again", KO: "{from} CE {n}건, 위치 {places}곳, 반복된 위치 없음"},
	"soft.stray_undated":    {EN: "{n} corrected {n|error|errors} at {places} {places|place|places}, dated by seconds since boot only; no place is known to have failed again", KO: "CE {n}건, 위치 {places}곳, 부팅 후 경과 시간만 기록됨, 반복이 확인된 위치 없음"},
	"soft.stray":            {EN: "{n} corrected {n|error|errors} at {places} {places|place|places} between {from} and {to}; no place failed again", KO: "{from}부터 {to}까지 CE {n}건, 위치 {places}곳, 반복된 위치 없음"},
	"soft.cell":             {EN: "one cell fails repeatedly ({where}): {n} errors since {from}, {recent} in the last {days} days", KO: "셀 하나가 반복 실패({where}): {from}부터 {n}건, 최근 {days}일 {recent}건"},
	"soft.fault":            {EN: "{mode} fault at {where}: {n} errors since {from}; the rate is not rising and no new place appeared in the last {days} days", KO: "{mode} 고장({where}): {from}부터 {n}건, 증가 없음, 최근 {days}일 새 위치 없음"},
	"soft.few":              {EN: "{mode} fault at {where}: {n} {n|error|errors} since {from}, fewer than the {min} needed to judge its course", KO: "{mode} 고장({where}): {from}부터 {n}건, 추이 판단 기준 {min}건 미만"},
	"soft.storm":            {EN: "a burst of {peak} errors within {minutes} minutes from {when}", KO: "{when}부터 {minutes}분 동안 {peak}건 폭주"},
	"soft.more":             {EN: "{faults} more {faults|fault|faults} with {n} {n|error|errors} in total, none of them growing", KO: "그 밖의 고장 {faults}개, 합계 {n}건, 커지는 고장 없음"},
	"soft.unlocated":        {EN: "{n} {n|error|errors} whose records carry no row or column: the fault mode cannot be determined", KO: "row와 column 정보가 없는 오류 {n}건: 고장 모드 판정 불가"},
	"soft.unlocated_rising": {EN: "{n} errors whose records carry no row or column, {recent} of them in the last {days} days ({ratio} times the daily rate before): the count is rising and the fault mode cannot be determined", KO: "row와 column 정보가 없는 오류 {n}건, 최근 {days}일 {recent}건(이전 일평균의 {ratio}배): 증가 중이나 고장 모드 판정 불가"},

	// R-SYS-01, R-SYS-02, R-SYS-03, R-MAP-01.
	"sys.channel":     {EN: "{slot} and {others} share one channel ({channel}) and both have errors that are not confined to one place: {n} here, {m} there, between {from} and {to}", KO: "같은 채널({channel})의 {slot}에 {n}건, {others}에 {m}건: 한 위치에 국한되지 않은 오류, {from}부터 {to}까지"},
	"sys.stays":       {EN: "the errors stayed in slot {slot} when its DIMM was changed on {when}: {before} with DIMM {old} before, {after} with DIMM {new} after", KO: "{when} DIMM을 바꾼 뒤에도 슬롯 {slot}에 오류 지속: 바꾸기 전 {old} {before}건, 바꾼 뒤 {new} {after}건"},
	"sys.left_quiet":  {EN: "since it left slot {slot}, DIMM {old} has had no errors in {now} for {days} days", KO: "슬롯 {slot} 이탈 뒤 DIMM {old}: {now}에서 {days}일 동안 0건"},
	"sys.reseat":      {EN: "{n} errors before the reseat of {when}, none in the {days} days since", KO: "{when} reseat 전 {n}건, 이후 {days}일 동안 0건"},
	"sys.reseat_few":  {EN: "{n} errors before the reseat of {when}, {after} in the {days} days since", KO: "{when} reseat 전 {n}건, 이후 {days}일 동안 {after}건"},
	"sys.moved_few":   {EN: "{n} errors in {from} before the move to {to} on {when}, {after} in the {days} days since", KO: "{when} 슬롯 이동({from} → {to}) 전 {n}건, 이후 {days}일 동안 {after}건"},
	"sys.moved_quiet": {EN: "{n} errors in {from} before the move to {to} on {when}, none in either slot in the {days} days since", KO: "{when} 슬롯 이동({from} → {to}) 전 {n}건, 이후 {days}일 동안 두 슬롯 모두 0건"},
	"map.conflict":    {EN: "{n} error {n|record|records} tied to {slot} carry a {name} ({value}) that belongs to {other}", KO: "{slot}에 묶인 오류 기록 {n}건에 {other}의 {name}({value}) 포함"},

	// R-LOOP-01, R-LOOP-02, R-LOOP-03 and the boots of a host. A stop is a
	// boot that a fatal hardware error ended.
	"loop.place":          {EN: "uncorrected errors at {where} in {n} of the last {of} boots: {stopped}", KO: "최근 부팅 {of}회 중 {n}회 {where}에서 UE: {stopped}"},
	"loop.place_near":     {EN: "uncorrected errors between {from} and {to}, taken as one row (estimated), in {n} of the last {of} boots: {stopped}", KO: "최근 부팅 {of}회 중 {n}회 {from}~{to}(같은 row로 추정)에서 UE: {stopped}"},
	"loop.dimm_places":    {EN: "uncorrected errors on this DIMM in {n} of the last {of} boots, at {places} different places: {stopped}", KO: "최근 부팅 {of}회 중 {n}회 이 DIMM에서 UE, 위치는 {places}곳으로 매번 다름: {stopped}"},
	"loop.dimm_only":      {EN: "uncorrected errors on this DIMM in {n} of the last {of} boots; the records name the DIMM only: {stopped}", KO: "최근 부팅 {of}회 중 {n}회 이 DIMM에서 UE, 기록에는 DIMM만 있고 위치 없음: {stopped}"},
	"loop.dimm_partly":    {EN: "uncorrected errors on this DIMM in {n} of the last {of} boots; one record has a place, the others name the DIMM only: {stopped}", KO: "최근 부팅 {of}회 중 {n}회 이 DIMM에서 UE, 위치가 있는 기록은 하나뿐이고 나머지는 DIMM만 있음: {stopped}"},
	"loop.spread":         {EN: "uncorrected errors in {n} of the last {of} boots, spread over {dimms} DIMMs, at most {most} {most|boot|boots} on one DIMM", KO: "최근 부팅 {of}회 중 {n}회 UE, DIMM {dimms}개에 흩어짐, 한 DIMM에 최대 {most}회"},
	"loop.spread_host":    {EN: "uncorrected errors in {n} of the last {of} boots, spread over {dimms} DIMMs ({slots}), at most {most} {most|boot|boots} on one DIMM", KO: "최근 부팅 {of}회 중 {n}회 UE, DIMM {dimms}개({slots})에 흩어짐, 한 DIMM에 최대 {most}회"},
	"loop.shared_unknown": {EN: "the records do not tell the channels and sockets of these DIMMs", KO: "해당 DIMM의 채널과 소켓은 기록에 없음"},
	"loop.shared_channel": {EN: "these DIMMs are all on one channel ({channel}): the channel, or what the DIMMs share on it", KO: "해당 DIMM 모두 한 채널({channel})에 있음: 채널, 또는 채널에서 함께 쓰는 부분이 의심됨"},
	"loop.shared_socket":  {EN: "these DIMMs are all on socket {socket}, on more than one channel: its processor, its memory controller or its power", KO: "해당 DIMM 모두 소켓 {socket}에 있고 채널은 여러 개: 그 프로세서, 메모리 컨트롤러, 전원이 의심됨"},
	"loop.shared_sockets": {EN: "these DIMMs are on {sockets} sockets: the board, the power supply or the firmware", KO: "해당 DIMM이 소켓 {sockets}개에 걸쳐 있음: 보드, 전원 공급 장치, 펌웨어가 의심됨"},
	"loop.fault_boots":    {EN: "the fault at {where} had errors in {n} of the last {of} boots", KO: "{where} 고장: 최근 부팅 {of}회 중 {n}회에서 오류"},
	"loop.stops":          {EN: "{n} of the last {of} boots ended in a fatal hardware error, the latest on {when} after {ran}", KO: "최근 부팅 {of}회 중 {n}회가 치명적 하드웨어 오류로 끝남, 최근 {when}, 부팅 후 {ran}"},
	"loop.where_dimm":     {EN: "{n} of them had an uncorrected error of {slot} ({rule})", KO: "그중 {n}회에 {slot}의 UE({rule})"},
	"loop.where_spread":   {EN: "the uncorrected errors are spread over {dimms} DIMMs ({slots}), R-LOOP-03", KO: "UE가 DIMM {dimms}개({slots})에 흩어짐(R-LOOP-03)"},
	"loop.where_one":      {EN: "uncorrected errors on {slot} in {n} {n|boot|boots}: too few for a rule of the boots", KO: "{slot}에서 UE, 부팅 {n}회: 부팅 반복 규칙에 미달"},
	"loop.where_few":      {EN: "uncorrected errors on {dimms} DIMMs ({slots}): too few boots each for a rule of the boots", KO: "DIMM {dimms}개({slots})에서 UE: 각각 부팅 반복 규칙에 미달"},
	"loop.where_none":     {EN: "no record of these boots names a DIMM", KO: "해당 부팅의 기록 중 DIMM을 지목한 것 없음"},

	// Checks of a finding against other explanations.
	"check.neighbors_clean":    {EN: "the other {n} {n|DIMM|DIMMs} of the channel ({slots}) {n|has|have} no errors: a cause in the slot or the channel is unlikely", KO: "같은 채널의 다른 DIMM {n}개({slots}) 오류 0건: 슬롯이나 채널 원인 가능성 낮음"},
	"check.neighbors_confined": {EN: "{slot} on the same channel has {n} {n|error|errors}, confined to one place: a fault of its own", KO: "같은 채널의 {slot}에 {n}건, 한 위치에 국한: 그 DIMM 자체의 고장"},
	"check.neighbors_few":      {EN: "{slot} on the same channel has {n} unconfined {n|error|errors}, fewer than the {min} that would point at the channel", KO: "같은 채널의 {slot}에 국한되지 않은 오류 {n}건: 채널 원인으로 볼 기준 {min}건 미만"},
	"check.neighbors_active":   {EN: "{slot} on the same channel has {n} unconfined errors in the same period: the channel may be involved", KO: "같은 채널의 {slot}에 같은 기간 국한되지 않은 오류 {n}건: 채널 관련 가능성"},
	"check.channel_alone":      {EN: "no other DIMM on this channel: there is nothing to compare with", KO: "이 채널에 다른 DIMM 없음: 비교 대상 없음"},
	"check.channel_unknown":    {EN: "the channel of this slot is unknown: the neighbours cannot be checked", KO: "이 슬롯의 채널 정보 없음: 이웃 DIMM 점검 불가"},
	"check.followed":           {EN: "the errors followed the DIMM from {from} to {to}: {before} before the move, {after} after", KO: "오류가 DIMM을 따라 이동({from} → {to}): 이동 전 {before}건, 이동 후 {after}건"},
	"check.no_cross_test":      {EN: "no slot move or reseat on record: a cause in the board is not ruled out", KO: "슬롯 교차 시험이나 reseat 기록 없음: 보드 원인을 배제하지 못함"},
	"check.host_wide":          {EN: "{n} other DIMMs of this host have unconfined errors in the same period ({slots}): a common cause is possible", KO: "같은 기간 이 호스트의 다른 DIMM {n}개({slots})에도 국한되지 않은 오류: 공통 원인 가능"},
	"check.after_change":       {EN: "the first error came {days} days after the {what} changed on {when} ({old} to {new}), with none in the {covered} days of history before", KO: "{when} {what} 변경({old} → {new}) {days}일 뒤 첫 오류, 그 전 이력 {covered}일 동안 0건"},
	"check.assumed":            {EN: "{n} {n|error falls|errors fall} at times when no inventory shows which DIMM was in the slot; {n|it is|they are} taken to be this DIMM's", KO: "오류 {n}건은 슬롯에 어느 DIMM이 있었는지 인벤토리로 확인되지 않는 시점의 것: 이 DIMM의 것으로 가정"},
	"check.host_clean":         {EN: "the other {n} {n|DIMM|DIMMs} of this host {n|has|have} no unconfined errors: a cause in the board is unlikely", KO: "이 호스트의 다른 DIMM {n}개에는 한 위치에 국한되지 않은 오류 없음: 보드 원인 가능성 낮음"},
	"check.estimated":          {EN: "the row is inferred from addresses that lie close together; the platform reports no rows and columns", KO: "row는 서로 가까운 주소에서 추정, 플랫폼이 row와 column을 보고하지 않음"},
	"check.device_unknown":     {EN: "the platform does not report the device: more than one device may be involved", KO: "플랫폼이 디바이스를 보고하지 않음: 디바이스가 둘 이상일 가능성"},
	"check.confined":           {EN: "errors of this DIMM stay within {where} ({mode} fault): a cause outside the DIMM does not explain that", KO: "이 DIMM의 오류가 {where}({mode} 고장) 안에 머묾: DIMM 밖 원인으로 설명되지 않음"},
	"check.quiet_short":        {EN: "{days} days have passed since the {kind} of {when}; {need} days without errors are needed to call the fault quiet", KO: "{when} {kind} 뒤 {days}일 경과: 조용해졌다고 보려면 {need}일 필요"},
	"check.no_patrol":          {EN: "no record says that the patrol scrubber found an error of this DIMM: silence can mean that the memory is not being read", KO: "패트롤 스크럽이 찾은 오류 기록 없음: 조용함이 해당 메모리를 읽지 않아서일 가능성"},
	"check.patrol_off":         {EN: "patrol scrub is off in the BIOS ({attribute} = {value}): memory that is not read is not checked, and silence says less", KO: "BIOS에서 패트롤 스크럽이 꺼져 있음 ({attribute} = {value}): 읽지 않는 메모리는 검사되지 않아 조용함의 의미가 약함"},
	"check.sppr_volatile":      {EN: "the repair was a soft PPR and the host rebooted on {when} before the errors came back: a soft PPR is lost at power off", KO: "리페어가 sPPR이고 재발 전 {when}에 재부팅: sPPR은 전원이 꺼지면 사라짐"},
	"check.repair_unlocated":   {EN: "{n} {n|repair names|repairs name} no place: whether errors came back at the repaired place is unknown", KO: "위치 정보 없는 리페어 기록 {n}건: 리페어한 위치의 재발 여부 확인 불가"},
	"check.named_by_other":     {EN: "{n} error {n|record|records} tied to {slot} carry a {name} of this slot: some errors of {slot} may belong here", KO: "{slot}에 묶인 오류 기록 {n}건에 이 슬롯의 {name} 포함: {slot}의 오류 일부가 이 슬롯 것일 가능성"},
	"check.host_no_place":      {EN: "{n} {n|error names|errors name} no slot: what recorded {n|it|them} decodes no DIMM", KO: "오류 {n}건에 슬롯 정보 없음: 기록한 쪽이 DIMM을 해석하지 않음"},
	"check.host_ambiguous":     {EN: "{n} {n|error|errors} in slot {slot} {n|falls|fall} between two DIMMs: the slot held another module before and after, and the time of the swap is not on record", KO: "슬롯 {slot}의 오류 {n}건은 두 DIMM 사이에 있음: 앞뒤로 다른 모듈이 꽂혀 있었고 교체 시점 기록이 없음"},
	"check.host_asked":         {EN: "{n} of the pages {n|was|were} taken offline because a program asked the kernel to, as a test of that path does: no error of the hardware is behind {n|it|them}", KO: "페이지 {n}개는 프로그램이 커널에 요청해 offline됨(이 경로를 시험할 때 쓰는 방식): 하드웨어 오류에서 나온 것이 아님"},
	"check.loop_alone":         {EN: "no other DIMM of the host had an uncorrected error in these boots", KO: "이 부팅들에서 호스트의 다른 DIMM에는 UE 없음"},
	"check.loop_confined":      {EN: "the uncorrected errors stay at one place of the DIMM: a fault of a contact or a trace corrupts whatever address is read", KO: "UE가 DIMM의 한 위치에 머묾: 접촉이나 배선 고장이면 읽는 주소마다 오류가 남"},
	"check.loop_changed":       {EN: "the BIOS version or the DIMMs of the host changed between these boots: one address may have been two places", KO: "이 부팅들 사이에 BIOS 버전이나 DIMM 구성이 바뀜: 같은 주소가 서로 다른 위치였을 수 있음"},
	"check.loop_not_ruled_out": {EN: "the slot and the channel are not ruled out: a fault of one cell or row stays at its place, these errors do not", KO: "슬롯과 채널을 배제하지 못함: 셀이나 row 고장은 한 위치에 머무는데 이 오류는 그렇지 않음"},
	"check.loop_other":         {EN: "{slot} had an uncorrected error in boot {boot} of the last {of}: two DIMMs in two boots also fit a cause they share", KO: "{slot}에도 최근 부팅 {of}회 중 {boot}번째에 UE: 두 부팅에 두 DIMM이면 공통 원인일 수도 있음"},
	"check.loop_own_fault":     {EN: "{slot} had a confined fault at {where} before the first of these boots: a fault of its own may explain its part", KO: "{slot}에는 이 부팅들 전부터 {where}에 국한된 고장이 있었음: 이 DIMM 자체 고장일 수 있음"},

	// Data a finding did not have.
	"missing.source":        {EN: "{kind}: {status}", KO: "{kind}: {status}"},
	"missing.source_detail": {EN: "{kind}: {status} ({detail})", KO: "{kind}: {status}({detail})"},
	"missing.no_errors":     {EN: "no source of error records was read", KO: "오류 기록 소스를 읽지 못함"},
	"missing.inventory":     {EN: "no SMBIOS inventory: DIMM serial numbers are unknown", KO: "SMBIOS 인벤토리 없음: DIMM 시리얼 미확인"},
	"missing.serial":        {EN: "the DIMM has no readable serial number: its history is tied to the slot and does not follow the DIMM", KO: "읽을 수 있는 시리얼 없음: 이력이 슬롯에 묶여 DIMM을 따라가지 않음"},
	"missing.unmapped":      {EN: "the EDAC position of this slot is not tied to an SMBIOS slot: the serial number of the DIMM is unknown", KO: "이 슬롯의 EDAC 위치가 SMBIOS 슬롯과 연결되지 않음: DIMM 시리얼 미확인"},
	"missing.rowcol":        {EN: "{n} {n|error is|errors are} known by physical address only (grade B): no row or column", KO: "오류 {n}건은 물리 주소만 있음(B 등급): row와 column 없음"},
	"missing.location":      {EN: "{n} {n|error is|errors are} known as a count only (grade C)", KO: "오류 {n}건은 카운트만 있음(C 등급)"},
	"missing.device":        {EN: "the platform does not report which DRAM device failed", KO: "플랫폼이 실패한 DRAM 디바이스를 보고하지 않음"},
	"missing.bits":          {EN: "the records do not name the failing bits: data lines (DQ) cannot be checked", KO: "실패한 비트 정보 없음: DQ 점검 불가"},
	"missing.scrub":         {EN: "the records do not say whether an error was found by the patrol scrubber or on access", KO: "오류를 패트롤 스크럽이 찾았는지 접근 중에 찾았는지 기록에 없음"},
	"missing.patrol_off":    {EN: "patrol scrub is off in the BIOS ({attribute} = {value}): errors a scrub would find are not expected, and the rule for uncorrected errors found by the scrubber (R-SCRUB-01) cannot be judged", KO: "BIOS에서 패트롤 스크럽이 꺼져 있음 ({attribute} = {value}): 스크럽이 찾을 오류는 기록될 수 없고, 스크럽이 찾은 UE 규칙(R-SCRUB-01)은 판단할 수 없음"},
	"missing.bmc_ce_off":    {EN: "the firmware logs no corrected error to the BMC ({attribute} = {value}): the BMC log holds none, and its silence says nothing about corrected errors", KO: "펌웨어가 CE를 BMC에 기록하지 않음 ({attribute} = {value}): BMC 로그에는 CE가 없으며, 로그가 조용해도 CE가 없었다는 뜻은 못 됨"},
	"missing.bmc_threshold": {EN: "the firmware logs the event of a corrected error threshold to the BMC ({attribute} = {value}): the BMC log counts threshold events, fewer than the corrected errors that occurred", KO: "펌웨어가 CE 임계값 이벤트를 BMC에 기록함 ({attribute} = {value}): BMC 로그는 임계값 이벤트를 세므로 실제 CE 수에 못 미침"},
	"missing.history":       {EN: "the ledger covers {days} days before the recent window; a trend needs {need}", KO: "최근 구간 앞 이력 {days}일: 추세 판단에 {need}일 필요"},
	"missing.dates":         {EN: "{n} {n|error carries|errors carry} seconds since boot and no date: left out of every trend", KO: "오류 {n}건은 부팅 후 경과 시간만 있음: 추세에서 제외"},
	"missing.unplaced":      {EN: "{n} {n|error|errors} of this host {n|names|name} no slot and {n|is|are} counted for no DIMM", KO: "이 호스트의 오류 {n}건은 슬롯 정보 없음: 어느 DIMM에도 계산되지 않음"},
	"missing.ambiguous":     {EN: "{n} {n|error|errors} in this slot {n|falls|fall} between two DIMMs whose swap time is unknown and {n|is|are} counted for neither", KO: "이 슬롯의 오류 {n}건은 교체 시점이 불명확한 두 DIMM 사이에 있음: 어느 쪽에도 계산되지 않음"},
	"missing.no_edac":       {EN: "no EDAC driver reports a memory controller on this host: the kernel names no DIMM for an error", KO: "이 호스트에 메모리 컨트롤러를 보고하는 EDAC 드라이버 없음: 커널이 오류의 DIMM을 알려 주지 않음"},
	"missing.virtual":       {EN: "this host is a virtual machine ({what}): a guest is not shown the corrected errors of the machine it runs on, and of the uncorrected ones at most those that hit its own memory", KO: "이 호스트는 가상 머신임({what}): 게스트에는 장비의 CE가 보이지 않고, UE도 게스트 메모리에 난 것까지만 보임"},
	"missing.capture_lost":  {EN: "my preflight found what a reset loses on this host: {checks}; a stop there may leave no record", KO: "my preflight 결과 이 호스트에서 reset 때 잃는 항목: {checks}. 중단이 기록 없이 지나갈 수 있음"},
	"missing.ecs":           {EN: "no ECS counters", KO: "ECS 카운터 없음"},
	"missing.temperature":   {EN: "no DIMM temperature history", KO: "DIMM 온도 이력 없음"},

	// Next steps.
	"next.replace":     {EN: "Replace the DIMM. Attach a case bundle of this DIMM to the return.", KO: "DIMM 교체. 이 DIMM의 케이스 번들을 반품에 첨부."},
	"next.plan":        {EN: "Plan a replacement for the next maintenance window and watch the fault until then.", KO: "다음 정비 때 교체 계획. 그때까지 이 고장 관찰."},
	"next.observe":     {EN: "No action. Look again when the count grows or a new place appears.", KO: "조치 없음. 건수가 늘거나 새 위치가 나오면 다시 확인."},
	"next.locate":      {EN: "Collect records with rows and columns (rasdaemon with the EDAC driver of the platform), so that the fault mode can be determined.", KO: "row와 column이 있는 기록 수집(플랫폼 EDAC 드라이버와 rasdaemon). 고장 모드 판정에 필요."},
	"next.channel":     {EN: "Do not pull a DIMM yet. Check the channel first: swap this DIMM with one from another channel and see whether the errors follow it.", KO: "DIMM을 아직 빼지 않음. 채널부터 확인: 다른 채널의 DIMM과 맞바꿔 오류가 따라가는지 확인."},
	"next.slot":        {EN: "Do not return the DIMM. Inspect slot {slot}, its channel and the board.", KO: "DIMM 반품 보류. 슬롯 {slot}, 해당 채널, 보드 점검."},
	"next.contact":     {EN: "Keep the DIMM in service and watch it: the contact may have been the cause.", KO: "DIMM을 계속 사용하며 관찰. 접촉 문제였을 가능성."},
	"next.mapping":     {EN: "Before pulling a DIMM, confirm which physical slot the errors belong to: compare the board print, the EDAC label and the SMBIOS locator.", KO: "DIMM을 빼기 전에 오류가 속한 물리 슬롯 확인: 보드 인쇄, EDAC 라벨, SMBIOS 로케이터 대조."},
	"next.removed":     {EN: "The DIMM is no longer in the host. Keep this finding with the returned part.", KO: "DIMM이 이미 호스트에서 빠짐. 이 소견을 반품 DIMM과 함께 보관."},
	"next.host_ue":     {EN: "Do not pull a DIMM on these records: none of them names one. Find the DIMM first, from the EDAC driver of the memory controller or the event log of the BMC, then scan again.", KO: "이 기록만으로 DIMM을 빼지 않음: DIMM을 지목한 기록이 없음. 메모리 컨트롤러의 EDAC 드라이버나 BMC 이벤트 로그로 DIMM부터 확인한 뒤 다시 스캔."},
	"next.host_ce":     {EN: "No action on a DIMM. The EDAC driver of the memory controller or the event log of the BMC ties further errors to a DIMM.", KO: "DIMM 조치 없음. 메모리 컨트롤러의 EDAC 드라이버나 BMC 이벤트 로그가 있으면 이후 오류가 DIMM과 연결됨."},
	"next.host_ue_vm":  {EN: "Do not pull a DIMM on these records: none of them names one, and a virtual machine has no DIMM of its own to name. The DIMM is found by a scan of the hypervisor host.", KO: "이 기록만으로 DIMM을 빼지 않음: DIMM을 지목한 기록이 없고, 가상 머신에는 지목할 실제 DIMM이 없음. DIMM은 하이퍼바이저 호스트를 스캔해 확인."},
	"next.host_ce_vm":  {EN: "No action in the guest. The DIMM behind such records is found by a scan of the hypervisor host.", KO: "게스트에서 할 조치 없음. 이런 기록의 DIMM은 하이퍼바이저 호스트를 스캔해 확인."},
	"next.gpu_replace": {EN: "Return the GPU for repair or replace it: its row remapping has failed.", KO: "GPU 수리 반품 또는 교체: row remapping 실패."},
	"next.loop_place":  {EN: "Replace the DIMM. Attach a case bundle written from the kept ledger to the return.", KO: "DIMM 교체. 보관 원장에서 만든 케이스 번들을 반품에 첨부."},
	"next.loop_dimm":   {EN: "Before the DIMM is returned, swap it with a DIMM of another channel and see whether the stops follow it.", KO: "DIMM 반품 전에 다른 채널의 DIMM과 맞바꿔 중단이 따라가는지 확인."},
	"next.loop_spread": {EN: "Do not pull the DIMMs one after another. Check the logs of the processor and the board, the power supply, and what changed before the first stop.", KO: "DIMM을 하나씩 차례로 빼지 않음. 프로세서와 보드 로그, 전원 공급 장치, 첫 중단 전에 바뀐 것부터 점검."},

	// Steps of the confidence.
	"conf.rule":            {EN: "the grade rests on {rule}", KO: "등급 근거 규칙: {rule}"},
	"conf.ok":              {EN: "no errors on record", KO: "오류 기록 없음"},
	"conf.grade_b":         {EN: "the deciding errors are known by address only", KO: "판정 근거 오류가 주소로만 파악됨"},
	"conf.grade_c":         {EN: "the deciding errors are known as counts only", KO: "판정 근거 오류가 카운트로만 파악됨"},
	"conf.estimated":       {EN: "the mode of the deciding fault is an estimate", KO: "판정 근거 고장의 모드가 추정"},
	"conf.no_serial":       {EN: "the DIMM has no serial number on record", KO: "DIMM 시리얼 기록 없음"},
	"conf.assumed":         {EN: "most errors fall at times when no inventory shows the DIMM in its slot", KO: "오류 대부분이 인벤토리로 DIMM 위치가 확인되지 않는 시점의 것"},
	"conf.sources":         {EN: "{n} {n|source|sources} could not be read in full", KO: "다 읽지 못한 소스 {n}개"},
	"conf.no_error_source": {EN: "no source of error records was read", KO: "오류 기록 소스를 읽지 못함"},
	"conf.supports":        {EN: "{n} {n|check supports|checks support} the finding", KO: "소견을 뒷받침하는 점검 {n}건"},
	"conf.weakens":         {EN: "{n} {n|check speaks|checks speak} against the finding", KO: "소견에 반하는 점검 {n}건"},
	"conf.open":            {EN: "{n} {n|check|checks} could not be made", KO: "하지 못한 점검 {n}건"},
}

// Text renders a statement of a finding in the given language. A statement
// whose code this engine does not know keeps the English text it was stored
// with.
func Text(lang i18n.Lang, st model.Statement) string {
	if !Messages.Has(st.Code) {
		return st.Text
	}
	return Messages.Text(lang, st.Code, st.Args)
}

// GradeText returns the name of a grade in the given language.
func GradeText(lang i18n.Lang, g model.VerdictGrade) string {
	return Messages.Text(lang, "grade."+string(g), nil)
}

// ModeText returns the name of a fault mode in the given language.
func ModeText(lang i18n.Lang, m model.FaultMode) string {
	return Messages.Text(lang, "mode."+string(m), nil)
}

// ConfidenceLevel names a confidence from 0 to 100: "high" from 75,
// "medium" from 50, "low" below.
func ConfidenceLevel(confidence int) string {
	switch {
	case confidence >= 75:
		return "high"
	case confidence >= 50:
		return "medium"
	}
	return "low"
}

// statement builds a statement from a code and pairs of argument name and
// value, with its English text.
func statement(code string, kv ...any) model.Statement {
	st := model.Statement{Code: code}
	if len(kv) > 0 {
		st.Args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			st.Args[kv[i].(string)] = kv[i+1]
		}
	}
	st.Text = Messages.Text(i18n.EN, code, st.Args)
	return st
}
