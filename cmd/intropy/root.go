package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	noColorFlag   bool
	changeDirFlag string
)

var rootCmd = &cobra.Command{
	Use:               "intropy",
	Short:             "Intropy CLI",
	Long:              "intropy is the command-line interface for working with Intropy integrations.",
	Version:           version,
	SilenceUsage:      true,
	SilenceErrors:     true,
	PersistentPreRunE: rootPreRun,
}

// chdirIfRequested applies -C before any preview warning, so the warning is
// never printed for a command that then fails to start.
func chdirIfRequested() error {
	if changeDirFlag == "" {
		return nil
	}
	if err := os.Chdir(changeDirFlag); err != nil {
		return fmt.Errorf("cannot change to directory %q: %w", changeDirFlag, err)
	}
	return nil
}

func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(rootCmd.ErrOrStderr(), "error:", err)
		return err
	}
	return nil
}

// validateOutputFlag returns a usageError if the given format is not in the
// allowed set. Use this for --output flags that accept a fixed list of formats.
func validateOutputFlag(format string, allowed ...string) error {
	for _, a := range allowed {
		if format == a {
			return nil
		}
	}
	return newUsageErrorf("invalid output format %q (allowed: %v)", format, allowed)
}

func init() {
	rootCmd.SetFlagErrorFunc(wrapFlagError)
	rootCmd.PersistentFlags().BoolVar(&noColorFlag, "no-color", false, "disable colored output")
	rootCmd.PersistentFlags().StringVarP(&changeDirFlag, "directory", "C", "", "change to directory before running the command")
	_ = rootCmd.MarkPersistentFlagDirname("directory")
}
