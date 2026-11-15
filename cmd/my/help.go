package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// words holds the texts of the commands: usage, flags, errors and hints.
var words = i18n.Catalog{
	// Errors in the command line.
	"err.see_help":       {EN: "run \"{cmd}\" for usage", KO: "사용법은 \"{cmd}\""},
	"err.extra":          {EN: "unexpected argument \"{arg}\"", KO: "불필요한 인자 \"{arg}\""},
	"err.command_first":  {EN: "the command comes before its flags: my {cmd} [flags]", KO: "명령을 옵션 앞에 써야 함: my {cmd} [옵션]"},
	"err.one_dimm":       {EN: "name one DIMM by its slot or serial number", KO: "DIMM 하나를 슬롯 이름이나 시리얼로 지정해야 함"},
	"err.color":          {EN: "--color {value}: want auto, always or never", KO: "--color {value}: auto, always, never 중 하나여야 함"},
	"err.width":          {EN: "--width {value}: want a number of columns", KO: "--width {value}: 칸 수여야 함"},
	"err.bank":           {EN: "--bank {value}: want a bank (3) or a bank group and a bank (0/3)", KO: "--bank {value}: bank 번호(3) 또는 bank group과 bank(0/3)여야 함"},
	"err.rows":           {EN: "--rows {value}: want a range of rows such as 0x1a00-0x1aff", KO: "--rows {value}: 0x1a00-0x1aff 같은 row 범위여야 함"},
	"err.days":           {EN: "--days {value}: want a number of days", KO: "--days {value}: 일수여야 함"},
	"err.not_dir":        {EN: "not a directory", KO: "디렉터리가 아님"},
	"err.empty":          {EN: "the ledger holds no host", KO: "원장에 호스트 없음"},
	"err.not_linux":      {EN: "this is not a Linux host ({os}). A scan reads the records the Linux kernel and its tools keep about memory errors. A case from such a host is shown with \"my open FILE\" and built from its files with \"my import FILE...\"", KO: "이 시스템은 Linux 호스트가 아님({os}). 스캔은 Linux 커널과 그 도구가 남긴 메모리 오류 기록을 읽음. Linux 호스트의 케이스는 \"my open FILE\"로 열고, 그 호스트의 파일로는 \"my import FILE...\"로 케이스를 만듦"},
	"err.blind_user":     {EN: "no source of error records can be read as this user, so the scan cannot say whether this host has memory errors:", KO: "이 사용자로는 오류 기록 소스를 하나도 읽지 못해 이 호스트에 메모리 오류가 있는지 알 수 없음:"},
	"err.blind_live":     {EN: "no source of error records can be read on this host, so the scan cannot say whether it has memory errors:", KO: "이 호스트에서 오류 기록 소스를 하나도 읽지 못해 메모리 오류가 있는지 알 수 없음:"},
	"err.blind_root":     {EN: "{dir} holds no source of error records of a host, so there is nothing to scan:", KO: "{dir}에 호스트의 오류 기록 소스가 없어 스캔할 것이 없음:"},
	"err.blind_sudo":     {EN: "As root the scan reads the kernel log and the rasdaemon database: sudo my", KO: "root로 실행하면 커널 로그와 rasdaemon DB를 읽음: sudo my"},
	"err.no_host":        {EN: "no host named {host} in the ledger (hosts: {hosts})", KO: "원장에 {host} 호스트 없음 (호스트: {hosts})"},
	"err.several_hosts":  {EN: "the ledger holds several hosts: name one with --host ({hosts})", KO: "원장에 호스트가 여럿임: --host로 하나를 지정 ({hosts})"},
	"err.empty_slot":     {EN: "slot {slot} holds no DIMM", KO: "슬롯 {slot}에 DIMM 없음"},
	"err.no_dimm":        {EN: "no slot or DIMM \"{ref}\" in host {host}.", KO: "호스트 {host}에 \"{ref}\" 슬롯이나 DIMM 없음."},
	"err.with_findings":  {EN: "DIMMs with errors or a finding: {slots}", KO: "오류나 소견이 있는 DIMM: {slots}"},
	"err.no_gpu":         {EN: "no GPU \"{ref}\" with memory records in host {host}; name a GPU by its index, its serial number, its UUID or its PCI address", KO: "호스트 {host}에 메모리 기록이 있는 GPU \"{ref}\" 없음. GPU는 인덱스, 시리얼, UUID, PCI 주소로 지정"},
	"err.expect":         {EN: "name the injection plan with --expect, for example --expect plan.yaml", KO: "주입 계획을 --expect로 지정해야 함 (예: --expect plan.yaml)"},
	"err.export_format":  {EN: "name the format to write: my export fmfm", KO: "내보낼 형식을 지정해야 함: my export fmfm"},
	"err.export_unknown": {EN: "unknown format \"{format}\"; the format this version writes is fmfm", KO: "알 수 없는 형식 \"{format}\". 이 버전이 쓰는 형식은 fmfm"},
	"export.written":     {EN: "FMFM analysis log written: {file} ({size})", KO: "FMFM 분석 로그 저장: {file} ({size})"},
	"export.holds":       {EN: "host {host}, finding {grade}: {dimms} {dimms|DIMM|DIMMs} analysed, {records} error {records|record|records} in the runtime log", KO: "호스트 {host}, 소견 {grade}: 분석한 DIMM {dimms}개, runtime 로그의 오류 기록 {records}건"},
	"export.no_cpad":     {EN: "the log holds findings and suggestions for a person, and no action descriptor (CPAD)", KO: "로그에는 사람이 읽을 소견과 제안만 있고 조치 기술자(CPAD)는 없음"},
	"err.plugin_alone":   {EN: "--plugin-timeout and --plugin-logs go with --plugin", KO: "--plugin-timeout과 --plugin-logs는 --plugin과 함께 써야 함"},
	"err.plugin_timeout": {EN: "--plugin-timeout {value}: want a time of at least 100ms, such as 5s or 2m", KO: "--plugin-timeout {value}: 5s나 2m처럼 100ms 이상의 시간이어야 함"},
	"err.plugin_logs":    {EN: "--plugin-logs {value}: want a number of records", KO: "--plugin-logs {value}: 기록 수여야 함"},
	"plugin.used":        {EN: "vendor analyzer {name}: analyses of {dimms} {dimms|DIMM|DIMMs}", KO: "벤더 분석기 {name}: DIMM {dimms}개 분석"},
	"plugin.note":        {EN: "analyzer: {note}", KO: "분석기: {note}"},
	"plugin.failed":      {EN: "Nothing was analysed. Without --plugin the findings are those of the open core.", KO: "분석하지 않았습니다. --plugin 없이 실행하면 공개 코어의 소견이 나옵니다."},
	"err.diff_files":     {EN: "name two cases, the earlier one first: my diff before.mycase after.mycase", KO: "케이스 둘을 앞선 것부터 지정해야 함: my diff before.mycase after.mycase"},
	"err.diff_order":     {EN: "{second} ({second_at}) is older than {first} ({first_at}): name the earlier case first", KO: "{second}({second_at})의 시점이 {first}({first_at}) 이전임: 앞선 케이스를 먼저 지정해야 함"},
	"err.diff_at":        {EN: "--at {value}: want a time after the earlier case ({first_at}) and up to the later one ({second_at})", KO: "--at {value}: 앞 케이스({first_at}) 뒤이고 뒤 케이스({second_at})까지인 시각이어야 함"},
	"err.open_file":      {EN: "name the case to open: a .mycase bundle or a ledger file", KO: "열 케이스를 지정해야 함: .mycase 번들 또는 원장 파일"},
	"err.open_command":   {EN: "unknown command \"{cmd}\" (after the file: dimm, why, map, timeline, gpu, bios, verify, export, or nothing for the overview)", KO: "알 수 없는 명령 \"{cmd}\" (파일 뒤에는 dimm, why, map, timeline, gpu, bios, verify, export, 또는 생략하면 개요)"},

	"err.bundle_out":  {EN: "name the file to write with -o, for example -o case.mycase", KO: "저장할 파일을 -o로 지정해야 함 (예: -o case.mycase)"},
	"err.key_without": {EN: "--key-file goes with --anonymize", KO: "--key-file은 --anonymize와 함께 써야 함"},
	"err.exists":      {EN: "{file} exists; --force replaces it", KO: "{file}이 이미 있음. --force를 주면 덮어씀"},
	"err.no_dir":      {EN: "the directory {file} does not exist", KO: "{file} 디렉터리가 없음"},
	"err.is_dir":      {EN: "{file} is a directory; name a file", KO: "{file}은 디렉터리임. 파일 이름을 지정해야 함"},
	"err.not_file":    {EN: "{file} is not a regular file: a ledger that is kept needs one", KO: "{file}은 일반 파일이 아님. 보관하는 원장은 일반 파일이어야 함"},
	"err.dir_denied":  {EN: "the directory {file} is not writable for this user", KO: "이 사용자는 {file} 디렉터리에 쓸 수 없음"},
	"err.dangling":    {EN: "{file} is a symbolic link that leads nowhere", KO: "{file}은 대상이 없는 심볼릭 링크임"},
	"err.terminal":    {EN: "-o - writes the bundle to the standard output, which is a terminal here: send it to a file or a pipe", KO: "-o -는 번들을 표준 출력으로 보내는데 지금은 터미널임. 파일이나 파이프로 받아야 함"},

	"err.report_out":    {EN: "name the file to write with -o, for example -o report.html", KO: "저장할 파일을 -o로 지정해야 함 (예: -o report.html)"},
	"report.figures":    {EN: "figures written: {dir} ({n} SVG {n|file|files})", KO: "그림 저장: {dir} (SVG 파일 {n}개)"},
	"report.no_figures": {EN: "no figure written: this host has no slots and no errors on record to draw", KO: "저장한 그림 없음: 이 호스트에는 그릴 슬롯도 오류 기록도 없음"},
	"err.report_scan":   {EN: "--root and --ledger go with a scan of the host; a case file is read as it is", KO: "--root와 --ledger는 호스트 스캔에 쓰는 옵션임. 케이스 파일은 그대로 읽음"},
	"err.audience":      {EN: "--audience {value}: want customer or fa", KO: "--audience {value}: customer 또는 fa여야 함"},

	"err.import_files":  {EN: "name the files to import", KO: "가져올 파일을 지정해야 함"},
	"err.anonymize_out": {EN: "--anonymize goes with -o: it applies to the bundle that is written", KO: "--anonymize는 -o와 함께 써야 함: 저장하는 번들에 적용됨"},
	"err.time":          {EN: "{flag} {value}: want a date (2026-11-12) or a time (2026-11-12T03:14:00Z)", KO: "{flag} {value}: 날짜(2026-11-12) 또는 시각(2026-11-12T03:14:00Z)이어야 함"},
	"err.tz":            {EN: "--tz {value}: want a time zone name such as Asia/Seoul or UTC", KO: "--tz {value}: Asia/Seoul이나 UTC 같은 시간대 이름이어야 함"},

	"err.listen":   {EN: "--listen {value}: want an address such as 127.0.0.1:10163 or :10163", KO: "--listen {value}: 127.0.0.1:10163이나 :10163 같은 주소여야 함"},
	"err.interval": {EN: "--interval {value}: want a time of at least 1s, such as 30s or 5m", KO: "--interval {value}: 30s나 5m처럼 1s 이상의 시간이어야 함"},

	"err.fleet_root":     {EN: "--fleet reads a store and --root a copy of a host's files: give one of the two", KO: "--fleet은 저장소를, --root는 호스트 파일 사본을 읽음: 둘 중 하나만 지정"},
	"err.fleet_host":     {EN: "no host named {host} in {store} ({n} {n|host|hosts}: {hosts})", KO: "{store}에 {host} 호스트 없음 (호스트 {n}개: {hosts})"},
	"err.fleet_one_host": {EN: "the store holds {n} hosts: name one with --host ({hosts})", KO: "저장소에 호스트가 {n}개임: --host로 하나를 지정 ({hosts})"},
	"err.fleet_empty":    {EN: "{store} holds no rows of a named host", KO: "{store}에 호스트 이름이 있는 행이 없음"},
	"err.since":          {EN: "--since {value}: want a date (2026-10-01), a time (2026-10-01T06:00:00Z), or days or hours back (30d, 72h)", KO: "--since {value}: 날짜(2026-10-01), 시각(2026-10-01T06:00:00Z), 또는 며칠이나 몇 시간 전(30d, 72h)이어야 함"},
	"err.since_alone":    {EN: "--since goes with --fleet", KO: "--since는 --fleet과 함께 써야 함"},
	"err.host_alone":     {EN: "--host goes with --fleet: it names one host of the store", KO: "--host는 --fleet과 함께 써야 함: 저장소의 호스트 하나를 지정"},
	"err.fleet_api_host": {EN: "name the host with ?host=NAME: the store holds {n} hosts ({hosts})", KO: "?host=NAME으로 호스트를 지정해야 함: 저장소에 호스트가 {n}개임 ({hosts})"},
	"fleet.unnamed":      {EN: "{n} {n|row|rows} of the store {n|carries|carry} no host name and {n|was|were} not read; RASDAEMON_HOSTNAME gives rasdaemon the name of its host", KO: "저장소의 행 {n}개에 호스트 이름이 없어 읽지 않음. rasdaemon에 RASDAEMON_HOSTNAME으로 호스트 이름을 줄 수 있음"},

	// What "my serve" writes to its log.
	"serve.listening":   {EN: "listening on {url}", KO: "{url}에서 요청을 받음"},
	"serve.no_metrics":  {EN: "/metrics is off; start with --metrics to serve Prometheus metrics. /api/dimm is served", KO: "/metrics 꺼짐. Prometheus 지표를 내려면 --metrics로 시작. /api/dimm은 제공"},
	"serve.scan":        {EN: "scan took {took}: {found}", KO: "스캔 {took}: {found}"},
	"serve.found":       {EN: "host {host}, finding {grade}, {dimms} {dimms|DIMM|DIMMs}, {listed} with errors or a finding", KO: "호스트 {host}, 소견 {grade}, DIMM {dimms}개, 오류나 소견이 있는 DIMM {listed}개"},
	"serve.scan_failed": {EN: "scan failed, the state of the scan before stays: {err}", KO: "스캔 실패, 직전 스캔의 상태 유지: {err}"},
	"serve.stopped":     {EN: "stopped", KO: "종료"},
	"serve.fleet":       {EN: "{store}, {n} {n|host|hosts}: {grades}", KO: "{store}, 호스트 {n}개: {grades}"},
	"serve.fleet_grade": {EN: "{n} {grade}", KO: "{grade} {n}"},

	// What "my import" says.
	"import.records": {EN: "{n} {n|record|records}", KO: "기록 {n}건"},
	"import.events":  {EN: "{n} error {n|record|records} (grade {grade})", KO: "오류 기록 {n}건({grade} 등급)"},
	"import.actions": {EN: "{n} {n|action|actions}", KO: "조치 {n}건"},
	"import.skipped": {EN: "{n} not usable", KO: "쓰지 못한 기록 {n}건"},
	"import.total":   {EN: "host {host}: {events} error {events|record|records} and {actions} {actions|action|actions} added, {dup} reported by more than one source and counted once", KO: "호스트 {host}: 오류 기록 {events}건과 조치 {actions}건 추가, 여러 소스가 함께 보고한 {dup}건은 한 번만 계산"},
	"import.finding": {EN: "host {host}, finding: {grade}", KO: "호스트 {host}, 소견: {grade}"},

	// What "my bundle" says.
	"bundle.written":    {EN: "case bundle written: {file} ({size})", KO: "케이스 번들 저장: {file} ({size})"},
	"bundle.sent":       {EN: "case bundle written to the standard output ({size})", KO: "케이스 번들을 표준 출력으로 보냄 ({size})"},
	"bundle.host":       {EN: "host {host}: {dimms} {dimms|DIMM|DIMMs}, CE {ce}, UE {ue}, finding: {grade}", KO: "호스트 {host}: DIMM {dimms}개, CE {ce}건, UE {ue}건, 소견: {grade}"},
	"bundle.subject":    {EN: "subject of the case: {dimm}, finding: {grade}", KO: "케이스 대상: {dimm}, 소견: {grade}"},
	"bundle.missing":    {EN: "not in the bundle: {what}", KO: "번들에 없음: {what}"},
	"bundle.note":       {EN: "note: {what}", KO: "참고: {what}"},
	"bundle.anonymized": {EN: "host names replaced, and {n} network {n|address|addresses}:", KO: "호스트 이름과 네트워크 주소 {n}개를 바꿈:"},
	"bundle.anon_hosts": {EN: "host names replaced:", KO: "호스트 이름을 바꿈:"},
	"bundle.anon_users": {EN: "{n} {n|name of a home directory|names of home directories} replaced in the paths of the files that were read", KO: "읽은 파일 경로에서 홈 디렉터리 이름 {n}개를 바꿈"},
	"bundle.keep_names": {EN: "Keep these lines: the bundle does not hold the names, and an answer about it will use the new ones.", KO: "이 줄을 보관하세요. 번들에는 원래 이름이 없고, 회신에는 바뀐 이름이 쓰입니다."},
	"bundle.open":       {EN: "look at it: my open {file}", KO: "열어 보기: my open {file}"},
	"case.bundle":       {EN: "case bundle of {when}, written by {tool}", KO: "{when} 케이스 번들, 작성 도구 {tool}"},
	"case.subject":      {EN: "subject {dimm}", KO: "대상 {dimm}"},
	"case.anonymized":   {EN: "host names and network addresses were replaced", KO: "호스트 이름과 네트워크 주소를 바꾼 번들"},
	"case.engine":       {EN: "The findings stored in this case were computed by engine {theirs}; this tool computed them again with engine {ours}.", KO: "이 케이스에 저장된 소견은 엔진 {theirs}의 결과이고, 지금 화면은 엔진 {ours}로 다시 계산한 것입니다."},
	"case.thresholds":   {EN: "The sender changed thresholds. To see what they saw, add: {sets}", KO: "보낸 쪽이 임계값을 바꿨습니다. 같은 화면을 보려면 추가: {sets}"},
	"case.grade":        {EN: "The sender's host finding was {theirs}; this run gives {ours}.", KO: "보낸 쪽의 호스트 소견은 {theirs}, 이번 실행은 {ours}입니다."},

	// "my version".
	"help.version": {EN: "Usage: my version [--json]\n\nPrints the version, the commit and its day, the Go version and the platform of this binary. With --json it also prints the versions of what the binary reads and writes: the engine, the ledger schema, the case bundle format and the JSON output.", KO: "사용법: my version [--json]\n\n이 바이너리의 버전, 커밋과 그 날짜, Go 버전, 플랫폼을 출력합니다. --json을 주면 바이너리가 읽고 쓰는 것의 버전도 함께 출력합니다: 엔진, 원장 스키마, 케이스 번들 형식, JSON 출력."},
	"err.version":  {EN: "takes no arguments; --json prints the build information as JSON", KO: "인자를 받지 않음. --json은 빌드 정보를 JSON으로 출력"},

	// What "my report" says.
	"report.written": {EN: "report written: {file} ({size})", KO: "리포트 저장: {file} ({size})"},
	"report.holds":   {EN: "{dimms} {dimms|DIMM|DIMMs} described, {records} {records|record|records} in the appendix, {audience}", KO: "DIMM {dimms}개 기술, 부록 기록 {records}건, {audience}"},
	"report.open":    {EN: "open it in a browser; printing it there gives a PDF", KO: "브라우저로 엽니다. 거기서 인쇄하면 PDF가 됩니다."},

	// What to run next.
	"hint.dimm":   {EN: "examination of this DIMM", KO: "이 DIMM 진찰 화면"},
	"hint.why":    {EN: "what the finding rests on", KO: "판정 근거"},
	"hint.bundle": {EN: "write a case bundle", KO: "번들 만들기"},
	"hint.import": {EN: "keep the import as a case to look closer", KO: "가져온 것을 케이스로 저장해 자세히 보기"},
	"hint.help":   {EN: "how to read the figures", KO: "판독법"},

	// Help.
	"help.usage":   {EN: "Usage:", KO: "사용법:"},
	"help.flags":   {EN: "Flags:", KO: "옵션:"},
	"help.reading": {EN: "How to read the output", KO: "출력 판독법"},
	"help.exit":    {EN: "Exit code: the grade of what was shown (0 ok, 1 observe, 2 consider replacement, 3 replace now, 4 hold replacement); 64 for a wrong command line, 70 when the command failed.", KO: "종료 코드: 보여준 대상의 등급(0 정상, 1 관찰, 2 교체 고려, 3 즉시 교체, 4 교체 보류). 명령줄 오류는 64, 실행 실패는 70."},
	"help.source":  {EN: "The command scans this host on every run and keeps nothing. To look at a stored case instead, put \"my open FILE\" in front: my open case.mycase {cmd}", KO: "이 명령은 실행할 때마다 이 호스트를 스캔하며 남기는 것이 없습니다. 저장된 케이스를 보려면 앞에 \"my open FILE\"을 붙입니다: my open case.mycase {cmd}"},
	"help.common": {
		EN: "--lang en|ko      language of the output (default: from the locale, English otherwise)\n" +
			"--json            print the data behind the figure as JSON\n" +
			"--ascii           draw with ASCII characters only\n" +
			"--color WHEN      auto (on a terminal, unless NO_COLOR is set), always or never\n" +
			"--width N         columns to fill (default: the terminal, 100 otherwise)\n" +
			"--host NAME       the host to show, when the ledger holds several\n" +
			"--config FILE     read thresholds from FILE (key = value lines)\n" +
			"--set KEY=VALUE   change one threshold; \"my help thresholds\" lists them\n" +
			"--plugin PROGRAM  run a vendor analyzer on the error records before the engine\n" +
			"                  (docs/plugin-contract.md); --plugin-timeout TIME bounds its run\n" +
			"                  (default 10s), --plugin-logs N sets the previous records sent\n" +
			"                  per DIMM (default 1000)",
		KO: "--lang en|ko      출력 언어 (기본: 로캘, 한국어가 아니면 영어)\n" +
			"--json            그림의 데이터를 JSON으로 출력\n" +
			"--ascii           ASCII 문자만으로 그림\n" +
			"--color WHEN      auto(터미널일 때, NO_COLOR가 없을 때), always, never\n" +
			"--width N         출력 폭 (기본: 터미널 폭, 터미널이 아니면 100)\n" +
			"--host NAME       원장에 호스트가 여럿일 때 볼 호스트\n" +
			"--config FILE     임계값 파일 (key = value 줄)\n" +
			"--set KEY=VALUE   임계값 하나 변경. 목록은 \"my help thresholds\"\n" +
			"--plugin PROGRAM  엔진 실행 전에 벤더 분석기에 오류 기록을 넘김\n" +
			"                  (docs/plugin-contract.md). --plugin-timeout TIME은 실행 제한 시간\n" +
			"                  (기본 10s), --plugin-logs N은 DIMM마다 보내는 이전 기록 수\n" +
			"                  (기본 1000)",
	},
	"help.output": {
		EN: "--lang en|ko        language of the output (default: from the locale, English otherwise)\n" +
			"--json              print the result as JSON\n" +
			"--ascii             draw with ASCII characters only\n" +
			"--color WHEN        auto (on a terminal, unless NO_COLOR is set), always or never\n" +
			"--width N           columns to fill (default: the terminal, 100 otherwise)\n" +
			"--config FILE       read thresholds from FILE (key = value lines)\n" +
			"--set KEY=VALUE     change one threshold; \"my help thresholds\" lists them\n" +
			"--plugin PROGRAM    run a vendor analyzer on the error records before the engine\n" +
			"                    (docs/plugin-contract.md); --plugin-timeout TIME bounds its run\n" +
			"                    (default 10s), --plugin-logs N sets the previous records sent\n" +
			"                    per DIMM (default 1000)",
		KO: "--lang en|ko        출력 언어 (기본: 로캘, 한국어가 아니면 영어)\n" +
			"--json              결과를 JSON으로 출력\n" +
			"--ascii             ASCII 문자만으로 그림\n" +
			"--color WHEN        auto(터미널일 때, NO_COLOR가 없을 때), always, never\n" +
			"--width N           출력 폭 (기본: 터미널 폭, 터미널이 아니면 100)\n" +
			"--config FILE       임계값 파일 (key = value 줄)\n" +
			"--set KEY=VALUE     임계값 하나 변경. 목록은 \"my help thresholds\"\n" +
			"--plugin PROGRAM    엔진 실행 전에 벤더 분석기에 오류 기록을 넘김\n" +
			"                    (docs/plugin-contract.md). --plugin-timeout TIME은 실행 제한 시간\n" +
			"                    (기본 10s), --plugin-logs N은 DIMM마다 보내는 이전 기록 수\n" +
			"                    (기본 1000)",
	},
	"help.scan": {
		EN: "--ledger FILE     keep the scan in the ledger FILE and add to it on every run\n" +
			"--root DIR        read a copy of a host's files below DIR instead of this host\n" +
			"--fleet KIND      read the store the rasdaemons of a fleet write to, mysql or postgresql,\n" +
			"                  instead of this host. Without --host the overview lists every host\n" +
			"--fleet-env FILE  settings of that store: the environment file rasdaemon is started with\n" +
			"                  (RAS_MYSQL_*, RAS_PG_*). Without it they come from the environment\n" +
			"--since TIME      with --fleet: read rows from then on (2026-10-01, or 30d for 30 days back)",
		KO: "--ledger FILE     스캔을 원장 FILE에 남기고 실행할 때마다 누적\n" +
			"--root DIR        이 호스트가 아닌, DIR 아래에 복사해 둔 호스트 파일을 읽음\n" +
			"--fleet KIND      플릿의 rasdaemon이 함께 쓰는 저장소(mysql 또는 postgresql)를 읽음.\n" +
			"                  이 호스트는 스캔하지 않음. --host가 없으면 개요에 모든 호스트를 나열\n" +
			"--fleet-env FILE  그 저장소의 접속 설정: rasdaemon을 띄울 때 쓰는 환경 파일.\n" +
			"                  없으면 환경 변수(RAS_MYSQL_*, RAS_PG_*)에서 읽음\n" +
			"--since TIME      --fleet과 함께: 그 시각 이후의 행만 읽음 (2026-10-01, 최근 30일은 30d)",
	},

	"help.overview.usage": {EN: "my [scan] [flags]", KO: "my [scan] [옵션]"},
	"help.overview.text":  {EN: "Scans this host and draws its slot map and the DIMMs ranked by risk. It reads the rasdaemon database, the kernel log, the EDAC counters and the SMBIOS tables. It changes nothing on the host. Sources it may not read as this user are named on the \"missing\" line with what root would add. A scan that can read no source of error records at all ends with exit code 70 and names what it looked for: it does not answer \"no errors on record\".", KO: "이 호스트를 스캔해 슬롯 맵과 위험 순위를 그립니다. rasdaemon DB, 커널 로그, EDAC 카운터, SMBIOS 테이블을 읽으며 호스트의 어떤 것도 바꾸지 않습니다. 현재 사용자로 읽지 못한 소스는 \"부족\" 줄에 나오고, root로 실행하면 무엇이 더해지는지도 함께 적습니다. 오류 기록 소스를 하나도 읽지 못하면 \"오류 기록 없음\"이라고 답하지 않고, 찾아본 소스를 적은 뒤 종료 코드 70으로 끝납니다."},
	"help.overview.flags": {EN: "--all             list every DIMM, also those without errors", KO: "--all             오류 없는 DIMM도 모두 나열"},

	"help.dimm.usage": {EN: "my dimm <slot|serial> [flags]", KO: "my dimm <슬롯|시리얼> [옵션]"},
	"help.dimm.text":  {EN: "The examination of one DIMM: its finding, the branch its errors sit on, the address map of the bank with the most errors, and its timeline. A DIMM is named by its slot, by its serial number or the end of it, or by the EDAC label of its slot.", KO: "DIMM 하나의 진찰 화면: 판정 카드, 오류가 몰린 가지, 오류가 가장 많은 bank의 주소 맵, 타임라인. DIMM은 슬롯 이름, 시리얼(끝자리만 써도 됨), 슬롯의 EDAC 라벨로 지정합니다."},

	"help.why.usage": {EN: "my why [slot|serial] [--oneline] [flags]", KO: "my why [슬롯|시리얼] [--oneline] [옵션]"},
	"help.why.text":  {EN: "The finding and everything it rests on: the rules that fired, the ledger records behind each statement, the checks against other explanations, the missing data, the steps of the confidence and the faults. Without a DIMM it prints this for every DIMM that has errors or a finding, the worst first.", KO: "판정과 그 근거 전체: 발동한 규칙, 문장마다의 원장 기록, 다른 설명과 견준 점검, 부족한 데이터, 신뢰도 계산 단계, 고장 목록. DIMM을 지정하지 않으면 오류나 소견이 있는 모든 DIMM을 위험한 순서로 출력합니다."},
	"help.why.flags": {
		EN: "--oneline         one line per DIMM, for a ticket\n" +
			"--all             also the DIMMs without errors",
		KO: "--oneline         DIMM마다 한 줄 (티켓에 붙이는 용도)\n" +
			"--all             오류 없는 DIMM도 포함",
	},

	"help.map.usage": {EN: "my map <slot|serial> [--bank N] [--rows A-B] [flags]", KO: "my map <슬롯|시리얼> [--bank N] [--rows A-B] [옵션]"},
	"help.map.text":  {EN: "The address map of one bank of a DIMM: errors by row and column. Without --bank it draws the bank with the most errors and names the other banks that have errors.", KO: "DIMM의 bank 하나를 row x column 주소 맵으로 그립니다. --bank가 없으면 오류가 가장 많은 bank를 그리고 오류가 있는 다른 bank를 알려 줍니다."},
	"help.map.flags": {
		EN: "--bank N          the bank to draw: a bank address (3), or bank group and bank (0/3)\n" +
			"--rank N          narrow the choice to a rank\n" +
			"--device N        narrow the choice to a device\n" +
			"--rows A-B        draw this range of rows (0x1a00-0x1aff)",
		KO: "--bank N          그릴 bank: bank 번호(3) 또는 bank group과 bank(0/3)\n" +
			"--rank N          rank로 범위를 좁힘\n" +
			"--device N        device로 범위를 좁힘\n" +
			"--rows A-B        이 row 범위를 그림 (0x1a00-0x1aff)",
	},

	"help.timeline.usage": {EN: "my timeline [slot|serial] [--days N] [flags]", KO: "my timeline [슬롯|시리얼] [--days N] [옵션]"},
	"help.timeline.text":  {EN: "Errors per day with boots, repairs, pages taken offline, slot changes and uncorrected errors, for one DIMM or, without one, for every DIMM of the host that has errors.", KO: "일별 오류에 부팅, 리페어, page offline, 슬롯 이동, UE를 표시합니다. DIMM을 지정하지 않으면 오류가 있는 모든 DIMM을 줄마다 그립니다."},
	"help.timeline.flags": {EN: "--days N          draw the last N days only", KO: "--days N          최근 N일만 그림"},

	"help.gpu.usage": {EN: "my gpu [index|serial|uuid|pci-address] [--days N] [flags]", KO: "my gpu [인덱스|시리얼|uuid|pci-주소] [--days N] [옵션]"},
	"help.gpu.text":  {EN: "The memory of the GPUs of this host as a repair budget. A GPU does not report where in its memory an error was; it reports how many rows were remapped, whether a remapping is pending or failed, and how many banks still have how many spare rows. The command reads the Xid lines of the kernel log and what nvidia-smi prints, and lists every GPU with memory records, the one with the least room first. Naming one GPU adds the counts behind its budget and its latest Xid records. The exit code is 3 when a row remapping failed on a GPU that is shown (rule R-REP-01) and 0 otherwise: no rule grades the other states.", KO: "이 호스트의 GPU 메모리를 리페어 예산으로 보여줍니다. GPU는 메모리의 어느 위치에서 오류가 났는지 보고하지 않습니다. 보고하는 것은 remap된 row 수, remap의 대기와 실패 여부, bank별로 남은 spare row입니다. 커널 로그의 Xid 줄과 nvidia-smi 출력을 읽어, 메모리 기록이 있는 GPU를 여유가 적은 순서로 나열합니다. GPU 하나를 지정하면 예산 뒤의 카운트와 최근 Xid 기록을 더합니다. 표시한 GPU 중 row remap이 실패한 것이 있으면 종료 코드는 3(규칙 R-REP-01)이고, 그 밖에는 0입니다. 다른 상태에 등급을 매기는 규칙은 없습니다."},
	"help.gpu.flags": {EN: "--days N          count the Xid records of the last N days (default 30)", KO: "--days N          최근 N일의 Xid 기록을 셈 (기본 30)"},

	"help.verify.usage": {EN: "my verify --expect PLAN [flags]", KO: "my verify --expect PLAN [옵션]"},
	"help.verify.text":  {EN: "Compares an error injection plan with what the sources of this host recorded. A plan is a text file that lists what a test put into the host: errors with their severity, address and place inside the DIMM, how many times, and pages taken offline. The command scans the host and looks every injection up in the ledger. It reports the injections that are missing or were found fewer times than planned, and every field a record decodes as something else than was injected: a wrong row, column, bank or channel is how a decoding bug shows. Fields the platform does not report are listed apart and are no mismatch; records beyond the plan are counted. The command injects nothing and changes nothing. The exit code is 0 when every injection was found as planned and 1 when one was not; it is no grade.", KO: "에러 주입 계획을 이 호스트의 소스가 기록한 것과 대조합니다. 계획은 시험이 호스트에 넣은 것을 적은 텍스트 파일입니다: 심각도와 주소와 DIMM 안 위치가 있는 오류, 주입 횟수, offline시킨 page. 명령은 호스트를 스캔하고 주입마다 원장에서 기록을 찾습니다. 기록이 없거나 계획한 수에 못 미치는 주입을 알려 주고, 기록이 주입과 다르게 디코딩한 필드를 모두 적습니다. row, column, bank, channel이 틀리게 나오는 것이 디코딩 버그의 모습입니다. 플랫폼이 보고하지 않는 필드는 따로 나열하며 불일치로 세지 않습니다. 계획 밖의 기록은 개수만 셉니다. 이 명령은 아무것도 주입하지 않고 아무것도 바꾸지 않습니다. 모든 주입이 계획대로 기록됐으면 종료 코드는 0, 하나라도 다르면 1입니다. 이 종료 코드는 등급이 아닙니다."},
	"help.verify.flags": {
		EN: "--expect PLAN     the injection plan: a text file with \"format: 1\", a scenario name and\n" +
			"                  a list of injections (id, inject, severity, count, address, row, ...)",
		KO: "--expect PLAN     주입 계획: \"format: 1\", 시나리오 이름, 주입 목록(id, inject, severity,\n" +
			"                  count, address, row 등)이 든 텍스트 파일",
	},

	"help.export.usage": {EN: "my export fmfm [-o FILE] [--records] [flags]", KO: "my export fmfm [-o FILE] [--records] [옵션]"},
	"help.export.text":  {EN: "Writes the findings of this host as an analysis log in the form of the OCP Fleet Memory Fault Management (FMFM) framework: one record per DIMM with its serial number, its place, the Level 1 fault mode, a risk and a severity class, a suggested action, and the finding of this tool with its rules and evidence. With --records the log also holds every error record with the field names of the FMFM runtime error log, so that records from a rasdaemon database, a kernel log or a BMC can be handed to an analyzer that reads that form. A field no source reported is left out; each record names the fields that are missing and the observation grade of its data. The log is JSON and goes to standard output, or into FILE with -o. It holds no action descriptor (CPAD): a suggestion is for a person, and nothing acts on it. docs/fmfm-mapping.md lists every field. The exit code is 0 when the log was written.", KO: "이 호스트의 소견을 OCP Fleet Memory Fault Management(FMFM) 프레임워크의 분석 로그 형식으로 내보냅니다. DIMM마다 기록 하나이며 시리얼, 위치, Level 1 고장 모드, 위험과 심각도 분류, 제안 조치, 그리고 이 도구의 소견과 규칙과 근거가 들어갑니다. --records를 주면 모든 오류 기록을 FMFM runtime 오류 로그의 필드명으로 함께 싣습니다. rasdaemon DB, 커널 로그, BMC에서 온 기록을 그 형식을 읽는 분석기에 넘길 수 있습니다. 어떤 소스도 보고하지 않은 필드는 비워 두고, 기록마다 빠진 필드와 데이터의 관측 등급을 적습니다. 로그는 JSON이며 표준 출력으로 나가고, -o를 주면 FILE에 저장합니다. 조치 기술자(CPAD)는 넣지 않습니다. 제안은 사람이 읽는 것이고 그것으로 실행되는 조치는 없습니다. 필드 목록은 docs/fmfm-mapping.md에 있습니다. 로그를 쓰면 종료 코드는 0입니다."},
	"help.export.flags": {
		EN: "-o FILE           write the log to FILE; without it, or with \"-\", to standard output\n" +
			"--records         add the runtime log: every error record with FMFM field names\n" +
			"--all             also the DIMMs without errors and without a finding\n" +
			"--force           replace FILE when it exists",
		KO: "-o FILE           로그를 FILE에 저장. 생략하거나 \"-\"를 주면 표준 출력\n" +
			"--records         runtime 로그 추가: FMFM 필드명으로 쓴 모든 오류 기록\n" +
			"--all             오류도 소견도 없는 DIMM까지 포함\n" +
			"--force           FILE이 있으면 덮어씀",
	},

	"help.diff.usage": {EN: "my diff BEFORE AFTER [--at TIME] [--all] [flags]", KO: "my diff BEFORE AFTER [--at TIME] [--all] [옵션]"},
	"help.diff.text":  {EN: "Sets two cases of one host side by side: BEFORE is the host before something was done to it (a DIMM reseated, two DIMMs swapped between slots, a DIMM replaced, the BIOS updated), AFTER the same host some time later. Each is a case bundle (.mycase) or a ledger file. The command lists what differs between the two: DIMMs that moved, left or came, BIOS, BMC and kernel versions, restarts, and the repairs and page offlines the later case recorded. For every DIMM it then counts the errors before and after, and says what the counts show: the errors moved with the DIMM, stayed with the slot, go on, or stopped. As after it counts the errors since the last restart before the later case was taken, because the hardware cannot have changed after that. Each case is analysed on its own; no file is changed and this host is not scanned. The exit code is the grade of the host in the later case.", KO: "한 호스트의 케이스 둘을 나란히 놓습니다. BEFORE는 조치(DIMM reseat, 두 슬롯의 DIMM 맞교환, DIMM 교체, BIOS 업데이트) 전의 호스트이고 AFTER는 얼마 뒤의 같은 호스트입니다. 둘 다 케이스 번들(.mycase)이나 원장 파일입니다. 명령은 둘 사이에 달라진 것을 나열합니다: 옮기거나 빠지거나 새로 들어온 DIMM, BIOS와 BMC와 커널 버전, 재시작, 뒤 케이스가 기록한 리페어와 page offline. 이어서 DIMM마다 전후 오류를 세고 그 수가 무엇을 보이는지 적습니다: 오류가 DIMM을 따라갔는지, 슬롯에 남았는지, 지속되는지, 멈췄는지. 이후로 세는 것은 뒤 케이스 직전의 마지막 재시작부터의 오류입니다. 그 뒤로는 하드웨어가 바뀔 수 없기 때문입니다. 케이스는 각각 따로 분석하며, 파일을 바꾸지 않고 이 호스트를 스캔하지 않습니다. 종료 코드는 뒤 케이스의 호스트 등급입니다."},
	"help.diff.flags": {
		EN: "--at TIME         when the change was made (2026-10-01, or 2026-10-01T03:00:00Z): errors\n" +
			"                  from then on count as after. It replaces the last restart as the start\n" +
			"--all             list every DIMM, also those in the same slot and without errors",
		KO: "--at TIME         변경한 시각 (2026-10-01 또는 2026-10-01T03:00:00Z). 그때부터의 오류를\n" +
			"                  이후로 셈. 마지막 재시작 대신 이 시각이 시작점이 됨\n" +
			"--all             슬롯이 그대로이고 오류가 없는 DIMM도 모두 나열",
	},

	"help.open.usage": {EN: "my open FILE [dimm|why|map|timeline|gpu|verify ...] [flags]", KO: "my open FILE [dimm|why|map|timeline|gpu|verify ...] [옵션]"},
	"help.open.text":  {EN: "Shows a stored case the way the commands show this host: FILE is a case bundle (.mycase) or a ledger file. Without a command it draws the overview; with one, the arguments of that command follow. The file is not changed and the host is not scanned.", KO: "저장된 케이스를 이 호스트를 볼 때와 같은 화면으로 보여줍니다. FILE은 케이스 번들(.mycase)이나 원장 파일입니다. 명령을 생략하면 개요를 그리고, 명령을 쓰면 그 명령의 인자가 이어집니다. 파일을 바꾸지 않고 호스트를 스캔하지 않습니다."},

	"help.bundle.usage": {EN: "my bundle [slot|serial] -o FILE [--anonymize] [flags]", KO: "my bundle [슬롯|시리얼] -o FILE [--anonymize] [옵션]"},
	"help.bundle.text":  {EN: "Scans this host and writes what it found as one file, a case bundle: the ledger with the error records, DIMM identities, boots and actions, the findings of this run, a manifest of what was collected, and the source inventory. A bundle holds records about memory errors and never the contents of memory. Naming a DIMM makes it the subject of the case; the bundle still holds the whole host, because a finding about one DIMM rests on its neighbours too. The file is a ZIP archive: \"unzip -l\" lists it. The exit code is 0 when the bundle was written.", KO: "이 호스트를 스캔해 찾은 것을 파일 하나(케이스 번들)로 저장합니다. 오류 기록, DIMM 신원, 부팅과 조치 이력이 든 원장, 이번 실행의 소견, 수집 항목 목록, 소스 인벤토리가 들어갑니다. 번들에는 메모리 오류 기록만 들어가며 메모리 내용은 들어가지 않습니다. DIMM을 지정하면 그 DIMM이 케이스 대상이 됩니다. 그래도 번들에는 호스트 전체가 들어갑니다. DIMM 하나의 소견이 이웃 DIMM에도 기대기 때문입니다. 파일은 ZIP 형식이라 \"unzip -l\"로 목록을 볼 수 있습니다. 번들을 저장하면 종료 코드는 0입니다."},
	"help.bundle.flags": {
		EN: "-o FILE           the bundle to write (case.mycase); \"-\" writes it to standard output,\n" +
			"                  as in: ssh HOST sudo my bundle -o - > case.mycase\n" +
			"--anonymize       replace host names, IP and MAC addresses, and the names of home\n" +
			"                  directories in file paths; DIMM serial numbers stay\n" +
			"--key-file FILE   with --anonymize: a key that makes every bundle replace a name alike\n" +
			"--force           replace FILE when it exists",
		KO: "-o FILE           저장할 번들 (case.mycase). \"-\"는 표준 출력\n" +
			"                  예: ssh HOST sudo my bundle -o - > case.mycase\n" +
			"--anonymize       호스트 이름, IP 주소, MAC 주소, 파일 경로의 홈 디렉터리 이름을 바꿈.\n" +
			"                  DIMM 시리얼은 그대로 둠\n" +
			"--key-file FILE   --anonymize와 함께: 같은 이름이 번들마다 같은 단어로 바뀌게 하는 키\n" +
			"--force           FILE이 있으면 덮어씀",
	},

	"help.import.usage": {EN: "my import <file|directory>... [-o FILE] [flags]", KO: "my import <파일|디렉터리>... [-o FILE] [옵션]"},
	"help.import.text":  {EN: "Builds a case from files, without the host they came from. Each input is recognised by its content: a rasdaemon database, a kernel log (dmesg, syslog, journal export), dmidecode output, a BMC event log, a Redfish dump, nvidia-smi output (-q, or a query in CSV form), the answer of the mcelog daemon to its client, an FMFM log, or a directory that is a copy of a host's files. An input that is not recognised stops the import. All inputs are taken to be one host. From a log that holds the records of several hosts, the records of the host named with --host are read. With -o the case is written as a bundle and the exit code is 0; without it the overview is drawn at once and the exit code is the grade of the host.", KO: "호스트 없이 파일만으로 케이스를 만듭니다. 입력은 내용으로 가려냅니다: rasdaemon DB, 커널 로그(dmesg, syslog, journal 내보내기), dmidecode 출력, BMC 이벤트 로그, Redfish 덤프, nvidia-smi 출력(-q 또는 CSV 형식 쿼리), mcelog 데몬이 클라이언트에 답한 출력, FMFM 로그, 호스트 파일 사본 디렉터리. 알아보지 못한 입력이 있으면 가져오기를 멈춥니다. 모든 입력을 한 호스트의 것으로 봅니다. 여러 호스트의 기록이 든 로그에서는 --host로 지정한 호스트의 기록을 읽습니다. -o를 주면 케이스를 번들로 저장하고 종료 코드는 0입니다. -o가 없으면 바로 개요를 그리고 종료 코드는 호스트 등급입니다."},
	"help.import.flags": {
		EN: "-o FILE             write the case as a bundle (case.mycase); \"-\" writes it to standard output\n" +
			"--host NAME         the host the files come from, when they do not say\n" +
			"--captured-at TIME  when a kernel log without dates was captured (2026-11-12T03:14:00Z)\n" +
			"--mtime             take the modification time of such a file as its capture time\n" +
			"--tz ZONE           the time zone of times that carry none (default UTC)\n" +
			"--nvidia-query LIST the fields of an nvidia-smi query that was saved without its\n" +
			"                    header line (index,uuid,remapped_rows.failure)\n" +
			"--ledger FILE       keep the import in the ledger FILE and add to it on every run\n" +
			"--anonymize         with -o: replace host names and network addresses\n" +
			"--key-file FILE     with --anonymize: a key that makes every bundle replace a name alike\n" +
			"--force             replace FILE when it exists\n" +
			"--all               in the overview, list every DIMM",
		KO: "-o FILE             케이스를 번들로 저장 (case.mycase). \"-\"는 표준 출력\n" +
			"--host NAME         파일에 호스트 이름이 없을 때 쓸 이름\n" +
			"--captured-at TIME  날짜 없는 커널 로그를 받은 시각 (2026-11-12T03:14:00Z)\n" +
			"--mtime             그런 파일의 수정 시각을 받은 시각으로 사용\n" +
			"--tz ZONE           시간대 표기가 없는 시각의 시간대 (기본 UTC)\n" +
			"--nvidia-query LIST 헤더 줄 없이 저장한 nvidia-smi 쿼리 출력의 필드 목록\n" +
			"                    (index,uuid,remapped_rows.failure)\n" +
			"--ledger FILE       가져온 것을 원장 FILE에 남기고 실행할 때마다 누적\n" +
			"--anonymize         -o와 함께: 호스트 이름과 네트워크 주소를 바꿈\n" +
			"--key-file FILE     --anonymize와 함께: 같은 이름이 번들마다 같은 단어로 바뀌게 하는 키\n" +
			"--force             FILE이 있으면 덮어씀\n" +
			"--all               개요에 모든 DIMM을 나열",
	},

	"help.serve.usage": {EN: "my serve [--metrics] [--fleet KIND] [--listen ADDR] [--interval TIME] [flags]", KO: "my serve [--metrics] [--fleet KIND] [--listen ADDR] [--interval TIME] [옵션]"},
	"help.serve.text": {
		EN: "Runs until it is stopped and answers HTTP requests about this host. It scans the host at start and again after every pause of --interval, each time into a new temporary ledger, and answers from the latest scan. It changes nothing on the host. " +
			"With --metrics, /metrics gives the Prometheus text format: per DIMM the finding, the error totals, the faults by mode, the pages taken offline and the repairs; per GPU the row remapping state; per source the observation grade. No metric has an address as a label. " +
			"/api/dimm/<slot|serial> answers with the JSON that \"my why <slot|serial> --json\" prints, and /api/dimm with that of every DIMM that has errors or a finding; ?lang=en|ko chooses the language. " +
			"When a scan fails, the metrics of the scan before stay and my_exporter_scan_failures_total counts it. /-/healthy answers 200 while the process runs, /-/ready once the first scan has finished. " +
			"With --fleet the server reads the store the rasdaemons of a fleet write to, in place of this host: one server for every host. A scan then reads the hosts that have new rows, and a host without new rows again after six hours; /api/dimm takes the host as ?host=NAME. " +
			"The server listens on 127.0.0.1 unless --listen says otherwise and has no authentication. SIGINT or SIGTERM stops it; the exit code is then 0.",
		KO: "중지할 때까지 실행되며 HTTP 요청에 이 호스트의 상태로 답합니다. 시작할 때 호스트를 스캔하고 --interval만큼 쉰 뒤 다시 스캔합니다. 스캔마다 새 임시 원장을 쓰고 가장 최근 스캔으로 답합니다. 호스트의 어떤 것도 바꾸지 않습니다. " +
			"--metrics를 주면 /metrics가 Prometheus 텍스트 형식으로 나옵니다. DIMM별 소견, 오류 누적, 모드별 고장 수, offline된 page 수, 리페어 횟수, GPU별 row remap 상태, 소스별 관측 등급입니다. 주소를 라벨로 쓰는 지표는 없습니다. " +
			"/api/dimm/<슬롯|시리얼>은 \"my why <슬롯|시리얼> --json\"과 같은 JSON으로 답하고, /api/dimm은 오류나 소견이 있는 모든 DIMM의 JSON으로 답합니다. ?lang=en|ko로 언어를 고릅니다. " +
			"스캔이 실패하면 직전 스캔의 지표가 남고 my_exporter_scan_failures_total이 늘어납니다. /-/healthy는 프로세스가 도는 동안, /-/ready는 첫 스캔이 끝난 뒤부터 200으로 답합니다. " +
			"--fleet을 주면 플릿의 rasdaemon이 함께 쓰는 저장소를 읽고 이 호스트는 스캔하지 않습니다. 서버 하나가 모든 호스트를 맡습니다. 이때 스캔은 새 행이 생긴 호스트만 읽고, 새 행이 없는 호스트는 6시간 뒤에 다시 읽습니다. /api/dimm에는 ?host=NAME으로 호스트를 지정합니다. " +
			"--listen을 주지 않으면 127.0.0.1에서만 받으며 인증 기능은 없습니다. SIGINT나 SIGTERM을 받으면 멈추고 종료 코드는 0입니다.",
	},
	"help.serve.flags": {
		EN: "--metrics         serve /metrics for Prometheus\n" +
			"--listen ADDR     where to listen (default 127.0.0.1:10163); :10163 listens on every interface\n" +
			"--interval TIME   pause between two scans, such as 30s or 5m (default 5m)\n" +
			"--root DIR        scan a copy of a host's files below DIR instead of this host\n" +
			"--fleet KIND      read the store the rasdaemons of a fleet write to, mysql or postgresql,\n" +
			"                  instead of this host\n" +
			"--fleet-env FILE  settings of that store: the environment file rasdaemon is started with\n" +
			"                  (RAS_MYSQL_*, RAS_PG_*). Without it they come from the environment\n" +
			"--since TIME      with --fleet: read rows from then on (2026-10-01, or 30d for 30 days back)\n" +
			"--host NAME       with --fleet: serve this host of the store only\n" +
			"--lang en|ko      language of the log and of API answers (default: the locale, else English)\n" +
			"--config FILE     read thresholds from FILE (key = value lines)\n" +
			"--set KEY=VALUE   change one threshold; \"my help thresholds\" lists them",
		KO: "--metrics         Prometheus용 /metrics 제공\n" +
			"--listen ADDR     받을 주소 (기본 127.0.0.1:10163). :10163은 모든 인터페이스에서 받음\n" +
			"--interval TIME   스캔 사이에 쉬는 시간. 30s, 5m 등 (기본 5m)\n" +
			"--root DIR        이 호스트가 아닌, DIR 아래에 복사해 둔 호스트 파일을 스캔\n" +
			"--fleet KIND      플릿의 rasdaemon이 함께 쓰는 저장소(mysql 또는 postgresql)를 읽음.\n" +
			"                  이 호스트는 스캔하지 않음\n" +
			"--fleet-env FILE  그 저장소의 접속 설정: rasdaemon을 띄울 때 쓰는 환경 파일.\n" +
			"                  없으면 환경 변수(RAS_MYSQL_*, RAS_PG_*)에서 읽음\n" +
			"--since TIME      --fleet과 함께: 그 시각 이후의 행만 읽음 (2026-10-01, 최근 30일은 30d)\n" +
			"--host NAME       --fleet과 함께: 저장소의 이 호스트만 제공\n" +
			"--lang en|ko      로그와 API 응답의 언어 (기본: 로캘, 한국어가 아니면 영어)\n" +
			"--config FILE     임계값 파일 (key = value 줄)\n" +
			"--set KEY=VALUE   임계값 하나 변경. 목록은 \"my help thresholds\"",
	},

	"help.report.usage": {EN: "my report [FILE] -o OUT.html [--audience customer|fa] [--figures DIR] [flags]", KO: "my report [FILE] -o OUT.html [--audience customer|fa] [--figures DIR] [옵션]"},
	"help.report.text":  {EN: "Writes the findings of a case as one HTML file. FILE is a case bundle (.mycase) or a ledger file; without FILE this host is scanned. The file holds everything it shows: the figures are inline SVG and nothing is loaded from elsewhere, so it opens without network access and can be sent as it is. Printing it from a browser gives a PDF. It has eight sections: summary, slot map, the finding of every DIMM with errors, topology bars and address map, timeline, checks against a wrong diagnosis, data quality, and an appendix with the original records the figures point at. In a browser, a point of a figure opens the records behind it. The exit code is 0 when the report was written.", KO: "케이스의 소견을 HTML 파일 하나로 저장합니다. FILE은 케이스 번들(.mycase)이나 원장 파일이고, FILE이 없으면 이 호스트를 스캔합니다. 파일에는 보여주는 것이 모두 들어 있습니다. 그림은 인라인 SVG이고 밖에서 불러오는 것이 없어 네트워크 없이 열리며 그대로 보낼 수 있습니다. 브라우저에서 인쇄하면 PDF가 됩니다. 여덟 절로 나뉩니다: 요약, 슬롯 맵, 오류가 있는 DIMM별 판정, 토폴로지 막대와 주소 맵, 타임라인, 오진 점검표, 데이터 품질, 그림이 가리키는 원본 기록을 실은 부록. 브라우저에서 그림의 점을 누르면 그 뒤의 기록이 나옵니다. 리포트를 저장하면 종료 코드는 0입니다."},
	"help.report.flags": {
		EN: "-o OUT.html       the report to write; \"-\" writes it to standard output\n" +
			"--audience WHO    customer (default): the findings with the public fault modes;\n" +
			"                  fa: adds the steps of the confidence, the faults one by one and,\n" +
			"                  when a vendor analyzer supplied it, its Level 2 output\n" +
			"--all             draw the figures of every DIMM with errors, also of those with\n" +
			"                  the finding \"observe\" and fewer than 10 errors\n" +
			"--figures DIR     also write each figure as an SVG file of its own into DIR,\n" +
			"                  for use outside the report; it can be given without -o\n" +
			"--force           replace OUT.html and the files in DIR when they exist",
		KO: "-o OUT.html       저장할 리포트. \"-\"는 표준 출력\n" +
			"--audience WHO    customer(기본): 공개 고장 모드까지의 소견\n" +
			"                  fa: 신뢰도 계산 단계와 고장 목록을 더하고, 벤더 분석기가\n" +
			"                  준 Level 2 결과가 있으면 함께 실음\n" +
			"--all             오류가 있는 모든 DIMM의 그림을 그림. 소견이 \"관찰\"이고\n" +
			"                  오류가 10건 미만인 DIMM도 포함\n" +
			"--figures DIR     그림마다 SVG 파일 하나씩 DIR에 따로 저장. 리포트 밖에서\n" +
			"                  쓰는 용도이며 -o 없이도 쓸 수 있음\n" +
			"--force           OUT.html과 DIR의 파일이 있으면 덮어씀",
	},

	"help.rules.title":      {EN: "Rules. A finding names the rules that fired.", KO: "규칙. 소견에는 발동한 규칙 ID가 찍힙니다."},
	"help.thresholds.title": {EN: "Thresholds, with their defaults. Change one with --set KEY=VALUE or a --config file.", KO: "임계값과 기본값. --set KEY=VALUE 또는 --config 파일로 바꿉니다."},
	"help.topics":           {EN: "More: my help reading (every figure), my help rules, my help thresholds.", KO: "더 보기: my help reading (모든 그림), my help rules, my help thresholds."},
}

