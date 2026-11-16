package main

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/preflight"
	"github.com/agenticode/my/internal/term"
)

// exitLost is the exit code of "my preflight" when a check would lose
// records. It is no grade.
const exitLost = 1

// runPreflight is "my preflight": what a reset right after a fatal memory
// error would leave on this host, and the settings that change that. It
// only reads; --script prints the fixes as a shell script, which my never
// runs.
func runPreflight(_ context.Context, env *cli.Env, args []string) int {
	o := &options{name: "my preflight", fs: flag.NewFlagSet("my preflight", flag.ContinueOnError), color: term.ColorAuto}
	o.fs.SetOutput(io.Discard)
	o.fs.StringVar(&o.lang, "lang", "", "")
	o.fs.BoolVar(&o.asJSON, "json", false, "")
	o.fs.BoolVar(&o.ascii, "ascii", false, "")
	o.fs.StringVar(&o.color, "color", term.ColorAuto, "")
	o.fs.IntVar(&o.width, "width", 0, "")
	o.fs.StringVar(&o.root, "root", "", "")
	o.fs.BoolVar(&o.help, "help", false, "")
	o.fs.BoolVar(&o.help, "h", false, "")
	script := o.fs.Bool("script", false, "")
	ledger := o.fs.String("ledger", preflight.DefaultLedger, "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "preflight", nil)
	}
	if len(pos) > 0 {
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[0]))
	}
	if *script && o.asJSON {
		return o.usageError(env, lang, "%s", say(lang, "err.preflight_script_json"))
	}
	opts := preflight.Options{Root: "/", Live: true, Ledger: *ledger}
	if o.root != "" && filepath.Clean(o.root) != "/" {
		if fi, err := os.Stat(o.root); err != nil || !fi.IsDir() {
			return o.usageError(env, lang, "--root %s: %s", o.root, say(lang, "err.not_dir"))
		}
		opts.Root, opts.Live = o.root, false
	} else if hostOS != "linux" {
		return o.failure(env, errorText(say(lang, "err.preflight_not_linux", "os", hostOS)))
	}
	if preflightProbe != nil {
		preflightProbe(&opts)
	}
	rep := preflight.Run(opts)
	exit := cli.ExitOK
	if rep.Lost > 0 {
		exit = exitLost
	}
	var err error
	switch {
	case *script:
		err = preflight.Script(env.Stdout, rep, lang, clock(env))
	case o.asJSON:
		e := newEnvelope("preflight", lang, rep.Host, "", rep)
		e.ExitCode = exit
		err = writeJSON(env.Stdout, e)
	default:
		err = term.Preflight(env.Stdout, rep, o.style(env, lang))
	}
	if err != nil {
		return o.failure(env, err)
	}
	return exit
}

// preflightProbe lets tests replace what "my preflight" asks the running
// system: the process list and the free space of a file system.
var preflightProbe func(*preflight.Options)

type errorText string

func (e errorText) Error() string { return string(e) }

