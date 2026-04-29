# credswitch

Selectively enable and disable AWS profiles, so AI agents (and your future self)
can't pick the wrong one by accident.

## What it does

You probably have a long `~/.aws/config` and `~/.aws/credentials` with profiles
spanning multiple accounts, including high-blast-radius admin profiles sitting
right next to read-only ones backed by the same SSO. An agent — or a typo — can
land on the wrong profile.

`credswitch` keeps the **master** copy of every profile in `~/.credswitch/`, and
treats `~/.aws/config` and `~/.aws/credentials` as the **enabled subset**. Toggle
profiles on and off; the live AWS files only contain what you've explicitly
turned on.

```
~/.credswitch/config       <- master (everything you've ever set up)
~/.credswitch/credentials
~/.aws/config              <- enabled subset, used by aws-cli, boto, agents, etc.
~/.aws/credentials
```

## Install

```sh
go install github.com/dan/credswitch@latest
```

Or from a clone:

```sh
git clone <repo> ~/Dev/credswitch
cd ~/Dev/credswitch
go install .
```

Make sure `~/go/bin` is on your `PATH`.

## First-time setup

```sh
credswitch init
```

Copies your existing `~/.aws/config` and `~/.aws/credentials` into
`~/.credswitch/`. It will refuse to run if `~/.credswitch/` already exists.
The live `~/.aws/` files are left alone — you can immediately start trimming
them with `disable` or via the TUI.

## Usage

```sh
credswitch                  # interactive TUI (up/down + space to toggle)
credswitch list             # show all profiles and their state
credswitch enable <name>    # add <name> to the live AWS files (master wins)
credswitch disable <name>   # remove <name> from the live AWS files
credswitch sync <name>      # copy <name> from live into master (live wins)
```

Profile names are the **bare** name — `workstuffprod1`, not `profile workstuffprod1`.
The `[default]` profile is always kept enabled and is **pass-through** (see below).

## How it works

Each `enable` / `disable` rewrites both live files atomically (temp file +
rename). The new files contain the `[default]` block plus any enabled profile,
in the order they appear in master.

Because the live files are fully derived from master, **manual edits to
`~/.aws/config` will be lost** the next time you toggle anything. Edit master
instead — it's just an INI file.

### Drift detection

The live files are derived from master, so any disagreement between them is a
problem. `credswitch` detects two kinds and refuses to rewrite the live
files until you resolve them:

- **Orphan**: a profile exists in `~/.aws/` but not in master (e.g. you ran
  `aws configure --profile newone` directly).
- **Drifted**: the same profile exists in both, but the contents differ (e.g.
  you changed a region inline in `~/.aws/config`).

Both are surfaced by `credswitch list` and in the error you get when toggling
something. The output shows a per-profile diff and the three resolution paths:

- `credswitch sync <name>` — keep live's version, copy it into master.
- `credswitch enable <name>` (or `disable`) — take master's version,
  overwriting the drifted live entry.
- Delete the section from `~/.aws/` manually, then retry.

The toggle commands implicitly resolve drift on the profile being toggled
(master wins), so `disable foo; enable foo` is the quickest way to reset a
drifted profile to master.

### The `[default]` profile is special

`[default]` is **pass-through**: credswitch never overwrites live's default
section, and drift detection ignores it. This is so tools like `aws-cli`
(which write directly to `~/.aws/credentials` for things like key rotation)
can do their job. If you want to capture changes to default in master, run
`credswitch sync default`.

This trade-off is deliberate: silent loss of credentials is worse than a
loud error.

## Project layout

```
main.go        Cobra CLI wiring
manager.go     enable/disable/list/init logic, paths, state model
awsfile.go     AWS-style INI parsing and atomic writes
tui.go         Bubble Tea TUI
```