// guides lists, per command, the reading guides of the figures it draws.
var guides = map[string][]string{
	"overview": {"guide.slotmap", "guide.ranking", "guide.grades", "guide.obs"},
	"dimm":     {"guide.card", "guide.topology", "guide.map", "guide.timeline", "guide.grades"},
	"why":      {"guide.card", "guide.grades", "guide.obs"},
	"map":      {"guide.map", "guide.obs"},
	"timeline": {"guide.timeline"},
	"gpu":      {"guide.gpu"},
	"verify":   {"guide.verify"},
	"diff":     {"guide.diff", "guide.grades"},
	"open":     {"guide.slotmap", "guide.ranking", "guide.grades"},
	"bundle":   nil,
	"report":   nil,
	"export":   nil,
	"import":   {"guide.slotmap", "guide.ranking", "guide.grades", "guide.obs"},
	"reading":  {"guide.slotmap", "guide.ranking", "guide.card", "guide.topology", "guide.map", "guide.timeline", "guide.gpu", "guide.grades", "guide.obs"},
}

// printGuides writes reading guides, each as a paragraph.
func printGuides(w io.Writer, lang i18n.Lang, st term.Style, codes []string) {
	for i, code := range codes {
		if i > 0 {
			fmt.Fprintln(w)
		}
		for _, line := range st.Wrap(view.Messages.Text(lang, code, st.Glyphs()), 2) {
			fmt.Fprintln(w, line)
		}
	}
}