// preflightWords are the texts of "my preflight".
var preflightWords = i18n.Catalog{
	"err.preflight_script_json": {EN: "--script and --json do not go together", KO: "--script와 --json은 함께 쓸 수 없음"},
	"err.preflight_not_linux":   {EN: "this is not a Linux host ({os}): the checks read what the Linux kernel and systemd keep. A copy of a host's files is read with --root DIR", KO: "이 시스템은 Linux 호스트가 아님({os}). 점검은 Linux 커널과 systemd가 남긴 것을 읽음. 호스트 파일 사본은 --root DIR로 읽음"},

	"help.preflight.usage": {EN: "my preflight [--script] [flags]", KO: "my preflight [--script] [옵션]"},
	"help.preflight.text": {
		EN: "Says what a reset right after a fatal memory error would leave on this host. A fatal uncorrected error panics the kernel: user space stops, so the journal and rasdaemon write nothing more, and on x86 the kernel restarts the host by itself 30 seconds later. What survives depends on settings, and each check reads one of them: whether the journal is on disk, whether a persistent store (pstore) takes the panic text and systemd-pstore empties it, whether a crash kernel would run first, whether the next boot logs what the machine check banks hold and prints it, how the host restarts, whether the firmware has a boot error region (BERT) and an error record store (ERST), whether a console leaves the host, whether rasdaemon records, and whether my harvest runs at every boot. " +
			"Each check is kept, lost, none (the platform does not offer it), unknown (not readable as this user, or not in a copy) or info. " +
			"The command only reads. It changes no kernel parameter, boot loader file, sysfs file or configuration. --script prints the fixes of the lost checks as a shell script, with what each costs, the public document that says so, and how to undo it; my never runs it. " +
			"--root reads a copy of a host's files; checks that read the running host are marked there. " +
			"The exit code is 0 when no check is lost and 1 when one is; it is no grade.",
		KO: "치명적 메모리 오류 직후 reset이 일어나면 이 호스트에 무엇이 남는지 알려 줍니다. 치명적 UE는 커널 panic을 일으킵니다. 사용자 공간이 멈추므로 저널과 rasdaemon은 더 쓰지 못하고, x86에서는 커널이 30초 뒤 스스로 호스트를 재시작합니다. 무엇이 남는지는 설정에 달려 있고, 점검마다 설정 하나를 읽습니다. 저널이 디스크에 있는지, 영구 저장소(pstore)가 panic 내용을 받고 systemd-pstore가 그것을 비우는지, crash 커널이 먼저 실행되는지, 다음 부팅이 머신 체크 뱅크에 남은 기록을 남기고 출력하는지, 호스트가 어떻게 재시작하는지, 펌웨어에 부팅 오류 영역(BERT)과 오류 기록 저장소(ERST)가 있는지, 호스트 밖으로 나가는 콘솔이 있는지, rasdaemon이 기록하는지, my harvest가 부팅마다 실행되는지입니다. " +
			"점검 결과는 남음, 잃음, 없음(플랫폼이 제공하지 않음), 모름(이 사용자로 읽을 수 없거나 사본에 없음), 참고 중 하나입니다. " +
			"이 명령은 읽기만 합니다. 커널 파라미터, 부트 로더 파일, sysfs 파일, 설정 어느 것도 바꾸지 않습니다. --script는 잃음인 점검의 해결책을 셸 스크립트로 출력하며, 각 해결책의 비용과 그 근거가 되는 공개 문서, 되돌리는 방법을 적습니다. my는 이 스크립트를 실행하지 않습니다. " +
			"--root는 호스트 파일 사본을 읽습니다. 실행 중인 호스트를 읽는 점검에는 표시를 붙입니다. " +
			"잃음인 점검이 없으면 종료 코드는 0, 하나라도 있으면 1입니다. 이 종료 코드는 등급이 아닙니다.",
	},
	"help.preflight.flags": {
		EN: "--script              print the fixes of the lost checks as a shell script; my runs none of it\n" +
			"--root DIR            read a copy of a host's files below DIR\n" +
			"--ledger FILE         the ledger my harvest keeps (default /var/lib/my/ledger.db)\n" +
			"--lang en|ko          language of the output (default: from the locale, English otherwise)\n" +
			"--json                print the checks as JSON\n" +
			"--ascii               draw with ASCII characters only\n" +
			"--color WHEN          auto (on a terminal, unless NO_COLOR is set), always or never\n" +
			"--width N             columns to fill (default: the terminal, 100 otherwise)",
		KO: "--script              잃음인 점검의 해결책을 셸 스크립트로 출력. my는 실행하지 않음\n" +
			"--root DIR            DIR 아래의 호스트 파일 사본을 읽음\n" +
			"--ledger FILE         my harvest가 보관하는 원장 (기본 /var/lib/my/ledger.db)\n" +
			"--lang en|ko          출력 언어 (기본: 로캘, 그 밖에는 영어)\n" +
			"--json                점검 결과를 JSON으로 출력\n" +
			"--ascii               ASCII 문자로만 그림\n" +
			"--color WHEN          auto(터미널일 때, NO_COLOR가 없을 때), always, never\n" +
			"--width N             채울 칸 수 (기본: 터미널, 그 밖에는 100)",
	},
}

func init() {
	for k, v := range preflightWords {
		words[k] = v
	}
}
