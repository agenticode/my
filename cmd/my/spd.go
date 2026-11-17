package main

import (
	"context"
	"io"
	"os"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// exitNoSPD is the exit code of my spd FILE... when no file holds an SPD
// that could be decoded.
const exitNoSPD = 70

// runSPD is "my spd": what each memory module of this host, or of an
// opened case, says about itself in its SPD EEPROM. It exits with the
// grade of the host. With files it decodes them instead and exits with 0
// when one was decoded and 70 when none was.
func runSPD(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("spd", src), src).withDetail()
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "spd", src)
	}
	if len(pos) > 0 {
		if src != nil || o.root != "" || o.ledger != "" {
			return o.usageError(env, lang, "%s", say(lang, "err.spd_files"))
		}
		return runSPDFiles(env, o, lang, pos)
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	if code, ok := s.needHost(); !ok {
		return code
	}
	v, err := s.c.SPD(ctx, s.h)
	if err != nil {
		return o.failure(env, err)
	}
	return s.emit("spd", s.h.Grade, v, func(w io.Writer) error { return term.SPD(w, v, s.st) })
}

// runSPDFiles decodes dump files that came without a host.
func runSPDFiles(env *cli.Env, o *options, lang i18n.Lang, paths []string) int {
	var files []view.FileInput
	for _, p := range paths {
		f := view.FileInput{Path: p}
		data, err := readSPDFile(p)
		if err != nil {
			f.Err = err
		} else {
			f.Input, f.Err = spd.Parse(data)
			if f.Err != nil && !spd.IsText(data) && len(data) > 0 && len(data) <= spd.MaxBinary {
				// The reader was told the file is an SPD: bytes that are no
				// SPD of a known form are decoded as they are.
				if ns, ok := f.Err.(*spd.NotSPD); ok && ns.What == spd.LooksNoType {
					f.Input, f.Err = &spd.Input{Form: spd.FormBinary, Bytes: data, Gap: -1, Back: -1}, nil
				}
			}
		}
		files = append(files, f)
	}
	v := view.DecodeFiles(lang, files)
	st := o.style(env, lang)
	code := 0
	if v.Read == 0 {
		code = exitNoSPD
	}
	if o.asJSON {
		e := newEnvelope("spd-files", lang, "", "", v)
		e.ExitCode = code
		if err := writeJSON(env.Stdout, e); err != nil {
			return o.failure(env, err)
		}
		return code
	}
	if err := term.SPDFiles(env.Stdout, v, st); err != nil {
		return o.failure(env, err)
	}
	return code
}

// readSPDFile reads a file given to my spd, at most the size of a text of
// an SPD and some.
func readSPDFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, spd.MaxText+1))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// runLots is "my lots": the DIMMs of this host, or of every host of an
// opened case, counted by maker, part number and manufacturing week. It
// exits with the worst grade it shows.
func runLots(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("lots", src), src)
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "lots", src)
	}
	if len(pos) > 0 {
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[0]))
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	hosts := s.c.Result.Hosts
	if s.h != nil {
		hosts = hosts[:0:0]
		hosts = append(hosts, s.h)
	}
	l, err := s.c.Lots(ctx, hosts)
	if err != nil {
		return o.failure(env, err)
	}
	return s.emit("lots", l.Grade, l, func(w io.Writer) error { return term.Lots(w, l, s.st) })
}

