package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
	"time"
)

// Environment variables that make a run write profiles of itself, for the
// question why a command is slow or large on some input. Each names a file
// to write; "go tool pprof" reads them. docs/performance.md shows how.
const (
	envCPUProfile = "MY_CPUPROFILE"
	// The allocations of the whole run, written at its end.
	envMemProfile = "MY_MEMPROFILE"
	// The heap at the moment it was largest: what the peak memory of a
	// run is made of. The heap is looked at ten times a second, and the
	// profile is written again whenever it has grown by a tenth.
	envPeakProfile = "MY_PEAKPROFILE"
)

// startProfiles starts what the environment asks for and returns the
// function that finishes it. A profile that cannot be written is reported
// and the run goes on without it.
func startProfiles(getenv func(string) string, stderr io.Writer) (stop func()) {
	var cpu *os.File
	if path := getenv(envCPUProfile); path != "" {
		f, err := os.Create(path)
		if err == nil {
			err = pprof.StartCPUProfile(f)
		}
		if err != nil {
			fmt.Fprintf(stderr, "my: %s: %v\n", envCPUProfile, err)
			if f != nil {
				f.Close()
			}
		} else {
			cpu = f
		}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	if path := getenv(envPeakProfile); path != "" {
		go func() {
			defer close(finished)
			var largest uint64
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
				}
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapInuse < largest+largest/10 {
					continue
				}
				largest = m.HeapInuse
				f, err := os.Create(path)
				if err == nil {
					err = pprof.Lookup("heap").WriteTo(f, 0)
					if cerr := f.Close(); err == nil {
						err = cerr
					}
				}
				if err != nil {
					fmt.Fprintf(stderr, "my: %s: %v\n", envPeakProfile, err)
					return
				}
			}
		}()
	} else {
		close(finished)
	}
	return func() {
		close(done)
		<-finished
		if cpu != nil {
			pprof.StopCPUProfile()
			if err := cpu.Close(); err != nil {
				fmt.Fprintf(stderr, "my: %s: %v\n", envCPUProfile, err)
			}
		}
		if path := getenv(envMemProfile); path != "" {
			f, err := os.Create(path)
			if err == nil {
				// The profile counts what is live after a collection and
				// what was allocated in all.
				runtime.GC()
				err = pprof.Lookup("allocs").WriteTo(f, 0)
				if cerr := f.Close(); err == nil {
					err = cerr
				}
			}
			if err != nil {
				fmt.Fprintf(stderr, "my: %s: %v\n", envMemProfile, err)
			}
		}
	}
}
