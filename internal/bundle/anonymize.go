package bundle

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// Anonymizing replaces what names a host on a network: host names, IP
// addresses and MAC addresses. It also replaces the name of a home
// directory in a path, which is the name of an account: an import records
// the paths of the files it read as they were given. Everything an analysis
// needs stays: times, locations, counts, slot names, and the serial numbers
// of the DIMMs, which are what a return is about.
//
// A name is replaced by a word made from a keyed hash of it ("host-" and
// eight hexadecimal digits), the same word wherever the name occurs, so
// that records still belong together. Without the key the word does not
// lead back to the name, and a list of guessed names cannot be tested
// against it. Identifiers the ledger derives from the host name (record
// fingerprints, derived boot IDs, placeholder DIMM identities) are hashed
// again with the key for the same reason.

// treatment says what anonymizing does to a column.
type treatment int

const (
	// keep leaves the column as it is.
	keep treatment = iota
	// hostName: the value is a host name.
	hostName
	// freeText: the value is text that may mention a host or an address.
	freeText
	// derived: the value is a hash the ledger computed from the host name
	// and other things.
	derived
	// bootID: a boot ID; the derived ones are hashes over the host name.
	bootID
	// dimmUID: a DIMM identity; the placeholder ones are hashes over the
	// host name.
	dimmUID
	// commandLine: a kernel command line. Besides hosts and addresses it
	// names the disks of the machine (root=UUID=..., rd.luks.uuid=...) and
	// may carry its machine ID.
	commandLine
)

