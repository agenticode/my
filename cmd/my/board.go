package main

import (
	"context"
	"io"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/term"
)

// runBoard is "my board": the board of this host or of an opened case, as
// the Memory Explorer draws it. It exits with the grade of the host.
func runBoard(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("board", src), src).withDetail()
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "board", src)
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
	b, err := s.c.Board(ctx, s.h)
	if err != nil {
		return o.failure(env, err)
	}
	return s.emit("board", s.h.Grade, b, func(w io.Writer) error { return term.Board(w, b, s.st) })
}

// boardWords are the texts of "my board".
var boardWords = i18n.Catalog{
	"help.board.usage": {EN: "my board [flags]", KO: "my board [옵션]"},
	"help.board.text": {
		EN: "Draws where each DIMM of this host sits: per CPU a strip of slots in the arrangement of the Memory Explorer, the channel numbers above, the slot names below, each slot in the shade of the finding of its DIMM and a mark for the errors on record. " +
			"The places are read from the slot names the SMBIOS tables give (locator and bank locator) each time the board is drawn; a slot whose names give no place on a CPU is set aside and drawn by its name below. The board ties no record to a slot and changes no finding. " +
			"With --detail the screen adds the size and caches of each CPU, a table of the modules and the lines that say how many slots were placed and why the others were not. The exit code is the grade of the host, as for my.",
		KO: "이 호스트의 DIMM이 어디에 꽂혀 있는지 그립니다. CPU마다 메모리 익스플로러의 배치로 슬롯 띠를 그리고, 위에 채널 번호, 아래에 슬롯 이름을 두며, 슬롯은 그 DIMM 소견의 음영으로, 기록된 오류는 표식으로 보입니다. " +
			"위치는 그릴 때마다 SMBIOS 테이블의 슬롯 이름(로케이터와 뱅크 로케이터)에서 읽습니다. 이름으로 CPU 위 위치를 알 수 없는 슬롯은 따로 두고 아래에 이름으로 그립니다. 보드는 기록을 슬롯에 잇지 않고 소견을 바꾸지 않습니다. " +
			"--detail을 주면 CPU마다 크기와 캐시, 모듈 표, 그리고 놓은 슬롯 수와 나머지를 놓지 못한 이유를 덧붙입니다. 종료 코드는 my와 같이 호스트 소견 등급입니다.",
	},
	"help.board.flags": {EN: "--detail          add the CPUs, a table of the modules and how the slots were placed", KO: "--detail          CPU, 모듈 표, 슬롯을 놓은 방식을 덧붙임"},
}

func init() {
	for code, m := range boardWords {
		words[code] = m
	}
}
