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
	Name     string
	InConfig bool
	InCreds  bool
	Enabled  bool
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

	confLive := nameSet(liveConf)
	credLive := nameSet(liveCreds)
	confMaster := nameSet(masterConf)
	credMaster := nameSet(masterCreds)

	seen := map[string]bool{}
	var profiles []Profile

	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		profiles = append(profiles, Profile{
			Name:     name,
			InConfig: confMaster[name],
			InCreds:  credMaster[name],
			Enabled:  isEnabled(name, confMaster, credMaster, confLive, credLive),
		})
	}

	for _, s := range masterConf {
		add(s.Name)
	}
	for _, s := range masterCreds {
		add(s.Name)
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

// A profile is enabled when every master file that defines it also has it
// in the corresponding live file. Default is treated as always enabled.
func isEnabled(name string, confMaster, credMaster, confLive, credLive map[string]bool) bool {
	if name == defaultProfile {
		return true
	}
	if confMaster[name] && !confLive[name] {
		return false
	}
	if credMaster[name] && !credLive[name] {
		return false
	}
	return confMaster[name] || credMaster[name]
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

// apply rewrites both live files from master. The [default] section is a
// pass-through (live wins, never overwritten). Other profiles: keep only
// the ones in `enabled`. Refuses to run if any non-default profile drifts
// from master, except for `ignoreDriftFor` — that profile's drift is
// implicitly resolved master→live by the rewrite itself.
func apply(p Paths, enabled map[string]bool, ignoreDriftFor string) error {
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

	drift := computeDrift(liveConf, masterConf, liveCreds, masterCreds)
	var blocking []Drift
	for _, d := range drift {
		if d.Name == ignoreDriftFor {
			continue
		}
		blocking = append(blocking, d)
	}
	if len(blocking) > 0 {
		return fmt.Errorf("%s", formatDrift(blocking))
	}

	newConf := buildLive(masterConf, liveConf, enabled)
	newCreds := buildLive(masterCreds, liveCreds, enabled)
	if err := writeSections(p.LiveConf, newConf); err != nil {
		return err
	}
	return writeSections(p.LiveCreds, newCreds)
}

// buildLive produces the new contents of one live file: live's [default] if
// present (else master's), then enabled profiles taken straight from master,
// in master order.
func buildLive(master, live []Section, enabled map[string]bool) []Section {
	liveByName := map[string]Section{}
	for _, s := range live {
		liveByName[s.Name] = s
	}
	var out []Section
	defaultEmitted := false
	if ls, ok := liveByName[defaultProfile]; ok {
		out = append(out, ls)
		defaultEmitted = true
	}
	for _, ms := range master {
		if ms.Name == defaultProfile {
			if !defaultEmitted {
				out = append(out, ms)
				defaultEmitted = true
			}
			continue
		}
		if enabled[ms.Name] {
			out = append(out, ms)
		}
	}
	return out
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
	b.WriteString("To resolve:\n")
	b.WriteString("  - keep live's version  ->  credswitch sync <name>\n")
	b.WriteString("  - take master's version ->  credswitch enable <name>  (or disable)\n")
	b.WriteString("  - or just delete the section from ~/.aws/ manually")
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

func currentEnabled(p Paths) (map[string]bool, error) {
	st, err := loadState(p)
	if err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, prof := range st.Profiles {
		if prof.Enabled && prof.Name != defaultProfile {
			m[prof.Name] = true
		}
	}
	return m, nil
}

func enableProfile(p Paths, name string) error {
	if !profileExists(p, name) {
		return fmt.Errorf("profile %q not found in master at %s — run `credswitch sync %s` first if it only exists in ~/.aws/", name, p.MasterDir, name)
	}
	en, err := currentEnabled(p)
	if err != nil {
		return err
	}
	en[name] = true
	return apply(p, en, name)
}

func disableProfile(p Paths, name string) error {
	if name == defaultProfile {
		return fmt.Errorf("cannot disable the default profile")
	}
	if !profileExists(p, name) {
		return fmt.Errorf("profile %q not found in master at %s", name, p.MasterDir)
	}
	en, err := currentEnabled(p)
	if err != nil {
		return err
	}
	delete(en, name)
	return apply(p, en, name)
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

func profileExists(p Paths, name string) bool {
	st, err := loadState(p)
	if err != nil {
		return false
	}
	for _, prof := range st.Profiles {
		if prof.Name == name {
			return true
		}
	}
	return false
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
