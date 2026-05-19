package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "credswitch",
	Short: "Selectively enable/disable AWS profiles",
	Long: `credswitch keeps a master copy of all your AWS profiles in ~/.credswitch/
and only the ones you've explicitly enabled in ~/.aws/.

With no arguments, launches an interactive TUI. Use subcommands for scripted
toggling.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUI(defaultPaths())
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Bootstrap ~/.credswitch from your existing ~/.aws/ files",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := defaultPaths()
		if err := initMaster(p); err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Master files copied to %s\n", p.MasterDir)
		fmt.Fprintln(out, "Live files in ~/.aws/ are unchanged. Run `credswitch disable <name>`")
		fmt.Fprintln(out, "or use the TUI (`credswitch`) to start trimming them.")
		return nil
	},
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all profiles and their enabled state",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := defaultPaths()
		st, err := loadState(p)
		if err != nil {
			return err
		}
		drift, err := loadDrift(p)
		if err != nil {
			return err
		}
		drifted := driftedNames(drift)
		out := cmd.OutOrStdout()

		if len(st.Profiles) == 0 && len(drift) == 0 {
			fmt.Fprintln(out, "No profiles found. Run `credswitch init` to bootstrap from ~/.aws/.")
			return nil
		}
		for _, prof := range st.Profiles {
			marker := " "
			if prof.Enabled {
				marker = "x"
			}
			annot := ""
			switch {
			case prof.Orphan:
				annot = "  ORPHAN"
			case drifted[prof.Name]:
				annot = "  DRIFTED"
			}
			if prof.Ephemeral {
				annot += "  EPHEMERAL"
			}
			fmt.Fprintf(out, "[%s] %-30s %s%s\n", marker, prof.Name, locationLabel(prof), annot)
		}
		if len(drift) > 0 {
			fmt.Fprintln(out)
			fmt.Fprintln(out, formatDrift(drift))
		}
		return nil
	},
}

var syncCmd = &cobra.Command{
	Use:   "sync <profile>",
	Short: "Copy a profile from ~/.aws/ into master (live wins; live -> master)",
	Long: `sync copies the named profile's section(s) from ~/.aws/ into ~/.credswitch/,
overwriting whatever was there. Use this to resolve drift in favour of live,
or to adopt an orphan profile that you want to keep.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := syncToMaster(defaultPaths(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Synced %s from ~/.aws/ into master\n", args[0])
		return nil
	},
}

var revertCmd = &cobra.Command{
	Use:   "revert <profile>",
	Short: "Make ~/.aws/ match master for a profile (master wins; master -> live)",
	Long: `revert resolves drift by taking master's view of the profile.

For drifted profiles, live's content is overwritten with master's.
For orphan profiles (only in live), the live entry is removed entirely —
since master has nothing to bring forward.

Errors if the profile has no drift; use enable or disable instead.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := revertProfile(defaultPaths(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Reverted %s to master's version\n", args[0])
		return nil
	},
}

var enableCmd = &cobra.Command{
	Use:   "enable <profile>",
	Short: "Enable a profile (copy it from master into ~/.aws/)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := enableProfile(defaultPaths(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Enabled %s\n", args[0])
		return nil
	},
}

var disableCmd = &cobra.Command{
	Use:   "disable <profile>",
	Short: "Disable a profile (remove it from ~/.aws/)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := disableProfile(defaultPaths(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Disabled %s\n", args[0])
		return nil
	},
}

var (
	agentHour   int
	agentMinute int
)

var installAgentCmd = &cobra.Command{
	Use:   "install-agent",
	Short: "Install a macOS LaunchAgent that runs `reap` at login and once a day",
	Long: `Generates ~/Library/LaunchAgents/com.credswitch.reap.plist pointing at the
current credswitch binary and loads it via launchctl.

The agent runs on:
  - every full login (RunAtLoad)
  - once daily at --hour:--minute (default 04:00). If the Mac is asleep at
    that time, launchd fires the missed job on the next wake — this is the
    trigger that effectively catches "first unlock of the day".

Re-run after upgrading or moving the binary; the plist freezes the path at
install time.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := installAgent(defaultPaths(), agentHour, agentMinute)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installed and loaded LaunchAgent at %s\n", path)
		fmt.Fprintf(cmd.OutOrStdout(), "Scheduled daily at %02d:%02d; runs at every login.\n", agentHour, agentMinute)
		return nil
	},
}

var uninstallAgentCmd = &cobra.Command{
	Use:   "uninstall-agent",
	Short: "Unload and remove the credswitch LaunchAgent",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := uninstallAgent()
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed LaunchAgent at %s\n", path)
		return nil
	},
}

var reapCmd = &cobra.Command{
	Use:   "reap",
	Short: "Disable every enabled profile listed in ~/.credswitch/ephemeral",
	Long: `reap disables every currently-enabled profile listed in
~/.credswitch/ephemeral (one bare name per line; '#' comments allowed).

Drifted or orphan profiles are skipped with a warning — resolve them with
sync/revert first. Intended for automatic invocation via a LaunchAgent at
login or overnight, but safe to run by hand.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := reapEphemeral(defaultPaths())
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()
		for _, name := range r.Reaped {
			fmt.Fprintf(out, "Disabled %s (ephemeral)\n", name)
		}
		for _, s := range r.Skipped {
			fmt.Fprintf(errOut, "warning: skipped %s — %s\n", s.Name, s.Reason)
		}
		if len(r.Reaped) == 0 && len(r.Skipped) == 0 {
			fmt.Fprintln(out, "Nothing to reap.")
		}
		return nil
	},
}

func locationLabel(p Profile) string {
	// For orphans, "where it exists" means the live files (master is empty
	// for them by definition). For master-known profiles, show the master
	// presence — that's what enable/disable actually targets.
	var inConfig, inCreds bool
	if p.Orphan {
		inConfig, inCreds = p.InLiveConfig, p.InLiveCreds
	} else {
		inConfig, inCreds = p.InMasterConfig, p.InMasterCreds
	}
	switch {
	case inConfig && inCreds:
		return "config+creds"
	case inConfig:
		return "config"
	case inCreds:
		return "creds"
	}
	return ""
}

func init() {
	installAgentCmd.Flags().IntVar(&agentHour, "hour", 4, "hour (0–23) for the daily reap")
	installAgentCmd.Flags().IntVar(&agentMinute, "minute", 0, "minute (0–59) for the daily reap")
	rootCmd.AddCommand(
		initCmd, listCmd, enableCmd, disableCmd,
		syncCmd, revertCmd, reapCmd,
		installAgentCmd, uninstallAgentCmd,
	)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		// Cobra already printed the error; just exit non-zero.
		os.Exit(1)
	}
}