// columns says how every text column of the ledger is treated. A text
// column that is not listed is treated as free text, which is the careful
// choice; a test fails for a column that is not listed, so that whoever
// adds one decides.
var columns = map[string]treatment{
	"action.host": hostName, "action.at": keep, "action.time_quality": keep, "action.kind": keep, "action.method": keep,
	"action.result": keep, "action.label": keep, "action.slot": keep, "action.dimm_uid": dimmUID, "action.boot_id": bootID,
	"action.detail": freeText, "action.source": keep, "action.raw_ref": freeText, "action.raw": freeText, "action.fingerprint": derived,

	"bios_setting.host": hostName, "bios_setting.observed_at": keep, "bios_setting.name": keep, "bios_setting.value": freeText,
	"bios_setting.pending": freeText, "bios_setting.value_type": keep, "bios_setting.registry": keep, "bios_setting.display_name": keep,
	"bios_setting.menu_path": keep, "bios_setting.allowed": freeText, "bios_setting.default_value": freeText, "bios_setting.source": keep,
	"bios_setting.raw_ref": freeText,

	// The address of a BMC names a machine on a network, like a host name.
	"bmc.host": hostName, "bmc.observed_at": keep, "bmc.address": hostName, "bmc.manager": keep, "bmc.firmware": keep,
	"bmc.bios_version": keep, "bmc.redfish_version": keep, "bmc.bios_registry": keep, "bmc.tls": keep, "bmc.auth": keep,
	"bmc.last_reset": keep, "bmc.classes": keep, "bmc.logs": freeText, "bmc.notes": freeText, "bmc.source": keep,
	"bmc.raw_ref": freeText, "bmc.fingerprint": derived,

	"bmc_memory.host": hostName, "bmc_memory.observed_at": keep, "bmc_memory.label": keep, "bmc_memory.slot": keep,
	"bmc_memory.device_locator": keep, "bmc_memory.service_label": keep, "bmc_memory.resource_id": keep, "bmc_memory.state": keep,
	"bmc_memory.health": keep, "bmc_memory.error_correction": keep, "bmc_memory.allowed_speeds": keep,
	"bmc_memory.device_width_source": keep, "bmc_memory.metrics": keep, "bmc_memory.current_since": keep, "bmc_memory.source": keep,
	"bmc_memory.raw_ref": freeText, "bmc_memory.raw": freeText, "bmc_memory.fingerprint": derived,

	"boot.host": hostName, "boot.boot_id": bootID, "boot.started_at": keep, "boot.start_quality": keep, "boot.last_seen": keep,
	"boot.kernel": keep, "boot.bios": keep, "boot.bmc": keep, "boot.cmdline": commandLine, "boot.source": keep, "boot.raw_ref": freeText,
	"boot.end": keep, "boot.end_at": keep, "boot.end_ref": keep, "boot.end_source": keep,

	// What preflight read names files and quotes them: the kernel command
	// line among them.
	"capture_check.host": hostName, "capture_check.name": keep, "capture_check.state": keep,
	"capture_check.detail": commandLine, "capture_check.evidence": commandLine,

	"counter_sample.host": hostName, "counter_sample.observed_at": keep, "counter_sample.mc_name": keep, "counter_sample.location": keep,
	"counter_sample.label": keep, "counter_sample.bank_locator": keep, "counter_sample.reset_at": keep, "counter_sample.mem_type": keep,
	"counter_sample.dev_type": keep, "counter_sample.edac_mode": keep, "counter_sample.source": keep, "counter_sample.raw_ref": freeText,

	"dimm.dimm_uid": dimmUID, "dimm.identity": keep, "dimm.manufacturer": keep, "dimm.part_number": keep, "dimm.serial": keep,
	"dimm.manufacturer_id": keep, "dimm.memory_type": keep, "dimm.form_factor": keep, "dimm.module_type": keep, "dimm.technology": keep,
	"dimm.mfg_date": keep, "dimm.first_seen": keep, "dimm.last_seen": keep,

	"ecs_sample.host": hostName, "ecs_sample.observed_at": keep, "ecs_sample.time_quality": keep, "ecs_sample.label": keep,
	"ecs_sample.slot": keep, "ecs_sample.dimm_uid": dimmUID, "ecs_sample.boot_id": bootID, "ecs_sample.source": keep,
	"ecs_sample.raw_ref": freeText, "ecs_sample.raw": freeText, "ecs_sample.fingerprint": derived,

	"event.host": hostName, "event.kind": keep, "event.ErrorDT": keep, "event.ErrorSeverity": keep, "event.ErrorScrubType": keep,
	"event.ErrorVector": keep, "event.source": keep, "event.raw_ref": freeText, "event.grade": keep, "event.boot_id": bootID,
	"event.dimm_uid": dimmUID, "event.dimm_link": keep, "event.dup_fingerprint": derived, "event.time_quality": keep,
	"event.window_start": keep, "event.edac_location": keep, "event.label": keep, "event.slot": keep, "event.error_type": keep,
	"event.decoder": keep, "event.fingerprint": derived, "event.read_boot_id": bootID, "event.not_before": keep,
	"event.cause_fingerprint": derived,

	"event_raw.text": freeText,

	"fault.host": hostName, "fault.dimm_uid": dimmUID, "fault.slot": keep, "fault.mode": keep, "fault.grade": keep,
	"fault.first_seen": keep, "fault.last_seen": keep, "fault.engine_version": keep,

	// A firmware record keeps the vendor's words: the place as written, the
	// arguments and the text may name a host or an address.
	"fw_record.host": hostName, "fw_record.at": keep, "fw_record.common": keep, "fw_record.flags": keep, "fw_record.clears": keep,
	"fw_record.vendor": keep, "fw_record.vendor_id": keep, "fw_record.registry": keep, "fw_record.registry_version": keep,
	"fw_record.vendor_severity": keep, "fw_record.origin": keep, "fw_record.log": keep, "fw_record.location": freeText,
	"fw_record.args": freeText, "fw_record.text": freeText, "fw_record.last_at": keep, "fw_record.entry_key": keep,
	"fw_record.mapping": keep, "fw_record.map_ref": keep, "fw_record.basis": keep, "fw_record.source": keep,
	"fw_record.raw_ref": freeText, "fw_record.fingerprint": derived, "fw_record.raw": freeText,

	"gpu_mem.host": hostName, "gpu_mem.observed_at": keep, "gpu_mem.kind": keep, "gpu_mem.gpu_serial": keep, "gpu_mem.gpu_uuid": keep,
	"gpu_mem.pci_bus": keep, "gpu_mem.model": keep, "gpu_mem.xid_text": freeText, "gpu_mem.boot_id": bootID, "gpu_mem.source": keep,
	"gpu_mem.raw_ref": freeText, "gpu_mem.raw": freeText, "gpu_mem.fingerprint": derived, "gpu_mem.time_quality": keep,

	"host.name": hostName, "host.arch": keep, "host.system_vendor": keep, "host.system_product": keep, "host.board": keep, "host.virtual": keep,
	"host.first_seen": keep, "host.last_seen": keep,

	"meta.key": freeText, "meta.value": freeText,

	"placement.dimm_uid": dimmUID, "placement.host": hostName, "placement.slot": keep, "placement.first_seen": keep,
	"placement.last_seen": keep, "placement.source": keep,

	"host_reading.host": hostName, "host_reading.kind": keep, "host_reading.key": keep, "host_reading.taken_at": keep,
	"host_reading.origin": freeText, "host_reading.text": freeText, "host.cpu": keep,
	"host.cpu_signature": keep, "host.cpu_present": keep,
	"registry_seen.host": hostName, "registry_seen.seen_at": keep, "registry_seen.prefix": keep, "registry_seen.version": keep,
	"registry_seen.owner": keep, "registry_seen.language": keep, "registry_seen.unmapped": keep, "registry_seen.source": freeText,

	"scan.host": hostName, "scan.kind": keep, "scan.started_at": keep, "scan.finished_at": keep, "scan.tool_version": keep, "scan.note": freeText,

	"slot.host": hostName, "slot.name": keep, "slot.locator": keep, "slot.bank_locator": keep, "slot.edac_location": keep,
	"slot.edac_label": keep, "slot.mapping": keep,

	"slot_alias.host": hostName, "slot_alias.alias": keep, "slot_alias.slot": keep,

	"source.kind": keep, "source.origin": freeText, "source.status": keep, "source.detail": freeText, "source.first_ts": keep,
	"source.last_ts": keep, "source.notes": freeText,

	"unread_record.ref": freeText, "unread_record.reason": freeText, "unread_record.text": freeText,

	"verdict.host": hostName, "verdict.dimm_uid": dimmUID, "verdict.slot": keep, "verdict.grade": keep, "verdict.rules": keep,
	"verdict.evidence": freeText, "verdict.counter_evidence": freeText, "verdict.missing": freeText, "verdict.next_action": freeText,
	"verdict.engine_version": keep, "verdict.computed_at": keep, "verdict.gpu": keep, "verdict.confidence_factors": freeText,
}

