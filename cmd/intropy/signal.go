package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// commandContext returns cmd's context, cancelled on interrupt or terminate,
// so a long-running command stops on the first Ctrl-C rather than after the
// current network call.
func commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
}
