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
		if len(st.Profiles) == 0 {
			fmt.Println("No profiles found. Run `credswitch init` to bootstrap from ~/.aws/.")
			return nil
		}
		for _, prof := range st.Profiles {
			marker := " "
			if prof.Enabled {
				marker = "x"
			}
			fmt.Printf("[%s] %-30s %s\n", marker, prof.Name, locationLabel(prof))
		}
		orphans, err := loadOrphans(p)
		if err != nil {
			return err
		}
		if len(orphans) > 0 {
			fmt.Println()
			fmt.Println("Unmanaged entries in ~/.aws/ (not in master):")
			for _, o := range orphans {
				fmt.Println("  -", o)
			}
			fmt.Println()
			fmt.Println("These block enable/disable. Move them into ~/.credswitch/ or remove from ~/.aws/.")
		}
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
	rootCmd.AddCommand(initCmd, listCmd, enableCmd, disableCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
