package view

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
)

// GPUs answers, for the memory of the GPUs of a host, the question the
// driver lets one answer: how much room for repair is left.
//
// A GPU does not say where in its memory an error was. What it reports is
// a budget. Every bank of its memory has spare rows; a row that fails is
// remapped to one of them, and when a bank has none left the next failure
// there cannot be repaired. The view shows the rows remapped so far,
// whether a remapping is pending or failed, how many banks still have how
// many spare rows, and the Xid records of the kernel log between the
// readings.
type GPUs struct {
	Host string `json:"host"`
	// AsOf is where the Xid window ends: the reference time of the host,
	// or the time of the latest GPU record when that is later.
	AsOf time.Time `json:"as_of,omitzero"`
	// Days is the length of the window the Xid records are counted over.
	Days int `json:"days"`
	// Grade is the worst finding among the GPUs shown.
	Grade     model.VerdictGrade `json:"grade"`
	GradeText string             `json:"grade_text"`
	// GPUs lists the GPUs with memory records, the one with the least
	// room first.
	GPUs []GPU `json:"gpus"`
	// Notes say what a reader has to know about the data: sources that
	// could not be read, GPUs that could not be keyed.
	Notes []Text `json:"notes,omitzero"`
	// Detail is set when the reader named one GPU: its records are listed.
	Detail bool `json:"detail,omitzero"`
}

// GPU states: where the memory of a GPU stands, the worst first. A state
// describes the budget; the finding beside it is what a rule of the engine
// says, and only a failed repair has a rule.
const (
	// GPUFailed: a row remapping failed. Nothing is left to repair with.
	GPUFailed = "failed"
	// GPUPending: a remapping or a page retirement waits for a GPU reset.
	GPUPending = "pending"
	// GPULow: a bank has one spare row left, or none.
	GPULow = "low"
	// GPURepaired: rows were remapped or pages retired, and room is left.
	GPURepaired = "repaired"
	// GPUErrors: memory errors on record and no repair on record.
	GPUErrors = "errors"
	// GPUQuiet: records, and no error among them.
	GPUQuiet = "quiet"
)

// gpuStateRank orders the states for the list.
var gpuStateRank = map[string]int{GPUFailed: 0, GPUPending: 1, GPULow: 2, GPURepaired: 3, GPUErrors: 4, GPUQuiet: 5}

// GPU is the memory of one GPU.
type GPU struct {
	N int `json:"n"`
	// Key names the GPU as the findings do: its serial number, else its
	// UUID, else its PCI address.
	Key    string         `json:"gpu"`
	Index  model.Opt[int] `json:"index,omitzero"`
	Serial string         `json:"serial,omitzero"`
	UUID   string         `json:"uuid,omitzero"`
	PCIBus string         `json:"pci_bus,omitzero"`
	Model  string         `json:"model,omitzero"`

	State     string `json:"state"`
	StateText string `json:"state_text"`
	// Grade is the finding of the engine for the GPU, ok when no rule
	// fired, with the rules that fired and what they saw.
	Grade     model.VerdictGrade `json:"grade"`
	GradeText string             `json:"grade_text"`
	Rules     []string           `json:"rules,omitzero"`
	Evidence  []Text             `json:"evidence,omitzero"`
	// Next is the step that follows.
	Next *Text `json:"next,omitzero"`

	// ReadAt is the time of the latest reading of the state; zero for a
	// GPU that is known through Xid records alone.
	ReadAt time.Time `json:"read_at,omitzero"`
	// Remapped rows, by the kind of error that caused the remapping.
	RemapCorrectable   model.Opt[int]  `json:"remap_correctable,omitzero"`
	RemapUncorrectable model.Opt[int]  `json:"remap_uncorrectable,omitzero"`
	RemapPending       model.Opt[bool] `json:"remap_pending,omitzero"`
	RemapFailed        model.Opt[bool] `json:"remap_failed,omitzero"`
	// Banks is the bank remap availability histogram, when the GPU has one.
	Banks *GPUBanks `json:"banks,omitzero"`
	// Retired pages, on GPUs that retire pages and have no row remapping.
	RetiredSBE     model.Opt[int]  `json:"retired_sbe,omitzero"`
	RetiredDBE     model.Opt[int]  `json:"retired_dbe,omitzero"`
	RetiredPending model.Opt[bool] `json:"retired_pending,omitzero"`
	// ECC error counts of the frame buffer memory kept for the life of
	// the GPU, and since the driver was loaded.
	ECCEnabled                model.Opt[bool] `json:"ecc_enabled,omitzero"`
	DRAMCorrectable           model.Opt[int]  `json:"dram_correctable,omitzero"`
	DRAMUncorrectable         model.Opt[int]  `json:"dram_uncorrectable,omitzero"`
	DRAMCorrectableVolatile   model.Opt[int]  `json:"dram_correctable_volatile,omitzero"`
	DRAMUncorrectableVolatile model.Opt[int]  `json:"dram_uncorrectable_volatile,omitzero"`
	// SRAMThresholdExceeded is the flag of the driver that the
	// uncorrectable SRAM errors of the GPU passed the vendor's limit.
	SRAMThresholdExceeded model.Opt[bool] `json:"sram_threshold_exceeded,omitzero"`

	// XIDs counts the Xid records about memory within the window, by
	// number; XIDTotal counts those on record in all.
	XIDs     []XIDCount `json:"xids,omitzero"`
	XIDTotal int        `json:"xid_total"`
	// Events lists the latest Xid records, the newest first.
	Events []GPUEvent `json:"events,omitzero"`
	// Readings is the number of snapshots on record.
	Readings int `json:"readings"`
	// Notes say what a reader has to know about this GPU: values that
	// come from an earlier reading, a flag the list has no column for.
	Notes []Text `json:"notes,omitzero"`
}

