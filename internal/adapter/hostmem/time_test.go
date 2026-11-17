package hostmem

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
)

// TestTimeOfTheReadings measures, on the running host, how long the
// readings take with the programs of the Memory Explorer and without them:
// 21 runs each, the median, the shortest and the longest. It runs only with
// MY_READINGS_TIME=1, as root (docs/performance.md).
func TestTimeOfTheReadings(t *testing.T) {
	if os.Getenv("MY_READINGS_TIME") != "1" {
		t.Skip("set MY_READINGS_TIME=1 to measure the readings of this host")
	}
	for _, programs := range []bool{true, false} {
		var ds []time.Duration
		var rep adapter.Report
		for range 21 {
			o := Options{Live: true}
			if !programs {
				o.Numactl, o.Numastat, o.Sar = "-", "-", "-"
			}
			start := time.Now()
			rep = New("/", o).Collect(context.Background(), &adapter.Collected{})
			ds = append(ds, time.Since(start))
		}
		slices.Sort(ds)
		t.Logf("programs %v: median %v, shortest %v, longest %v; %d readings, notes %q", programs, ds[10], ds[0], ds[20], rep.Records, rep.Notes)
	}
}
