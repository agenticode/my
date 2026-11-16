package main

import (
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/preflight"
)

// harvestWords are the texts of "my harvest". They are added to the
// catalog of the commands.
var harvestWords = i18n.Catalog{
	"err.harvest_what":      {EN: "unknown subcommand \"{what}\": my harvest [install|uninstall|status]", KO: "알 수 없는 하위 명령 \"{what}\": my harvest [install|uninstall|status]"},
	"err.harvest_deadline":  {EN: "--deadline {value}: want a time such as 10m", KO: "--deadline {value}: 10m 같은 시간이어야 함"},
	"err.harvest_not_linux": {EN: "this is not a Linux host ({os}): the harvest reads what the Linux kernel keeps over a reset", KO: "이 시스템은 Linux 호스트가 아님({os}). harvest는 Linux 커널이 reset 동안 남기는 것을 읽음"},

	"harvest.busy":      {EN: "another harvest has {ledger} open; this one ends", KO: "다른 harvest가 {ledger}를 쓰고 있어 이번 실행은 끝냄"},
	"harvest.cannot":    {EN: "the ledger {ledger} cannot be written, and it stays as it was: {err}", KO: "원장 {ledger}에 쓸 수 없어 원장은 그대로 둠: {err}"},
	"harvest.low_space": {EN: "{mib} MiB free in {dir}: only what the boot before left is read", KO: "{dir}의 여유 공간이 {mib} MiB임. 이전 부팅이 남긴 것만 읽음"},
	"harvest.phase_a":   {EN: "phase A: {sources} {sources|source|sources} of what the boot before left, {records} {records|record|records} added, on disk after {seconds} s", KO: "A 단계: 이전 부팅이 남긴 소스 {sources}개, 기록 {records}건 추가, {seconds}초 만에 디스크에 기록"},
	"harvest.phase_b":   {EN: "phase B: {sources} more {sources|source|sources}, {records} {records|record|records} added, done after {seconds} s with the ledger passes", KO: "B 단계: 소스 {sources}개 더, 기록 {records}건 추가, 원장 정리까지 {seconds}초"},
	"harvest.deadline":  {EN: "{n} {n|source was|sources were} not read: the run reached its deadline of {deadline}", KO: "소스 {n}개는 읽지 못함: 실행 제한 시간 {deadline}에 도달"},
	"harvest.nothing":   {EN: "nothing new since the last run: no scan entry is kept", KO: "직전 실행 이후 새것 없음: 스캔 항목을 남기지 않음"},
	"harvest.done":      {EN: "{boots} {boots|boot|boots} on record in {ledger} ({kib} KiB), {seconds} s", KO: "{ledger}에 부팅 {boots}개 기록됨 ({kib} KiB), {seconds}초"},

	"harvest.not_root":       {EN: "install and uninstall write below /etc/systemd/system and run systemctl: run them as root (--dry-run shows what they do)", KO: "install과 uninstall은 /etc/systemd/system 아래에 쓰고 systemctl을 실행함: root로 실행해야 함(--dry-run은 할 일만 보여 줌)"},
	"harvest.no_systemd":     {EN: "systemd is not the init system of this host (no /run/systemd/system): run my harvest from the boot scripts of the host", KO: "이 호스트의 init 시스템이 systemd가 아님(/run/systemd/system 없음): 호스트의 부팅 스크립트에서 my harvest를 실행해야 함"},
	"harvest.bad_binary":     {EN: "{binary} is {why}: a unit that runs it as root at every boot needs a binary only root can replace. Copy it: install -m 0755 -o root {binary} /usr/local/bin/my", KO: "{binary} 파일이 {why}. 부팅마다 root로 실행하는 유닛에는 root만 바꿀 수 있는 바이너리가 필요함. 복사: install -m 0755 -o root {binary} /usr/local/bin/my"},
	"harvest.foreign":        {EN: "{file} is there and was not written by my harvest install: it is left alone and nothing is installed", KO: "{file} 파일이 있으나 my harvest install이 쓴 것이 아님. 그대로 두고 아무것도 설치하지 않음"},
	"harvest.left_alone":     {EN: "{file} was not written by my harvest install: left alone", KO: "{file} 파일은 my harvest install이 쓴 것이 아니어서 그대로 둠"},
	"harvest.writes":         {EN: "writes", KO: "쓰기"},
	"harvest.removes":        {EN: "removes", KO: "삭제"},
	"harvest.runs":           {EN: "runs", KO: "실행"},
	"harvest.dry_run":        {EN: "--dry-run: nothing was done", KO: "--dry-run: 아무것도 하지 않음"},
	"harvest.installed":      {EN: "installed: the next boot reads what the boot before left into {ledger}", KO: "설치함: 다음 부팅부터 이전 부팅이 남긴 것을 {ledger}에 읽어 넣음"},
	"harvest.uninstalled":    {EN: "uninstalled; the ledger stays: {ledger}", KO: "제거함. 원장은 남음: {ledger}"},
	"harvest.none_installed": {EN: "no unit of my harvest install is there; the ledger, if any, stays: {ledger}", KO: "my harvest install이 쓴 유닛이 없음. 원장이 있다면 남음: {ledger}"},
	"harvest.state.absent":   {EN: "not installed", KO: "설치 안 됨"},
	"harvest.state.foreign":  {EN: "a file my harvest install did not write", KO: "my harvest install이 쓰지 않은 파일"},
	"harvest.state.disabled": {EN: "installed, not enabled", KO: "설치됨, 활성화 안 됨"},
	"harvest.state.enabled":  {EN: "installed and enabled", KO: "설치되고 활성화됨"},
	"harvest.ledger":         {EN: "ledger", KO: "원장"},
	"harvest.no_ledger":      {EN: "{ledger} is not there yet", KO: "{ledger} 파일은 아직 없음"},
	"harvest.ledger_is":      {EN: "{ledger}, {kib} KiB, {boots} {boots|boot|boots} of {hosts} {hosts|host|hosts}", KO: "{ledger}, {kib} KiB, 호스트 {hosts}개의 부팅 {boots}개"},
	"harvest.last":           {EN: "last run", KO: "마지막 실행"},
	"harvest.never":          {EN: "none on record", KO: "기록 없음"},
	"case.cut":               {EN: "a run that started {time} did not finish: the ledger passes did not run after it, so an error may be counted twice until a run completes", KO: "{time}에 시작한 실행이 끝나지 않음: 그 뒤 원장 정리가 돌지 않아 실행 하나가 끝날 때까지 오류가 두 번 셀 수 있음"},
	"hint.kept":              {EN: "the boots this host kept", KO: "이 호스트가 보관한 부팅"},

	"help.harvest.usage": {EN: "my harvest [install|uninstall|status] [flags]", KO: "my harvest [install|uninstall|status] [옵션]"},
	"help.harvest.text": {
		EN: "Reads what the boot before left into a kept ledger, at every boot. A fatal memory error resets the host before the journal or rasdaemon store anything, and on a hard fault every boot ends the same way. What survives a reset is read first (phase A): the pstore files and their archive, the boot error region of the firmware, the text logs of kdump, and the ring buffer of this boot, which holds the records the kernel found in the machine check banks. Then the ledger is written to disk, so that a reset seconds later cannot take it, and the rest of the host is read (phase B): the journal from the boot of the last run on, rasdaemon, the counters, SMBIOS. One line per phase goes to standard error, which the journal keeps. " +
			"The ledger is " + preflight.DefaultLedgerDoc + " unless --ledger names another. One run at a time holds it; a second run ends at once. A run that added nothing leaves no scan entry behind. Below 32 MiB of free space only phase A is read. --deadline bounds the reading; the sources not read by then are named, and the run ends with exit code 0. A ledger that cannot be written ends the run with exit code 70 and stays as it was. " +
			"\"my harvest install\" writes the unit my-harvest.service, and with --timer my-harvest.timer, to /etc/systemd/system, and runs systemctl daemon-reload and systemctl enable for them; --now starts the service as well, and --dry-run prints the paths of the files and the commands and does nothing. It is the one command of MemorY that changes the host, and it needs root. A unit file of that name that install did not write is left alone. \"my harvest uninstall\" disables the units and removes the files; the ledger stays. \"my harvest status\" says whether the units are installed and enabled, what the ledger holds, and when the last run was. The units are in contrib/systemd for those who install them by hand. " +
			"\"my open " + preflight.DefaultLedgerDoc + "\" shows the kept ledger with every command.",
		KO: "부팅할 때마다 이전 부팅이 남긴 것을 보관 원장에 읽어 넣습니다. 치명적 메모리 오류는 저널이나 rasdaemon이 무엇이든 저장하기 전에 호스트를 reset시키고, 고장이 굳은 경우에는 부팅마다 같은 일이 되풀이됩니다. reset을 견딘 것을 먼저 읽습니다(A 단계): pstore 파일과 그 보관본, 펌웨어의 부팅 오류 영역, kdump 텍스트 로그, 그리고 커널이 머신 체크 뱅크에서 찾은 기록이 든 이번 부팅의 링 버퍼입니다. 그다음 원장을 디스크에 기록해 몇 초 뒤의 reset에도 남게 하고, 호스트의 나머지를 읽습니다(B 단계): 지난 실행의 부팅부터의 저널, rasdaemon, 카운터, SMBIOS입니다. 단계마다 한 줄을 표준 오류로 내보내며 저널이 이를 보관합니다. " +
			"원장은 --ledger로 다른 파일을 지정하지 않으면 " + preflight.DefaultLedgerDoc + "입니다. 한 번에 한 실행만 원장을 잡고, 두 번째 실행은 바로 끝납니다. 새로 더한 것이 없는 실행은 스캔 항목을 남기지 않습니다. 여유 공간이 32 MiB 미만이면 A 단계만 읽습니다. --deadline은 읽는 시간을 제한하며, 그때까지 읽지 못한 소스를 적고 종료 코드 0으로 끝납니다. 원장에 쓸 수 없으면 종료 코드 70으로 끝나고 원장은 그대로 남습니다. " +
			"\"my harvest install\"은 my-harvest.service 유닛과, --timer를 주면 my-harvest.timer를 /etc/systemd/system에 쓰고 systemctl daemon-reload와 systemctl enable을 실행합니다. --now는 서비스도 시작하고, --dry-run은 파일 경로와 명령을 출력만 하고 아무것도 하지 않습니다. MemorY에서 호스트를 바꾸는 유일한 명령이며 root가 필요합니다. install이 쓰지 않은 같은 이름의 유닛 파일은 그대로 둡니다. \"my harvest uninstall\"은 유닛을 비활성화하고 파일을 지웁니다. 원장은 남습니다. \"my harvest status\"는 유닛의 설치와 활성화 여부, 원장 내용, 마지막 실행 시각을 알려 줍니다. 손으로 설치할 사람을 위해 유닛 파일은 contrib/systemd에도 있습니다. " +
			"\"my open " + preflight.DefaultLedgerDoc + "\"로 보관 원장을 모든 명령으로 볼 수 있습니다.",
	},
	"help.harvest.flags": {
		EN: "--ledger FILE         the kept ledger (default " + preflight.DefaultLedgerDoc + ")\n" +
			"--deadline TIME       time for reading the sources (default 10m)\n" +
			"--timer               install: also my-harvest.timer, a run every hour of a long boot\n" +
			"--now                 install: start the service at once\n" +
			"--dry-run             install, uninstall: print the paths of the files and the commands, do nothing\n" +
			"--lang en|ko          language of the output (default: from the locale, English otherwise)",
		KO: "--ledger FILE         보관 원장 (기본 " + preflight.DefaultLedgerDoc + ")\n" +
			"--deadline TIME       소스를 읽는 시간 (기본 10m)\n" +
			"--timer               install: my-harvest.timer도 설치. 긴 부팅 중 한 시간마다 실행\n" +
			"--now                 install: 서비스를 바로 시작\n" +
			"--dry-run             install, uninstall: 파일 경로와 명령을 출력만 하고 아무것도 안 함\n" +
			"--lang en|ko          출력 언어 (기본: 로캘, 그 밖에는 영어)",
	},
}

func init() {
	for k, v := range harvestWords {
		words[k] = v
	}
}