// GPUBanks is the bank remap availability histogram: the number of banks
// by the spare rows they have left.
type GPUBanks struct {
	Max     int `json:"max"`
	High    int `json:"high"`
	Partial int `json:"partial"`
	Low     int `json:"low"`
	None    int `json:"none"`
	// ReadAt is the time of the reading the histogram comes from.
	ReadAt time.Time `json:"read_at,omitzero"`
}

// Total returns the number of banks.
func (b *GPUBanks) Total() int { return b.Max + b.High + b.Partial + b.Low + b.None }

// XIDCount is the number of Xid records of one number.
type XIDCount struct {
	XID   int    `json:"xid"`
	Count int    `json:"count"`
	Name  string `json:"name,omitzero"`
}

// GPUEvent is one Xid record.
type GPUEvent struct {
	At          time.Time         `json:"at"`
	TimeQuality model.TimeQuality `json:"time_quality,omitzero"`
	XID         int               `json:"xid"`
	Text        string            `json:"text,omitzero"`
	Record      model.RecordRef   `json:"record"`
}

// GPUOptions choose what the view shows.
type GPUOptions struct {
	// Days is the length of the Xid window; zero means GPUDays.
	Days int
	// Ref names one GPU: its index, its serial number or the end of it,
	// its UUID or its PCI address. Empty shows every GPU.
	Ref string
}

// GPUDays is the default length of the Xid window.
const GPUDays = 30

// maxGPUEvents bounds the Xid records listed for one GPU.
const maxGPUEvents = 12

// xidRemapFailed is the Xid the driver logs when a row remapping failed.
const xidRemapFailed = 64

// ErrNoSuchGPU: nothing in the host matches the name of a GPU.
var ErrNoSuchGPU = errors.New("no such GPU")

// GPUKey names a GPU the way the findings do: by its serial number, else
// by its UUID, its PCI address or its index.
func GPUKey(r *model.GPUMem) string {
	switch {
	case r.GPUSerial != "":
		return r.GPUSerial
	case r.GPUUUID != "":
		return r.GPUUUID
	case r.PCIBus != "":
		return r.PCIBus
	case r.GPUIndex.Valid:
		return fmt.Sprintf("GPU %d", r.GPUIndex.V)
	}
	return ""
}

