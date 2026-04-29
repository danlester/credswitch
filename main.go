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
	rootCmd.AddCommand(initCmd, listCmd, enableCmd, disableCmd, syncCmd, revertCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		// Cobra already printed the error; just exit non-zero.
		os.Exit(1)
	}
}
