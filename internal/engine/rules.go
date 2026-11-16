package engine

import "github.com/agenticode/my/internal/model"

// Rule IDs. They are stable: screens, reports and stored findings refer to
// rules by ID.
const (
	RuleUE         = "R-UE-01"    // an uncorrected error on the DIMM
	RuleFirmware   = "R-FW-01"    // BMC or BIOS marks the DIMM as failed or disabled
	RuleRepairGone = "R-REP-01"   // repair resources are used up
	RuleRow        = "R-ROW-03"   // a row, column, bank or wider fault is rising or spreading
	RuleOffline    = "R-OFF-02"   // the number of pages taken offline keeps growing
	RuleScrub      = "R-SCRUB-01" // the patrol scrubber found an uncorrected error
	RuleRecurred   = "R-REP-02"   // errors came back at the same place after a repair
	RuleSoft       = "R-SOFT-01"  // corrected errors without a growing fault
	RuleQuiet      = "R-QUIET-01" // quiet since a page offline or a repair
	RuleChannel    = "R-SYS-01"   // errors on two or more DIMMs of one channel
	RuleSlot       = "R-SYS-02"   // the DIMM was moved and the errors stayed in the slot
	RuleReseat     = "R-SYS-03"   // the errors stopped after a reseat
	RuleMapping    = "R-MAP-01"   // the slot names of different sources disagree
	RuleHostUE     = "R-HOST-01"  // uncorrected errors of the host that name no DIMM
	RuleHostCE     = "R-HOST-02"  // corrected errors or page offlines of the host that name no DIMM
	RuleLoopPlace  = "R-LOOP-01"  // uncorrected errors at one place of the DIMM in several boots
	RuleLoopDIMM   = "R-LOOP-02"  // uncorrected errors on the DIMM in several boots, at places not the same or not known
	RuleLoopSpread = "R-LOOP-03"  // uncorrected errors in several boots, spread over DIMMs
	RuleFWPredict  = "R-FW-02"    // firmware predicts a failure of the DIMM
	RuleFWTrain    = "R-FW-03"    // the DIMM failed training and shows no error of its own cells
)

// HostRule reports whether a rule speaks about a host as a whole and not
// about one DIMM or GPU.
func HostRule(id string) bool { return id == RuleHostUE || id == RuleHostCE }

// Rule describes one rule: its ID, the grade it gives, and the code of the
// message that states its condition (see Text).
type Rule struct {
	ID        string
	Grade     model.VerdictGrade
	Condition string
}

// Rules lists the rules by grade: replace now, consider replacement,
// observe, hold replacement. Within a finding the hold rules come first:
// when a cause outside the DIMM is suspected, what to check before pulling
// a DIMM has to be shown before anything else.
var Rules = []Rule{
	{RuleUE, model.VerdictReplaceNow, "rule.R-UE-01"},
	{RuleFirmware, model.VerdictReplaceNow, "rule.R-FW-01"},
	{RuleRepairGone, model.VerdictReplaceNow, "rule.R-REP-01"},
	{RuleLoopPlace, model.VerdictReplaceNow, "rule.R-LOOP-01"},
	{RuleLoopDIMM, model.VerdictReplaceNow, "rule.R-LOOP-02"},
	{RuleRow, model.VerdictConsiderReplacement, "rule.R-ROW-03"},
	{RuleOffline, model.VerdictConsiderReplacement, "rule.R-OFF-02"},
	{RuleScrub, model.VerdictConsiderReplacement, "rule.R-SCRUB-01"},
	{RuleRecurred, model.VerdictConsiderReplacement, "rule.R-REP-02"},
	{RuleFWPredict, model.VerdictConsiderReplacement, "rule.R-FW-02"},
	{RuleSoft, model.VerdictObserve, "rule.R-SOFT-01"},
	{RuleQuiet, model.VerdictObserve, "rule.R-QUIET-01"},
	{RuleHostCE, model.VerdictObserve, "rule.R-HOST-02"},
	{RuleChannel, model.VerdictHoldReplacement, "rule.R-SYS-01"},
	{RuleSlot, model.VerdictHoldReplacement, "rule.R-SYS-02"},
	{RuleReseat, model.VerdictHoldReplacement, "rule.R-SYS-03"},
	{RuleMapping, model.VerdictHoldReplacement, "rule.R-MAP-01"},
	{RuleHostUE, model.VerdictHoldReplacement, "rule.R-HOST-01"},
	{RuleLoopSpread, model.VerdictHoldReplacement, "rule.R-LOOP-03"},
	{RuleFWTrain, model.VerdictHoldReplacement, "rule.R-FW-03"},
}

// RuleByID returns the rule with the given ID.
func RuleByID(id string) (Rule, bool) {
	for _, r := range Rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// gradeRank orders grades for "which finding is worse": hold replacement
// outranks every other grade, then replace now, consider replacement,
// observe and ok. It is also the order of the process exit codes.
func gradeRank(g model.VerdictGrade) int { return g.ExitCode() }

// worse reports whether grade a outranks grade b.
func worse(a, b model.VerdictGrade) bool { return gradeRank(a) > gradeRank(b) }

// rulePosition orders the rules of a finding: by the rank of their grade,
// then in the order of Rules.
func rulePosition(id string) int {
	for i, r := range Rules {
		if r.ID == id {
			return (4-gradeRank(r.Grade))*100 + i
		}
	}
	return 1 << 20
}
