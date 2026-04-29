package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultProfile = "default"

type Paths struct {
	MasterDir   string
	MasterConf  string
	MasterCreds string
	LiveConf    string
	LiveCreds   string
}

func defaultPaths() Paths {
	home, _ := os.UserHomeDir()
	return Paths{
		MasterDir:   filepath.Join(home, ".credswitch"),
		MasterConf:  filepath.Join(home, ".credswitch", "config"),
		MasterCreds: filepath.Join(home, ".credswitch", "credentials"),
		LiveConf:    filepath.Join(home, ".aws", "config"),
		LiveCreds:   filepath.Join(home, ".aws", "credentials"),
	}
}

type Profile struct {
	Name           string
	InMasterConfig bool
	InMasterCreds  bool
	InLiveConfig   bool
	InLiveCreds    bool
	// Enabled and Orphan are derived from the four flags above. Stored as
	// fields so the loop in `list` doesn't recompute on each access.
	Enabled bool
	Orphan  bool
}

type State struct {
	Profiles []Profile
}

func loadState(p Paths) (*State, error) {
	masterConf, err := loadOrEmpty(p.MasterConf, KindConfig)
	if err != nil {
		return nil, err
	}
	masterCreds, err := loadOrEmpty(p.MasterCreds, KindCreds)
	if err != nil {
		return nil, err
	}
	liveConf, err := loadOrEmpty(p.LiveConf, KindConfig)
	if err != nil {
		return nil, err
	}
	liveCreds, err := loadOrEmpty(p.LiveCreds, KindCreds)
	if err != nil {
		return nil, err
	}

	mc := nameSet(masterConf)
	mr := nameSet(masterCreds)
	lc := nameSet(liveConf)
	lr := nameSet(liveCreds)

	seen := map[string]bool{}
	var profiles []Profile

	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		prof := Profile{
			Name:           name,
			InMasterConfig: mc[name],
			InMasterCreds:  mr[name],
			InLiveConfig:   lc[name],
			InLiveCreds:    lr[name],
		}
		// Default is special: it's pass-through, never an orphan, always
		// considered enabled.
		if name == defaultProfile {
			prof.Enabled = true
		} else if !prof.InMasterConfig && !prof.InMasterCreds {
			prof.Orphan = true
			prof.Enabled = prof.InLiveConfig || prof.InLiveCreds
		} else {
			// Master-known: enabled iff every master file that contains it
			// also has it in the corresponding live file.
			enabled := true
			if prof.InMasterConfig && !prof.InLiveConfig {
				enabled = false
			}
			if prof.InMasterCreds && !prof.InLiveCreds {
				enabled = false
			}
			prof.Enabled = enabled
		}
		profiles = append(profiles, prof)
	}

	// Master-known profiles in master order.
	for _, s := range masterConf {
		add(s.Name)
	}
	for _, s := range masterCreds {
		add(s.Name)
	}
	// Default may exist only in live (rare); add it explicitly.
	if !seen[defaultProfile] && (lc[defaultProfile] || lr[defaultProfile]) {
		add(defaultProfile)
	}
	// Orphans — present in live, missing from master in both files.
	for _, s := range liveConf {
		if s.Name == defaultProfile {
			continue
		}
		if !mc[s.Name] && !mr[s.Name] {
			add(s.Name)
		}
	}
	for _, s := range liveCreds {
		if s.Name == defaultProfile {
			continue
		}
		if !mc[s.Name] && !mr[s.Name] {
			add(s.Name)
		}
	}
	return &State{Profiles: profiles}, nil
}

func loadOrEmpty(path string, kind FileKind) ([]Section, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	return parseFile(path, kind)
}

func nameSet(sections []Section) map[string]bool {
	m := map[string]bool{}
	for _, s := range sections {
		m[s.Name] = true
	}
	return m
}

type DriftKind int

const (
	DriftOrphan   DriftKind = iota // present in live, missing from master
	DriftModified                  // present in both, content differs
)

// Drift is one disagreement between a live file and the corresponding master
// file, scoped to a single section. A profile that drifts in both config and
// credentials produces two Drift entries.
type Drift struct {
	File   string // "config" or "credentials"
	Name   string // logical profile name
	Kind   DriftKind
	Live   Section
	Master Section // zero value when Kind == DriftOrphan
}

// computeDrift compares each live section against its master counterpart and
// returns one Drift entry per disagreement. Default is exempt — its content
// is allowed to differ (aws-cli writes there directly).
func computeDrift(liveConf, masterConf, liveCreds, masterCreds []Section) []Drift {
	var out []Drift
	out = append(out, fileDrift("config", liveConf, masterConf)...)
	out = append(out, fileDrift("credentials", liveCreds, masterCreds)...)
	return out
}

