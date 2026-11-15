package testutil

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	promName  = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	promLabel = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// Exposition is a parsed Prometheus text exposition.
type Exposition struct {
	// Series maps a series as it is written, name and labels, to its value.
	Series map[string]float64
	// Types maps a metric name to its type, and Help to its help text.
	Types map[string]string
	Help  map[string]string
	// Labels maps a metric name to the label names of its series.
	Labels map[string][]string
}

// ParseExposition reads the Prometheus text exposition format strictly,
// the way a Prometheus server does when it refuses a scrape: every metric
// has one HELP and one TYPE line before its series, the series of a metric
// stand together, no series appears twice, names and values are well
// formed, and every series of a metric has the same label names.
func ParseExposition(text string) (*Exposition, error) {
	x := &Exposition{Series: map[string]float64{}, Types: map[string]string{}, Help: map[string]string{}, Labels: map[string][]string{}}
	if text != "" && !strings.HasSuffix(text, "\n") {
		return nil, fmt.Errorf("the last line does not end with a line feed")
	}
	closed := map[string]bool{}
	current := ""
	open := func(name string, n int) error {
		if name == current {
			return nil
		}
		if closed[name] {
			return fmt.Errorf("line %d: the lines of %s do not stand together", n, name)
		}
		if current != "" {
			closed[current] = true
		}
		current = name
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	for i, line := range lines {
		n := i + 1
		switch {
		case line == "":
			return nil, fmt.Errorf("line %d: empty line", n)
		case strings.HasPrefix(line, "# HELP "), strings.HasPrefix(line, "# TYPE "):
			kind := line[2:6]
			name, rest, _ := strings.Cut(line[7:], " ")
			if !promName.MatchString(name) {
				return nil, fmt.Errorf("line %d: metric name %q", n, name)
			}
			if err := open(name, n); err != nil {
				return nil, err
			}
			into := x.Help
			if kind == "TYPE" {
				into = x.Types
				switch rest {
				case "counter", "gauge", "histogram", "summary", "untyped":
				default:
					return nil, fmt.Errorf("line %d: metric type %q", n, rest)
				}
			}
			if _, dup := into[name]; dup {
				return nil, fmt.Errorf("line %d: second %s line of %s", n, kind, name)
			}
			if len(x.Labels[name]) > 0 || hasSeries(x, name) {
				return nil, fmt.Errorf("line %d: %s line of %s after its series", n, kind, name)
			}
			into[name] = rest
		case strings.HasPrefix(line, "#"):
			return nil, fmt.Errorf("line %d: comment %q", n, line)
		default:
			name, labels, value, err := parseSample(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v: %q", n, err, line)
			}
			if err := open(name, n); err != nil {
				return nil, err
			}
			if x.Types[name] == "" || x.Help[name] == "" {
				return nil, fmt.Errorf("line %d: series of %s before its HELP and TYPE lines", n, name)
			}
			key := line[:strings.LastIndexByte(line, ' ')]
			if _, dup := x.Series[key]; dup {
				return nil, fmt.Errorf("line %d: series %s appears twice", n, key)
			}
			if prev, seen := x.Labels[name]; seen && strings.Join(prev, ",") != strings.Join(labels, ",") {
				return nil, fmt.Errorf("line %d: labels %v differ from %v of the series before", n, labels, prev)
			}
			x.Labels[name] = labels
			x.Series[key] = value
		}
	}
	return x, nil
}

func hasSeries(x *Exposition, name string) bool {
	for key := range x.Series {
		if key == name || strings.HasPrefix(key, name+"{") {
			return true
		}
	}
	return false
}

// parseSample reads one sample line: name, optional labels, value.
func parseSample(line string) (name string, labels []string, value float64, err error) {
	i := strings.IndexAny(line, "{ ")
	if i < 0 {
		return "", nil, 0, fmt.Errorf("no value")
	}
	name = line[:i]
	if !promName.MatchString(name) {
		return "", nil, 0, fmt.Errorf("metric name %q", name)
	}
	rest := line[i:]
	if rest[0] == '{' {
		rest = rest[1:]
		seen := map[string]bool{}
		for {
			eq := strings.IndexByte(rest, '=')
			if eq < 0 || len(rest) < eq+2 || rest[eq+1] != '"' {
				return "", nil, 0, fmt.Errorf("label without a quoted value")
			}
			label := rest[:eq]
			if !promLabel.MatchString(label) || strings.HasPrefix(label, "__") {
				return "", nil, 0, fmt.Errorf("label name %q", label)
			}
			if seen[label] {
				return "", nil, 0, fmt.Errorf("label %q twice", label)
			}
			seen[label] = true
			labels = append(labels, label)
			rest = rest[eq+2:]
			// The value runs to the next quote that is not escaped.
			end := -1
			for j := 0; j < len(rest); j++ {
				if rest[j] == '\\' {
					if j+1 >= len(rest) || !strings.ContainsRune(`\"n`, rune(rest[j+1])) {
						return "", nil, 0, fmt.Errorf("escape in the value of %q", label)
					}
					j++
					continue
				}
				if rest[j] == '"' {
					end = j
					break
				}
			}
			if end < 0 {
				return "", nil, 0, fmt.Errorf("value of %q is not closed", label)
			}
			rest = rest[end+1:]
			if strings.HasPrefix(rest, ",") {
				rest = rest[1:]
				continue
			}
			if strings.HasPrefix(rest, "}") {
				rest = rest[1:]
				break
			}
			return "", nil, 0, fmt.Errorf("text after the value of %q", label)
		}
	}
	if !strings.HasPrefix(rest, " ") || strings.ContainsAny(rest[1:], " \t") {
		return "", nil, 0, fmt.Errorf("want one space and a value after the series")
	}
	value, err = strconv.ParseFloat(rest[1:], 64)
	if err != nil {
		return "", nil, 0, fmt.Errorf("value %q", rest[1:])
	}
	return name, labels, value, nil
}

// MustParseExposition is ParseExposition for a test: text that a
// Prometheus server would refuse fails the test.
func MustParseExposition(tb testing.TB, text string) *Exposition {
	tb.Helper()
	x, err := ParseExposition(text)
	if err != nil {
		tb.Fatalf("not a valid exposition: %v\n%s", err, text)
	}
	return x
}