// printHelp writes the help of a command: usage, what it does, its flags,
// the exit code, and how to read what it prints.
func printHelp(env *cli.Env, lang i18n.Lang, st term.Style, topic string, src *source) int {
	w := env.Stdout
	st.Lang = lang
	fmt.Fprintf(w, "%s %s\n\n", say(lang, "help.usage"), say(lang, "help."+topic+".usage"))
	for _, line := range st.Wrap(say(lang, "help."+topic+".text"), 0) {
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "\n%s\n", say(lang, "help.flags"))
	blocks := []string{"help." + topic + ".flags"}
	switch topic {
	case "open", "diff":
		// A stored case is read as it is: nothing is scanned.
		blocks = append(blocks, "help.common")
	case "import":
		// An import has no host to scan, and names its host itself.
		blocks = append(blocks, "help.output")
	case "serve", "collect":
		// A server and a collection draw no figure: they list every flag
		// they take themselves.
	default:
		blocks = append(blocks, "help.scan", "help.common")
	}
	for _, code := range blocks {
		if !words.Has(code) {
			continue
		}
		for _, line := range strings.Split(say(lang, code), "\n") {
			fmt.Fprintln(w, "  "+line)
		}
	}
	if topic != "bundle" && topic != "import" && topic != "serve" && topic != "report" && topic != "verify" && topic != "export" && topic != "collect" && topic != "bios" {
		fmt.Fprintln(w)
		for _, line := range st.Wrap(say(lang, "help.exit"), 0) {
			fmt.Fprintln(w, line)
		}
	}
	if topic != "open" && topic != "diff" && topic != "overview" && topic != "bundle" && topic != "import" && topic != "serve" && topic != "report" && topic != "export" && topic != "collect" {
		fmt.Fprintln(w)
		for _, line := range st.Wrap(say(lang, "help.source", "cmd", topic), 0) {
			fmt.Fprintln(w, line)
		}
	}
	if g := guides[topic]; len(g) > 0 {
		fmt.Fprintf(w, "\n%s\n\n", say(lang, "help.reading"))
		printGuides(w, lang, st, g)
	}
	fmt.Fprintf(w, "\n%s\n", say(lang, "help.topics"))
	return cli.ExitOK
}

