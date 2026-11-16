package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/fwmap"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/term"
)

// eventMaps is the --event-map flag: user tables of firmware messages,
// read before the shipped ones. It can be given several times.
type eventMaps []string

func (e *eventMaps) String() string     { return strings.Join(*e, ",") }
func (e *eventMaps) Set(v string) error { *e = append(*e, v); return nil }

// useEventMaps reads the user tables and makes the readers ask them first.
// A table that cannot be read as a whole is a usage error; lines that
// cannot be read are left out and named on standard error.
func useEventMaps(env *cli.Env, lang i18n.Lang, files eventMaps) error {
	if len(files) == 0 {
		fwmap.Use(nil)
		return nil
	}
	data := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			// The file error names the file.
			return fmt.Errorf("--event-map %w", err)
		}
		if len(b) > 1<<20 {
			return worded("err.event_map_large", "file", f)
		}
		data[f] = b
	}
	c, err := fwmap.Shipped().WithUser(data)
	if err != nil {
		return fmt.Errorf("--event-map %w", err)
	}
	for _, n := range c.Notes {
		fmt.Fprintf(env.Stderr, "%s\n", say(lang, "vendors.note", "note", n))
	}
	fwmap.Use(c)
	return nil
}

// vendorTable is one table as "my vendors" shows it.
type vendorTable struct {
	Table     string   `json:"table"`
	Version   int      `json:"version"`
	Vendor    string   `json:"vendor"`
	Messages  int      `json:"messages"`
	Basis     string   `json:"basis"`
	Unchecked bool     `json:"not_checked_on_real_hardware"`
	Applies   []string `json:"applies,omitzero"`
	Sources   []struct {
		Name  string `json:"name"`
		Title string `json:"title"`
		URL   string `json:"url"`
		Read  string `json:"read,omitzero"`
	} `json:"sources,omitzero"`
	Lines []vendorLine `json:"lines,omitzero"`
}

type vendorLine struct {
	ID    string `json:"id"`
	NArgs int    `json:"nargs,omitzero"`
	// Text is the pattern of a line that reads a sentence without its ID.
	Text     string   `json:"text,omitzero"`
	Event    string   `json:"event"`
	Flags    []string `json:"flags,omitzero"`
	Clears   string   `json:"clears,omitzero"`
	Args     []string `json:"args,omitzero"`
	Severity string   `json:"severity,omitzero"`
	Sources  []string `json:"sources"`
	Basis    string   `json:"basis"`
}

// unclassifiedID is a message key of the ledger no table classes.
type unclassifiedID struct {
	Vendor   string `json:"vendor,omitzero"`
	Registry string `json:"registry,omitzero"`
	ID       string `json:"id"`
	Records  int    `json:"records"`
}

func tableOf(t *fwmap.Table, lines bool) vendorTable {
	v := vendorTable{Table: t.Name, Version: t.Version, Vendor: t.Vendor, Messages: len(t.Lines), Basis: t.Basis, Unchecked: fwmap.Unchecked(t.Basis)}
	for _, p := range t.Applies.Prefixes {
		if p == "" {
			v.Applies = append(v.Applies, "bare-id")
			continue
		}
		v.Applies = append(v.Applies, "registry "+p)
	}
	if t.Applies.AnyRegistry {
		v.Applies = append(v.Applies, "any registry")
	}
	if t.Applies.IPMI {
		v.Applies = append(v.Applies, "ipmi")
	}
	for _, m := range t.Applies.Manufacturers {
		v.Applies = append(v.Applies, "manufacturer "+m)
	}
	for _, name := range t.SourceOrder {
		s := t.Sources[name]
		v.Sources = append(v.Sources, struct {
			Name  string `json:"name"`
			Title string `json:"title"`
			URL   string `json:"url"`
			Read  string `json:"read,omitzero"`
		}{name, s.Title, s.URL, s.Read})
	}
	if lines {
		for _, l := range t.Lines {
			vl := vendorLine{ID: l.ID, NArgs: l.NArgs, Event: string(l.Event), Clears: string(l.Clears), Args: l.Args, Severity: l.Severity,
				Sources: l.Sources, Basis: l.BasisOf()}
			if l.Text != nil {
				vl.Text = l.Text.String()
			}
			for _, f := range l.Flags {
				vl.Flags = append(vl.Flags, string(f))
			}
			v.Lines = append(v.Lines, vl)
		}
	}
	return v
}

