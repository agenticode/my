package main

import "github.com/agenticode/my/internal/i18n"

// collectWords are the texts of "my collect". They are added to the
// catalog of the commands.
var collectWords = i18n.Catalog{
	"err.collect_what":         {EN: "name what to collect: my collect redfish --bmc ADDRESS -o OUT, or my collect support DIR", KO: "수집할 대상을 지정해야 함: my collect redfish --bmc ADDRESS -o OUT 또는 my collect support DIR"},
	"err.collect_unknown":      {EN: "unknown source \"{what}\"; this version collects from redfish, and \"support\" prints the support table of collections", KO: "알 수 없는 대상 \"{what}\". 이 버전은 redfish에서 수집하고, \"support\"는 수집 결과의 지원 표를 출력"},
	"err.collect_bmc":          {EN: "name the BMC with --bmc ADDRESS, or a file of BMCs with --targets FILE", KO: "--bmc ADDRESS로 BMC를, 또는 --targets FILE로 BMC 목록 파일을 지정해야 함"},
	"err.collect_both":         {EN: "--bmc names one BMC and --targets a file of them: give one of the two", KO: "--bmc는 BMC 하나를, --targets는 목록 파일을 지정함: 둘 중 하나만 지정"},
	"err.collect_out":          {EN: "name where the answers go with -o: a new directory, or a case bundle such as case.mycase", KO: "응답을 저장할 곳을 -o로 지정해야 함: 새 디렉터리 또는 case.mycase 같은 케이스 번들"},
	"err.collect_dir":          {EN: "-o {dir}: {why}", KO: "-o {dir}: {why}"},
	"err.collect_not_empty":    {EN: "it is not empty; a collection goes into a new directory", KO: "비어 있지 않음. 수집 결과는 새 디렉터리에 저장함"},
	"err.collect_inputs":       {EN: "--with-inputs goes with -o NAME.mycase: the answers travel in a case bundle", KO: "--with-inputs는 -o NAME.mycase와 함께 써야 함: 응답은 케이스 번들에 들어감"},
	"err.collect_file":         {EN: "it is a file; a collection is a directory, and a case bundle has a name that ends in {ext}", KO: "파일임. 수집 결과는 디렉터리이고, 케이스 번들은 이름이 {ext}로 끝남"},
	"err.collect_unreadable":   {EN: "{why}", KO: "{why}"},
	"err.collect_parallel":     {EN: "--parallel {value}: want a number from 1 to {max}", KO: "--parallel {value}: 1에서 {max} 사이의 수여야 함"},
	"err.collect_entries":      {EN: "--max-log-entries {value}: want a number of entries, 1 or more; --no-logs reads no logs", KO: "--max-log-entries {value}: 1 이상의 항목 수여야 함. 로그를 읽지 않으려면 --no-logs"},
	"err.collect_time":         {EN: "{flag} {value}: want a time of at least 1s, such as 30s or 20m", KO: "{flag} {value}: 30s나 20m처럼 1s 이상의 시간이어야 함"},
	"err.collect_retries":      {EN: "--retries {value}: want a number from 0 to 20", KO: "--retries {value}: 0에서 20 사이의 수여야 함"},
	"err.collect_tls":          {EN: "--ca-file, --fingerprint and --insecure are three ways to treat the certificate of the BMC: give one", KO: "--ca-file, --fingerprint, --insecure는 BMC 인증서를 다루는 세 가지 방법임: 하나만 지정"},
	"err.collect_fingerprint":  {EN: "--fingerprint: want the SHA-256 fingerprint of the certificate, 64 hexadecimal digits with or without colons", KO: "--fingerprint: 인증서의 SHA-256 지문이어야 함. 16진수 64자리이며 콜론은 있어도 되고 없어도 됨"},
	"err.collect_http":         {EN: "{bmc}: plain http sends the password unencrypted; use https, or say --insecure to allow it", KO: "{bmc}: http는 암호를 암호화하지 않고 보냄. https를 쓰거나 --insecure로 허용해야 함"},
	"err.collect_same_name":    {EN: "{a} and {b} would both be written to the directory {dir}: give one of them another name with name=NAME", KO: "{a}와 {b}가 같은 디렉터리 {dir}에 저장됨: name=NAME으로 한쪽에 다른 이름을 지정"},
	"err.collect_session_user": {EN: "--session logs in with an account: name it with --user or MY_REDFISH_USER", KO: "--session은 계정으로 로그인함: --user 또는 MY_REDFISH_USER로 계정을 지정"},
	"err.collect_ca_pem":       {EN: "--ca-file {file}: no certificate in PEM form found", KO: "--ca-file {file}: PEM 형식의 인증서가 없음"},
	"err.collect_no_user":      {EN: "a password is given and no user: name the account with --user or MY_REDFISH_USER", KO: "암호는 있는데 사용자가 없음: --user 또는 MY_REDFISH_USER로 계정을 지정"},
	"err.collect_password":     {EN: "no password for user {user}: set MY_REDFISH_PASSWORD, name a file with --password-file, or run the command on a terminal to be asked", KO: "사용자 {user}의 암호가 없음: MY_REDFISH_PASSWORD를 설정하거나, --password-file로 파일을 지정하거나, 터미널에서 실행해 입력"},
	"err.collect_support":      {EN: "name the collections to compare: directories written by \"my collect redfish -o DIR\"", KO: "비교할 수집 결과를 지정해야 함: \"my collect redfish -o DIR\"로 만든 디렉터리"},
	"err.bmc_alone":            {EN: "{flag} says how a BMC is reached: name the BMC with --bmc ADDRESS", KO: "{flag}: BMC 접속 방법을 정하는 옵션이므로 --bmc ADDRESS로 BMC를 지정해야 함"},
	"err.bmc_fleet":            {EN: "--bmc asks the BMC of the one host that is scanned, and --fleet reads the store of many hosts: give one of the two. \"my collect redfish --targets FILE\" collects from several BMCs", KO: "--bmc는 스캔하는 호스트 한 대의 BMC에 요청하고 --fleet은 여러 호스트의 저장소를 읽음: 둘 중 하나만 지정. 여러 BMC는 \"my collect redfish --targets FILE\"로 수집"},
	"err.bmc_nothing":          {EN: "the BMC at {bmc} gave nothing about the system: see the lines above. The host was not scanned; without --bmc it is scanned alone", KO: "{bmc}의 BMC에서 시스템 정보를 받지 못함(위 줄 참고). 호스트는 스캔하지 않았으며, --bmc 없이 실행하면 호스트만 스캔"},
	"err.bmc_refused":          {EN: "the answers of the BMC at {bmc} were not added: {why}", KO: "{bmc}의 BMC 응답을 추가하지 않음: {why}"},

	"collect.prompt":       {EN: "Password for {user} on the BMC: ", KO: "BMC 사용자 {user}의 암호: "},
	"collect.loose_file":   {EN: "warning: other users of this machine can read the password file {file}", KO: "경고: 이 기기의 다른 사용자가 암호 파일 {file}을 읽을 수 있음"},
	"collect.insecure":     {EN: "warning: --insecure: the certificate of the BMC is not checked, so the answers and the password can be read or changed on the way", KO: "경고: --insecure: BMC 인증서를 확인하지 않으므로 응답과 암호가 전송 중에 노출되거나 바뀔 수 있음"},
	"collect.not_started":  {EN: "not started: the run was interrupted", KO: "시작하지 않음: 실행이 중단됨"},
	"collect.progress":     {EN: "{n} resources read", KO: "리소스 {n}개 읽음"},
	"collect.read":         {EN: "{n} {n|resource|resources} read in {took} ({requests} {requests|request|requests}, {size})", KO: "리소스 {n}개를 {took} 동안 읽음 (요청 {requests}건, {size})"},
	"collect.bmc_firmware": {EN: "BMC firmware {version}", KO: "BMC 펌웨어 {version}"},
	"collect.logs_left":    {EN: "logs left out: {logs}. They hold no hardware events; --log NAME reads one", KO: "읽지 않은 로그: {logs}. 하드웨어 이벤트가 없는 로그이며, --log NAME으로 읽을 수 있음"},
	"collect.stopped":      {EN: "stopped early: {why}", KO: "중간에 멈춤: {why}"},
	"collect.secrets":      {EN: "{n} {n|place|places} where an answer held the password or the session token of this run; it was taken out before the answer was written", KO: "응답에 이번 실행의 암호나 세션 토큰이 들어 있던 {n}곳을 지우고 저장"},
	"collect.failed":       {EN: "not collected: {why}", KO: "수집 실패: {why}"},
	"collect.nothing":      {EN: "no BMC answered: nothing was collected", KO: "응답한 BMC가 없어 수집한 것이 없음"},
	"collect.written":      {EN: "collection written: {dir}", KO: "수집 결과 저장: {dir}"},
	"collect.next_import":  {EN: "read it as a case: my import {dir}", KO: "케이스로 읽기: my import {dir}"},
	"collect.next_support": {EN: "which resources each BMC gave: my collect support {dir}", KO: "BMC별로 준 리소스 보기: my collect support {dir}"},
	"collect.case_hosts":   {EN: "{n} {n|host|hosts}: {hosts}", KO: "호스트 {n}개: {hosts}"},

	"collect.unknown_machine": {EN: "machine not named by the service", KO: "서비스가 기종을 밝히지 않음"},
	"collect.bmcs":            {EN: "{n} {n|BMC|BMCs} ({names})", KO: "BMC {n}대 ({names})"},
	"collect.more":            {EN: "and {n} more", KO: "외 {n}대"},
	"collect.legend":          {EN: "ok: every resource of the class came back. partial: some did. unsupported: the service answered 404, 405, 410 or 501. denied: 401 or 403. not offered: the service links no such resource, so it was not asked for. skipped: left out on purpose. not reached: the run ended before.", KO: "읽음: 그 종류의 리소스를 모두 받음. 일부: 일부만 받음. 미지원: 서비스가 404, 405, 410, 501로 응답. 거부: 401 또는 403. 제공 안 함: 서비스에 그 리소스로 가는 링크가 없어 요청하지 않음. 건너뜀: 일부러 읽지 않음. 도달 못 함: 그 전에 실행이 끝남."},

	"collect.class.service-root":       {EN: "service root", KO: "서비스 루트"},
	"collect.class.system":             {EN: "system", KO: "시스템"},
	"collect.class.memory":             {EN: "memory modules", KO: "메모리 모듈"},
	"collect.class.memory-metrics":     {EN: "memory metrics", KO: "메모리 메트릭"},
	"collect.class.bios":               {EN: "BIOS settings", KO: "BIOS 설정"},
	"collect.class.bios-settings":      {EN: "pending BIOS settings", KO: "대기 중인 BIOS 설정"},
	"collect.class.attribute-registry": {EN: "attribute registry", KO: "속성 레지스트리"},
	"collect.class.manager":            {EN: "manager", KO: "매니저"},
	"collect.class.log-service":        {EN: "log services", KO: "로그 서비스"},
	"collect.class.log-entries":        {EN: "log entries", KO: "로그 항목"},
	"collect.class.firmware-inventory": {EN: "firmware inventory", KO: "펌웨어 목록"},
	"collect.class.chassis":            {EN: "chassis", KO: "섀시"},
	"collect.class.message-registry":   {EN: "message registries", KO: "메시지 레지스트리"},

	"collect.state.ok":          {EN: "ok", KO: "읽음"},
	"collect.state.partial":     {EN: "partial", KO: "일부"},
	"collect.state.unsupported": {EN: "unsupported", KO: "미지원"},
	"collect.state.denied":      {EN: "denied", KO: "거부"},
	"collect.state.timeout":     {EN: "timeout", KO: "시간 초과"},
	"collect.state.error":       {EN: "error", KO: "오류"},
	"collect.state.skipped":     {EN: "skipped", KO: "건너뜀"},
	"collect.state.not offered": {EN: "not offered", KO: "제공 안 함"},
	"collect.state.not reached": {EN: "not reached", KO: "도달 못 함"},

	"help.collect.usage": {EN: "my collect redfish --bmc ADDRESS -o OUT [flags]", KO: "my collect redfish --bmc ADDRESS -o OUT [옵션]"},
	"help.collect.text": {
		EN: "Asks the BMC of a server over Redfish for what MemorY reads about its memory, and writes the answers to OUT: the memory modules and their error counts, the BIOS settings with the registry that explains them, the event logs, the firmware versions and the chassis. " +
			"It only reads. Every request is a GET, and nothing on the BMC is changed: no setting, no log, no action. With --session the command logs in to the session service and deletes its session at the end; these are the only two requests that are no GET. " +
			"OUT is a new directory, laid out the way the DMTF mockup tools lay out a service (one index.json per URI), with a manifest.json that lists every URI and what became of it. When the name of OUT ends in .mycase, a case bundle is made from the collection instead; its manifest lists the manifest.json of each collection with its size and SHA-256, and with --with-inputs every answer of the BMC travels in the bundle. " +
			"A BMC that lacks a resource, denies one or stops answering still yields a collection: what came back is kept, and the manifest says what did not. " +
			"The password comes from the environment variable MY_REDFISH_PASSWORD, from --password-file, or from the terminal. It is never given on the command line, and it is in no file the command writes. " +
			"The certificate of the BMC is checked. Most BMCs ship with a certificate they signed themselves: --fingerprint accepts exactly that certificate, --ca-file a private authority. " +
			"--targets collects from several BMCs, a few at a time, each into a directory of its own below OUT. " +
			"\"my collect support DIR...\" prints which BMC gave which class of resources, from the manifests of collections. " +
			"The exit code is 0 when everything that was asked for came back, 1 when a collection was written and lacks something that was denied, timed out or failed, and 70 when no BMC answered.",
		KO: "서버의 BMC에 Redfish로 MemorY가 메모리에 관해 읽는 항목을 요청하고 응답을 OUT에 저장합니다. 메모리 모듈과 오류 카운트, BIOS 설정과 그 설정을 설명하는 레지스트리, 이벤트 로그, 펌웨어 버전, 섀시입니다. " +
			"이 명령은 읽기만 합니다. 모든 요청이 GET이며 BMC의 설정, 로그, 액션 어느 것도 바꾸지 않습니다. --session을 주면 세션 서비스에 로그인하고 끝날 때 자기 세션을 삭제합니다. 이 두 요청에만 다른 메서드(POST와 DELETE)를 씁니다. " +
			"OUT은 새 디렉터리이고, DMTF 목업 도구와 같은 구조(URI마다 index.json 하나)로 저장하며, 모든 URI와 그 결과를 적은 manifest.json을 함께 둡니다. OUT의 이름이 .mycase로 끝나면 수집 결과로 케이스 번들을 만듭니다. 번들의 manifest에는 수집마다 manifest.json의 크기와 SHA-256이 적히고, --with-inputs를 주면 BMC의 응답 전부가 번들에 들어갑니다. " +
			"BMC에 없는 리소스, 거부된 리소스, 도중에 응답이 끊긴 경우에도 수집 결과는 남습니다. 받은 응답은 저장하고, 받지 못한 요청은 매니페스트에 적습니다. " +
			"암호는 환경 변수 MY_REDFISH_PASSWORD, --password-file, 또는 터미널 입력에서 받습니다. 명령줄 인자로는 받지 않으며 명령이 쓰는 어떤 파일에도 남지 않습니다. " +
			"BMC 인증서는 검증합니다. BMC 대부분은 자체 서명 인증서로 출하됩니다. --fingerprint는 그 인증서 하나만 받아들이고, --ca-file은 사설 인증 기관을 지정합니다. " +
			"--targets는 여러 BMC에서 몇 대씩 동시에 수집해 OUT 아래에 BMC마다 디렉터리 하나씩 저장합니다. " +
			"\"my collect support DIR...\"은 수집 결과의 매니페스트를 읽어 어느 BMC가 어떤 종류의 리소스를 주었는지 출력합니다. " +
			"요청한 것을 모두 받으면 종료 코드는 0, 수집 결과는 저장했으나 거부나 시간 초과나 실패로 빠진 것이 있으면 1, 응답한 BMC가 없으면 70입니다.",
	},
	"help.collect.flags": {
		EN: "--bmc ADDRESS         the BMC to ask: https://10.0.0.5, a host name, or host:port\n" +
			"--targets FILE        several BMCs, one per line: ADDRESS [name=NAME] [user=NAME]\n" +
			"                      [password-file=FILE]; lines that start with # are comments\n" +
			"-o OUT                a new directory for the collection, or a case bundle when\n" +
			"                      the name ends in .mycase\n" +
			"--user NAME           the account to read with (or MY_REDFISH_USER)\n" +
			"--password-file FILE  read the password from the first line of FILE\n" +
			"--session             log in to the session service; the session is deleted at the end\n" +
			"--ca-file FILE        PEM file with the authority that signed the BMC's certificate\n" +
			"--fingerprint SHA256  accept exactly the certificate with this SHA-256 fingerprint\n" +
			"--insecure            accept any certificate, and allow plain http\n" +
			"--timeout TIME        time for one request (default 30s)\n" +
			"--deadline TIME       time for one BMC (default 20m)\n" +
			"--retries N           how often a request is sent again after a timeout or an\n" +
			"                      overload answer (default 3)\n" +
			"--max-log-entries N   entries to read of one log (default 5000)\n" +
			"--log NAME            also read this log, named by the Id of its log service, which\n" +
			"                      is skipped by default (Journal, AuditLog); may be repeated\n" +
			"--no-logs             read no logs\n" +
			"--parallel N          with --targets: BMCs asked at the same time (default 4)\n" +
			"--force               replace OUT when it is a case bundle that exists\n" +
			"--with-inputs         with -o NAME.mycase: put every answer of the BMC into the bundle;\n" +
			"                      the answers can hold account names and addresses\n" +
			"--quiet               print no progress\n" +
			"--event-map FILE      with -o NAME.mycase: a table of firmware messages of one's own,\n" +
			"                      asked before the shipped tables; may be repeated\n" +
			"--lang en|ko          language of the output (default: from the locale, English otherwise)\n" +
			"--json                print the result as JSON\n" +
			"--width N             columns to fill (default: the terminal, 100 otherwise)",
		KO: "--bmc ADDRESS         요청할 BMC: https://10.0.0.5, 호스트 이름, 또는 host:port\n" +
			"--targets FILE        여러 BMC를 한 줄에 하나씩: ADDRESS [name=NAME] [user=NAME]\n" +
			"                      [password-file=FILE]. #로 시작하는 줄은 주석\n" +
			"-o OUT                수집 결과를 저장할 새 디렉터리. 이름이 .mycase로 끝나면\n" +
			"                      케이스 번들\n" +
			"--user NAME           읽는 데 쓸 계정 (또는 MY_REDFISH_USER)\n" +
			"--password-file FILE  FILE의 첫 줄에서 암호를 읽음\n" +
			"--session             세션 서비스에 로그인. 세션은 끝날 때 삭제\n" +
			"--ca-file FILE        BMC 인증서를 서명한 인증 기관의 PEM 파일\n" +
			"--fingerprint SHA256  이 SHA-256 지문의 인증서 하나만 받아들임\n" +
			"--insecure            어떤 인증서든 받아들이고 http도 허용\n" +
			"--timeout TIME        요청 하나의 제한 시간 (기본 30s)\n" +
			"--deadline TIME       BMC 한 대의 제한 시간 (기본 20m)\n" +
			"--retries N           시간 초과나 과부하 응답 뒤에 다시 보내는 횟수 (기본 3)\n" +
			"--max-log-entries N   로그 하나에서 읽을 항목 수 (기본 5000)\n" +
			"--log NAME            기본으로 건너뛰는 로그(Journal, AuditLog)도 읽음. 로그 서비스의\n" +
			"                      Id로 지정하며 여러 번 쓸 수 있음\n" +
			"--no-logs             로그를 읽지 않음\n" +
			"--parallel N          --targets와 함께: 동시에 요청할 BMC 수 (기본 4)\n" +
			"--force               OUT이 이미 있는 케이스 번들이면 덮어씀\n" +
			"--with-inputs         -o NAME.mycase와 함께: BMC의 응답 전부를 번들에 넣음. 응답에는\n" +
			"                      계정 이름과 주소가 들어 있을 수 있음\n" +
			"--quiet               진행 상황을 출력하지 않음\n" +
			"--event-map FILE      -o NAME.mycase와 함께: 내장 표보다 먼저 볼, 직접 만든\n" +
			"                      펌웨어 메시지 표. 여러 번 줄 수 있음\n" +
			"--lang en|ko          출력 언어 (기본: 로캘, 한국어가 아니면 영어)\n" +
			"--json                결과를 JSON으로 출력\n" +
			"--width N             출력 폭 (기본: 터미널 폭, 터미널이 아니면 100)",
	},
}

func init() {
	for code, m := range collectWords {
		words[code] = m
	}
}
