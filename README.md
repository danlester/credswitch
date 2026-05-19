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
credswitch enable <name>    # add <name> to the live AWS files
credswitch disable <name>   # remove <name> from the live AWS files
credswitch sync <name>      # live -> master  (keep live, copy to master)
credswitch revert <name>    # master -> live  (master wins; for orphans, removes from live)
credswitch reap             # disable all profiles tagged ephemeral (see below)
credswitch install-agent    # auto-run reap at login + first wake each day (macOS)
credswitch uninstall-agent  # remove the LaunchAgent
```

In the TUI: `space` toggle, `e` ephemeral on/off, `s` sync, `r` revert, `q` quit.

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

The live files can disagree with master in two ways:

- **Orphan**: a profile exists in `~/.aws/` but not in master (e.g. you ran
  `aws configure --profile newone` directly).
- **Drifted**: the same profile exists in both but the contents differ (e.g.
  you changed a region inline in `~/.aws/config`).

Both are surfaced in `credswitch list` and the TUI with `ORPHAN` / `DRIFTED`
annotations. The bottom of `list` shows a per-profile diff.

`enable`, `disable`, and the TUI toggle are **all blocked** for any profile
in drift or orphan state. They would silently destroy live data — either
overwrite your edits (enable) or delete the orphan content (disable). You
have to resolve drift explicitly with `sync` or `revert`:

- `credswitch sync <name>` — keep live's version, copy it into master.
- `credswitch revert <name>` — make live match master. For drifted profiles,
  live's content is overwritten with master's. For orphans, the live entry
  is removed (master has nothing to bring forward).

Both commands reduce the profile to a "clean" state, and `enable` /
`disable` work normally on it again.

Drift on profile X never blocks operations on profile Y. Each profile is
gated independently.

### Ephemeral profiles

Some profiles shouldn't sit enabled all day — high-blast-radius admin
profiles you only enable for a quick task. Tag them as **ephemeral** and
`credswitch reap` will disable them on demand (or on a schedule).

To tag a profile: highlight it in the TUI and press `e`. It'll show an
`EPHEMERAL` annotation. Press `e` again to untag.

The list lives at `~/.credswitch/ephemeral` — one bare name per line, `#`
comments allowed. You can edit it by hand too:

```
workstuffadmin
prod-deploy
# personal-aws  (commented out — not currently ephemeral)
```

`credswitch reap` disables every currently-enabled profile in that list.
Profiles with drift or orphan state are skipped with a stderr warning —
reap never clobbers live edits, consistent with the drift gate. Profiles
that are already disabled, or missing entirely, are silently ignored.

`list` and the TUI show an `EPHEMERAL` annotation next to tagged profiles.

#### Auto-reap on macOS

```sh
credswitch install-agent              # default: daily at 04:00 + every login
credswitch install-agent --hour 3     # change the time
credswitch uninstall-agent
```

This writes `~/Library/LaunchAgents/com.credswitch.reap.plist` pointing at
the current binary and loads it via `launchctl`. The agent fires:

- at every full login (`RunAtLoad`)
- once per day at the scheduled time. **If the Mac is asleep at that time,
  launchd fires the missed job on the next wake** — so picking a small-hours
  time (default 04:00) effectively means "first unlock of the day".

It is *not* per-unlock — a lunch-break unlock won't re-reap once the day's
fire has happened. Logs go to `~/.credswitch/reap.log`.

Re-run `install-agent` after upgrading or moving the binary (`go install`
keeps the path stable at `~/go/bin/credswitch`, so this is rare).

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
