package report

import (
	"encoding/json"
	"strings"

	"github.com/agenticode/my/internal/fmfmlog"
)

// fmfmLines writes the error records of the report with FMFM field names,
// one JSON object per line.
func (doc *Document) fmfmLines() (string, int, error) {
	var b strings.Builder
	n := 0
	for _, r := range doc.Records {
		if r.Event == nil {
			continue
		}
		line, err := json.Marshal(fmfmlog.RuntimeOf(r.Event))
		if err != nil {
			return "", 0, err
		}
		b.Write(line)
		b.WriteByte('\n')
		n++
	}
	return b.String(), n, nil
}