// topicCommand is a help topic that is not a command: "my help reading".
func topicCommand(name string, print func(w io.Writer, lang i18n.Lang, st term.Style)) *cli.Command {
	return &cli.Command{Name: name, Hidden: true, Run: func(_ context.Context, env *cli.Env, args []string) int {
		o := newOptions(env, "my help "+name, &source{})
		_, lang, code, done := o.parse(env, args)
		if done {
			return code
		}
		print(env.Stdout, lang, o.style(env, lang))
		return cli.ExitOK
	}}
}

func printReading(w io.Writer, lang i18n.Lang, st term.Style) {
	st.Lang = lang
	fmt.Fprintf(w, "%s\n\n", say(lang, "help.reading"))
	printGuides(w, lang, st, guides["reading"])
}

func printRuleList(w io.Writer, lang i18n.Lang, st term.Style) {
	fmt.Fprintf(w, "%s\n\n", say(lang, "help.rules.title"))
	idW, gradeW := 0, 0
	for _, r := range engine.Rules {
		idW = max(idW, term.Width(r.ID))
		gradeW = max(gradeW, term.Width(engine.GradeText(lang, r.Grade)))
	}
	st.Lang = lang
	for _, r := range engine.Rules {
		grade := engine.GradeText(lang, r.Grade)
		lines := st.Wrap(engine.Messages.Text(lang, r.Condition, nil), idW+gradeW+6)
		for i, line := range lines {
			if i == 0 {
				fmt.Fprintf(w, "  %-*s  %s%s  %s\n", idW, r.ID, grade, strings.Repeat(" ", gradeW-term.Width(grade)), strings.TrimLeft(line, " "))
			} else {
				fmt.Fprintln(w, line)
			}
		}
	}
}

func printThresholdList(w io.Writer, lang i18n.Lang, _ term.Style) {
	fmt.Fprintf(w, "%s\n\n", say(lang, "help.thresholds.title"))
	cfg := engine.DefaultConfig()
	printThresholds(w, &cfg)
}
