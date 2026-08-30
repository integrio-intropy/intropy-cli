package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// previewAnnotation marks a command as a preview in cmd.Annotations.
const previewAnnotation = "intropy.dev/preview"

// markPreview flags cmd as a preview command: it is shown with "(preview)" in
// help listings, its Long text carries the stability note, and running it
// prints a warning to stderr from the root's PersistentPreRunE.
//
// The warning is not emitted per command — cobra runs only the innermost
// PersistentPreRunE, so one here would shadow the root's -C handling.
func markPreview(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[previewAnnotation] = "true"
	cmd.Short += " (preview)"
	if cmd.Long != "" {
		cmd.Long += "\n\n"
	}
	cmd.Long += "This command is a preview. It is under evaluation and may change or be removed in a future release."
}

// isPreview reports whether cmd or any ancestor is marked as a preview, so a
// preview group's subcommands warn as well.
func isPreview(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[previewAnnotation] == "true" {
			return true
		}
	}
	return false
}

// warnIfPreview prints the preview warning for cmd to its error stream.
func warnIfPreview(cmd *cobra.Command) {
	if !isPreview(cmd) {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "warning: '%s' is a preview command and may change or be removed\n", cmd.CommandPath())
}
