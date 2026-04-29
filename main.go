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
		fmt.Printf("Master files copied to %s\n", p.MasterDir)
		fmt.Println("Live files in ~/.aws/ are unchanged. Run `credswitch disable <name>`")
		fmt.Println("or use the TUI (`credswitch`) to start trimming them.")
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

		if len(st.Profiles) == 0 && len(drift) == 0 {
			fmt.Println("No profiles found. Run `credswitch init` to bootstrap from ~/.aws/.")
			return nil
		}
		for _, prof := range st.Profiles {
			marker := " "
			if prof.Enabled {
				marker = "x"
			}
			annot := ""
			if drifted[prof.Name] {
				annot = "  DRIFTED"
			}
			fmt.Printf("[%s] %-30s %s%s\n", marker, prof.Name, locationLabel(prof), annot)
		}
		if len(drift) > 0 {
			fmt.Println()
			fmt.Println(formatDrift(drift))
		}
		return nil
	},
}

var syncCmd = &cobra.Command{
	Use:   "sync <profile>",
	Short: "Copy a profile from ~/.aws/ into master (live wins for that profile)",
	Long: `sync copies the named profile's section(s) from ~/.aws/ into ~/.credswitch/,
overwriting whatever was there. Use this to adopt an orphan profile, or to
resolve drift by keeping the live version.

The opposite direction (master wins, overwriting live) happens automatically
when you run enable or disable on the same profile.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := syncToMaster(defaultPaths(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Synced %s from ~/.aws/ into master\n", args[0])
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
		fmt.Printf("Enabled %s\n", args[0])
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
		fmt.Printf("Disabled %s\n", args[0])
		return nil
	},
}

func locationLabel(p Profile) string {
	switch {
	case p.InConfig && p.InCreds:
		return "config+creds"
	case p.InConfig:
		return "config"
	case p.InCreds:
		return "creds"
	}
	return ""
}

func main() {
	rootCmd.AddCommand(initCmd, listCmd, enableCmd, disableCmd, syncCmd)
	if err := rootCmd.Execute(); err != nil {
		// Cobra already printed the error; just exit non-zero.
		os.Exit(1)
	}
}