// plainNames are host names that name no host in particular. They stay.
var plainNames = map[string]bool{"localhost": true, "localhost.localdomain": true, "": true}

type anonymizer struct {
	key []byte
	// names maps host names of the ledger to their words.
	names map[string]string
	// tokens are what is looked for in text: the names, and the first
	// label of a name that is fully qualified. Longest first.
	tokens []token
	// addresses collects the addresses that were replaced, for the count.
	addresses map[string]string
	// users collects the names of home directories that were replaced.
	users map[string]string
}

type token struct {
	lower string
	word  string
}

func newAnonymizer(key []byte) (*anonymizer, error) {
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
	}
	return &anonymizer{key: key, names: map[string]string{}, addresses: map[string]string{}, users: map[string]string{}}, nil
}

// word returns prefix and n hexadecimal digits of the keyed hash of value.
func (a *anonymizer) word(prefix, value string, n int) string {
	h := hmac.New(sha256.New, a.key)
	h.Write([]byte(prefix))
	h.Write([]byte{0})
	h.Write([]byte(value))
	return prefix + hex.EncodeToString(h.Sum(nil))[:n]
}

// host returns the word for a host name.
func (a *anonymizer) host(name string) string {
	if plainNames[strings.ToLower(name)] {
		return name
	}
	if w, ok := a.names[name]; ok {
		return w
	}
	// Names that differ in case only are one host.
	w := a.word("host-", strings.ToLower(name), 8)
	a.names[name] = w
	return w
}

