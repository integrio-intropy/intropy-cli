package main

import "github.com/spf13/cobra"

// Cobra reports its own errors untyped, which would land them at exit 1.
// Three seams cover the three places that happens, mapped to cobra's
// execute order (ParseFlags -> ValidateArgs -> PersistentPreRunE -> PreRunE
// -> ValidateRequiredFlags/ValidateFlagGroups -> RunE):
//
//   - Flag parse errors: wrapFlagError, installed on the root at init.
//     cobra's FlagErrorFunc walks the parent chain, so one install covers
//     every subcommand.
//   - Arg-count failures: usageArgs (main.go), wrapped around each
//     command's Args validator.
//   - Missing required flags and failed flag groups: rootPreRun validates
//     them and wraps the failure. cobra re-validates after PreRunE, but
//     validation that passed once cannot fail on the repeat.
//
// What remains untyped after this is the unknown-command error from cobra's
// Find, which runs before any command exists; exitCode sniffs its message.
func wrapFlagError(_ *cobra.Command, err error) error {
	return &usageError{err: err}
}

// rootPreRun is the root command's PersistentPreRunE, named so a subcommand
// with its own pre-run can invoke it rather than silently shadowing it
// (cobra runs only the innermost).
func rootPreRun(cmd *cobra.Command, args []string) error {
	if err := cmd.ValidateRequiredFlags(); err != nil {
		return &usageError{err: err}
	}
	if err := cmd.ValidateFlagGroups(); err != nil {
		return &usageError{err: err}
	}
	if err := chdirIfRequested(); err != nil {
		return err
	}
	warnIfPreview(cmd)
	return nil
}
