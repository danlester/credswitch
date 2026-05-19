# credswitch — notes for Claude

Personal tool for Dan. Single-user, runs locally. Not packaged for distribution.

## Mental model

- **Master** files live in `~/.credswitch/{config,credentials}`. They are the
  source of truth.
- **Live** files at `~/.aws/{config,credentials}` are *derived* — they always
  equal `[default] ∪ {enabled profiles}`, taken straight from master.
- "Enabled" is not stored anywhere as separate state. It is computed from
  what's currently in the live files. No drift, no state file to keep in sync.

## Code layout (flat, single package `main`)

- `main.go` — Cobra command wiring. Add new subcommands here.
- `manager.go` — high-level operations: `loadState`, `enableProfile`,
  `disableProfile`, `apply`, `initMaster`. Owns the master/live distinction.
- `awsfile.go` — AWS-style INI parse + atomic write. The parser is
  intentionally minimal: line-based, splits on `[header]` lines, preserves
  inner content verbatim.
- `tui.go` — Bubble Tea model. Reloads `loadState` after every toggle.

There's no separate package boundary. If this grows, the natural split is
`internal/awsfile` and `internal/profiles`, but don't preemptively refactor.

## Conventions and gotchas

- **Profile name normalization**: in `~/.aws/config` profiles are
  `[profile foo]`, but `[default]` has no prefix. In credentials everything is
  bare (`[foo]`, `[default]`). `parseFile` strips the `profile ` prefix and
  exposes the logical name. Round-tripping preserves the raw header line, so
  re-writing produces a valid file without re-formatting.
- **Comments and lines outside any section are dropped** on rewrite. If you
  ever want comment preservation, attach the comment to the *next* section
  and round-trip it as part of `Section.Lines`. Right now master files'
  group-header comments (e.g. `# Workstuff admin profiles`) are lost when
  written to live files. That's fine because the master file is never
  written to by this tool — only read.
- **Atomic writes**: `awsfile.atomicWrite` uses tmp-in-same-dir + rename,
  so partial writes can never corrupt `~/.aws/config`.
- **No backups**. We deliberately do not write `*.bak` files — they would
  be exactly the kind of credential trail an unwanted reader (including an
  AI agent) could pick up. Recovery is via the master copy, which is the
  whole point of the tool.
- **Per-profile mutation, not whole-file rewrite**. `enableProfile` and
  `disableProfile` only touch the named profile's section(s) in live.
  Every other section — including drifted ones, orphans, comments, and
  ordering — is preserved as-is. There is no `apply()` / `buildLive()`;
  those got removed because rewriting the whole file required a
  global drift block, which deadlocked when two issues were present
  (each one blocked resolving the other).
- **Strict drift gate**: `enableProfile`, `disableProfile`, and the TUI
  toggle are all blocked for any profile in drift or orphan state.
  `requireClean()` is the gate, called from each. Resolution is via the
  two explicit commands `sync` (live → master, keep edits) and `revert`
  (master → live; for orphans, removes from live). Both commands reduce
  a profile to clean state, after which enable/disable works again.
  Drift on X never gates operations on Y — each profile is independent.
- **Orphans in `loadState`**. Profile struct tracks four presence flags
  (`InMaster{Config,Creds}`, `InLive{Config,Creds}`) plus derived
  `Enabled` and `Orphan`. Orphans appear in `list` and the TUI with the
  `ORPHAN` annotation. `enableProfile` rejects them (they must be
  synced first); `disableProfile` removes them like anything else.
- **Default profile** (`[default]`):
  - **Pass-through in apply**: `buildLive()` keeps live's default if
    present; falls back to master's default only when live has none.
    This protects credential rotation done via `aws configure`.
  - **Exempt from drift detection**: `fileDrift` skips it entirely.
  - **Always considered enabled**. `disableProfile("default")` errors;
    the TUI also blocks toggling it.
- **Resolution paths exposed to the user**:
  - `credswitch sync <name>` — live → master (keep live's edits).
  - `credswitch revert <name>` — master → live (master wins; for orphans,
    removes from live).
  - In the TUI: `s` and `r` keys run sync and revert on the highlighted
    profile.
  - Manual deletion from `~/.aws/` is also fine.
- **Ephemeral profiles + `reap`**. `~/.credswitch/ephemeral` is an opt-in
  newline list of profile names (with `#` comments). `credswitch reap`
  disables every enabled+ephemeral profile by calling `disableProfile` on
  each. Drift/orphan state causes reap to skip with a stderr warning —
  it never bypasses the drift gate. `loadEphemeral` returns an empty set
  when the file is missing (feature is invisible until used). `Profile`
  carries an `Ephemeral` bool populated by `loadState`; `list` and the
  TUI render an `EPHEMERAL` annotation. The TUI `e` key calls
  `toggleEphemeral`, which preserves comments and other entries — only
  the toggled name is added or removed. No LaunchAgent installer ships
  with the tool — wiring reap to login/overnight is left to the user.

## Running locally

```sh
go run .              # invokes the TUI
go run . list         # CLI subcommand
go install .          # drops binary in ~/go/bin
```

## What's deliberately not here

- No tests. Add table-driven tests in `awsfile_test.go` if the parser starts
  growing edge cases (mixed quoting, indented headers, etc.).
- No config file. Paths are hardcoded to `~/.aws/` and `~/.credswitch/`. Add
  flags only if a real reason appears.
- No encryption of master files. Out of scope — the README is explicit that
  master is plain text.
- No logging. Errors go to stderr via Cobra; the TUI surfaces them inline.
- No "sync" / drift-detection command. If the user manually edits live files
  and then toggles, their edits are lost. Worth a `credswitch doctor` if this
  ever becomes annoying.

## Likely future work

- Add `credswitch status <name>` for scripting (exit 0 if enabled, 1 if not).
- Filter/search in the TUI (`/` prefix, fuzzy match).
- Group-by-prefix display in the TUI (`workstuff*` collapsed into a section).
- Show drift state in the TUI itself (currently TUI only shows the post-toggle
  error if you happen to bump into a drifted profile from elsewhere).
- Tests, especially for the parser and `sectionsEqual` edge cases.