// learn makes the tokens for the host names seen so far.
func (a *anonymizer) learn() {
	seen := map[string]bool{}
	a.tokens = nil
	for name, word := range a.names {
		lower := strings.ToLower(name)
		if !seen[lower] {
			seen[lower] = true
			a.tokens = append(a.tokens, token{lower, word})
		}
		// The short form of a fully qualified name appears in log lines.
		if short, _, ok := strings.Cut(lower, "."); ok && len(short) >= 3 && !seen[short] && strings.Trim(short, "0123456789") != "" {
			seen[short] = true
			a.tokens = append(a.tokens, token{short, word})
		}
	}
	sort.Slice(a.tokens, func(i, j int) bool {
		if len(a.tokens[i].lower) != len(a.tokens[j].lower) {
			return len(a.tokens[i].lower) > len(a.tokens[j].lower)
		}
		return a.tokens[i].lower < a.tokens[j].lower
	})
}

func wordChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// asciiLower lowers ASCII letters and keeps the length of the string, so
// that positions in the result are positions in the original.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// fileEndings are endings of file names. A dot and one of these after a
// host name is the name of a file, not the domain of the host.
var fileEndings = map[string]bool{
	"txt": true, "log": true, "db": true, "gz": true, "json": true, "xml": true, "dmesg": true, "out": true, "csv": true,
	"bin": true, "tar": true, "zip": true, "mycase": true, "sql": true, "bz2": true, "xz": true, "old": true, "bak": true,
}

// domainEnd returns where the domain that follows a host name at s[i:]
// ends: ".dc1.example.com" belongs to the name and goes with it.
func domainEnd(s string, i int) int {
	end := i
	for j := i; j < len(s) && s[j] == '.'; {
		k := j + 1
		for k < len(s) && (wordChar(s[k]) || s[k] == '-') {
			k++
		}
		if k == j+1 {
			break
		}
		end, j = k, k
	}
	if end == i {
		return i
	}
	// The last label of a domain is made of letters and is no file ending.
	last := s[strings.LastIndexByte(s[:end], '.')+1 : end]
	if strings.Trim(asciiLower(last), "abcdefghijklmnopqrstuvwxyz") != "" || fileEndings[asciiLower(last)] {
		return i
	}
	return end
}

// hosts replaces the host names in a text.
func (a *anonymizer) hosts(s string) string {
	for _, t := range a.tokens {
		lower := asciiLower(s)
		if !strings.Contains(lower, t.lower) {
			continue
		}
		var b strings.Builder
		at := 0
		for {
			i := strings.Index(lower[at:], t.lower)
			if i < 0 {
				break
			}
			i += at
			j := i + len(t.lower)
			// A name is a whole word: "n034" inside "n0345" is another name.
			if (i > 0 && wordChar(s[i-1])) || (j < len(s) && wordChar(s[j])) {
				b.WriteString(s[at:j])
				at = j
				continue
			}
			b.WriteString(s[at:i])
			b.WriteString(t.word)
			at = domainEnd(s, j)
		}
		b.WriteString(s[at:])
		s = b.String()
	}
	return s
}

var (
	ipv4RE = regexp.MustCompile(`(^|[^0-9A-Za-z.])((?:[0-9]{1,3}\.){3}[0-9]{1,3})(?:$|[^0-9A-Za-z.]|\.(?:$|[^0-9]))`)
	// Pairs of hexadecimal digits joined by colons or dashes: a hardware
	// address when there are six of them (or eight, or twenty).
	octetsRE = regexp.MustCompile(`[0-9A-Fa-f]{2}(?:[:-][0-9A-Fa-f]{2})+`)
	ipv6RE   = regexp.MustCompile(`[0-9A-Fa-f:]*:[0-9A-Fa-f:]*:[0-9A-Fa-f:.]*`)
	// Identifiers the ledger derives from the host name, where they are
	// quoted inside text.
	slotUIDRE = regexp.MustCompile(`\bs[0-9a-f]{19}\b`)
	derivedRE = regexp.MustCompile(`\bderived-[0-9a-f]{16}\b`)
	// The name of a home directory in a path: what follows "/home/" or
	// "/Users/" up to the end of that part of the path.
	homeRE = regexp.MustCompile(`(/home/|/Users/)([^/\\\s"'<>|:;,(){}\[\]]+)`)
	// userWordRE is the word a home directory name is replaced by.
	userWordRE = regexp.MustCompile(`^user-[0-9a-f]{8}$`)
)

