package explorer

// MapInput holds what the commands of the memory map printed, with the
// account they ran under. An empty text is a command that printed nothing,
// failed or was not run.
type MapInput struct {
	Dmidecode  string // dmidecode -t memory
	Numactl    string // numactl --hardware
	Numastat   string // numastat, whole: MemoryMap reads its first 20 lines
	EdacCE     string // the lines "dimm<N>:<count>" of the CE counts
	EdacUE     string // the same of the UE counts
	Lshw       string // lshw -c memory -c processor
	SysfsCache string // the lines "SOCKET:n LEVEL:n TYPE:t SIZE:s"

	// User is the account; "" counts as root.
	User string
	// Profile is what is known of the account profile of the host:
	// "" (nothing known), "operator", "developer" or "custom".
	Profile string
}

// MapResult is the memory map: slots, NUMA nodes and caches.
type MapResult struct {
	Dimms       []Dimm        `json:"dimms"`
	Numa        []NumaNode    `json:"numa"`
	CPUCaches   []CPUCache    `json:"cpuCaches"`
	Unsupported []Unsupported `json:"unsupported,omitempty"`
}

// Unsupported names a command the account could not run and why.
type Unsupported struct {
	FeatureID string `json:"featureId"`
	// Reason is a code: "root-only" (the profile developer or custom),
	// "not-supported" (another profile).
	Reason string `json:"reason"`
	// Profile is the profile the reason is given for.
	Profile string `json:"profile"`
	// SudoTried is set when the account is not root and sudo -n was tried.
	SudoTried bool `json:"sudoTried,omitempty"`
}

// supported tells what each account profile may run: dmidecode needs the operator profile, lshw runs partly for
// every profile. A host of which nothing is known may run all.
func supported(feature, profile string) bool {
	if profile == "" {
		return true
	}
	switch profile {
	case "operator":
		return true
	case "developer", "custom":
		return feature != "memory.dmidecode"
	}
	return false
}

func unsupportedReason(profile string) string {
	if profile == "developer" || profile == "custom" {
		return "root-only"
	}
	return "not-supported"
}

func isRootUser(user string) bool { return user == "root" || user == "" }

// MapCommands returns the commands the memory map runs for an account,
// in its order: dmidecode only when the profile allows it or the account
// is not root (then through sudo -n, as lshw).
func MapCommands(user, profile string) []string {
	prefix := ""
	if !isRootUser(user) {
		prefix = "sudo -n "
	}
	var cmds []string
	if supported("memory.dmidecode", profile) || !isRootUser(user) {
		cmds = append(cmds, prefix+"dmidecode -t memory")
	}
	return append(cmds, "numactl --hardware", "numastat", "edac dimm_ce_count", "edac dimm_ue_count",
		prefix+"lshw -c memory -c processor", "sysfs cache")
}

// headLines is head -n n of the text.
func headLines(text string, n int) string {
	lines := Lines(text)
	if len(lines) <= n {
		return text
	}
	out := ""
	for _, l := range lines[:n] {
		out += l + "\n"
	}
	return out
}

// MemoryMap puts the slots, the NUMA nodes and the cache sizes together:
// slots from dmidecode, from lshw when dmidecode gives none; cache sizes
// from lshw, from sysfs when sysfs has more packages.
func MemoryMap(in MapInput) MapResult {
	dmiOK := supported("memory.dmidecode", in.Profile)
	lshwOK := supported("memory.lshw", in.Profile)
	root := isRootUser(in.User)
	dmi := in.Dmidecode
	if !dmiOK && root {
		dmi = "" // not run
	}
	ce, ue := ParseEdacCounts(in.EdacCE), ParseEdacCounts(in.EdacUE)
	if on(FixEdacControllers) && (repeated(in.EdacCE) || repeated(in.EdacUE)) {
		// Counts of several controllers have one name per controller and
		// cannot be tied to slots by their order.
		ce, ue = Counts{}, Counts{}
	}
	dimms := ParseDmidecodeMemory(dmi, ce, ue)
	caches := ParseLshwCacheHierarchy(in.Lshw)
	if in.SysfsCache != "" {
		if sys := ParseSysfsCacheInfo(in.SysfsCache); len(sys) > len(caches) {
			caches = sys
		}
	}
	if len(dimms) == 0 && in.Lshw != "" {
		dimms = ParseLshwDimms(in.Lshw, ce, ue)
	}
	numa := ParseNumaInfo(in.Numactl, headLines(in.Numastat, 20))
	var un []Unsupported
	if !dmiOK && len(dimms) == 0 {
		un = append(un, Unsupported{FeatureID: "memory.dmidecode", Reason: unsupportedReason(in.Profile), Profile: in.Profile, SudoTried: !root})
	}
	if !lshwOK && len(caches) == 0 && len(dimms) == 0 {
		un = append(un, Unsupported{FeatureID: "memory.lshw", Reason: unsupportedReason(in.Profile), Profile: in.Profile})
	}
	return MapResult{Dimms: orEmpty(dimms), Numa: orEmpty(numa), CPUCaches: orEmpty(caches), Unsupported: un}
}

// orEmpty gives an empty list for nil.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// repeated tells EDAC count lines in which a name comes twice: the lines
// of more than one memory controller.
func repeated(raw string) bool {
	seen := map[string]bool{}
	for _, line := range Lines(raw) {
		if m, ok := reEdacLine.Match(line); ok {
			if seen[m.Group(1)] {
				return true
			}
			seen[m.Group(1)] = true
		}
	}
	return false
}
