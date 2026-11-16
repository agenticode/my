package engine

import "github.com/agenticode/my/internal/store"

// stopsByEvent returns the stops of in.Stops by the counted error record
// that stopped the host: the record before the kernel's panic line, or a
// carried uncorrected record the next boot read (store.Stops).
func (in *Input) stopsByEvent() map[int64]store.Stop {
	var out map[int64]store.Stop
	for _, st := range in.Stops {
		if st.EventID == 0 {
			continue
		}
		if out == nil {
			out = map[int64]store.Stop{}
		}
		out[st.EventID] = st
	}
	return out
}
