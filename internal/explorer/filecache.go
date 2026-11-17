package explorer

import (
	"strings"
)

// FileCache is one file in the page cache, as estimated.
type FileCache struct {
	Path           string `json:"path"`
	SizeBytes      Num    `json:"sizeBytes"`
	ResidentBytes  Num    `json:"residentBytes"`
	ResidencyRatio Num    `json:"residencyRatio"`
	FileType       string `json:"fileType"`
	Source         string `json:"source"`
}

var (
	reCacheField = jsre(`^(\S+):\s+(\d+)\s+kB`, "")
	reStatLine   = jsre(`^(\d+)\s+(.+)$`, "")
)

// InferFileType names the kind of a file by its path.
func InferFileType(path string) string {
	switch {
	case strings.Contains(path, ".log") || strings.Contains(path, "/log/"):
		return "log"
	case strings.Contains(path, ".jar") || strings.Contains(path, ".class"):
		return "java"
	case strings.Contains(path, "/docker/") || strings.Contains(path, "/overlay2/"):
		return "docker"
	case strings.HasSuffix(path, ".db") || strings.HasSuffix(path, ".sqlite"):
		return "database"
	case strings.HasSuffix(path, ".so") || strings.Contains(path, "/lib/"):
		return "library"
	}
	return "other"
}

// parseCacheFields reads the kB lines of meminfo the file cache estimate
// uses.
func parseCacheFields(raw string) Record {
	var r Record
	for _, line := range Lines(raw) {
		if m, ok := reCacheField.Match(line); ok {
			r.Set(m.Group(1), ParseInt(m.Group(2))*1024)
		}
	}
	return r
}

// ratio is a / b when b is above 0, else 0.
func ratio(a, b Num) Num {
	if b > 0 {
		return a / b
	}
	return 0
}

// ParseFincore reads "fincore --bytes --noheadings": four columns (pages,
// resident, size, file) or three, of which only size and file are read;
// files under 1 MiB are left out. The fincore
func ParseFincore(raw string) []FileCache {
	var out []FileCache
	for _, line := range Lines(raw) {
		parts := SplitSpace(Trim(line))
		if len(parts) < 3 {
			continue
		}
		path := parts[len(parts)-1]
		if path == "" || !strings.HasPrefix(path, "/") {
			continue
		}
		var size, res Num
		if len(parts) >= 4 {
			res = Or(ParseInt(parts[1]), 0)
			size = Or(ParseInt(parts[2]), 0)
		} else {
			size = Or(ParseInt(parts[0]), 0)
		}
		if size < 1048576 {
			continue
		}
		if len(out) > MaxLines {
			break
		}
		out = append(out, FileCache{Path: path, SizeBytes: size, ResidentBytes: res, ResidencyRatio: ratio(res, size),
			FileType: InferFileType(path), Source: "fincore"})
	}
	return out
}

// ParseStatSizes reads "stat -c '%s %n'" and estimates the cached part of
// each file of 1 MiB and more: at most its size, or 30 % of the page cache,
// times the share of active file pages.
func ParseStatSizes(raw string, totalCached, activeRatio Num) []FileCache {
	var out []FileCache
	for _, line := range Lines(raw) {
		m, ok := reStatLine.Match(line)
		if !ok {
			continue
		}
		size := Or(ParseInt(m.Group(1)), 0)
		if size < 1048576 {
			continue
		}
		path := Trim(m.Group(2))
		limit := size
		if totalCached > 0 {
			limit = totalCached * 0.3
		}
		maxRes := Num(minf(float64(size), float64(limit)))
		res := Num(Round(float64(maxRes * activeRatio)))
		if len(out) > MaxLines {
			break
		}
		out = append(out, FileCache{Path: path, SizeBytes: size, ResidentBytes: res, ResidencyRatio: ratio(res, size),
			FileType: InferFileType(path), Source: "estimate"})
	}
	return out
}

