package bundle

import (
	"fmt"
	"strings"

	"github.com/agenticode/my/internal/i18n"
)

// readme writes README.txt: what the bundle is, for a person who receives
// the file and has not seen the tool.
func readme(m *Manifest) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	p("MemorY case bundle")
	p("")
	p("Written by %s on %s UTC.", m.Tool, m.CreatedAt.UTC().Format("2006-01-02 15:04:05"))
	p("It holds records about the memory errors of %d %s, collected for analysis.", len(m.Hosts), plural(len(m.Hosts), "host", "hosts"))
	p("MemorY is a read-only tool: https://github.com/agenticode/my")
	if s := m.Subject; s != nil {
		line := "The case is about the DIMM in slot " + s.Slot + " of host " + s.Host
		if s.Serial != "" {
			line += " (serial number " + s.Serial + ")"
		}
		p("%s.", line)
	}
	p("")
	p("Look at it")
	p("  my open FILE              the same screens the sender saw")
	p("  unzip -l FILE             list the members")
	p("  unzip FILE %s       then open the ledger with any SQLite tool", ledgerName)
	p("")
	p("Members")
	p("  %-14s what the bundle is and what it holds, with the checksum of the ledger", manifestName)
	p("  %-14s every source that was looked for, and what it yielded or why it could not be read", sourcesName)
	p("  %-14s the ledger: a SQLite file (schema version %d)", ledgerName, m.Ledger.SchemaVersion)
	p("  %-14s this text", readmeName)
	p("")
	p("Hosts")
	for _, h := range m.Hosts {
		line := fmt.Sprintf("  %s: %d %s, CE %s, UE %s", h.Name, h.DIMMs, plural(h.DIMMs, "DIMM", "DIMMs"), i18n.Thousands(h.CE), i18n.Thousands(h.UE))
		if !h.Last.IsZero() {
			line += ", " + h.First.UTC().Format("2006-01-02") + " to " + h.Last.UTC().Format("2006-01-02")
		}
		p("%s", line)
		if h.Boots > 0 {
			boots := fmt.Sprintf("    %d %s", h.Boots, plural(h.Boots, "boot", "boots"))
			if h.Stops > 0 {
				boots += fmt.Sprintf(", %d ended by a fatal hardware error", h.Stops)
			}
			if h.Panics > 0 {
				boots += fmt.Sprintf(", %d in another panic", h.Panics)
			}
			p("%s", boots)
		}
		if len(h.Sources) > 0 {
			p("    records from %s", strings.Join(h.Sources, ", "))
		}
	}
	p("")
	p("What the ledger holds")
	width := 0
	for _, it := range m.Collected {
		width = max(width, len(it.Table))
	}
	for _, it := range m.Collected {
		if it.Rows == 0 {
			continue
		}
		p("  %-*s  %9s  %s", width, it.Table, i18n.Thousands(it.Rows), it.Holds)
	}
	p("")
	p("What a bundle never holds")
	for _, n := range m.NotCollected {
		p("  %s", n)
	}
	p("")
	if m.Anonymized {
		p("Host names, IP addresses, MAC addresses, the disk identifiers of the kernel command line")
		p("and the names of home directories in file paths were replaced before this bundle was written.")
		p("DIMM serial numbers were kept: a return is about them.")
	} else {
		p("Host names and network addresses are in this bundle as the sources recorded them.")
		p("\"my bundle --anonymize\" replaces them.")
	}
	return b.String()
}

func plural(n int, one, other string) string {
	if n == 1 {
		return one
	}
	return other
}