func fileDrift(file string, live, master []Section) []Drift {
	masterByName := map[string]Section{}
	for _, s := range master {
		masterByName[s.Name] = s
	}
	var out []Drift
	for _, ls := range live {
		if ls.Name == defaultProfile {
			continue
		}
		ms, present := masterByName[ls.Name]
		if !present {
			out = append(out, Drift{File: file, Name: ls.Name, Kind: DriftOrphan, Live: ls})
			continue
		}
		if !sectionsEqual(ls, ms) {
			out = append(out, Drift{File: file, Name: ls.Name, Kind: DriftModified, Live: ls, Master: ms})
		}
	}
	return out
}

// loadDrift is the entry point for callers that want drift but don't have
// master/live sections in hand already.
func loadDrift(p Paths) ([]Drift, error) {
	masterConf, err := loadOrEmpty(p.MasterConf, KindConfig)
	if err != nil {
		return nil, err
	}
	masterCreds, err := loadOrEmpty(p.MasterCreds, KindCreds)
	if err != nil {
		return nil, err
	}
	liveConf, err := loadOrEmpty(p.LiveConf, KindConfig)
	if err != nil {
		return nil, err
	}
	liveCreds, err := loadOrEmpty(p.LiveCreds, KindCreds)
	if err != nil {
		return nil, err
	}
	return computeDrift(liveConf, masterConf, liveCreds, masterCreds), nil
}