// ParseFindLarge reads "find ... -printf '%s %p\n'" and estimates half the
// share of active file pages of each file as cached.
func ParseFindLarge(raw string, activeRatio Num) []FileCache {
	var out []FileCache
	for _, line := range Lines(raw) {
		parts := SplitSpace(Trim(line))
		if len(parts) < 2 {
			continue
		}
		size := Or(ParseInt(parts[0]), 0)
		path := strings.Join(parts[1:], " ")
		if size <= 0 {
			continue
		}
		res := Num(Round(float64(size * activeRatio * 0.5)))
		if len(out) > MaxLines {
			break
		}
		out = append(out, FileCache{Path: path, SizeBytes: size, ResidentBytes: res, ResidencyRatio: ratio(res, size),
			FileType: InferFileType(path), Source: "estimate"})
	}
	return out
}

// FileCacheInput holds what the commands of the file cache estimate
// printed. Meminfo is /proc/meminfo whole: the estimate picks its lines.
type FileCacheInput struct {
	Load string
	// FdWalkDangerous is set when the host has more than 30,000
	// established TCP sessions (FdWalkDangerous of load.go).
	FdWalkDangerous bool
	Meminfo         string
	OpenFiles       string // the paths of open files, most often open first
	Fincore         string
	Stat            string
	Find            string
}

// FileCacheEstimate estimates the page cache per file: fincore, then stat
// with the active ratio, then find; under load, three sums of meminfo.
func FileCacheEstimate(in FileCacheInput) []FileCache {
	out := []FileCache{}
	if in.Load != "normal" || in.FdWalkDangerous {
		f := parseCacheFields(GrepPrefix(in.Meminfo, "Cached:", "Buffers:", "Active(file):", "Inactive(file):", "Dirty:", "Writeback:"))
		sum := func(path string, n Num) {
			out = append(out, FileCache{Path: path, SizeBytes: n, ResidentBytes: n, ResidencyRatio: 1, FileType: "summary", Source: "estimate"})
		}
		if c := f.Or0("Cached"); c != 0 {
			sum("(Total Page Cache)", c+Or(f.Or0("Buffers"), 0))
		}
		if a := f.Or0("Active(file)"); a != 0 {
			sum("(Active File Cache)", a)
		}
		if d := f.Or0("Dirty"); d != 0 {
			sum("(Dirty Pages)", d)
		}
		return out
	}
	var totalCached, activeFile Num
	for _, line := range Lines(GrepPrefix(in.Meminfo, "Cached:", "Buffers:", "Active(file):", "Inactive(file):")) {
		m, ok := reCacheField.Match(line)
		if !ok {
			continue
		}
		v := ParseInt(m.Group(2)) * 1024
		switch m.Group(1) {
		case "Cached":
			totalCached = v
		case "Active(file)":
			activeFile = v
		}
	}
	activeRatio := Num(0.3)
	if totalCached > 0 {
		activeRatio = Num(minf(float64(activeFile/totalCached), 1.0))
	}
	var open []string
	for _, l := range strings.Split(Trim(in.OpenFiles), "\n") {
		if l != "" {
			open = append(open, l)
		}
	}
	if len(open) > 0 {
		if Trim(in.Fincore) != "" {
			out = append(out, ParseFincore(in.Fincore)...)
		}
		if len(out) == 0 && in.Stat != "" {
			out = append(out, ParseStatSizes(in.Stat, totalCached, activeRatio)...)
		}
	}
	if len(out) == 0 && in.Find != "" {
		out = append(out, ParseFindLarge(in.Find, activeRatio)...)
	}
	return out
}

// OpenFilePaths are the files handed to fincore and stat: the
// first 50 of the list of open files.
func OpenFilePaths(openFiles string) []string {
	var out []string
	for _, l := range strings.Split(Trim(openFiles), "\n") {
		if l != "" && len(out) < 50 {
			out = append(out, l)
		}
	}
	return out
}

// GrepPrefix is grep -E '^(a|b|...)' of the file: the lines that start with
// one of the words, each with its newline.
func GrepPrefix(text string, words ...string) string {
	var b strings.Builder
	for _, l := range textLines(text) {
		for _, w := range words {
			if strings.HasPrefix(l, w) {
				b.WriteString(l + "\n")
				break
			}
		}
	}
	return b.String()
}

func minf(a, b float64) float64 {
	if a < b || a != a {
		return a
	}
	return b
}