// runVendors is "my vendors [TABLE] [--seen] [--print TABLE]": the tables
// that map the messages of each vendor to common events, the lines of one,
// a table as a file to start a user table from, or what the BMCs of a case
// published and which of their messages no table classes.
// vendorsOptions are the flags of "my vendors". The command scans nothing,
// so it takes none of the flags that point a scan somewhere: the language,
// JSON, the width, the tables of one's own, and for --seen the ledger and
// its host.
func vendorsOptions(src *source) (o *options, seen *bool, print *string) {
	name := cmdName("vendors", src)
	o = &options{name: name, fs: flag.NewFlagSet(name, flag.ContinueOnError), color: term.ColorNever}
	o.fs.SetOutput(io.Discard) // errors and help are written in the reader's language
	o.fs.StringVar(&o.lang, "lang", "", "")
	o.fs.BoolVar(&o.asJSON, "json", false, "")
	o.fs.IntVar(&o.width, "width", 0, "")
	o.fs.StringVar(&o.host, "host", "", "")
	if src == nil {
		o.fs.StringVar(&o.ledger, "ledger", "", "")
	}
	o.fs.Var(&o.maps, "event-map", "")
	seen = o.fs.Bool("seen", false, "")
	print = o.fs.String("print", "", "")
	o.fs.BoolVar(&o.help, "help", false, "")
	o.fs.BoolVar(&o.help, "h", false, "")
	return o, seen, print
}

func runVendors(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o, seen, print := vendorsOptions(src)
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "vendors", src)
	}
	if len(pos) > 1 {
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[1]))
	}
	if err := useEventMaps(env, lang, o.maps); err != nil {
		return o.usageError(env, lang, "%s", errText(lang, err))
	}
	defer fwmap.Use(nil)
	c := fwmap.Current()
	st := o.style(env, lang)
	emit := func(data any, draw func(w io.Writer) error) int {
		if o.asJSON {
			enc := json.NewEncoder(env.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(envelope{View: "vendors", Version: jsonVersion, Lang: lang, Data: data}); err != nil {
				return o.failure(env, err)
			}
			return cli.ExitOK
		}
		if err := draw(env.Stdout); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}

	switch {
	case *print != "":
		b, err := fwmap.ShippedText(*print)
		if err != nil {
			return o.usageError(env, lang, "%s", say(lang, "err.vendors_table", "table", *print, "tables", strings.Join(tableNames(c), ", ")))
		}
		if _, err := env.Stdout.Write(b); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK

	case *seen:
		if src == nil && o.ledger == "" {
			return o.usageError(env, lang, "%s", say(lang, "err.vendors_seen"))
		}
		path := o.ledger
		if src != nil {
			path = src.ledger
		}
		l, err := store.Open(ctx, path, store.ReadOnly)
		if err != nil {
			return o.failure(env, err)
		}
		defer l.Close()
		v, err := seenOf(ctx, l, o.host)
		if err != nil {
			return o.failure(env, err)
		}
		return emit(v, func(w io.Writer) error { return drawSeen(w, lang, st, v) })

	case len(pos) == 1:
		t := c.Table(pos[0])
		if t == nil {
			return o.usageError(env, lang, "%s", say(lang, "err.vendors_table", "table", pos[0], "tables", strings.Join(tableNames(c), ", ")))
		}
		v := tableOf(t, true)
		return emit(v, func(w io.Writer) error { return drawTable(w, lang, st, v) })
	}
	var list []vendorTable
	for _, t := range c.Tables() {
		list = append(list, tableOf(t, false))
	}
	rules := 0
	if w := c.Wording(); w != nil {
		rules = len(w.Rules)
	}
	data := struct {
		Tables  []vendorTable `json:"tables"`
		Wording int           `json:"wording_rules"`
		Notes   []string      `json:"notes,omitzero"`
	}{list, rules, c.Notes}
	return emit(data, func(w io.Writer) error { return drawTables(w, lang, st, list, rules, c.Notes) })
}

