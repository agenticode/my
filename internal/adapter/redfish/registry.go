package redfish

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/fwmap"
	"github.com/agenticode/my/internal/model"
)

// A message registry a BMC serves says, for each of its message IDs, the
// text, the severity and the number of arguments. A dump that holds the
// registry of an entry's MessageId tells the classifier how many arguments
// the message has, the severity of an entry that gives none, and, for a
// key no table names, a text the wording rules can read. What a registry
// covers is recorded per registry (registry_seen).

// Bounds of what is read of one registry.
const (
	maxRegistryBytes    = 16 << 20
	maxRegistryMessages = 20000
	maxRegistryText     = 2 << 10
)

// bmcRegistry is a message registry read from the dump.
type bmcRegistry struct {
	prefix, version, owner, language string
	messages                         map[string]fwmap.RegistryMessage
}

// isMessageRegistry tells a message registry: by its type, or by a prefix
// beside a map of messages.
func isMessageRegistry(o object) bool {
	if odataType(o) == "MessageRegistry" {
		return true
	}
	_, prefix := o["RegistryPrefix"].(string)
	_, msgs := o["Messages"].(object)
	return odataType(o) == "" && prefix && msgs
}

// readRegistries reads the message registries of the dump. A registry that
// cannot be read is noted and left out; the entries are then classed
// without it.
func (r *run) readRegistries() {
	for i := range r.msgRegistries {
		res := &r.msgRegistries[i]
		reg, err := parseRegistry(res.obj)
		if err != nil {
			r.rep.Notes = append(r.rep.Notes, fmt.Sprintf("the message registry %s was not read: %v", res.ref, err))
			continue
		}
		if r.msgRegs == nil {
			r.msgRegs = map[string][]*bmcRegistry{}
		}
		r.msgRegs[reg.prefix] = append(r.msgRegs[reg.prefix], reg)
	}
}

func parseRegistry(o object) (*bmcRegistry, error) {
	reg := &bmcRegistry{prefix: str(o, "RegistryPrefix"), version: str(o, "RegistryVersion"), owner: str(o, "OwningEntity"),
		language: str(o, "Language"), messages: map[string]fwmap.RegistryMessage{}}
	if reg.prefix == "" || len(reg.prefix) > 64 {
		return nil, fmt.Errorf("no RegistryPrefix")
	}
	msgs, ok := o["Messages"].(object)
	if !ok {
		return nil, fmt.Errorf("no Messages")
	}
	if len(msgs) > maxRegistryMessages {
		return nil, fmt.Errorf("%d messages, more than %d", len(msgs), maxRegistryMessages)
	}
	for k, v := range msgs {
		m, ok := v.(object)
		if !ok || k == "" || len(k) > 128 {
			continue
		}
		text := str(m, "Message")
		if len(text) > maxRegistryText {
			text = text[:maxRegistryText]
		}
		sev := str(m, "MessageSeverity")
		if sev == "" {
			sev = str(m, "Severity")
		}
		n := -1
		if v, ok := integer(m, "NumberOfArgs"); ok && v >= 0 && v <= 64 {
			n = int(v)
		}
		reg.messages[k] = fwmap.RegistryMessage{Text: text, Severity: sev, NArgs: n}
	}
	return reg, nil
}

// registryOf returns the message of a MessageId in the registry of the dump
// with its prefix: the one of the same major and minor version, else the
// newest one with the prefix.
func (r *run) registryOf(msgID string) *fwmap.RegistryMessage {
	prefix, version, key := fwmap.SplitMessageID(msgID)
	regs := r.msgRegs[prefix]
	if len(regs) == 0 || key == "" {
		return nil
	}
	pick := regs[len(regs)-1]
	for _, reg := range regs {
		if version != "" && strings.HasPrefix(reg.version+".", version+".") {
			pick = reg
			break
		}
	}
	m, ok := pick.messages[key]
	if !ok {
		return nil
	}
	return &m
}

// registriesSeen says for every registry of the dump how many of its
// messages are about memory and how many of those this version classes.
func (r *run) registriesSeen(sink adapter.Sink) error {
	c := fwmap.Current()
	var prefixes []string
	for p := range r.msgRegs {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	for _, p := range prefixes {
		for _, reg := range r.msgRegs[p] {
			seen := &model.RegistrySeen{SeenAt: r.now, Prefix: reg.prefix, Version: reg.version, Owner: reg.owner, Language: reg.language,
				Messages: len(reg.messages), Source: r.origin}
			keys := make([]string, 0, len(reg.messages))
			for k := range reg.messages {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				m := reg.messages[k]
				res := c.Classify(fwmap.Entry{MessageID: reg.prefix + "." + reg.version + "." + k, Registry: &m})
				if !res.Memory {
					continue
				}
				seen.Memory++
				switch res.Mapping {
				case model.MappingTable:
					seen.Mapped++
				case model.MappingRegistry, model.MappingWording:
					seen.Wording++
				default:
					if len(seen.Unmapped) < model.MaxRegistryUnmapped {
						seen.Unmapped = append(seen.Unmapped, k)
					}
				}
			}
			if err := sink.Registry(seen); err != nil {
				return err
			}
			r.rep.Notes = append(r.rep.Notes, fmt.Sprintf("message registry %s %s: %d messages, %d about memory, %d in a table, %d by wording",
				reg.prefix, reg.version, seen.Messages, seen.Memory, seen.Mapped, seen.Wording))
		}
	}
	return nil
}

// cperPlace reads the memory section a log entry carries in DiagnosticData,
// when it carries one: a whole CPER record, or one section of it.
func cperPlace(o object) (m cper.MemError, severity int, ok bool) {
	kind := str(o, "DiagnosticDataType")
	if kind != "CPER" && kind != "CPERSection" {
		return m, 0, false
	}
	text := str(o, "DiagnosticData")
	if text == "" || len(text) > 1<<20 {
		return m, 0, false
	}
	b, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return m, 0, false
	}
	if kind == "CPERSection" {
		if t := str(child(o, "CPER"), "SectionType"); t != "" && !cper.IsMemorySectionType(t) {
			return m, 0, false
		}
		mem, err := cper.ParseSection(b)
		return mem, -1, err == nil
	}
	sections, err := cper.ParseRecord(b)
	if err != nil {
		return m, 0, false
	}
	for _, s := range sections {
		if !s.Memory {
			continue
		}
		if mem, err := cper.ParseSection(s.Body); err == nil {
			return mem, s.Severity, true
		}
	}
	return m, 0, false
}
