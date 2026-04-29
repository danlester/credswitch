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
- **Orphan detection**: `apply()` calls `findOrphans()` and refuses to run
  if `~/.aws/` contains any profile not in master. Same check gates the
  TUI on startup. The default profile is exempt (its content is allowed
  to drift; we always rewrite it from master).
- **Default profile**: always kept enabled. `disableProfile("default")`
  returns an error. The TUI also blocks toggling it.
- **Manual edits to live files** to existing profiles are clobbered on
  the next toggle, by design. Edit master instead. *New* profiles in live
  trigger orphan detection — they aren't silently lost.

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
- Add `credswitch import <name>` to move an orphan into master in one step
  (currently the user has to copy the section by hand).
- Filter/search in the TUI (`/` prefix, fuzzy match).
- Group-by-prefix display in the TUI (`workstuff*` collapsed into a section).
- Tests, especially for the parser's edge cases.