// formatDrift renders drift entries with a per-profile diff and the
// resolution options. Used for both error messages and `list` output.
func formatDrift(drift []Drift) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d profile change(s) in ~/.aws/ not reflected in master:\n\n", len(drift))
	for _, d := range drift {
		switch d.Kind {
		case DriftModified:
			fmt.Fprintf(&b, "  %s:%s (drifted)\n", d.File, d.Name)
			masterOnly, liveOnly := diffNormalized(d.Master, d.Live)
			for _, line := range masterOnly {
				fmt.Fprintf(&b, "    - master only:  %s\n", line)
			}
			for _, line := range liveOnly {
				fmt.Fprintf(&b, "    + live only:    %s\n", line)
			}
		case DriftOrphan:
			fmt.Fprintf(&b, "  %s:%s (orphan — only in live)\n", d.File, d.Name)
			for _, line := range normalizeSection(d.Live) {
				fmt.Fprintf(&b, "    + %s\n", line)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("To resolve any of these:\n")
	b.WriteString("  - keep live's version          ->  credswitch sync <name>\n")
	b.WriteString("  - drop from live entirely      ->  credswitch disable <name>\n")
	b.WriteString("  - take master's version (drift only)\n")
	b.WriteString("                                 ->  credswitch disable <name> && credswitch enable <name>")
	return b.String()
}

func diffNormalized(a, b Section) (aOnly, bOnly []string) {
	an := normalizeSection(a)
	bn := normalizeSection(b)
	aset := map[string]bool{}
	for _, s := range an {
		aset[s] = true
	}
	bset := map[string]bool{}
	for _, s := range bn {
		bset[s] = true
	}
	for _, s := range an {
		if !bset[s] {
			aOnly = append(aOnly, s)
		}
	}
	for _, s := range bn {
		if !aset[s] {
			bOnly = append(bOnly, s)
		}
	}
	return
}

// driftedNames returns the set of profile names that have a DriftModified
// entry. Used by `list` to annotate the main output.
func driftedNames(drift []Drift) map[string]bool {
	m := map[string]bool{}
	for _, d := range drift {
		if d.Kind == DriftModified {
			m[d.Name] = true
		}
	}
	return m
}

// enableProfile makes live mirror master for one profile, leaving every other
// section in live (including drifted and orphan ones) untouched. Refuses if
// the profile is currently in live with content drift, since enable would
// silently overwrite the live edits — the user must explicitly choose via
// disable+enable (master wins) or sync (live wins).
func enableProfile(p Paths, name string) error {
	prof, ok := findProfile(p, name)
	if !ok {
		return fmt.Errorf("profile %q not found in master or in ~/.aws/", name)
	}
	if prof.Orphan {
		return fmt.Errorf("profile %q exists in ~/.aws/ but not in master — run `credswitch sync %s` to add it to master, then enable", name, name)
	}
	masterConf, err := loadOrEmpty(p.MasterConf, KindConfig)
	if err != nil {
		return err
	}
	masterCreds, err := loadOrEmpty(p.MasterCreds, KindCreds)
	if err != nil {
		return err
	}
	liveConf, err := loadOrEmpty(p.LiveConf, KindConfig)
	if err != nil {
		return err
	}
	liveCreds, err := loadOrEmpty(p.LiveCreds, KindCreds)
	if err != nil {
		return err
	}

	// Block if this specific profile has modified drift in either live
	// file — enable would silently destroy the live edits.
	allDrift := computeDrift(liveConf, masterConf, liveCreds, masterCreds)
	var blocking []Drift
	for _, d := range allDrift {
		if d.Name == name && d.Kind == DriftModified {
			blocking = append(blocking, d)
		}
	}
	if len(blocking) > 0 {
		return fmt.Errorf("profile %q is currently in ~/.aws/ with edits not in master. Enabling would overwrite those edits.\n\n%s", name, formatDrift(blocking))
	}

	msc := findSection(masterConf, name)
	msr := findSection(masterCreds, name)
	if msc != nil {
		liveConf = upsertSection(liveConf, *msc)
	} else {
		liveConf = removeSection(liveConf, name)
	}
	if msr != nil {
		liveCreds = upsertSection(liveCreds, *msr)
	} else {
		liveCreds = removeSection(liveCreds, name)
	}

	if err := writeSections(p.LiveConf, liveConf); err != nil {
		return err
	}
	return writeSections(p.LiveCreds, liveCreds)
}

// disableProfile removes a profile from both live files. Other sections in
// live are untouched. Works on master-known and orphan profiles alike.
func disableProfile(p Paths, name string) error {
	if name == defaultProfile {
		return fmt.Errorf("cannot disable the default profile")
	}
	if _, ok := findProfile(p, name); !ok {
		return fmt.Errorf("profile %q not found in master or in ~/.aws/", name)
	}
	liveConf, err := loadOrEmpty(p.LiveConf, KindConfig)
	if err != nil {
		return err
	}
	liveCreds, err := loadOrEmpty(p.LiveCreds, KindCreds)
	if err != nil {
		return err
	}
	liveConf = removeSection(liveConf, name)
	liveCreds = removeSection(liveCreds, name)
	if err := writeSections(p.LiveConf, liveConf); err != nil {
		return err
	}
	return writeSections(p.LiveCreds, liveCreds)
}

func removeSection(secs []Section, name string) []Section {
	out := make([]Section, 0, len(secs))
	for _, s := range secs {
		if s.Name != name {
			out = append(out, s)
		}
	}
	return out
}

func findProfile(p Paths, name string) (Profile, bool) {
	st, err := loadState(p)
	if err != nil {
		return Profile{}, false
	}
	for _, prof := range st.Profiles {
		if prof.Name == name {
			return prof, true
		}
	}
	return Profile{}, false
}

// syncToMaster copies a single profile from live to master in whichever file(s)
// it appears in live. Used to adopt orphans and resolve drift in favour of
// the live version.
func syncToMaster(p Paths, name string) error {
	if _, err := os.Stat(p.MasterDir); os.IsNotExist(err) {
		return fmt.Errorf("%s does not exist; run `credswitch init` first", p.MasterDir)
	}
	liveConf, err := loadOrEmpty(p.LiveConf, KindConfig)
	if err != nil {
		return err
	}
	liveCreds, err := loadOrEmpty(p.LiveCreds, KindCreds)
	if err != nil {
		return err
	}
	masterConf, err := loadOrEmpty(p.MasterConf, KindConfig)
	if err != nil {
		return err
	}
	masterCreds, err := loadOrEmpty(p.MasterCreds, KindCreds)
	if err != nil {
		return err
	}

	found := false
	if sec := findSection(liveConf, name); sec != nil {
		masterConf = upsertSection(masterConf, *sec)
		found = true
	}
	if sec := findSection(liveCreds, name); sec != nil {
		masterCreds = upsertSection(masterCreds, *sec)
		found = true
	}
	if !found {
		return fmt.Errorf("profile %q not found in ~/.aws/ — nothing to sync", name)
	}

	if err := writeSections(p.MasterConf, masterConf); err != nil {
		return err
	}
	return writeSections(p.MasterCreds, masterCreds)
}

func findSection(secs []Section, name string) *Section {
	for i := range secs {
		if secs[i].Name == name {
			return &secs[i]
		}
	}
	return nil
}

func upsertSection(secs []Section, sec Section) []Section {
	for i := range secs {
		if secs[i].Name == sec.Name {
			secs[i] = sec
			return secs
		}
	}
	return append(secs, sec)
}

// initMaster copies the user's existing ~/.aws files into ~/.credswitch.
// Refuses to overwrite an existing master directory. Live files are left
// alone — disabling profiles is a separate, deliberate step.
func initMaster(p Paths) error {
	if _, err := os.Stat(p.MasterDir); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite", p.MasterDir)
	}
	if err := os.MkdirAll(p.MasterDir, 0o700); err != nil {
		return err
	}
	if err := copyIfExists(p.LiveConf, p.MasterConf); err != nil {
		return err
	}
	return copyIfExists(p.LiveCreds, p.MasterCreds)
}

func copyIfExists(src, dst string) error {
	data, err := os.ReadFile(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}
