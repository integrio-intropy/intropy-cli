package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/integrio-intropy/intropy-cli/internal/dashboard"
	"github.com/spf13/cobra"
)

// dashboard serves the local integration dashboard. The HTTP server and
// JSON API live in internal/dashboard; this file is flag plumbing.
type dashboardFlags struct {
	port            int
	noBrowser       bool
	templateVersion string
}

var dashboardOpts dashboardFlags

var dashboardCmd = &cobra.Command{
	Use:   "dashboard [dir]",
	Short: "Launch the local integration dashboard",
	Long: "Start a local web dashboard that visualizes the integrations and systems scaffolded under dir " +
		"(default: the current directory) — their template, pinned source, version, scaffold values and " +
		"system topology. The flow view can also start and stop a system's host locally (dotnet run); " +
		"started hosts stop when the dashboard does. The dashboard is served from the CLI itself and " +
		"opens in your browser. Press Ctrl+C to stop.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root := "."
		if len(args) == 1 {
			root = args[0]
		}
		if info, err := os.Stat(root); err != nil {
			return fmt.Errorf("dashboard: %w", err)
		} else if !info.IsDir() {
			return newUsageErrorf("dashboard: %s is not a directory", root)
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return dashboard.Serve(ctx, dashboard.Options{
			Root:            root,
			Addr:            "127.0.0.1",
			Port:            dashboardOpts.port,
			OpenBrowser:     !dashboardOpts.noBrowser,
			Version:         version,
			TemplateVersion: dashboardOpts.templateVersion,
			Organization:    resolvedOrganization(),
			Stdout:          cmd.OutOrStdout(),
			Stderr:          cmd.ErrOrStderr(),
		})
	},
}

// resolvedOrganization reads the config's organization for the
// dashboard's template suggestions. A config that cannot be read yields
// "": the dashboard serves workspaces without one, and template forms
// simply offer no organization candidate.
func resolvedOrganization() string {
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	return cfg.Resolve(config.Flags{}).Organization
}

func init() {
	f := dashboardCmd.Flags()
	f.IntVarP(&dashboardOpts.port, "port", "p", 8730, "port to bind (0 picks a free port)")
	f.BoolVar(&dashboardOpts.noBrowser, "no-browser", false, "do not open the dashboard in a browser")
	f.StringVar(&dashboardOpts.templateVersion, "template-version", "", flagUsageTemplateVer)
	markPreview(dashboardCmd)
	rootCmd.AddCommand(dashboardCmd)
}