// sharedHomes are directories beside the home directories that belong to
// no account.
var sharedHomes = map[string]bool{"Shared": true, "shared": true, "lost+found": true}

// replaceGroup replaces the second group of every match of re.
func replaceGroup(re *regexp.Regexp, s string, fn func(string) (string, bool)) string {
	var b strings.Builder
	at := 0
	for at <= len(s) {
		m := re.FindStringSubmatchIndex(s[at:])
		if m == nil {
			break
		}
		from, to := at+m[4], at+m[5]
		repl, ok := fn(s[from:to])
		b.WriteString(s[at:from])
		if ok {
			b.WriteString(repl)
		} else {
			b.WriteString(s[from:to])
		}
		at = to
	}
	b.WriteString(s[at:])
	return b.String()
}

// address returns the word for a network address.
func (a *anonymizer) address(prefix, addr string) string {
	key := strings.ToLower(addr)
	if w, ok := a.addresses[key]; ok {
		return w
	}
	w := a.word(prefix, key, 8)
	a.addresses[key] = w
	return w
}

// user returns the word for the name of a home directory.
func (a *anonymizer) user(name string) string {
	if w, ok := a.users[name]; ok {
		return w
	}
	w := a.word("user-", name, 8)
	a.users[name] = w
	return w
}

// homes replaces the names of home directories in the paths of a text. A
// name that is already such a word stays, so that a text keeps its form
// when it is anonymized again with another key.
func (a *anonymizer) homes(s string) string {
	if !strings.Contains(s, "/home/") && !strings.Contains(s, "/Users/") {
		return s
	}
	return homeRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := homeRE.FindStringSubmatch(m)
		if sharedHomes[sub[2]] || userWordRE.MatchString(sub[2]) {
			return m
		}
		return sub[1] + a.user(sub[2])
	})
}

// text replaces host names, addresses and the names of home directories
// in free text.
func (a *anonymizer) text(s string) string {
	if s == "" {
		return s
	}
	s = a.homes(s)
	s = a.hosts(s)
	if strings.ContainsAny(s, ".:-") {
		s = a.mac(s)
		s = replaceGroup(ipv4RE, s, func(m string) (string, bool) {
			addr, err := netip.ParseAddr(m)
			if err != nil || !addr.Is4() {
				return "", false // 300.1.2.3 is a version number
			}
			return a.address("ip-", addr.String()), true
		})
		s = a.ipv6(s)
	}
	s = slotUIDRE.ReplaceAllStringFunc(s, a.uid)
	s = derivedRE.ReplaceAllStringFunc(s, a.boot)
	return s
}

// mac replaces the hardware addresses in a text: Ethernet (six octets),
// the form a network boot passes on the kernel command line ("01-" and six
// octets), and the longer addresses of other link types (eight or twenty
// octets). Three octets are a time of day and stay.
func (a *anonymizer) mac(s string) string {
	var b strings.Builder
	at := 0
	for _, m := range octetsRE.FindAllStringIndex(s, -1) {
		from, to := m[0], m[1]
		if from < at || (from > 0 && wordChar(s[from-1])) || (to < len(s) && wordChar(s[to])) {
			continue
		}
		switch n := (to - from + 1) / 3; {
		case n == 6, n == 8, n == 20:
		case n == 7 && strings.HasPrefix(s[from:to], "01-"):
		default:
			continue
		}
		b.WriteString(s[at:from])
		b.WriteString(a.address("mac-", s[from:to]))
		at = to
	}
	b.WriteString(s[at:])
	return b.String()
}

