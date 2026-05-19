package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupHome creates a temp HOME, points CREDSWITCH_HOME at it, and creates
// empty .aws and .credswitch dirs. Caller pre-populates whichever files the
// test cares about. Returns the temp home path.
func setupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CREDSWITCH_HOME", home)
	mustMkdir(t, filepath.Join(home, ".aws"))
	return home
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runCLI drives rootCmd in-process and captures stdout/stderr separately.
// rootCmd is a package-level singleton, so this reseeds its IO and args on
// every call to keep tests independent.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs(args)
	err = rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// Standard fixture content used by most tests.
const masterConfigContent = `[default]
region = us-east-1

[profile foo]
region = eu-west-1

[profile bar]
region = ap-south-1
`

const masterCredsContent = `[default]
aws_access_key_id = DEFAULTKEY
aws_secret_access_key = defaultsecret

[foo]
aws_access_key_id = FOOKEY
aws_secret_access_key = foosecret

[bar]
aws_access_key_id = BARKEY
aws_secret_access_key = barsecret
`

const liveDefaultOnlyConfig = `[default]
region = us-east-1
`

const liveDefaultOnlyCreds = `[default]
aws_access_key_id = DEFAULTKEY
aws_secret_access_key = defaultsecret
`

// seedMaster writes the standard master fixture and a default-only live.
func seedMaster(t *testing.T, home string) {
	t.Helper()
	writeFile(t, filepath.Join(home, ".credswitch", "config"), masterConfigContent)
	writeFile(t, filepath.Join(home, ".credswitch", "credentials"), masterCredsContent)
	writeFile(t, filepath.Join(home, ".aws", "config"), liveDefaultOnlyConfig)
	writeFile(t, filepath.Join(home, ".aws", "credentials"), liveDefaultOnlyCreds)
}

func TestInitBootstrapsMasterFromLive(t *testing.T) {
	home := setupHome(t)
	writeFile(t, filepath.Join(home, ".aws", "config"), masterConfigContent)
	writeFile(t, filepath.Join(home, ".aws", "credentials"), masterCredsContent)

	stdout, _, err := runCLI(t, "init")
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if !strings.Contains(stdout, "Master files copied") {
		t.Errorf("missing success message; got: %q", stdout)
	}

	got := readFile(t, filepath.Join(home, ".credswitch", "config"))
	if got != masterConfigContent {
		t.Errorf("master config mismatch:\nwant:\n%s\ngot:\n%s", masterConfigContent, got)
	}
	got = readFile(t, filepath.Join(home, ".credswitch", "credentials"))
	if got != masterCredsContent {
		t.Errorf("master creds mismatch:\nwant:\n%s\ngot:\n%s", masterCredsContent, got)
	}

	// Live files should be untouched.
	if got := readFile(t, filepath.Join(home, ".aws", "config")); got != masterConfigContent {
		t.Errorf("live config was modified by init")
	}
}

func TestInitRefusesIfMasterExists(t *testing.T) {
	home := setupHome(t)
	writeFile(t, filepath.Join(home, ".credswitch", "config"), "")

	_, _, err := runCLI(t, "init")
	if err == nil {
		t.Fatal("expected init to fail when master dir exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error, got: %v", err)
	}
}