// spdWords are the texts of "my spd" and "my lots".
var spdWords = i18n.Catalog{
	"help.spd.usage": {EN: "my spd [FILE...] [--detail] [flags]", KO: "my spd [FILE...] [--detail] [옵션]"},
	"help.spd.text": {
		EN: "Shows what each memory module of this host says about itself in its SPD EEPROM: the manufacturing week, the location code, the maker, part and serial number, ranks and device width, die capacity and package. Each module is tied to its slot by serial number, and a table of date codes sets the DIMMs with a finding against those without one. " +
			"A scan reads the SPD from the eeprom files of the kernel drivers ee1004 (DDR4) and spd5118 (DDR5), without root and without a write, in at most 5 seconds; --no-spd leaves it out. Where Linux has no path to the SPD, as on most servers, the screen names the reason and what the reader can do. " +
			"With FILE... it decodes dumps instead, with no scan and no ledger: binary files, hex text of hexdump, od, xxd and i2cdump, and the output of decode-dimms. The screen holds no finding and changes no grade. The exit code is the grade of the host, as for my; with files it is 0 when one file was decoded and 70 when none was.",
		KO: "이 호스트의 메모리 모듈이 SPD EEPROM에 스스로 적어 둔 내용을 보여 줍니다: 제조 주차, 위치 코드, 제조사, 부품 번호와 시리얼, rank와 장치 폭, 다이 용량과 패키지. 모듈마다 시리얼로 슬롯과 연결하고, 날짜 코드 표에서 판정이 있는 DIMM과 없는 DIMM을 나란히 셉니다. " +
			"스캔은 커널 드라이버 ee1004(DDR4)와 spd5118(DDR5)의 eeprom 파일에서 root 없이, 쓰기 없이, 최대 5초 동안 SPD를 읽습니다. --no-spd를 주면 읽지 않습니다. 대부분의 서버처럼 Linux에서 SPD로 가는 길이 없으면 그 이유와 할 수 있는 일을 적습니다. " +
			"FILE...을 주면 스캔도 원장도 없이 덤프를 해석합니다: 바이너리 파일, hexdump, od, xxd, i2cdump의 16진 텍스트, decode-dimms 출력. 이 화면에는 판정이 없고 등급을 바꾸지 않습니다. 종료 코드는 my와 같이 호스트 판정 등급이고, 파일을 주면 하나라도 해석했을 때 0, 하나도 해석하지 못했을 때 70입니다.",
	},
	"help.spd.flags": {EN: "--detail          add per module the SPD revision, the CRCs, the package, the DRAM maker\n" +
		"                  and stepping, the bus, address and driver, and the tie",
		KO: "--detail          모듈마다 SPD 리비전, CRC, 패키지, DRAM 제조사와 스테핑,\n" +
			"                  버스와 주소와 드라이버, 연결 근거를 덧붙임"},
	"help.lots.usage": {EN: "my lots [flags]", KO: "my lots [옵션]"},
	"help.lots.text": {
		EN: "Counts the DIMMs of this host, or of every host of an opened case, by maker, part number and manufacturing week: the DIMMs, those with a finding, those with an uncorrected error on record, and the slots or hosts of those with a finding; then the same counts by ranks and device width. The week comes from the SPD, or from a BMC where no SPD was read. " +
			"A shared week is a lead for the supplier to check and no proof that a lot is at fault; a line names a week two DIMMs with a finding share, with both sides. For a fleet: my import --ledger fleet.db COLLECTION once per host, then my open fleet.db lots. The exit code is the worst grade the table shows.",
		KO: "이 호스트, 또는 연 케이스의 모든 호스트의 DIMM을 제조사, 부품 번호, 제조 주차별로 셉니다: DIMM 수, 판정이 있는 DIMM, UE 기록이 있는 DIMM, 판정이 있는 DIMM의 슬롯이나 호스트. 이어서 rank와 장치 폭별로 같은 수를 셉니다. 주차는 SPD에서, SPD를 읽지 못했으면 BMC에서 가져옵니다. " +
			"같은 주차는 공급사가 확인할 단서일 뿐 로트에 문제가 있다는 증거가 아닙니다. 판정이 있는 DIMM 둘 이상이 같은 주차를 가지면 양쪽 수를 모두 적은 줄을 덧붙입니다. 플릿이라면 호스트마다 my import --ledger fleet.db COLLECTION을 한 번씩 실행한 뒤 my open fleet.db lots를 실행합니다. 종료 코드는 표에 나온 가장 나쁜 등급입니다.",
	},
	"help.lots.flags": {EN: "(none beyond those below)", KO: "(아래 옵션 외에 없음)"},
	"err.spd_files":   {EN: "my spd FILE... decodes files without a scan: --root, --ledger and my open do not go with files", KO: "my spd FILE...은 스캔 없이 파일을 해석함: --root, --ledger, my open과 함께 쓸 수 없음"},
}

func init() {
	for code, m := range spdWords {
		words[code] = m
	}
}
