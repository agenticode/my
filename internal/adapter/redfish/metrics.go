package redfish

import (
	"fmt"
	"strings"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// readings counts what was read of the modules as the BMC describes them.
type readings struct {
	modules   int
	counted   int // modules with at least one error count
	noLink    int // modules that link no metrics
	missing   int // modules whose metrics are linked and not in the dump
	failed    map[string]int
	ce, ue    int64 // lifetime counts, where given, else current
	withCE    int   // modules with a corrected error count above zero
	withAlarm int
	// used are the MemoryMetrics resources that belong to a module.
	used map[string]bool
}

// metricsState says what became of the MemoryMetrics resource a module
// links: "ok" with the resource, "not offered" when the module links none,
// what the manifest of a collection says when the resource did not come
// back, and "" when the dump lacks it and nothing says why.
func (r *run) metricsState(module, link string) (*resource, string) {
	if link == "" {
		// A dump can hold the metrics below the module without the link
		// that leads to them.
		if module != "" {
			if m, ok := r.metrics[module+"/MemoryMetrics"]; ok {
				return m, "ok"
			}
		}
		return nil, "not offered"
	}
	if m, ok := r.metrics[link]; ok {
		return m, "ok"
	}
	if status, _ := r.manifest.status(link); status != "" && status != "ok" {
		return nil, status
	}
	return nil, ""
}

// count reads an error count. A count is a whole number that is not below
// zero.
func count(o object, key string) model.Opt[int64] {
	if n, ok := integer(o, key); ok && n >= 0 {
		return model.Some(n)
	}
	return model.Opt[int64]{}
}

// reading records a populated module as the BMC describes it: its names,
// its state, its speeds, and the error counts of its MemoryMetrics.
func (r *run) reading(sink adapter.Sink, res *resource, c *readings) error {
	o := res.obj
	label := locator(o)
	status := child(o, "Status")
	m := &model.BMCMemory{ObservedAt: r.now, Label: label, DeviceLocator: str(o, "DeviceLocator"), ResourceID: str(o, "Id"),
		State: str(status, "State"), Health: str(status, "Health"), ErrorCorrection: str(o, "ErrorCorrection"),
		OperatingSpeed: optInt(o, "OperatingSpeedMhz"), RankSpare: optBool(o, "IsRankSpareEnabled"), SpareDevice: optBool(o, "IsSpareDeviceEnabled"),
		Source: Kind + "/memory", RawRef: res.ref}
	if pl := child(child(o, "Location"), "PartLocation"); pl != nil {
		m.ServiceLabel = str(pl, "ServiceLabel")
	}
	if speeds, ok := o["AllowedSpeedsMHz"].([]any); ok {
		for _, v := range speeds {
			if n, ok := integer(object{"v": v}, "v"); ok && n > 0 && n < 1<<31 && len(m.AllowedSpeeds) < 32 {
				m.AllowedSpeeds = append(m.AllowedSpeeds, int(n))
			}
		}
	}

	link := strings.TrimRight(str(child(o, "Metrics"), "@odata.id"), "/")
	mres, state := r.metricsState(strings.TrimRight(str(o, "@odata.id"), "/"), link)
	m.Metrics = state
	c.modules++
	switch {
	case mres != nil:
		if c.used == nil {
			c.used = map[string]bool{}
		}
		c.used[mres.id] = true
		mo := mres.obj
		cur, life := child(mo, "CurrentPeriod"), child(mo, "LifeTime")
		m.CECurrent, m.UECurrent = count(cur, "CorrectableECCErrorCount"), count(cur, "UncorrectableECCErrorCount")
		m.CELifetime, m.UELifetime = count(life, "CorrectableECCErrorCount"), count(life, "UncorrectableECCErrorCount")
		if alarms := child(child(mo, "HealthData"), "AlarmTrips"); alarms != nil {
			m.AlarmCE, m.AlarmUE = optBool(alarms, "CorrectableECCError"), optBool(alarms, "UncorrectableECCError")
		}
		m.Raw = raw(mo)
		if m.CECurrent.Valid || m.UECurrent.Valid {
			// Counts "since reset" started no earlier than the last reset.
			m.CurrentSince = r.lastReset
		}
		if m.HasCounts() {
			c.counted++
			ce, ue := m.CELifetime, m.UELifetime
			if !ce.Valid {
				ce = m.CECurrent
			}
			if !ue.Valid {
				ue = m.UECurrent
			}
			c.ce, c.ue = c.ce+ce.Or(0), c.ue+ue.Or(0)
			if ce.Or(0) > 0 {
				c.withCE++
			}
		}
		if m.AlarmCE.Or(false) || m.AlarmUE.Or(false) {
			c.withAlarm++
		}
	case state == "not offered":
		c.noLink++
	case state == "":
		c.missing++
	default:
		if c.failed == nil {
			c.failed = map[string]int{}
		}
		c.failed[state]++
	}
	m.Fingerprint = model.Fingerprint(Kind, "memory", model.NormalizeLabel(label), strings.ToUpper(str(o, "SerialNumber")), r.now.Format("2006-01-02T15:04:05Z"))
	return sink.BMCMemory(m)
}

// notes says in the report what the BMC's own error counts are and where
// they are missing.
func (c *readings) notes(r *run) {
	add := func(format string, a ...any) { r.rep.Notes = append(r.rep.Notes, fmt.Sprintf(format, a...)) }
	if c.counted > 0 {
		add("the BMC keeps error counts for %d of %d modules: %d corrected and %d uncorrected errors in all, %d modules with corrected errors; these counts are readings and are in no total", c.counted, c.modules, c.ce, c.ue, c.withCE)
	}
	if c.withAlarm > 0 {
		add("%d modules crossed an error threshold of the BMC", c.withAlarm)
	}
	if c.noLink > 0 {
		add("%d modules link no MemoryMetrics: this BMC keeps no error counts for them", c.noLink)
	}
	if c.missing > 0 {
		add("%d modules link MemoryMetrics that are not in the dump", c.missing)
	}
	for _, state := range []string{"unsupported", "denied", "timeout", "error"} {
		if n := c.failed[state]; n > 0 {
			add("the MemoryMetrics of %d modules did not come back from the BMC (%s)", n, state)
		}
	}
	if n := len(r.metrics) - len(c.used); n > 0 {
		add("%d MemoryMetrics resources belong to no module of the dump: their counts are not read", n)
	}
}
