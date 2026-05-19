package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultProfile = "default"

type Paths struct {
	MasterDir     string
	MasterConf    string
	MasterCreds   string
	LiveConf      string
	LiveCreds     string
	EphemeralList string
}

func defaultPaths() Paths {
	home := os.Getenv("CREDSWITCH_HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return Paths{
		MasterDir:     filepath.Join(home, ".credswitch"),
		MasterConf:    filepath.Join(home, ".credswitch", "config"),
		MasterCreds:   filepath.Join(home, ".credswitch", "credentials"),
		LiveConf:      filepath.Join(home, ".aws", "config"),
		LiveCreds:     filepath.Join(home, ".aws", "credentials"),
		EphemeralList: filepath.Join(home, ".credswitch", "ephemeral"),
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
	// Ephemeral is true when the profile is listed in ~/.credswitch/ephemeral
	// and is therefore a target for `credswitch reap`.
	Ephemeral bool
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
	eph, err := loadEphemeral(p)
	if err != nil {
		return nil, err
	}

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
			Ephemeral:      eph[name],
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

// formatDrift renders drift entries with per-profile diffs and a generic
// resolution footer. Used by `list` to summarise all drift.
func formatDrift(drift []Drift) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d profile change(s) in ~/.aws/ not reflected in master:\n\n", len(drift))
	b.WriteString(renderDrift(drift))
	b.WriteString("To resolve any of these:\n")
	b.WriteString("  - keep live's version    ->  credswitch sync <name>     (live -> master)\n")
	b.WriteString("  - take master's version  ->  credswitch revert <name>   (master -> live;\n")
	b.WriteString("                                                          for orphans, removes from live)")
	return b.String()
}

// renderDrift renders the per-entry diff lines without header or footer.
// Shared by formatDrift and requireClean.
func renderDrift(drift []Drift) string {
	var b strings.Builder
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

// enableProfile inserts master's view of a profile into live. Refuses if the
// profile has drift or orphan state — those are reconciled via sync/revert.
func enableProfile(p Paths, name string) error {
	if name == defaultProfile {
		return fmt.Errorf("default profile is pass-through; cannot enable explicitly")
	}
	masterConf, masterCreds, liveConf, liveCreds, err := loadAllSections(p)
	if err != nil {
		return err
	}
	if err := requireClean(p, name, "enable", liveConf, masterConf, liveCreds, masterCreds); err != nil {
		return err
	}

	msc := findSection(masterConf, name)
	msr := findSection(masterCreds, name)
	if msc == nil && msr == nil {
		return fmt.Errorf("profile %q not found in master", name)
	}
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

// disableProfile removes a profile from both live files. Refuses if the
// profile has drift or orphan state — disable would silently destroy the
// drifted/orphaned content.
func disableProfile(p Paths, name string) error {
	if name == defaultProfile {
		return fmt.Errorf("cannot disable the default profile")
	}
	masterConf, masterCreds, liveConf, liveCreds, err := loadAllSections(p)
	if err != nil {
		return err
	}
	if err := requireClean(p, name, "disable", liveConf, masterConf, liveCreds, masterCreds); err != nil {
		return err
	}
	if _, ok := findProfile(p, name); !ok {
		return fmt.Errorf("profile %q not found in master or in ~/.aws/", name)
	}
	liveConf = removeSection(liveConf, name)
	liveCreds = removeSection(liveCreds, name)
	if err := writeSections(p.LiveConf, liveConf); err != nil {
		return err
	}
	return writeSections(p.LiveCreds, liveCreds)
}

// revertProfile makes live match master for one profile (master → live).
// For DriftModified: live's content is overwritten with master's. For
// DriftOrphan: live's section is removed (master has nothing to bring
// forward). Errors if the profile is clean — there's nothing to revert.
func revertProfile(p Paths, name string) error {
	if name == defaultProfile {
		return fmt.Errorf("default profile is pass-through; nothing to revert")
	}
	masterConf, masterCreds, liveConf, liveCreds, err := loadAllSections(p)
	if err != nil {
		return err
	}
	driftHere := driftFor(liveConf, masterConf, liveCreds, masterCreds, name)
	if len(driftHere) == 0 {
		return fmt.Errorf("profile %q has no drift to revert (use enable/disable to change its state)", name)
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

func loadAllSections(p Paths) (masterConf, masterCreds, liveConf, liveCreds []Section, err error) {
	if masterConf, err = loadOrEmpty(p.MasterConf, KindConfig); err != nil {
		return
	}
	if masterCreds, err = loadOrEmpty(p.MasterCreds, KindCreds); err != nil {
		return
	}
	if liveConf, err = loadOrEmpty(p.LiveConf, KindConfig); err != nil {
		return
	}
	liveCreds, err = loadOrEmpty(p.LiveCreds, KindCreds)
	return
}

func driftFor(liveConf, masterConf, liveCreds, masterCreds []Section, name string) []Drift {
	var out []Drift
	for _, d := range computeDrift(liveConf, masterConf, liveCreds, masterCreds) {
		if d.Name == name {
			out = append(out, d)
		}
	}
	return out
}

// requireClean errors out if the profile has drift or orphan state, with a
// focused message pointing at sync/revert for that specific name.
func requireClean(p Paths, name, action string, liveConf, masterConf, liveCreds, masterCreds []Section) error {
	d := driftFor(liveConf, masterConf, liveCreds, masterCreds, name)
	if len(d) == 0 {
		return nil
	}
	allOrphan := true
	for _, e := range d {
		if e.Kind != DriftOrphan {
			allOrphan = false
			break
		}
	}
	revertHint := "replaces live with master's version"
	if allOrphan {
		revertHint = "removes from live (master has nothing to bring forward)"
	}
	return fmt.Errorf(
		"cannot %s profile %q: it differs from master.\n\n%sResolve first with:\n"+
			"  credswitch sync %s     (keep live, copy to master)\n"+
			"  credswitch revert %s   (master wins; %s)",
		action, name, renderDrift(d), name, name, revertHint,
	)
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

// loadEphemeral reads the set of profile names tagged for auto-reap from
// ~/.credswitch/ephemeral. One name per line; blank lines and `#` comments
// are ignored. A missing file is not an error — opt-in feature.
func loadEphemeral(p Paths) (map[string]bool, error) {
	set := map[string]bool{}
	data, err := os.ReadFile(p.EphemeralList)
	if os.IsNotExist(err) {
		return set, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		set[line] = true
	}
	return set, nil
}

// toggleEphemeral flips whether `name` is tagged in ~/.credswitch/ephemeral.
// Returns the new state. Preserves comments, blank lines, and the relative
// order of other entries — only the toggled name is added or removed.
// Refuses on default (pass-through can't be ephemeral); allows any other
// name (including orphans — reap will skip them at run time).
func toggleEphemeral(p Paths, name string) (bool, error) {
	if name == defaultProfile {
		return false, fmt.Errorf("default profile cannot be ephemeral")
	}
	if _, err := os.Stat(p.MasterDir); os.IsNotExist(err) {
		return false, fmt.Errorf("%s does not exist; run `credswitch init` first", p.MasterDir)
	}

	var lines []string
	data, err := os.ReadFile(p.EphemeralList)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err == nil {
		raw := strings.TrimRight(string(data), "\n")
		if raw != "" {
			lines = strings.Split(raw, "\n")
		}
	}

	found := false
	kept := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) == name {
			found = true
			continue
		}
		kept = append(kept, line)
	}
	lines = kept

	nowEphemeral := !found
	if nowEphemeral {
		lines = append(lines, name)
	}

	var buf strings.Builder
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return nowEphemeral, atomicWrite(p.EphemeralList, []byte(buf.String()))
}

// ReapResult summarises one run of reapEphemeral.
type ReapResult struct {
	Reaped  []string   // disabled successfully
	Skipped []ReapSkip // ephemeral but left enabled, with reason
}

type ReapSkip struct {
	Name   string
	Reason string
}

// reapEphemeral disables every currently-enabled profile tagged in the
// ephemeral list. Drifted/orphan profiles are skipped (consistent with the
// strict drift gate — clobbering live edits is exactly what we must not do
// silently). Unknown names in the list are silently ignored.
func reapEphemeral(p Paths) (ReapResult, error) {
	var r ReapResult
	eph, err := loadEphemeral(p)
	if err != nil {
		return r, err
	}
	if len(eph) == 0 {
		return r, nil
	}
	st, err := loadState(p)
	if err != nil {
		return r, err
	}
	drift, err := loadDrift(p)
	if err != nil {
		return r, err
	}
	drifted := driftedNames(drift)

	for _, prof := range st.Profiles {
		if !eph[prof.Name] {
			continue
		}
		if prof.Name == defaultProfile {
			r.Skipped = append(r.Skipped, ReapSkip{prof.Name, "default is pass-through; cannot be ephemeral"})
			continue
		}
		if !prof.Enabled {
			continue
		}
		if prof.Orphan {
			r.Skipped = append(r.Skipped, ReapSkip{prof.Name, "orphan in live; resolve with sync/revert"})
			continue
		}
		if drifted[prof.Name] {
			r.Skipped = append(r.Skipped, ReapSkip{prof.Name, "drifted from master; resolve with sync/revert"})
			continue
		}
		if err := disableProfile(p, prof.Name); err != nil {
			r.Skipped = append(r.Skipped, ReapSkip{prof.Name, err.Error()})
			continue
		}
		r.Reaped = append(r.Reaped, prof.Name)
	}
	return r, nil
}
