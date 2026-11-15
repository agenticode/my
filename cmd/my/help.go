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
	"err.see_help":      {EN: "run \"{cmd}\" for usage", KO: "사용법은 \"{cmd}\""},
	"err.extra":         {EN: "unexpected argument \"{arg}\"", KO: "불필요한 인자 \"{arg}\""},
	"err.one_dimm":      {EN: "name one DIMM by its slot or serial number", KO: "DIMM 하나를 슬롯 이름이나 시리얼로 지정해야 함"},
	"err.color":         {EN: "--color {value}: want auto, always or never", KO: "--color {value}: auto, always, never 중 하나여야 함"},
	"err.width":         {EN: "--width {value}: want a number of columns", KO: "--width {value}: 칸 수여야 함"},
	"err.bank":          {EN: "--bank {value}: want a bank (3) or a bank group and a bank (0/3)", KO: "--bank {value}: bank 번호(3) 또는 bank group과 bank(0/3)여야 함"},
	"err.rows":          {EN: "--rows {value}: want a range of rows such as 0x1a00-0x1aff", KO: "--rows {value}: 0x1a00-0x1aff 같은 row 범위여야 함"},
	"err.days":          {EN: "--days {value}: want a number of days", KO: "--days {value}: 일수여야 함"},
	"err.not_dir":       {EN: "not a directory", KO: "디렉터리가 아님"},
	"err.empty":         {EN: "the ledger holds no host", KO: "원장에 호스트 없음"},
	"err.no_host":       {EN: "no host named {host} in the ledger (hosts: {hosts})", KO: "원장에 {host} 호스트 없음 (호스트: {hosts})"},
	"err.several_hosts": {EN: "the ledger holds several hosts: name one with --host ({hosts})", KO: "원장에 호스트가 여럿임: --host로 하나를 지정 ({hosts})"},
	"err.empty_slot":    {EN: "slot {slot} holds no DIMM", KO: "슬롯 {slot}에 DIMM 없음"},
	"err.no_dimm":       {EN: "no slot or DIMM \"{ref}\" in host {host}.", KO: "호스트 {host}에 \"{ref}\" 슬롯이나 DIMM 없음."},
	"err.with_findings": {EN: "DIMMs with errors or a finding: {slots}", KO: "오류나 소견이 있는 DIMM: {slots}"},
	"err.open_file":     {EN: "name the case to open: a .mycase bundle or a ledger file", KO: "열 케이스를 지정해야 함: .mycase 번들 또는 원장 파일"},
	"err.open_command":  {EN: "unknown command \"{cmd}\" (after the file: dimm, why, map, timeline, or nothing for the overview)", KO: "알 수 없는 명령 \"{cmd}\" (파일 뒤에는 dimm, why, map, timeline, 또는 생략하면 개요)"},

	"err.bundle_out":  {EN: "name the file to write with -o, for example -o case.mycase", KO: "저장할 파일을 -o로 지정해야 함 (예: -o case.mycase)"},
	"err.key_without": {EN: "--key-file goes with --anonymize", KO: "--key-file은 --anonymize와 함께 써야 함"},
	"err.exists":      {EN: "{file} exists; --force replaces it", KO: "{file}이 이미 있음. --force를 주면 덮어씀"},

	"err.import_files":  {EN: "name the files to import", KO: "가져올 파일을 지정해야 함"},
	"err.anonymize_out": {EN: "--anonymize goes with -o: it applies to the bundle that is written", KO: "--anonymize는 -o와 함께 써야 함: 저장하는 번들에 적용됨"},
	"err.time":          {EN: "{flag} {value}: want a date (2026-11-12) or a time (2026-11-12T03:14:00Z)", KO: "{flag} {value}: 날짜(2026-11-12) 또는 시각(2026-11-12T03:14:00Z)이어야 함"},
	"err.tz":            {EN: "--tz {value}: want a time zone name such as Asia/Seoul or UTC", KO: "--tz {value}: Asia/Seoul이나 UTC 같은 시간대 이름이어야 함"},

	// What "my import" says.
	"import.records": {EN: "{n} {n|record|records}", KO: "기록 {n}건"},
	"import.events":  {EN: "{n} error {n|record|records} (grade {grade})", KO: "오류 기록 {n}건({grade} 등급)"},
	"import.actions": {EN: "{n} {n|action|actions}", KO: "조치 {n}건"},
	"import.skipped": {EN: "{n} not usable", KO: "쓰지 못한 기록 {n}건"},
	"import.total":   {EN: "host {host}: {events} error {events|record|records} and {actions} {actions|action|actions} added, {dup} reported by more than one source and counted once", KO: "호스트 {host}: 오류 기록 {events}건과 조치 {actions}건 추가, 여러 소스가 함께 보고한 {dup}건은 한 번만 계산"},
	"import.finding": {EN: "host {host}, finding: {grade}", KO: "호스트 {host}, 소견: {grade}"},

	// What "my bundle" says.
	"bundle.written":    {EN: "case bundle written: {file} ({size})", KO: "케이스 번들 저장: {file} ({size})"},
	"bundle.host":       {EN: "host {host}: {dimms} {dimms|DIMM|DIMMs}, CE {ce}, UE {ue}, finding: {grade}", KO: "호스트 {host}: DIMM {dimms}개, CE {ce}건, UE {ue}건, 소견: {grade}"},
	"bundle.subject":    {EN: "subject of the case: {dimm}, finding: {grade}", KO: "케이스 대상: {dimm}, 소견: {grade}"},
	"bundle.missing":    {EN: "not in the bundle: {what}", KO: "번들에 없음: {what}"},
	"bundle.anonymized": {EN: "host names replaced, and {n} network {n|address|addresses}:", KO: "호스트 이름과 네트워크 주소 {n}개를 바꿈:"},
	"bundle.anon_hosts": {EN: "host names replaced:", KO: "호스트 이름을 바꿈:"},
	"bundle.keep_names": {EN: "Keep these lines: the bundle does not hold the names, and an answer about it will use the new ones.", KO: "이 줄을 보관하세요. 번들에는 원래 이름이 없고, 회신에는 바뀐 이름이 쓰입니다."},
	"bundle.open":       {EN: "look at it: my open {file}", KO: "열어 보기: my open {file}"},
	"case.bundle":       {EN: "case bundle of {when}, written by {tool}", KO: "{when} 케이스 번들, 작성 도구 {tool}"},
	"case.subject":      {EN: "subject {dimm}", KO: "대상 {dimm}"},
	"case.anonymized":   {EN: "host names and network addresses were replaced", KO: "호스트 이름과 네트워크 주소를 바꾼 번들"},
	"case.engine":       {EN: "The findings stored in this case were computed by engine {theirs}; this tool computed them again with engine {ours}.", KO: "이 케이스에 저장된 소견은 엔진 {theirs}의 결과이고, 지금 화면은 엔진 {ours}로 다시 계산한 것입니다."},
	"case.thresholds":   {EN: "The sender changed thresholds. To see what they saw, add: {sets}", KO: "보낸 쪽이 임계값을 바꿨습니다. 같은 화면을 보려면 추가: {sets}"},
	"case.grade":        {EN: "The sender's host finding was {theirs}; this run gives {ours}.", KO: "보낸 쪽의 호스트 소견은 {theirs}, 이번 실행은 {ours}입니다."},

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
			"--set KEY=VALUE   change one threshold; \"my help thresholds\" lists them",
		KO: "--lang en|ko      출력 언어 (기본: 로캘, 한국어가 아니면 영어)\n" +
			"--json            그림의 데이터를 JSON으로 출력\n" +
			"--ascii           ASCII 문자만으로 그림\n" +
			"--color WHEN      auto(터미널일 때, NO_COLOR가 없을 때), always, never\n" +
			"--width N         출력 폭 (기본: 터미널 폭, 터미널이 아니면 100)\n" +
			"--host NAME       원장에 호스트가 여럿일 때 볼 호스트\n" +
			"--config FILE     임계값 파일 (key = value 줄)\n" +
			"--set KEY=VALUE   임계값 하나 변경. 목록은 \"my help thresholds\"",
	},
	"help.output": {
		EN: "--lang en|ko        language of the output (default: from the locale, English otherwise)\n" +
			"--json              print the result as JSON\n" +
			"--ascii             draw with ASCII characters only\n" +
			"--color WHEN        auto (on a terminal, unless NO_COLOR is set), always or never\n" +
			"--width N           columns to fill (default: the terminal, 100 otherwise)\n" +
			"--config FILE       read thresholds from FILE (key = value lines)\n" +
			"--set KEY=VALUE     change one threshold; \"my help thresholds\" lists them",
		KO: "--lang en|ko        출력 언어 (기본: 로캘, 한국어가 아니면 영어)\n" +
			"--json              결과를 JSON으로 출력\n" +
			"--ascii             ASCII 문자만으로 그림\n" +
			"--color WHEN        auto(터미널일 때, NO_COLOR가 없을 때), always, never\n" +
			"--width N           출력 폭 (기본: 터미널 폭, 터미널이 아니면 100)\n" +
			"--config FILE       임계값 파일 (key = value 줄)\n" +
			"--set KEY=VALUE     임계값 하나 변경. 목록은 \"my help thresholds\"",
	},
	"help.scan": {
		EN: "--ledger FILE     keep the scan in the ledger FILE and add to it on every run\n" +
			"--root DIR        read a copy of a host's files below DIR instead of this host",
		KO: "--ledger FILE     스캔을 원장 FILE에 남기고 실행할 때마다 누적\n" +
			"--root DIR        이 호스트가 아닌, DIR 아래에 복사해 둔 호스트 파일을 읽음",
	},

	"help.overview.usage": {EN: "my [scan] [flags]", KO: "my [scan] [옵션]"},
	"help.overview.text":  {EN: "Scans this host and draws its slot map and the DIMMs ranked by risk. It reads the rasdaemon database, the kernel log, the EDAC counters and the SMBIOS tables. It changes nothing on the host. Sources it may not read as this user are named on the \"missing\" line with what root would add.", KO: "이 호스트를 스캔해 슬롯 맵과 위험 순위를 그립니다. rasdaemon DB, 커널 로그, EDAC 카운터, SMBIOS 테이블을 읽으며 호스트의 어떤 것도 바꾸지 않습니다. 현재 사용자로 읽지 못한 소스는 \"부족\" 줄에 나오고, root로 실행하면 무엇이 더해지는지도 함께 적습니다."},
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

	"help.open.usage": {EN: "my open FILE [dimm|why|map|timeline ...] [flags]", KO: "my open FILE [dimm|why|map|timeline ...] [옵션]"},
	"help.open.text":  {EN: "Shows a stored case the way the commands show this host: FILE is a case bundle (.mycase) or a ledger file. Without a command it draws the overview; with one, the arguments of that command follow. The file is not changed and the host is not scanned.", KO: "저장된 케이스를 이 호스트를 볼 때와 같은 화면으로 보여줍니다. FILE은 케이스 번들(.mycase)이나 원장 파일입니다. 명령을 생략하면 개요를 그리고, 명령을 쓰면 그 명령의 인자가 이어집니다. 파일을 바꾸지 않고 호스트를 스캔하지 않습니다."},

	"help.bundle.usage": {EN: "my bundle [slot|serial] -o FILE [--anonymize] [flags]", KO: "my bundle [슬롯|시리얼] -o FILE [--anonymize] [옵션]"},
	"help.bundle.text":  {EN: "Scans this host and writes what it found as one file, a case bundle: the ledger with the error records, DIMM identities, boots and actions, the findings of this run, a manifest of what was collected, and the source inventory. A bundle holds records about memory errors and never the contents of memory. Naming a DIMM makes it the subject of the case; the bundle still holds the whole host, because a finding about one DIMM rests on its neighbours too. The file is a ZIP archive: \"unzip -l\" lists it. The exit code is 0 when the bundle was written.", KO: "이 호스트를 스캔해 찾은 것을 파일 하나(케이스 번들)로 저장합니다. 오류 기록, DIMM 신원, 부팅과 조치 이력이 든 원장, 이번 실행의 소견, 수집 항목 목록, 소스 인벤토리가 들어갑니다. 번들에는 메모리 오류 기록만 들어가며 메모리 내용은 들어가지 않습니다. DIMM을 지정하면 그 DIMM이 케이스 대상이 됩니다. 그래도 번들에는 호스트 전체가 들어갑니다. DIMM 하나의 소견이 이웃 DIMM에도 기대기 때문입니다. 파일은 ZIP 형식이라 \"unzip -l\"로 목록을 볼 수 있습니다. 번들을 저장하면 종료 코드는 0입니다."},
	"help.bundle.flags": {
		EN: "-o FILE           the bundle to write (case.mycase)\n" +
			"--anonymize       replace host names, IP addresses and MAC addresses; DIMM serial numbers stay\n" +
			"--key-file FILE   with --anonymize: a key that makes every bundle replace a name alike\n" +
			"--force           replace FILE when it exists",
		KO: "-o FILE           저장할 번들 (case.mycase)\n" +
			"--anonymize       호스트 이름, IP 주소, MAC 주소를 바꿈. DIMM 시리얼은 그대로 둠\n" +
			"--key-file FILE   --anonymize와 함께: 같은 이름이 번들마다 같은 단어로 바뀌게 하는 키\n" +
			"--force           FILE이 있으면 덮어씀",
	},

	"help.import.usage": {EN: "my import <file|directory>... [-o FILE] [flags]", KO: "my import <파일|디렉터리>... [-o FILE] [옵션]"},
	"help.import.text":  {EN: "Builds a case from files, without the host they came from. Each input is recognised by its content: a rasdaemon database, a kernel log (dmesg, syslog, journal export), dmidecode output, a BMC event log, a Redfish dump, or a directory that is a copy of a host's files. An input that is not recognised stops the import. All inputs are taken to be one host. With -o the case is written as a bundle and the exit code is 0; without it the overview is drawn at once and the exit code is the grade of the host.", KO: "호스트 없이 파일만으로 케이스를 만듭니다. 입력은 내용으로 가려냅니다: rasdaemon DB, 커널 로그(dmesg, syslog, journal 내보내기), dmidecode 출력, BMC 이벤트 로그, Redfish 덤프, 호스트 파일 사본 디렉터리. 알아보지 못한 입력이 있으면 가져오기를 멈춥니다. 모든 입력을 한 호스트의 것으로 봅니다. -o를 주면 케이스를 번들로 저장하고 종료 코드는 0입니다. -o가 없으면 바로 개요를 그리고 종료 코드는 호스트 등급입니다."},
	"help.import.flags": {
		EN: "-o FILE             write the case as a bundle (case.mycase)\n" +
			"--host NAME         the host the files come from, when they do not say\n" +
			"--captured-at TIME  when a kernel log without dates was captured (2026-11-12T03:14:00Z)\n" +
			"--mtime             take the modification time of such a file as its capture time\n" +
			"--tz ZONE           the time zone of times that carry none (default UTC)\n" +
			"--ledger FILE       keep the import in the ledger FILE and add to it on every run\n" +
			"--anonymize         with -o: replace host names and network addresses\n" +
			"--key-file FILE     with --anonymize: a key that makes every bundle replace a name alike\n" +
			"--force             replace FILE when it exists\n" +
			"--all               in the overview, list every DIMM",
		KO: "-o FILE             케이스를 번들로 저장 (case.mycase)\n" +
			"--host NAME         파일에 호스트 이름이 없을 때 쓸 이름\n" +
			"--captured-at TIME  날짜 없는 커널 로그를 받은 시각 (2026-11-12T03:14:00Z)\n" +
			"--mtime             그런 파일의 수정 시각을 받은 시각으로 사용\n" +
			"--tz ZONE           시간대 표기가 없는 시각의 시간대 (기본 UTC)\n" +
			"--ledger FILE       가져온 것을 원장 FILE에 남기고 실행할 때마다 누적\n" +
			"--anonymize         -o와 함께: 호스트 이름과 네트워크 주소를 바꿈\n" +
			"--key-file FILE     --anonymize와 함께: 같은 이름이 번들마다 같은 단어로 바뀌게 하는 키\n" +
			"--force             FILE이 있으면 덮어씀\n" +
			"--all               개요에 모든 DIMM을 나열",
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
	"open":     {"guide.slotmap", "guide.ranking", "guide.grades"},
	"bundle":   nil,
	"import":   {"guide.slotmap", "guide.ranking", "guide.grades", "guide.obs"},
	"reading":  {"guide.slotmap", "guide.ranking", "guide.card", "guide.topology", "guide.map", "guide.timeline", "guide.grades", "guide.obs"},
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
	case "open":
		blocks = append(blocks, "help.common")
	case "import":
		// An import has no host to scan, and names its host itself.
		blocks = append(blocks, "help.output")
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
	if topic != "bundle" && topic != "import" {
		fmt.Fprintln(w)
		for _, line := range st.Wrap(say(lang, "help.exit"), 0) {
			fmt.Fprintln(w, line)
		}
	}
	if topic != "open" && topic != "overview" && topic != "bundle" && topic != "import" {
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
