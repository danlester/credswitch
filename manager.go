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

// apply rewrites both live files from master, keeping only the [default]
// section plus any profile in `enabled`. Refuses to run if the live files
// contain any profile not present in master (would be silent data loss).
func apply(p Paths, enabled map[string]bool) error {
	masterConf, err := loadOrEmpty(p.MasterConf, KindConfig)
	if err != nil {
		return err
	}
	masterCreds, err := loadOrEmpty(p.MasterCreds, KindCreds)
	if err != nil {
		return err
	}

	orphans, err := findOrphans(p, masterConf, masterCreds)
	if err != nil {
		return err
	}
	if len(orphans) > 0 {
		return fmt.Errorf(
			"refusing to rewrite ~/.aws/: %d profile(s) exist in live files but not in master:\n  %s\n\n"+
				"These would be erased. Resolve by either:\n"+
				"  1. Copying the missing sections into ~/.credswitch/config or ~/.credswitch/credentials, or\n"+
				"  2. Deleting them from ~/.aws/config or ~/.aws/credentials if you don't need them.\n"+
				"Then retry. (`credswitch list` also shows orphans.)",
			len(orphans), strings.Join(orphans, "\n  "),
		)
	}

	keep := func(name string) bool {
		return name == defaultProfile || enabled[name]
	}

	var newConf, newCreds []Section
	for _, s := range masterConf {
		if keep(s.Name) {
			newConf = append(newConf, s)
		}
	}
	for _, s := range masterCreds {
		if keep(s.Name) {
			newCreds = append(newCreds, s)
		}
	}

	if err := writeSections(p.LiveConf, newConf); err != nil {
		return err
	}
	return writeSections(p.LiveCreds, newCreds)
}

// findOrphans returns names of profiles present in the live files but not in
// master, formatted as "config:<name>" or "credentials:<name>". The default
// profile is exempt (it's tool-managed and content is allowed to differ).
func findOrphans(p Paths, masterConf, masterCreds []Section) ([]string, error) {
	liveConf, err := loadOrEmpty(p.LiveConf, KindConfig)
	if err != nil {
		return nil, err
	}
	liveCreds, err := loadOrEmpty(p.LiveCreds, KindCreds)
	if err != nil {
		return nil, err
	}
	confMaster := nameSet(masterConf)
	credMaster := nameSet(masterCreds)
	var orphans []string
	for _, s := range liveConf {
		if s.Name == defaultProfile {
			continue
		}
		if !confMaster[s.Name] {
			orphans = append(orphans, "config:"+s.Name)
		}
	}
	for _, s := range liveCreds {
		if s.Name == defaultProfile {
			continue
		}
		if !credMaster[s.Name] {
			orphans = append(orphans, "credentials:"+s.Name)
		}
	}
	return orphans, nil
}

// loadOrphans is a convenience wrapper for callers that just need the list
// of orphans (e.g. `list`, TUI startup) and don't have master sections in
// hand already.
func loadOrphans(p Paths) ([]string, error) {
	masterConf, err := loadOrEmpty(p.MasterConf, KindConfig)
	if err != nil {
		return nil, err
	}
	masterCreds, err := loadOrEmpty(p.MasterCreds, KindCreds)
	if err != nil {
		return nil, err
	}
	return findOrphans(p, masterConf, masterCreds)
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
		return fmt.Errorf("profile %q not found in master files at %s", name, p.MasterDir)
	}
	en, err := currentEnabled(p)
	if err != nil {
		return err
	}
	en[name] = true
	return apply(p, en)
}

func disableProfile(p Paths, name string) error {
	if name == defaultProfile {
		return fmt.Errorf("cannot disable the default profile")
	}
	if !profileExists(p, name) {
		return fmt.Errorf("profile %q not found in master files at %s", name, p.MasterDir)
	}
	en, err := currentEnabled(p)
	if err != nil {
		return err
	}
	delete(en, name)
	return apply(p, en)
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