// ipv6 replaces the IPv6 addresses in a text.
func (a *anonymizer) ipv6(s string) string {
	var b strings.Builder
	at := 0
	for _, m := range ipv6RE.FindAllStringIndex(s, -1) {
		from, to := m[0], m[1]
		cand := strings.TrimRight(s[from:to], ".")
		to = from + len(cand)
		// A time (03:14:07), a PCI address or the "::" of a symbol name is
		// not an address: an address has "::" or eight groups, stands by
		// itself and holds a number.
		if !strings.Contains(cand, "::") && strings.Count(cand, ":") != 7 {
			continue
		}
		if (from > 0 && wordChar(s[from-1])) || (to < len(s) && wordChar(s[to])) || strings.Trim(cand, ":.") == "" {
			continue
		}
		addr, err := netip.ParseAddr(cand)
		if err != nil || !addr.Is6() {
			continue
		}
		b.WriteString(s[at:from])
		b.WriteString(a.address("ip6-", addr.String()))
		at = to
	}
	b.WriteString(s[at:])
	return b.String()
}

// What a kernel command line holds that names the machine's disks and the
// machine itself: UUIDs of file systems, partitions and encrypted volumes,
// the UUID of a software RAID array, and a machine ID.
var (
	diskUUIDRE  = regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`)
	raidUUIDRE  = regexp.MustCompile(`[0-9A-Fa-f]{8}(?::[0-9A-Fa-f]{8}){3}`)
	machineIDRE = regexp.MustCompile(`(machine[_-]id=)([0-9A-Fa-f]{32})\b`)
)

// cmdline anonymizes a kernel command line: what free text gets, and the
// identifiers of disks and of the machine. The same identifier gives the
// same word, so two boots of one host still show the same root.
func (a *anonymizer) cmdline(s string) string {
	if s == "" {
		return s
	}
	id := func(m string) string { return a.word("uuid-", asciiLower(m), 8) }
	s = diskUUIDRE.ReplaceAllStringFunc(s, id)
	s = raidUUIDRE.ReplaceAllStringFunc(s, id)
	s = machineIDRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := machineIDRE.FindStringSubmatch(m)
		return sub[1] + a.word("id-", asciiLower(sub[2]), 8)
	})
	return a.text(s)
}

// uid rewrites a DIMM identity. The identity of a DIMM with a serial
// number does not depend on the host and stays; a placeholder ("s" and
// 19 digits) is a hash over the host name.
func (a *anonymizer) uid(v string) string {
	if len(v) == 20 && v[0] == 's' {
		return a.word("s", v, 19)
	}
	return v
}

// boot rewrites a boot ID. A kernel boot ID is random and stays; a derived
// one is a hash over the host name.
func (a *anonymizer) boot(v string) string {
	if rest, ok := strings.CutPrefix(v, "derived-"); ok {
		return a.word("derived-", rest, 16)
	}
	return v
}

// apply returns the anonymized form of a value.
func (a *anonymizer) apply(t treatment, v string) string {
	switch t {
	case hostName:
		return a.host(v)
	case freeText:
		return a.text(v)
	case commandLine:
		return a.cmdline(v)
	case derived:
		if v == "" {
			return v
		}
		return a.word("", v, 32)
	case bootID:
		return a.boot(v)
	case dimmUID:
		return a.uid(v)
	}
	return v
}

// textColumns lists the text columns of a table.
func textColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, type FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		if strings.EqualFold(typ, "TEXT") || typ == "" {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

// treatmentOf returns how a column is treated. A text column the table
// above does not know is free text.
func treatmentOf(table, column string) treatment {
	if t, ok := columns[table+"."+column]; ok {
		return t
	}
	return freeText
}

// run anonymizes the ledger file at path in place. The file is the copy
// that goes into the bundle, never the ledger of the sender.
func (a *anonymizer) run(ctx context.Context, path string) error {
	db, err := openLedger(path, "rw")
	if err != nil {
		return err
	}
	defer db.Close()
	// Identities change in several tables at once.
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	names, err := tableNames(ctx, db)
	if err != nil {
		return err
	}

	// The host names first: the text of every table is searched for them.
	type target struct {
		table   string
		columns []string
		how     []treatment
	}
	var targets []target
	for _, table := range names {
		cols, err := textColumns(ctx, db, table)
		if err != nil {
			return err
		}
		t := target{table: table}
		for _, c := range cols {
			how := treatmentOf(table, c)
			if how == keep {
				continue
			}
			t.columns, t.how = append(t.columns, c), append(t.how, how)
			if how != hostName {
				continue
			}
			rows, err := db.QueryContext(ctx, `SELECT DISTINCT `+quote(c)+` FROM `+quote(table)+` WHERE `+quote(c)+` IS NOT NULL`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					rows.Close()
					return err
				}
				a.host(name)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		if len(t.columns) > 0 {
			targets = append(targets, t)
		}
	}
	a.learn()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Tables with a row ID are rewritten row by row. The others are
		// small and keyed by their text: they are rewritten value by value.
		if _, err := tx.ExecContext(ctx, `SELECT rowid FROM `+quote(t.table)+` LIMIT 0`); err != nil {
			err = a.byValue(ctx, tx, t.table, t.columns, t.how)
		} else {
			err = a.byRow(ctx, tx, t.table, t.columns, t.how)
		}
		if err != nil {
			return fmt.Errorf("table %s: %w", t.table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// What was replaced must not stay behind in free pages of the file.
	_, err = db.ExecContext(ctx, `VACUUM`)
	return err
}

func (a *anonymizer) byValue(ctx context.Context, tx *sql.Tx, table string, cols []string, how []treatment) error {
	for i, c := range cols {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT `+quote(c)+` FROM `+quote(table)+` WHERE `+quote(c)+` IS NOT NULL AND `+quote(c)+` != ''`)
		if err != nil {
			return err
		}
		var old []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			old = append(old, v)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, v := range old {
			if nv := a.apply(how[i], v); nv != v {
				if _, err := tx.ExecContext(ctx, `UPDATE `+quote(table)+` SET `+quote(c)+` = ? WHERE `+quote(c)+` = ?`, nv, v); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (a *anonymizer) byRow(ctx context.Context, tx *sql.Tx, table string, cols []string, how []treatment) error {
	quoted := make([]string, len(cols))
	sets := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quote(c)
		sets[i] = quote(c) + ` = ?`
	}
	sel := `SELECT rowid, ` + strings.Join(quoted, `, `) + ` FROM ` + quote(table) + ` WHERE rowid > ? ORDER BY rowid LIMIT 2000`
	upd, err := tx.PrepareContext(ctx, `UPDATE `+quote(table)+` SET `+strings.Join(sets, `, `)+` WHERE rowid = ?`)
	if err != nil {
		return err
	}
	defer upd.Close()
	type change struct {
		id   int64
		vals []any
	}
	last := int64(-1 << 62)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, sel, last)
		if err != nil {
			return err
		}
		var changes []change
		n := 0
		for rows.Next() {
			var id int64
			vals := make([]sql.NullString, len(cols))
			dest := make([]any, 0, len(cols)+1)
			dest = append(dest, &id)
			for i := range vals {
				dest = append(dest, &vals[i])
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return err
			}
			n++
			last = id
			changed := false
			out := make([]any, len(cols)+1)
			for i, v := range vals {
				if !v.Valid {
					out[i] = nil
					continue
				}
				nv := a.apply(how[i], v.String)
				changed = changed || nv != v.String
				out[i] = nv
			}
			if changed {
				out[len(cols)] = id
				changes = append(changes, change{id, out})
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, ch := range changes {
			if _, err := upd.ExecContext(ctx, ch.vals...); err != nil {
				return err
			}
		}
		if n == 0 {
			return nil
		}
	}
}
