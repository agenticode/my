package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/view"
)

// labelFlags are the flags of "my export labels".
type labelFlags struct {
	names, vendor, model, product string
}

// exportLabels is "my export labels": the EDAC positions of the host with
// the names of their slots, as a DIMM label file for rasdaemon. The file
// goes to standard output, or with -o into a file. Nothing is installed
// and no label of the running kernel is changed: the file is for
// "ras-mc-ctl --register-labels" to read.
func exportLabels(ctx context.Context, env *cli.Env, o *options, lang i18n.Lang, src *source, lf labelFlags, out string, force bool) int {
	switch lf.names {
	case "", view.LabelSlot, view.LabelBMC:
	default:
		return o.usageError(env, lang, "%s", say(lang, "err.labels_names", "value", lf.names))
	}
	toFile := out != "" && out != "-"
	if toFile {
		// A file that cannot be written is found before the host is scanned.
		if code, ok := o.checkTarget(env, lang, out, force); !ok {
			return code
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
	f, err := s.c.LabelFile(ctx, s.h, view.LabelOptions{Names: lf.names, Vendor: lf.vendor, Model: lf.model, Product: lf.product})
	switch {
	case errors.Is(err, view.ErrNoLabels):
		return o.failure(env, errors.New(say(lang, "err.labels_none", "host", s.h.Host)))
	case errors.Is(err, view.ErrNoBoard):
		return o.failure(env, errors.New(say(lang, "err.labels_board", "host", s.h.Host)))
	case errors.Is(err, view.ErrBoardName):
		return o.failure(env, errors.New(say(lang, "err.labels_name", "why", err.Error())))
	case err != nil:
		return o.failure(env, err)
	}
	text := f.Text(s.c, toolName())
	if toFile {
		if err := writeWhole(out, text, force); err != nil {
			return o.failure(env, errors.New(targetText(lang, err)))
		}
	}
	if o.asJSON {
		res := struct {
			Path string `json:"path,omitzero"`
			*view.LabelFile
		}{LabelFile: f}
		if toFile {
			res.Path = out
		}
		if err := writeJSON(env.Stdout, res); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	if !toFile {
		if _, err := env.Stdout.Write(text); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	machine := f.Vendor
	if f.Model != "" {
		machine += " " + f.Model
	} else {
		machine += " " + f.Product
	}
	fmt.Fprintln(env.Stdout, say(lang, "labels.written", "file", out, "n", len(f.Labels), "machine", machine))
	if len(f.Skipped) > 0 {
		fmt.Fprintln(env.Stdout, "  "+say(lang, "labels.skipped", "n", len(f.Skipped)))
	}
	if f.VendorGuessed {
		fmt.Fprintln(env.Stdout, "  "+say(lang, "labels.vendor_guessed", "vendor", f.Vendor))
	}
	fmt.Fprintln(env.Stdout, "  "+say(lang, "labels.install", "file", out))
	return cli.ExitOK
}

// labelWords are the texts of "my export labels". They are added to the
// catalog of the commands.
var labelWords = i18n.Catalog{
	"err.labels_names": {EN: "--names {value}: want slot (the name of the ledger) or bmc (the service label of the BMC)", KO: "--names {value}: slot(원장의 슬롯 이름) 또는 bmc(BMC의 서비스 라벨)여야 함"},
	"err.labels_none":  {EN: "host {host}: no EDAC position is tied to a slot that SMBIOS or a BMC names, so there is no label to write. A label file needs the EDAC counters of the host and its SMBIOS tables or its BMC", KO: "호스트 {host}: SMBIOS나 BMC가 이름을 준 슬롯에 연결된 EDAC 위치가 없어 쓸 라벨이 없음. 라벨 파일에는 호스트의 EDAC 카운터와 SMBIOS 테이블 또는 BMC가 필요함"},
	"err.labels_board": {EN: "host {host}: the ledger does not name the vendor and the model of the machine. \"ras-mc-ctl --mainboard\" prints them on the host: pass them with --vendor and --model", KO: "호스트 {host}: 원장에 기기의 벤더와 모델이 없음. 호스트에서 \"ras-mc-ctl --mainboard\"로 확인해 --vendor와 --model로 지정"},
	"err.labels_name":  {EN: "{why}: a label file reads a comma and a semicolon as the end of a name. Pass the name with --vendor, --model or --product", KO: "{why}: 라벨 파일은 쉼표와 세미콜론을 이름의 끝으로 읽음. --vendor, --model, --product로 이름을 지정"},

	"labels.written":        {EN: "label file written: {file} ({n} {n|label|labels} for {machine})", KO: "라벨 파일 저장: {file} ({machine}의 라벨 {n}개)"},
	"labels.skipped":        {EN: "{n} EDAC {n|position|positions} got no label; the comments in the file say why", KO: "EDAC 위치 {n}곳에는 라벨이 없음. 이유는 파일의 주석에 있음"},
	"labels.vendor_guessed": {EN: "the Vendor line is the system vendor, {vendor}: compare it with what \"ras-mc-ctl --mainboard\" prints", KO: "Vendor 줄은 시스템 벤더 {vendor}임: \"ras-mc-ctl --mainboard\"의 출력과 비교해야 함"},
	"labels.install":        {EN: "install on the host: sudo cp {file} /etc/ras/dimm_labels.d/ && sudo ras-mc-ctl --register-labels", KO: "호스트에 설치: sudo cp {file} /etc/ras/dimm_labels.d/ && sudo ras-mc-ctl --register-labels"},
}

func init() {
	for code, m := range labelWords {
		words[code] = m
	}
}
