package main

import (
	"context"
	"io"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/memras"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// runBIOS is "my bios": the BIOS settings of the host that bear on memory
// errors, checked against a settings profile. The settings come from a
// BMC; the command reads what the ledger holds of them. Its exit code is
// 0 when every expectation of the profile is met and 1 when one is not;
// it is no grade.
func runBIOS(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("bios", src), src)
	profilePath := o.fs.String("profile", "", "")
	printProfile := o.fs.Bool("print-profile", false, "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "bios", src)
	}
	if len(pos) > 0 {
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[0]))
	}
	if *printProfile {
		// The built-in profile as it is written, to start one's own from.
		if _, err := env.Stdout.Write(memras.BuiltinText()); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	var profile *memras.Profile
	if *profilePath != "" {
		var err error
		if profile, err = memras.ReadFile(*profilePath); err != nil {
			return o.usageError(env, lang, "--profile %s", errText(lang, err))
		}
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	if code, ok := s.needHost(); !ok {
		return code
	}
	v, err := s.c.BIOS(ctx, s.h, view.BIOSOptions{Profile: profile})
	if err != nil {
		return o.failure(env, err)
	}
	exit := cli.ExitOK
	if v.Mismatches > 0 {
		exit = exitDiffers
	}
	return s.emitCode("bios", "", exit, v, func(w io.Writer) error { return term.BIOS(w, v, s.st) })
}

// biosWords are the texts of "my bios". They are added to the catalog of
// the commands.
var biosWords = i18n.Catalog{
	"help.bios.usage": {EN: "my bios [--profile FILE] [flags]", KO: "my bios [--profile FILE] [옵션]"},
	"help.bios.text": {
		EN: "Checks the BIOS settings of this host that bear on memory errors against a settings profile: patrol scrub, ADDDC and the memory mode, the logging of corrected errors, post package repair, the memory frequency and the refresh rate. " +
			"The settings are the ones a BMC reports over Redfish. \"my bios --bmc ADDRESS\" asks the BMC of this host for them in the same run; without --bmc the command asks nothing and reads what the ledger or the opened case holds. \"my collect redfish\" reads them into a collection, and a case made from it carries them. " +
			"Every server vendor names these settings differently. The profile says which BIOS attribute of which vendor is which setting and what each value means, and names the public document each mapping was read from. A setting no public document gives an attribute for is reported as not mapped. " +
			"For each setting the command prints the value in effect, a pending value that takes effect after a reboot, and whether it is what the profile expects. When the attribute registry of this BIOS allows no value with the expected meaning, the expectation does not fit this BIOS version; that is said and is no mismatch. " +
			"ADDDC that is on with x8 DIMMs is reported as on and without effect on them. DIMMs that run below their rated speed are listed with what the records show that can be the reason. " +
			"The built-in profile expects patrol scrub and the logging of corrected errors to be on, and reports the other settings without judging them. --profile checks against a profile of one's own. " +
			"The command changes no setting. The exit code is 0 when every expectation is met, also when no settings are on record, and 1 when a setting is not as expected; it is no grade.",
		KO: "이 호스트의 BIOS 설정 중 메모리 오류에 영향을 주는 것을 설정 프로필과 대조합니다. 패트롤 스크럽, ADDDC와 메모리 모드, CE 로깅, PPR, 메모리 주파수, 리프레시 속도입니다. " +
			"설정은 BMC가 Redfish로 알려 주는 값입니다. \"my bios --bmc ADDRESS\"는 같은 실행에서 이 호스트의 BMC에 설정을 요청합니다. --bmc가 없으면 아무것도 요청하지 않고 원장이나 열어 둔 케이스에 있는 값을 읽습니다. \"my collect redfish\"는 설정을 수집 결과로 읽어 오고, 그 수집 결과로 만든 케이스에 설정이 들어갑니다. " +
			"서버 벤더마다 이 설정의 이름이 다릅니다. 프로필은 어느 벤더의 어느 BIOS 속성이 어떤 설정이고 값마다 무슨 뜻인지를 적고, 매핑마다 근거가 된 공개 문서를 밝힙니다. 공개 문서에 속성이 없는 설정은 매핑 없음으로 보고합니다. " +
			"설정마다 현재 값, 재부팅 뒤에 적용될 대기 값, 프로필의 기대와 맞는지를 출력합니다. 이 BIOS의 속성 레지스트리에 기대한 뜻의 값이 없으면 기대값이 이 BIOS 버전에 맞지 않는 것으로 적으며 불일치로 세지 않습니다. " +
			"x8 DIMM이 있는데 ADDDC가 켜져 있으면 켜져 있으나 그 DIMM에는 효과가 없다고 적습니다. 정격 속도에 못 미치게 동작하는 DIMM은 기록에서 보이는 원인 후보와 함께 나열합니다. " +
			"내장 프로필은 패트롤 스크럽과 CE 로깅이 켜져 있기를 기대하고, 나머지 설정은 평가 없이 값만 보고합니다. --profile로 직접 만든 프로필과 대조할 수 있습니다. " +
			"이 명령은 어떤 설정도 바꾸지 않습니다. 모든 기대가 충족되면 종료 코드는 0이며 설정 기록이 없을 때도 0입니다. 기대와 다른 설정이 있으면 1입니다. 이 종료 코드는 등급이 아닙니다.",
	},
	"help.bios.flags": {
		EN: "--profile FILE    check against the settings profile in FILE\n" +
			"--print-profile   print the built-in profile, to start one's own from, and exit",
		KO: "--profile FILE    FILE의 설정 프로필과 대조\n" +
			"--print-profile   내장 프로필을 출력하고 종료. 직접 만들 프로필의 출발점",
	},
}

func init() {
	for code, m := range biosWords {
		words[code] = m
	}
}