func TestListShowsEnabledAndDisabled(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	// Enable foo by adding it to live.
	writeFile(t, filepath.Join(home, ".aws", "config"), liveDefaultOnlyConfig+`
[profile foo]
region = eu-west-1
`)
	writeFile(t, filepath.Join(home, ".aws", "credentials"), liveDefaultOnlyCreds+`
[foo]
aws_access_key_id = FOOKEY
aws_secret_access_key = foosecret
`)

	stdout, _, err := runCLI(t, "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	// foo enabled, bar not.
	if !strings.Contains(stdout, "[x] foo") {
		t.Errorf("expected foo to show enabled marker; output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "[ ] bar") {
		t.Errorf("expected bar to show disabled marker; output:\n%s", stdout)
	}
	// No drift annotations expected.
	if strings.Contains(stdout, "DRIFTED") || strings.Contains(stdout, "ORPHAN") {
		t.Errorf("unexpected drift annotation in clean state:\n%s", stdout)
	}
}

func TestListEmptyWhenNoProfiles(t *testing.T) {
	setupHome(t)
	stdout, _, err := runCLI(t, "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(stdout, "No profiles found") {
		t.Errorf("expected empty notice; got: %q", stdout)
	}
}

func TestEnableAddsProfileToLive(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)

	stdout, _, err := runCLI(t, "enable", "foo")
	if err != nil {
		t.Fatalf("enable foo failed: %v", err)
	}
	if !strings.Contains(stdout, "Enabled foo") {
		t.Errorf("expected success message; got: %q", stdout)
	}

	liveConf := readFile(t, filepath.Join(home, ".aws", "config"))
	if !strings.Contains(liveConf, "[profile foo]") {
		t.Errorf("foo missing from live config:\n%s", liveConf)
	}
	if !strings.Contains(liveConf, "region = eu-west-1") {
		t.Errorf("foo body missing from live config:\n%s", liveConf)
	}
	if !strings.Contains(liveConf, "[default]") {
		t.Errorf("default lost from live config:\n%s", liveConf)
	}

	liveCreds := readFile(t, filepath.Join(home, ".aws", "credentials"))
	if !strings.Contains(liveCreds, "[foo]") {
		t.Errorf("foo missing from live creds:\n%s", liveCreds)
	}
	if !strings.Contains(liveCreds, "FOOKEY") {
		t.Errorf("foo creds body missing:\n%s", liveCreds)
	}
}

func TestEnableUnknownProfileFails(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)

	_, _, err := runCLI(t, "enable", "nonexistent")
	if err == nil {
		t.Fatal("expected enable of unknown profile to fail")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestEnableDefaultRejected(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)

	_, _, err := runCLI(t, "enable", "default")
	if err == nil {
		t.Fatal("expected enable default to fail")
	}
}

func TestDisableRemovesProfileFromLive(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	if _, _, err := runCLI(t, "enable", "foo"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, _, err := runCLI(t, "disable", "foo"); err != nil {
		t.Fatalf("disable: %v", err)
	}

	liveConf := readFile(t, filepath.Join(home, ".aws", "config"))
	if strings.Contains(liveConf, "[profile foo]") {
		t.Errorf("foo still in live config after disable:\n%s", liveConf)
	}
	liveCreds := readFile(t, filepath.Join(home, ".aws", "credentials"))
	if strings.Contains(liveCreds, "[foo]") {
		t.Errorf("foo still in live creds after disable:\n%s", liveCreds)
	}
	// Master untouched.
	if got := readFile(t, filepath.Join(home, ".credswitch", "config")); got != masterConfigContent {
		t.Errorf("master config was modified by disable; got:\n%s", got)
	}
}

func TestDisableDefaultRejected(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)

	_, _, err := runCLI(t, "disable", "default")
	if err == nil {
		t.Fatal("expected disable default to fail")
	}
}

func TestEnableDisableRoundtrip(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)

	beforeConf := readFile(t, filepath.Join(home, ".aws", "config"))
	beforeCreds := readFile(t, filepath.Join(home, ".aws", "credentials"))

	if _, _, err := runCLI(t, "enable", "foo"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, _, err := runCLI(t, "disable", "foo"); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// Live should be back to default-only (content equality, modulo whitespace).
	if got := readFile(t, filepath.Join(home, ".aws", "config")); got != beforeConf {
		t.Errorf("config not restored after roundtrip:\nbefore:\n%s\nafter:\n%s", beforeConf, got)
	}
	if got := readFile(t, filepath.Join(home, ".aws", "credentials")); got != beforeCreds {
		t.Errorf("creds not restored after roundtrip:\nbefore:\n%s\nafter:\n%s", beforeCreds, got)
	}
}

func TestDriftDetectedWhenLiveModified(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	// Enable foo, then mutate live's foo so it drifts from master.
	if _, _, err := runCLI(t, "enable", "foo"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	driftedLive := liveDefaultOnlyConfig + `
[profile foo]
region = us-west-2
`
	writeFile(t, filepath.Join(home, ".aws", "config"), driftedLive)

	stdout, _, err := runCLI(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stdout, "DRIFTED") {
		t.Errorf("expected DRIFTED annotation; output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "config:foo") {
		t.Errorf("expected drift detail listing config:foo; output:\n%s", stdout)
	}
	// Drift block uses normalized "key=value" form (no spaces).
	if !strings.Contains(stdout, "live only:    region=us-west-2") {
		t.Errorf("expected live-only line in drift block; output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "master only:  region=eu-west-1") {
		t.Errorf("expected master-only line in drift block; output:\n%s", stdout)
	}
}

func TestDriftBlocksEnable(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	// foo is enabled but drifted; bar is unrelated and clean.
	writeFile(t, filepath.Join(home, ".aws", "config"), liveDefaultOnlyConfig+`
[profile foo]
region = somewhere-else
`)

	_, _, err := runCLI(t, "enable", "foo")
	if err == nil {
		t.Fatal("expected enable on drifted profile to fail")
	}
	if !strings.Contains(err.Error(), "differs from master") {
		t.Errorf("expected drift-gate error, got: %v", err)
	}

	// Drift on foo must NOT block bar.
	if _, _, err := runCLI(t, "enable", "bar"); err != nil {
		t.Errorf("drift on foo should not block enable of bar: %v", err)
	}
}

func TestDriftBlocksDisable(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	writeFile(t, filepath.Join(home, ".aws", "config"), liveDefaultOnlyConfig+`
[profile foo]
region = somewhere-else
`)

	_, _, err := runCLI(t, "disable", "foo")
	if err == nil {
		t.Fatal("expected disable on drifted profile to fail")
	}
}

func TestOrphanDetected(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	// quux exists only in live config — nothing in master.
	writeFile(t, filepath.Join(home, ".aws", "config"), liveDefaultOnlyConfig+`
[profile quux]
region = mars-1
`)

	stdout, _, err := runCLI(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stdout, "quux") {
		t.Errorf("orphan profile not listed; output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "ORPHAN") {
		t.Errorf("expected ORPHAN annotation; output:\n%s", stdout)
	}
}

func TestSyncAdoptsOrphan(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	writeFile(t, filepath.Join(home, ".aws", "config"), liveDefaultOnlyConfig+`
[profile quux]
region = mars-1
`)

	if _, _, err := runCLI(t, "sync", "quux"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	masterConf := readFile(t, filepath.Join(home, ".credswitch", "config"))
	if !strings.Contains(masterConf, "[profile quux]") {
		t.Errorf("quux not adopted into master config:\n%s", masterConf)
	}
	if !strings.Contains(masterConf, "region = mars-1") {
		t.Errorf("quux body missing from master config:\n%s", masterConf)
	}

	// After sync, list should show no drift.
	stdout, _, err := runCLI(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(stdout, "ORPHAN") || strings.Contains(stdout, "DRIFTED") {
		t.Errorf("expected clean state after sync; got:\n%s", stdout)
	}
}

func TestSyncResolvesDriftLiveWins(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	writeFile(t, filepath.Join(home, ".aws", "config"), liveDefaultOnlyConfig+`
[profile foo]
region = us-west-2
`)

	if _, _, err := runCLI(t, "sync", "foo"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	masterConf := readFile(t, filepath.Join(home, ".credswitch", "config"))
	if !strings.Contains(masterConf, "region = us-west-2") {
		t.Errorf("master not updated with live's value:\n%s", masterConf)
	}
	if strings.Contains(masterConf, "region = eu-west-1") {
		t.Errorf("master still contains old foo value:\n%s", masterConf)
	}
}

func TestRevertOverwritesLiveWithMaster(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	writeFile(t, filepath.Join(home, ".aws", "config"), liveDefaultOnlyConfig+`
[profile foo]
region = us-west-2
`)

	if _, _, err := runCLI(t, "revert", "foo"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	liveConf := readFile(t, filepath.Join(home, ".aws", "config"))
	if !strings.Contains(liveConf, "region = eu-west-1") {
		t.Errorf("live not restored to master's value:\n%s", liveConf)
	}
	if strings.Contains(liveConf, "region = us-west-2") {
		t.Errorf("live still contains drifted value:\n%s", liveConf)
	}
}

func TestRevertRemovesOrphan(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	writeFile(t, filepath.Join(home, ".aws", "config"), liveDefaultOnlyConfig+`
[profile quux]
region = mars-1
`)

	if _, _, err := runCLI(t, "revert", "quux"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	liveConf := readFile(t, filepath.Join(home, ".aws", "config"))
	if strings.Contains(liveConf, "quux") {
		t.Errorf("orphan still in live after revert:\n%s", liveConf)
	}
}

func TestRevertCleanProfileFails(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)

	_, _, err := runCLI(t, "revert", "foo")
	if err == nil {
		t.Fatal("expected revert of clean profile to fail")
	}
	if !strings.Contains(err.Error(), "no drift") {
		t.Errorf("expected 'no drift' error, got: %v", err)
	}
}

// Default's content can differ between master and live (aws-cli writes to
// live's default during `aws configure`); this must NOT count as drift.
func TestDefaultDiffersWithoutDrift(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	// Live default has rotated credentials.
	writeFile(t, filepath.Join(home, ".aws", "credentials"), `[default]
aws_access_key_id = ROTATEDKEY
aws_secret_access_key = rotatedsecret
`)

	stdout, _, err := runCLI(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(stdout, "DRIFTED") {
		t.Errorf("default difference must not flag drift; output:\n%s", stdout)
	}
	// And enable on a clean profile must still work.
	if _, _, err := runCLI(t, "enable", "foo"); err != nil {
		t.Errorf("default difference must not block enable: %v", err)
	}
}

// Atomic write must not leave .credswitch-* tempfiles behind, and live file
// permissions must be 0600 after writing.
func TestWritesAreAtomicAndPrivate(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	if _, _, err := runCLI(t, "enable", "foo"); err != nil {
		t.Fatalf("enable: %v", err)
	}

	for _, dir := range []string{filepath.Join(home, ".aws"), filepath.Join(home, ".credswitch")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("readdir %s: %v", dir, err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".credswitch-") {
				t.Errorf("found stale tempfile %s in %s", e.Name(), dir)
			}
		}
	}

	for _, p := range []string{
		filepath.Join(home, ".aws", "config"),
		filepath.Join(home, ".aws", "credentials"),
	} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		// Mode bits below 0o777 only.
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s has perms %o, want 0600", p, perm)
		}
	}
}

func TestReapDisablesEphemeralEnabled(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	if _, _, err := runCLI(t, "enable", "foo"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, _, err := runCLI(t, "enable", "bar"); err != nil {
		t.Fatalf("enable bar: %v", err)
	}
	writeFile(t, filepath.Join(home, ".credswitch", "ephemeral"), "foo\n# bar is intentionally not ephemeral\n")

	stdout, _, err := runCLI(t, "reap")
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if !strings.Contains(stdout, "Disabled foo") {
		t.Errorf("expected foo reaped; got: %q", stdout)
	}
	if strings.Contains(readFile(t, filepath.Join(home, ".aws", "config")), "[profile foo]") {
		t.Errorf("foo still in live config after reap")
	}
	if !strings.Contains(readFile(t, filepath.Join(home, ".aws", "config")), "[profile bar]") {
		t.Errorf("bar (non-ephemeral) should remain enabled")
	}
}

func TestReapSkipsDrifted(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	if _, _, err := runCLI(t, "enable", "foo"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	writeFile(t, filepath.Join(home, ".aws", "config"), liveDefaultOnlyConfig+`
[profile foo]
region = somewhere-else
`)
	writeFile(t, filepath.Join(home, ".credswitch", "ephemeral"), "foo\n")

	stdout, stderr, err := runCLI(t, "reap")
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if strings.Contains(stdout, "Disabled foo") {
		t.Errorf("drifted profile must not be reaped; stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "skipped foo") || !strings.Contains(stderr, "drifted") {
		t.Errorf("expected skip warning for drifted foo; stderr:\n%s", stderr)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, ".aws", "config")), "region = somewhere-else") {
		t.Errorf("reap clobbered drifted live content")
	}
}

func TestReapNoListIsNoop(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	if _, _, err := runCLI(t, "enable", "foo"); err != nil {
		t.Fatalf("enable: %v", err)
	}

	stdout, _, err := runCLI(t, "reap")
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if !strings.Contains(stdout, "Nothing to reap") {
		t.Errorf("expected nothing-to-reap message; got: %q", stdout)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, ".aws", "config")), "[profile foo]") {
		t.Errorf("foo wrongly disabled by no-op reap")
	}
}

func TestReapIgnoresDisabledEphemeral(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	// foo is in master but not enabled in live.
	writeFile(t, filepath.Join(home, ".credswitch", "ephemeral"), "foo\n")

	stdout, _, err := runCLI(t, "reap")
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if strings.Contains(stdout, "Disabled foo") {
		t.Errorf("disabled-already foo should not be reported as reaped; stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Nothing to reap") {
		t.Errorf("expected nothing-to-reap; got: %q", stdout)
	}
}

func TestListShowsEphemeralAnnotation(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	writeFile(t, filepath.Join(home, ".credswitch", "ephemeral"), "foo\n")

	stdout, _, err := runCLI(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, " foo ") {
			if !strings.Contains(line, "EPHEMERAL") {
				t.Errorf("expected EPHEMERAL on foo line; got: %q", line)
			}
		}
		if strings.Contains(line, " bar ") && strings.Contains(line, "EPHEMERAL") {
			t.Errorf("bar should not be marked ephemeral; line: %q", line)
		}
	}
}

func TestToggleEphemeralRoundtrip(t *testing.T) {
	home := setupHome(t)
	seedMaster(t, home)
	// Seed file with a comment and an existing entry to make sure they survive.
	writeFile(t, filepath.Join(home, ".credswitch", "ephemeral"), "# admin profiles\nbar\n")

	on, err := toggleEphemeral(defaultPaths(), "foo")
	if err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	if !on {
		t.Errorf("expected foo to be tagged on first toggle")
	}
	got := readFile(t, filepath.Join(home, ".credswitch", "ephemeral"))
	if !strings.Contains(got, "# admin profiles") {
		t.Errorf("comment lost after toggle: %q", got)
	}
	if !strings.Contains(got, "bar") || !strings.Contains(got, "foo") {
		t.Errorf("expected bar and foo in list: %q", got)
	}

	off, err := toggleEphemeral(defaultPaths(), "foo")
	if err != nil {
		t.Fatalf("toggle off: %v", err)
	}
	if off {
		t.Errorf("expected foo to be untagged on second toggle")
	}
	got = readFile(t, filepath.Join(home, ".credswitch", "ephemeral"))
	if strings.Contains(got, "foo") {
		t.Errorf("foo not removed: %q", got)
	}
	if !strings.Contains(got, "bar") {
		t.Errorf("bar wrongly removed: %q", got)
	}
}

func TestToggleEphemeralRejectsDefault(t *testing.T) {
	setupHome(t)
	writeFile(t, filepath.Join(os.Getenv("CREDSWITCH_HOME"), ".credswitch", "config"), "")
	if _, err := toggleEphemeral(defaultPaths(), "default"); err == nil {
		t.Fatal("expected default to be rejected")
	}
}

func TestSyncRequiresInit(t *testing.T) {
	home := setupHome(t)
	// No master dir.
	writeFile(t, filepath.Join(home, ".aws", "config"), `[profile quux]
region = mars-1
`)
	_, _, err := runCLI(t, "sync", "quux")
	if err == nil {
		t.Fatal("expected sync to fail with no master dir")
	}
	if !strings.Contains(err.Error(), "credswitch init") {
		t.Errorf("expected init hint in error, got: %v", err)
	}
	if fileExists(filepath.Join(home, ".credswitch")) {
		t.Errorf("sync must not create master dir on its own")
	}
}