// GPUs builds the view of the GPU memory of a host.
func (c *Case) GPUs(ctx context.Context, h *engine.HostResult, opts GPUOptions) (*GPUs, error) {
	recs, err := c.store.GPUMem(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	days := opts.Days
	if days <= 0 {
		days = GPUDays
	}
	// The window ends at the reference time of the host, or at the latest
	// GPU record when that is later: a reading of the GPU state is no error
	// record and does not move the reference time of the host.
	asOf := h.AsOf
	for i := range recs {
		if r := &recs[i]; r.TimeQuality != model.TimeRelative && r.ObservedAt.After(asOf) {
			asOf = r.ObservedAt
		}
	}
	v := &GPUs{Host: h.Host, AsOf: asOf, Days: days, Grade: model.VerdictOK, GPUs: []GPU{}}
	from := asOf.AddDate(0, 0, -days)

	by := map[string]*GPU{}
	var keys []string
	// latest holds, per GPU, the time of the latest snapshot that carries
	// each kind of value: a reading made with a query that asked for less
	// than the one before leaves the earlier values standing.
	type stamps struct{ remap, flags, retired, ecc time.Time }
	latest := map[string]*stamps{}
	xids := map[string]map[int]int{}
	for i := range recs {
		r := &recs[i]
		key := GPUKey(r)
		if key == "" {
			continue
		}
		g := by[key]
		if g == nil {
			g = &GPU{Key: key, Grade: model.VerdictOK}
			by[key], latest[key], xids[key] = g, &stamps{}, map[int]int{}
			keys = append(keys, key)
		}
		if r.GPUSerial != "" {
			g.Serial = r.GPUSerial
		}
		if r.GPUUUID != "" {
			g.UUID = r.GPUUUID
		}
		if r.PCIBus != "" {
			g.PCIBus = r.PCIBus
		}
		if r.Model != "" {
			g.Model = r.Model
		}
		if r.GPUIndex.Valid {
			g.Index = r.GPUIndex
		}
		switch r.Kind {
		case model.GPUXID:
			if !r.XID.Valid {
				continue
			}
			g.XIDTotal++
			if r.XID.V == xidRemapFailed {
				// The record of a failure stands whatever a reading says.
				g.RemapFailed = model.Some(true)
			}
			if r.TimeQuality == model.TimeRelative || !r.ObservedAt.Before(from) {
				xids[key][r.XID.V]++
			}
			g.Events = append(g.Events, GPUEvent{At: r.ObservedAt, TimeQuality: r.TimeQuality, XID: r.XID.V, Text: r.XIDText,
				Record: model.RecordRef{Kind: model.RecordGPU, ID: r.ID, RawRef: r.RawRef}})
		case model.GPUSnapshot:
			g.Readings++
			st := latest[key]
			at := r.ObservedAt
			if !at.Before(g.ReadAt) {
				g.ReadAt = at
			}
			if (r.RemapCorrectable.Valid || r.RemapUncorrectable.Valid) && !at.Before(st.remap) {
				g.RemapCorrectable, g.RemapUncorrectable, st.remap = r.RemapCorrectable, r.RemapUncorrectable, at
			}
			if (r.RemapPending.Valid || r.RemapFailure.Valid) && !at.Before(st.flags) {
				g.RemapPending, st.flags = r.RemapPending, at
				// A failure stands for the life of the GPU.
				if r.RemapFailure.Valid && !g.RemapFailed.V {
					g.RemapFailed = r.RemapFailure
				}
			}
			if hasBanks(r) && (g.Banks == nil || !at.Before(g.Banks.ReadAt)) {
				g.Banks = &GPUBanks{Max: r.BankMax.Or(0), High: r.BankHigh.Or(0), Partial: r.BankPartial.Or(0), Low: r.BankLow.Or(0), None: r.BankNone.Or(0), ReadAt: at}
			}
			if (r.RetiredSBE.Valid || r.RetiredDBE.Valid || r.RetiredPending.Valid) && !at.Before(st.retired) {
				g.RetiredSBE, g.RetiredDBE, g.RetiredPending, st.retired = r.RetiredSBE, r.RetiredDBE, r.RetiredPending, at
			}
			if (r.ECCEnabled.Valid || r.DRAMCorrectable.Valid || r.DRAMUncorrectable.Valid || r.DRAMCorrectableVolatile.Valid || r.DRAMUncorrectableVolatile.Valid) && !at.Before(st.ecc) {
				g.ECCEnabled, st.ecc = r.ECCEnabled, at
				g.DRAMCorrectable, g.DRAMUncorrectable = r.DRAMCorrectable, r.DRAMUncorrectable
				g.DRAMCorrectableVolatile, g.DRAMUncorrectableVolatile = r.DRAMCorrectableVolatile, r.DRAMUncorrectableVolatile
			}
			if r.SRAMThresholdExceeded.Valid && !(g.SRAMThresholdExceeded.Valid && g.SRAMThresholdExceeded.V) {
				g.SRAMThresholdExceeded = r.SRAMThresholdExceeded
			}
		}
	}

	findings := map[string]*engine.GPUResult{}
	for _, f := range h.GPUs {
		findings[f.Key] = f
	}
	for _, key := range keys {
		g := by[key]
		// Xid records, by number and the newest first.
		for n, count := range xids[key] {
			x := XIDCount{XID: n, Count: count}
			if code := "xid." + strconv.Itoa(n); Messages.Has(code) {
				x.Name = Messages.Text(c.Lang, code, nil)
			}
			g.XIDs = append(g.XIDs, x)
		}
		sort.Slice(g.XIDs, func(i, j int) bool { return g.XIDs[i].XID < g.XIDs[j].XID })
		sort.SliceStable(g.Events, func(i, j int) bool { return g.Events[i].At.After(g.Events[j].At) })
		if len(g.Events) > maxGPUEvents {
			g.Events = g.Events[:maxGPUEvents]
		}

		if f := findings[key]; f != nil && f.Verdict != nil {
			g.Grade, g.Rules = f.Verdict.Grade, f.Verdict.Rules
			for _, e := range f.Verdict.Evidence {
				t := c.statement(e.Statement)
				if e.Rule != "" {
					t.Text = "[" + e.Rule + "] " + t.Text
				}
				g.Evidence = append(g.Evidence, t)
			}
			if f.Verdict.NextAction.Code != "" {
				t := c.statement(f.Verdict.NextAction)
				g.Next = &t
			}
		}
		g.GradeText = engine.GradeText(c.Lang, g.Grade)
		g.State = gpuState(g, xids[key])
		g.StateText = Messages.Text(c.Lang, "gpu.state."+g.State, nil)
		c.gpuNotes(g, latest[key].remap, latest[key].flags)
		if g.Next == nil {
			if code := gpuNext(g); code != "" {
				t := c.say(code)
				g.Next = &t
			}
		}
	}

	// One GPU, when the reader named one.
	if ref := strings.TrimSpace(opts.Ref); ref != "" {
		var found []string
		for _, key := range keys {
			if gpuMatches(by[key], ref) {
				found = append(found, key)
			}
		}
		switch len(found) {
		case 0:
			return nil, fmt.Errorf("%w: %s", ErrNoSuchGPU, ref)
		case 1:
		default:
			return nil, fmt.Errorf("%w: %s matches %s", ErrAmbiguousDIMM, ref, strings.Join(found, ", "))
		}
		keys = found
		v.Detail = true
	}

	sort.SliceStable(keys, func(i, j int) bool {
		a, b := by[keys[i]], by[keys[j]]
		if ra, rb := gpuStateRank[a.State], gpuStateRank[b.State]; ra != rb {
			return ra < rb
		}
		if a.Index.Valid != b.Index.Valid {
			return a.Index.Valid
		}
		if a.Index.V != b.Index.V {
			return a.Index.V < b.Index.V
		}
		return a.Key < b.Key
	})
	for i, key := range keys {
		g := by[key]
		g.N = i + 1
		if g.Grade.ExitCode() > v.Grade.ExitCode() {
			v.Grade = g.Grade
		}
		v.GPUs = append(v.GPUs, *g)
	}
	v.GradeText = engine.GradeText(c.Lang, v.Grade)

	// What the sources of the latest scan said about GPU memory.
	scans, err := c.store.Scans(ctx)
	if err != nil {
		return nil, err
	}
	var scan *model.Scan
	for i := range scans {
		if s := &scans[i]; s.Host == h.Host && (scan == nil || s.StartedAt.After(scan.StartedAt) || (s.StartedAt.Equal(scan.StartedAt) && s.ID > scan.ID)) {
			scan = s
		}
	}
	if scan != nil {
		srcs, err := c.store.Sources(ctx, scan.ID)
		if err != nil {
			return nil, err
		}
		for _, s := range srcs {
			if s.Kind != "nvidia-smi" {
				continue
			}
			switch s.Status {
			case model.SourcePartial, model.SourceUnreadable, model.SourceError:
				v.Notes = append(v.Notes, c.missingSource(s))
			}
		}
	}
	return v, nil
}

func hasBanks(r *model.GPUMem) bool {
	return r.BankMax.Valid || r.BankHigh.Valid || r.BankPartial.Valid || r.BankLow.Valid || r.BankNone.Valid
}

// gpuState says where the memory of a GPU stands.
func gpuState(g *GPU, xids map[int]int) string {
	positive := func(n model.Opt[int]) bool { return n.Valid && n.V > 0 }
	yes := func(b model.Opt[bool]) bool { return b.Valid && b.V }
	switch {
	case g.Grade == model.VerdictReplaceNow || yes(g.RemapFailed):
		return GPUFailed
	case yes(g.RemapPending) || yes(g.RetiredPending):
		return GPUPending
	case g.Banks != nil && g.Banks.Low+g.Banks.None > 0:
		return GPULow
	case positive(g.RemapCorrectable) || positive(g.RemapUncorrectable) || positive(g.RetiredSBE) || positive(g.RetiredDBE):
		return GPURepaired
	case g.XIDTotal > 0 || positive(g.DRAMCorrectable) || positive(g.DRAMUncorrectable) || positive(g.DRAMCorrectableVolatile) || positive(g.DRAMUncorrectableVolatile):
		return GPUErrors
	}
	return GPUQuiet
}

// gpuNext names the step that follows for a state no rule speaks about.
func gpuNext(g *GPU) string {
	switch g.State {
	case GPUPending:
		return "gpu.next.pending"
	case GPULow:
		return "gpu.next.low"
	}
	return ""
}

// gpuNotes adds what the list has no column for.
func (c *Case) gpuNotes(g *GPU, remapAt, flagsAt time.Time) {
	day := func(t time.Time) string { return t.UTC().Format("2006-01-02") }
	if g.Readings == 0 {
		g.Notes = append(g.Notes, c.say("gpu.note.no_reading"))
	}
	// Values of an earlier reading stand where the latest did not ask.
	if !g.ReadAt.IsZero() {
		var old []string
		if !remapAt.IsZero() && remapAt.Before(g.ReadAt) {
			old = append(old, Messages.Text(c.Lang, "gpu.part.remap", nil)+" "+day(remapAt))
		}
		if !flagsAt.IsZero() && flagsAt.Before(g.ReadAt) && !flagsAt.Equal(remapAt) {
			old = append(old, Messages.Text(c.Lang, "gpu.part.flags", nil)+" "+day(flagsAt))
		}
		if g.Banks != nil && g.Banks.ReadAt.Before(g.ReadAt) {
			old = append(old, Messages.Text(c.Lang, "gpu.part.banks", nil)+" "+day(g.Banks.ReadAt))
		}
		if len(old) > 0 {
			g.Notes = append(g.Notes, c.say("gpu.note.older", "when", day(g.ReadAt), "parts", strings.Join(old, ", ")))
		}
	}
	if g.SRAMThresholdExceeded.Valid && g.SRAMThresholdExceeded.V {
		g.Notes = append(g.Notes, c.say("gpu.note.sram"))
	}
	if g.ECCEnabled.Valid && !g.ECCEnabled.V {
		g.Notes = append(g.Notes, c.say("gpu.note.no_ecc"))
	}
	if g.Readings > 0 && !g.RemapCorrectable.Valid && !g.RemapUncorrectable.Valid && g.Banks == nil && !g.RetiredSBE.Valid && !g.RetiredDBE.Valid &&
		!g.DRAMCorrectable.Valid && !g.DRAMUncorrectable.Valid && !g.RemapFailed.Valid && !g.RemapPending.Valid {
		g.Notes = append(g.Notes, c.say("gpu.note.no_data"))
	}
	if g.Serial == "" && g.UUID == "" {
		g.Notes = append(g.Notes, c.say("gpu.note.no_identity"))
	}
}

// gpuMatches reports whether a reader's name fits a GPU: its index, its
// serial number or the end of it, its UUID or its PCI address.
func gpuMatches(g *GPU, ref string) bool {
	low := strings.ToLower(ref)
	if n, err := strconv.Atoi(ref); err == nil && g.Index.Valid && g.Index.V == n {
		return true
	}
	if g.Serial != "" {
		s := strings.ToLower(g.Serial)
		suffix := strings.TrimLeft(low, ".")
		if s == low || (len(suffix) >= minSerialSuffix && strings.HasSuffix(s, suffix)) {
			return true
		}
	}
	if g.UUID != "" && (strings.EqualFold(g.UUID, ref) || strings.EqualFold(strings.TrimPrefix(g.UUID, "GPU-"), ref)) {
		return true
	}
	if g.PCIBus != "" {
		bus := strings.ToLower(g.PCIBus)
		if bus == low || strings.TrimSuffix(bus, ".0") == low || strings.TrimLeft(low, "0") == strings.TrimLeft(bus, "0") {
			return true
		}
	}
	return strings.EqualFold(g.Key, ref)
}
