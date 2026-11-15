package engine

import (
	"github.com/agenticode/my/internal/memras"
	"github.com/agenticode/my/internal/model"
)

// biosFacts is what the BIOS settings on record say about how to read the
// records of a host. They add lines to findings where they matter: a
// check when patrol scrub is off, a missing line when the firmware keeps
// corrected errors from the BMC. No setting changes a grade or a
// threshold.
type biosFacts struct {
	// patrolOff: patrol scrub is off. Errors a scrub would find are not
	// expected, and silence says less.
	patrolOff               bool
	patrolAttr, patrolValue string
	// ceOff: the firmware logs no corrected error to the BMC.
	ceOff           bool
	ceAttr, ceValue string
	// ceThreshold: the firmware logs the event of a corrected error
	// threshold to the BMC, which is fewer events than errors.
	ceThreshold                   bool
	thresholdAttr, thresholdValue string
}

// biosFactsOf reads the facts from the latest BIOS settings of a host, by
// the built-in settings profile.
func biosFactsOf(in *Input, host, manufacturer string) biosFacts {
	var own []model.BIOSSetting
	for i := range in.Settings {
		if in.Settings[i].Host == host {
			own = append(own, in.Settings[i])
		}
	}
	var b biosFacts
	if len(own) == 0 {
		return b
	}
	res := memras.Builtin().Check(memras.Input{Manufacturer: manufacturer, Settings: memras.Latest(own)})
	if it := res.Item(memras.PatrolScrub); it != nil && it.State == "off" {
		b.patrolOff, b.patrolAttr, b.patrolValue = true, it.Attribute, it.Value
	}
	if it := res.Item(memras.CELogging); it != nil && it.State == "off" {
		b.ceOff, b.ceAttr, b.ceValue = true, it.Attribute, it.Value
	}
	if it := res.Item(memras.CEThreshold); it != nil && it.State == "on" {
		b.ceThreshold, b.thresholdAttr, b.thresholdValue = true, it.Attribute, it.Value
	}
	return b
}
