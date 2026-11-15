package main

import (
	"context"
	"io"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// runInventory is "my inventory": the DIMMs of the host one per line, with
// the names every source has for a slot, and the corrected errors the
// operating system and the BMC each recorded, side by side. It reports and
// judges nothing: its exit code is 0.
func runInventory(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("inventory", src), src)
	all := o.fs.Bool("all", false, "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "inventory", src)
	}
	if len(pos) > 0 {
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[0]))
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	if code, ok := s.needHost(); !ok {
		return code
	}
	v, err := s.c.Inventory(ctx, s.h, view.InventoryOptions{All: *all})
	if err != nil {
		return o.failure(env, err)
	}
	return s.emitCode("inventory", "", cli.ExitOK, v, func(w io.Writer) error { return term.Inventory(w, v, s.st) })
}

// inventoryWords are the texts of "my inventory". They are added to the
// catalog of the commands.
var inventoryWords = i18n.Catalog{
	"help.inventory.usage": {EN: "my inventory [flags]", KO: "my inventory [옵션]"},
	"help.inventory.text": {
		EN: "Lists the DIMMs of this host, one per line: the slot, the maker, the part number, the size, ranks and device width, the speed in use and the rated speed, the memory RAS settings of the BIOS that act on the module, and the corrected errors the operating system and the BMC each have on record for it. " +
			"Below a module stand the names other sources have for its slot (the BMC, EDAC), its serial number, the BMC's own error count and the newest entries of the BMC logs about it. " +
			"With --bmc ADDRESS the BMC of the host is asked in the same run; without it the command asks nothing and shows what the host itself and the ledger or the opened case hold. " +
			"Below the table the two sides are set side by side over the period both cover. Where one side has corrected errors the other lacks, the command lists what the records show that can explain it: a BIOS setting that keeps corrected errors out of the BMC log, a log that is full or was read in part, firmware that handles errors without telling the operating system, an OS source that was not read. " +
			"These are candidates, and the command says so; it establishes no cause and changes no finding. The exit code is 0.",
		KO: "이 호스트의 DIMM을 한 줄에 하나씩 나열합니다. 슬롯, 제조사, 파트 넘버, 용량, rank 수와 디바이스 폭, 동작 속도와 정격 속도, 그 모듈에 적용되는 BIOS 메모리 RAS 설정, 운영체제와 BMC가 각각 기록한 CE 수입니다. " +
			"모듈 아래 줄에는 다른 소스(BMC, EDAC)가 그 슬롯을 부르는 이름, 시리얼 번호, BMC가 센 오류 카운트, BMC 로그의 최근 항목이 나옵니다. " +
			"--bmc ADDRESS를 주면 같은 실행에서 호스트의 BMC에 요청합니다. 없으면 아무것도 요청하지 않고 호스트와 원장, 또는 열어 둔 케이스에 있는 내용을 보여 줍니다. " +
			"표 아래에는 양쪽 기록이 겹치는 기간을 기준으로 운영체제와 BMC를 나란히 놓습니다. 한쪽에만 CE가 있으면 기록에서 보이는 원인 후보를 나열합니다. CE가 BMC 로그에 남지 않게 하는 BIOS 설정, 가득 찼거나 일부만 읽은 로그, 운영체제에 알리지 않고 오류를 처리하는 펌웨어, 읽지 못한 운영체제 소스입니다. " +
			"이것은 후보이며 명령도 그렇게 적습니다. 원인을 확정하지 않고 소견도 바꾸지 않습니다. 종료 코드는 0입니다.",
	},
	"help.inventory.flags": {EN: "--all             list the empty slots as well", KO: "--all             빈 슬롯도 나열"},
}

func init() {
	for code, m := range inventoryWords {
		words[code] = m
	}
}
