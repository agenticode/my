package ingest

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"time"

	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// The SPD of a module is tied to a DIMM of the ledger on evidence only:
// its serial number equals the serial number of exactly one DIMM SMBIOS
// placed on the host, after the folding serialKey does, or equals it with
// the four bytes in reverse order (some firmware prints the serial that
// way). A serial that fits two DIMMs, a serial two readings of one scan
// carry (the same EEPROM seen behind two bus segments), a placeholder
// serial, or a host without SMBIOS serial numbers ties nothing. No reading
// is tied by bus address, slot order or part number: the kernel does not
// know in which slots the modules are.

// SPDDecode decodes an SPD reading of the ledger: the bytes, or the fields
// of decode-dimms text.
func SPDDecode(r model.SPDReading) spd.Decoded {
	if r.Form == model.SPDFormDecodeDimms {
		var m spd.DimmsModule
		if json.Unmarshal([]byte(r.Fields), &m) != nil {
			return spd.Decoded{State: spd.StateShort}
		}
		return m.Decoded()
	}
	if r.Raw == nil && !spd.Read(r.State) {
		return spd.Decoded{State: r.State}
	}
	return spd.Decode(r.Raw)
}

// spdSerialKeys returns the folded serial of a decode and the folded
// serial with its four bytes reversed.
func spdSerialKeys(serial string) (key, reversed string) {
	key = serialKey(serial)
	b, err := hex.DecodeString(serial)
	if err != nil || len(b) != 4 || key == "" {
		return key, ""
	}
	return key, serialKey(hex.EncodeToString([]byte{b[3], b[2], b[1], b[0]}))
}

var weekRE = regexp.MustCompile(`^(\d{4})-W(\d{2})$`)

// plausibleDate tells a date a DIMM row may take: of decoded bytes any
// date (BCD, week 1 to 53); of decode-dimms text only one that lies no
// later than the reading, since decode-dimms reads date bytes that are no
// BCD as binary numbers.
func plausibleDate(r model.SPDReading, date string) bool {
	if date == "" {
		return false
	}
	if r.Form != model.SPDFormDecodeDimms {
		return true
	}
	m := weekRE.FindStringSubmatch(date)
	if m == nil {
		return false
	}
	y, _ := strconv.Atoi(m[1])
	w, _ := strconv.Atoi(m[2])
	at := r.LastSeen
	if at.IsZero() {
		at = time.Now()
	}
	return w >= 1 && w <= 53 && y <= at.Year()
}

// tieSPD ties the SPD readings of a host to its DIMMs and writes what a
// tied reading in the state ok says into the DIMM row: the manufacturing
// week and the device width, and the fields SMBIOS left empty.
func tieSPD(ctx context.Context, s *store.Store, host string) error {
	readings, err := s.SPDReadings(ctx, host)
	if err != nil || len(readings) == 0 {
		return err
	}
	placements, err := s.Placements(ctx, host, "")
	if err != nil {
		return err
	}
	dimms, err := s.PlacedDIMMs(ctx, host)
	if err != nil {
		return err
	}
	byKey := map[string]map[string]bool{}
	for _, p := range placements {
		d, ok := dimms[p.DIMMUID]
		if p.Source != smbiosSource || !ok || d.Identity != model.IdentitySerial {
			continue
		}
		if k := serialKey(d.Serial); k != "" {
			if byKey[k] == nil {
				byKey[k] = map[string]bool{}
			}
			byKey[k][d.UID] = true
		}
	}
	one := func(k string) string {
		if len(byKey[k]) != 1 {
			return ""
		}
		for uid := range byKey[k] {
			return uid
		}
		return ""
	}
	// Serial numbers that two origins carry at the same time.
	type seen struct {
		at  time.Time
		key string
	}
	origins := map[seen]map[string]bool{}
	decoded := make([]spd.Decoded, len(readings))
	for i, r := range readings {
		decoded[i] = SPDDecode(r)
		k, _ := spdSerialKeys(decoded[i].Serial)
		if k == "" {
			continue
		}
		x := seen{r.LastSeen, k}
		if origins[x] == nil {
			origins[x] = map[string]bool{}
		}
		origins[x][r.Origin] = true
	}
	type fillOf struct {
		r model.SPDReading
		d spd.Decoded
	}
	latest := map[string]fillOf{}
	return s.Update(ctx, func(w *store.Writer) error {
		for i, r := range readings {
			d := decoded[i]
			uid, tie := "", ""
			k, rev := spdSerialKeys(d.Serial)
			if k != "" && len(origins[seen{r.LastSeen, k}]) == 1 {
				switch {
				case one(k) != "":
					uid, tie = one(k), model.SPDTieSerial
				case rev != "" && one(rev) != "":
					uid, tie = one(rev), model.SPDTieSerialReversed
				}
			}
			if uid != r.DIMMUID || tie != r.Tie {
				if err := w.TieSPD(r.ID, uid, tie); err != nil {
					return err
				}
			}
			if uid == "" || d.State != spd.StateOK {
				continue
			}
			if f, ok := latest[uid]; !ok || r.LastSeen.After(f.r.LastSeen) {
				latest[uid] = fillOf{r, d}
			}
		}
		for uid, f := range latest {
			date := ""
			if plausibleDate(f.r, f.d.Date) {
				date = f.d.Date
			}
			fill := model.DIMM{MemoryType: f.d.MemoryType, ModuleType: f.d.ModuleType, Ranks: f.d.PackageRanks, SpeedMTs: f.d.SpeedMTs,
				ManufacturerID: f.d.ModuleMaker}
			if f.d.SizeBytes.Valid {
				fill.SizeBytes = f.d.SizeBytes.V
			}
			if err := w.SetDIMMFromSPD(uid, date, f.d.DeviceWidth, fill); err != nil {
				return err
			}
		}
		return nil
	})
}
