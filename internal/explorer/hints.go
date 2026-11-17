package explorer

// This file is MemorY's own: what a dmidecode text says of the system
// beside its memory devices.

import (
	"regexp"
	"strings"

	"github.com/agenticode/my/internal/model"
)

// textHints are what a dmidecode text says of the system that the slots
// alone do not.
type textHints struct {
	// vendor and processor are the maker of the system and the name of the
	// first processor record, as the fixes of the first round read them.
	vendor, processor string
	// sysVendor and product are the maker and product of the system, board
	// is the maker and product of the board in one string, each without the
	// placeholders firmware writes, as the ledger keeps them.
	sysVendor, product, board string
	// cpus are the processor records in table order. A processor block
	// without a Handle line before it (a text written from what a ledger
	// keeps without the socket count) gives cpuName alone: the name of the
	// processor, and no record.
	cpus    []cpuRecord
	cpuName string
}

// cpuRecord is one processor record.
type cpuRecord struct {
	name      string // the version string, "" for a placeholder
	signature string // "family/model/stepping", "" when not known
	status    int    // 1 populated, 0 not, -1 not known
}

var (
	reSystemMaker = regexp.MustCompile(`(?m)^System Information\n(?:[ \t]+[^\n]*\n)*?[ \t]+Manufacturer:[ \t]*([^\n]*)`)
	reProcessor   = regexp.MustCompile(`(?m)^Processor Information\n(?:[ \t]+[^\n]*\n)*?[ \t]+Version:[ \t]*([^\n]*)`)
	reHandleLine  = regexp.MustCompile(`^Handle (0x[0-9A-Fa-f]+), DMI type \d+`)
	reSignature   = regexp.MustCompile(`Family (\d+), Model (\d+), Stepping (\d+)`)
)

// hintsOf reads the system, board and processor records of a text.
func hintsOf(raw string) textHints {
	var h textHints
	if m := reSystemMaker.FindStringSubmatch(raw); m != nil {
		h.vendor = strings.TrimSpace(m[1])
	}
	if m := reProcessor.FindStringSubmatch(raw); m != nil {
		h.processor = strings.TrimSpace(m[1])
	}
	seen := map[string]bool{}
	for _, r := range records(raw, "System Information", "Base Board Information", "Processor Information") {
		if on(FixRepeatedTable) && r.handle != "" {
			if seen[r.key] {
				continue
			}
			seen[r.key] = true
		}
		get := func(k string) string {
			if v := r.fields[k]; !model.Placeholder(v) {
				return strings.TrimSpace(v)
			}
			return ""
		}
		switch r.title {
		case "System Information":
			h.sysVendor, h.product = get("Manufacturer"), get("Product Name")
		case "Base Board Information":
			if h.board == "" {
				h.board = strings.TrimSpace(get("Manufacturer") + " " + get("Product Name"))
			}
		case "Processor Information":
			if r.handle == "" {
				if h.cpuName == "" {
					h.cpuName = get("Version")
				}
				continue
			}
			c := cpuRecord{name: get("Version"), status: -1}
			if st, ok := r.fields["Status"]; ok {
				c.status = 0
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(st)), "populated") {
					c.status = 1
				}
			}
			if m := reSignature.FindStringSubmatch(r.fields["Signature"]); m != nil {
				c.signature = m[1] + "/" + m[2] + "/" + m[3]
			}
			h.cpus = append(h.cpus, c)
		}
	}
	return h
}

func (h textHints) dell() bool { return strings.HasPrefix(strings.ToLower(h.vendor), "dell") }

// record is one record of a dmidecode text: its title, its fields (the
// first of each key), its handle and its whole text as the key of a repeat.
type record struct {
	title, handle, key string
	fields             map[string]string
}

// records reads the records with one of the titles: the title line, then the
// lines indented by white space. The handle is that of the line before the
// title, when it is a Handle line.
func records(raw string, titles ...string) []record {
	want := map[string]bool{}
	for _, t := range titles {
		want[t] = true
	}
	lines := strings.Split(raw, "\n")
	var out []record
	for i := 0; i < len(lines) && len(out) <= MaxSlots; i++ {
		title := strings.TrimRight(lines[i], " \t")
		if !want[title] {
			continue
		}
		r := record{title: title, fields: map[string]string{}}
		var key strings.Builder
		if i > 0 {
			if m := reHandleLine.FindStringSubmatch(lines[i-1]); m != nil {
				r.handle = m[1]
				key.WriteString(lines[i-1] + "\n")
			}
		}
		key.WriteString(title + "\n")
		j := i + 1
		for ; j < len(lines) && lines[j] != "" && (lines[j][0] == '\t' || lines[j][0] == ' '); j++ {
			key.WriteString(lines[j] + "\n")
			k, v, ok := strings.Cut(strings.TrimSpace(lines[j]), ":")
			if !ok {
				continue
			}
			if _, dup := r.fields[k]; !dup {
				r.fields[k] = strings.TrimSpace(v)
			}
		}
		r.key = key.String()
		out = append(out, r)
		i = j - 1
	}
	return out
}