func tableNames(c *fwmap.Classifier) []string {
	var out []string
	for _, t := range c.Tables() {
		out = append(out, t.Name)
	}
	return out
}

// basisText says how far a table was checked.
func basisText(lang i18n.Lang, basis string) string {
	if fwmap.Unchecked(basis) {
		return say(lang, "vendors.unchecked", "basis", basis)
	}
	return basis
}

func drawTables(w io.Writer, lang i18n.Lang, st term.Style, list []vendorTable, rules int, notes []string) error {
	rows := [][]string{{say(lang, "vendors.h_table"), say(lang, "vendors.h_vendor"), say(lang, "vendors.h_messages"), say(lang, "vendors.h_basis")}}
	for _, t := range list {
		rows = append(rows, []string{fmt.Sprintf("%s@%d", t.Table, t.Version), t.Vendor, fmt.Sprint(t.Messages), basisText(lang, t.Basis)})
	}
	rows = append(rows, []string{"wording@1", say(lang, "vendors.wording"), fmt.Sprint(rules), say(lang, "vendors.rules")})
	if err := writeColumns(w, rows, 2); err != nil {
		return err
	}
	for _, n := range notes {
		if _, err := fmt.Fprintf(w, " %s\n", say(lang, "vendors.note", "note", n)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, " %s\n", say(lang, "vendors.more"))
	return err
}

// writeColumns writes rows with each column as wide as its widest cell;
// the column at right is aligned to the right.
func writeColumns(w io.Writer, rows [][]string, right int) error {
	if len(rows) == 0 {
		return nil
	}
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], term.Width(c))
		}
	}
	for _, r := range rows {
		var b strings.Builder
		b.WriteString(" ")
		for i, c := range r {
			pad := strings.Repeat(" ", widths[i]-term.Width(c))
			if i == right {
				b.WriteString(pad + c)
			} else {
				b.WriteString(c)
				if i < len(r)-1 {
					b.WriteString(pad)
				}
			}
			if i < len(r)-1 {
				b.WriteString("  ")
			}
		}
		if _, err := fmt.Fprintln(w, strings.TrimRight(b.String(), " ")); err != nil {
			return err
		}
	}
	return nil
}

func drawTable(w io.Writer, lang i18n.Lang, st term.Style, t vendorTable) error {
	p := func(format string, a ...any) error { _, err := fmt.Fprintf(w, format+"\n", a...); return err }
	if err := p("%s@%d  %s  %s", t.Table, t.Version, t.Vendor, basisText(lang, t.Basis)); err != nil {
		return err
	}
	if len(t.Applies) > 0 {
		var ap []string
		for _, a := range t.Applies {
			switch {
			case a == "bare-id":
				ap = append(ap, say(lang, "vendors.ap_bare"))
			case a == "any registry":
				ap = append(ap, say(lang, "vendors.ap_any"))
			case a == "ipmi":
				ap = append(ap, say(lang, "vendors.ap_ipmi"))
			case strings.HasPrefix(a, "manufacturer "):
				maker := strings.TrimPrefix(a, "manufacturer ")
				if maker != "" {
					maker = strings.ToUpper(maker[:1]) + maker[1:]
				}
				ap = append(ap, say(lang, "vendors.ap_maker", "maker", maker))
			default:
				ap = append(ap, say(lang, "vendors.ap_registry", "prefix", strings.TrimPrefix(a, "registry ")))
			}
		}
		for i, l := range st.Wrap(say(lang, "vendors.applies", "list", strings.Join(ap, ", ")), 2) {
			if i == 0 {
				l = " " + strings.TrimLeft(l, " ")
			}
			if err := p("%s", l); err != nil {
				return err
			}
		}
	}
	if err := p(" %s", say(lang, "vendors.sources")); err != nil {
		return err
	}
	for _, s := range t.Sources {
		for i, l := range st.Wrap(s.Name+": "+s.Title+" "+s.URL, 4) {
			if i == 0 {
				l = "  " + strings.TrimLeft(l, " ")
			}
			if err := p("%s", l); err != nil {
				return err
			}
		}
	}
	byEvent := map[string][]string{}
	for _, l := range t.Lines {
		id := l.ID
		switch {
		case l.Text != "":
			id = say(lang, "vendors.sentence", "id", id)
		case l.NArgs != 0:
			id = fmt.Sprintf("%s(%d)", id, l.NArgs)
		}
		byEvent[l.Event] = append(byEvent[l.Event], id)
	}
	width := 0
	for _, n := range fwevent.Names() {
		if len(byEvent[string(n)]) > 0 {
			width = max(width, len(n))
		}
	}
	for _, n := range fwevent.Names() {
		ids := byEvent[string(n)]
		if len(ids) == 0 {
			continue
		}
		sort.Strings(ids)
		head := fmt.Sprintf(" %-*s  ", width, n)
		for i, l := range st.Wrap(strings.Join(ids, ", "), len(head)) {
			if i == 0 {
				l = head + strings.TrimLeft(l, " ")
			}
			if err := p("%s", l); err != nil {
				return err
			}
		}
	}
	return nil
}

// seenView is what the BMCs of a case published and which of their
// messages no table classes.
type seenView struct {
	Registries   []seenRegistry   `json:"registries"`
	Unclassified []unclassifiedID `json:"unclassified"`
}

type seenRegistry struct {
	Prefix   string   `json:"prefix"`
	Version  string   `json:"version,omitzero"`
	Owner    string   `json:"owner,omitzero"`
	Messages int      `json:"messages"`
	Memory   int      `json:"memory"`
	Mapped   int      `json:"mapped"`
	Wording  int      `json:"wording"`
	Unmapped []string `json:"unmapped,omitzero"`
}

func seenOf(ctx context.Context, l *store.Store, host string) (*seenView, error) {
	v := &seenView{Registries: []seenRegistry{}, Unclassified: []unclassifiedID{}}
	hosts, err := l.Hosts(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[unclassifiedID]int{}
	for _, h := range hosts {
		if host != "" && h.Name != host {
			continue
		}
		regs, err := l.RegistriesSeen(ctx, h.Name)
		if err != nil {
			return nil, err
		}
		for _, r := range regs {
			v.Registries = append(v.Registries, seenRegistry{Prefix: r.Prefix, Version: r.Version, Owner: r.Owner, Messages: r.Messages,
				Memory: r.Memory, Mapped: r.Mapped, Wording: r.Wording, Unmapped: r.Unmapped})
		}
		fws, err := l.FirmwareRecords(ctx, store.FirmwareFilter{Host: h.Name, Common: []string{string(fwevent.Unclassified)}})
		if err != nil {
			return nil, err
		}
		for _, f := range fws {
			id := f.VendorID
			if id == "" {
				id = f.Text
				if len(id) > 60 {
					id = id[:60] + "..."
				}
			}
			counts[unclassifiedID{Vendor: f.Vendor, Registry: f.Registry, ID: id}]++
		}
	}
	for k, n := range counts {
		k.Records = n
		v.Unclassified = append(v.Unclassified, k)
	}
	sort.Slice(v.Unclassified, func(i, j int) bool {
		a, b := v.Unclassified[i], v.Unclassified[j]
		if a.Records != b.Records {
			return a.Records > b.Records
		}
		return a.Registry+a.ID < b.Registry+b.ID
	})
	return v, nil
}

func drawSeen(w io.Writer, lang i18n.Lang, st term.Style, v *seenView) error {
	p := func(format string, a ...any) error { _, err := fmt.Fprintf(w, format+"\n", a...); return err }
	if len(v.Registries) == 0 {
		if err := p(" %s", say(lang, "vendors.no_registry")); err != nil {
			return err
		}
	}
	for _, r := range v.Registries {
		if err := p(" %s", say(lang, "vendors.registry", "prefix", r.Prefix, "version", r.Version, "memory", r.Memory, "mapped", r.Mapped,
			"wording", r.Wording, "unmapped", r.Memory-r.Mapped-r.Wording)); err != nil {
			return err
		}
	}
	if len(v.Unclassified) == 0 {
		return p(" %s", say(lang, "vendors.none_unclassified"))
	}
	if err := p(" %s", say(lang, "vendors.unclassified")); err != nil {
		return err
	}
	for _, u := range v.Unclassified {
		id := u.ID
		if u.Registry != "" {
			id = u.Registry + " " + id
		}
		if err := p("  %6d  %s", u.Records, id); err != nil {
			return err
		}
	}
	return nil
}

// vendorsWords are the texts of "my vendors".
var vendorsWords = i18n.Catalog{
	"help.vendors.usage": {EN: "my vendors [TABLE] [--seen] [--print TABLE] [flags]", KO: "my vendors [TABLE] [--seen] [--print TABLE] [옵션]"},
	"help.vendors.text": {
		EN: "Lists the tables that read the memory messages of BMC and BIOS vendors as common events: one table per vendor or standard, with its version, the number of messages it maps and how far it was checked. " +
			"Every table rests on public registries or documents, named per line in the table. A table whose mapping was not compared with a record of a real machine says so: not checked on real hardware. " +
			"With TABLE the command lists the messages of that table by common event and the sources they were read from. --print TABLE writes the table as a file, to start a table of one's own; --event-map FILE on the commands that read sources asks such a table before the shipped ones. " +
			"With --seen and a case (my open CASE vendors --seen) or --ledger FILE it lists the message registries the BMCs of the case published, how many of their memory messages a table or the wording rules class, and the message IDs of the case no table classes. The exit code is 0.",
		KO: "BMC와 BIOS 벤더의 메모리 메시지를 공통 이벤트로 읽는 표를 나열합니다. 벤더나 표준마다 표가 하나이고, 표마다 버전, 매핑한 메시지 수, 확인한 정도를 적습니다. " +
			"모든 표는 공개 레지스트리나 문서에 근거하고, 근거는 표의 줄마다 적혀 있습니다. 실제 장비의 기록과 맞춰 보지 않은 표는 실물 장비에서 확인하지 않았다고 밝힙니다. " +
			"TABLE을 주면 그 표의 메시지를 공통 이벤트별로, 읽은 근거와 함께 나열합니다. --print TABLE은 표를 파일 형태로 출력하므로 직접 만들 표의 출발점이 됩니다. 소스를 읽는 명령에 --event-map FILE을 주면 그 표를 내장 표보다 먼저 봅니다. " +
			"--seen과 케이스(my open CASE vendors --seen)나 --ledger FILE을 주면, 케이스의 BMC가 내놓은 메시지 레지스트리와 그 메모리 메시지 중 표나 문구 규칙이 분류한 수, 그리고 어느 표도 분류하지 못한 메시지 ID를 나열합니다. 종료 코드는 0입니다.",
	},
	"help.vendors.flags": {
		EN: "TABLE               the messages of one table by common event\n" +
			"--print TABLE       write the table as a file and stop\n" +
			"--seen              with a case or --ledger FILE: the registries the BMCs published and\n" +
			"                    the message IDs no table classes\n" +
			"--ledger FILE       the ledger --seen reads\n" +
			"--event-map FILE    a table of one's own, asked before the shipped ones (repeatable)\n" +
			"--lang en|ko        language of the output (default: from the locale, English otherwise)\n" +
			"--json              print the tables as JSON\n" +
			"--width N           columns to fill (default: the terminal, 100 otherwise)\n" +
			"--host NAME         with --seen: the host to show, when the ledger holds several",
		KO: "TABLE               표 하나의 메시지를 공통 이벤트별로\n" +
			"--print TABLE       표를 파일 형태로 출력하고 종료\n" +
			"--seen              케이스나 --ledger FILE과 함께: BMC가 내놓은 레지스트리와 분류하지 못한 ID\n" +
			"--ledger FILE       --seen이 읽을 원장\n" +
			"--event-map FILE    내장 표보다 먼저 볼 직접 만든 표 (여러 번 줄 수 있음)\n" +
			"--lang en|ko        출력 언어 (기본: 로캘, 한국어가 아니면 영어)\n" +
			"--json              표를 JSON으로 출력\n" +
			"--width N           출력 폭 (기본: 터미널 폭, 터미널이 아니면 100)\n" +
			"--host NAME         --seen과 함께: 원장에 호스트가 여럿일 때 볼 호스트",
	},
	"vendors.h_table":           {EN: "table", KO: "표"},
	"vendors.h_vendor":          {EN: "vendor or standard", KO: "벤더 또는 표준"},
	"vendors.h_messages":        {EN: "messages", KO: "메시지"},
	"vendors.h_basis":           {EN: "basis", KO: "근거"},
	"vendors.unchecked":         {EN: "{basis}: not checked on real hardware", KO: "{basis}: 실물 장비에서 확인하지 않음"},
	"vendors.wording":           {EN: "any, by wording", KO: "모든 벤더, 문구로"},
	"vendors.rules":             {EN: "rules of this tool", KO: "이 도구의 규칙"},
	"vendors.more":              {EN: "my vendors TABLE: the messages of a table; my vendors --seen: what the BMCs of a case published", KO: "my vendors TABLE: 표의 메시지, my vendors --seen: 케이스의 BMC가 내놓은 것"},
	"vendors.applies":           {EN: "applies to: {list}", KO: "적용 대상: {list}"},
	"vendors.ap_registry":       {EN: "message IDs of the registry {prefix}", KO: "{prefix} 레지스트리의 메시지 ID"},
	"vendors.ap_bare":           {EN: "message IDs without a registry", KO: "레지스트리 없는 메시지 ID"},
	"vendors.ap_any":            {EN: "the same names under any registry", KO: "어느 레지스트리든 같은 이름"},
	"vendors.ap_ipmi":           {EN: "states of the IPMI memory sensor", KO: "IPMI 메모리 센서 상태"},
	"vendors.ap_maker":          {EN: "sentences without an ID in a log of a {maker} system", KO: "{maker} 시스템 로그의 ID 없는 문장"},
	"vendors.sentence":          {EN: "{id} (sentence)", KO: "{id}(문장)"},
	"vendors.sources":           {EN: "sources:", KO: "근거:"},
	"vendors.note":              {EN: "note: {note}", KO: "참고: {note}"},
	"vendors.no_registry":       {EN: "no message registry of a BMC is on record", KO: "기록된 BMC 메시지 레지스트리 없음"},
	"vendors.registry":          {EN: "{prefix} {version}: {memory} memory messages, {mapped} in a table, {wording} by wording, {unmapped} not classed", KO: "{prefix} {version}: 메모리 메시지 {memory}개, 표에 {mapped}개, 문구로 {wording}개, 분류 못 함 {unmapped}개"},
	"vendors.unclassified":      {EN: "records about memory that no table classes, by message ID:", KO: "어느 표도 분류하지 못한 메모리 기록 (메시지 ID별):"},
	"vendors.none_unclassified": {EN: "every firmware record of the case about memory has a class", KO: "케이스의 메모리 관련 펌웨어 기록은 모두 분류됨"},
	"err.vendors_table":         {EN: "no table {table}; the tables are {tables}", KO: "{table} 표가 없음. 있는 표: {tables}"},
	"err.event_map_large":       {EN: "--event-map {file}: larger than 1 MiB", KO: "--event-map {file}: 크기가 1 MiB를 넘음"},
	"err.vendors_seen":          {EN: "--seen reads a case: my open CASE vendors --seen, or --ledger FILE", KO: "--seen은 케이스를 읽음: my open CASE vendors --seen 또는 --ledger FILE"},
}

func init() {
	for code, m := range vendorsWords {
		words[code] = m
	}
}
