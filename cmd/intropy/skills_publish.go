package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
	"github.com/spf13/cobra"
)

type skillsPublishFlags struct {
	path    string
	ref     string
	version string
	tag     string // deprecated alias for version
	force   bool
	sign    bool
}

var skillsPublishOpts skillsPublishFlags

var skillsPublishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish a skill to an OCI registry",
	Long: `Packages a skill directory as an OCI artifact and pushes it to a
registry. --ref is the OCI repository path (without tag); --version is the
version to publish. The version becomes the OCI tag and the skill version in
the OCI config.

Example:
  intropy skills publish --path ./skills/pr-review --ref ghcr.io/example/skills/pr-review --version 1.2.0`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// --version is the cross-CLI spelling (release create, deploy's
		// positional version); --tag predates that convention and stays as a
		// deprecated alias. The tag is the skill version in the OCI config,
		// so the two names never meant different things.
		version := skillsPublishOpts.version
		tagSet := cmd.Flags().Changed("tag")
		versionSet := cmd.Flags().Changed("version")
		switch {
		case tagSet && versionSet && skillsPublishOpts.tag != version:
			return newUsageErrorf("cannot combine --tag with --version (they are the same flag; --tag is deprecated)")
		case tagSet:
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: --tag is deprecated; use --version (matching release create and the deploy version argument)")
			if !versionSet {
				version = skillsPublishOpts.tag
			}
		}
		if version == "" {
			return newUsageErrorf("required flag(s) \"version\" not set")
		}

		ref := skillsPublishOpts.ref + ":" + version
		if _, err := oci.ParseReference(ref); err != nil {
			return fmt.Errorf("publish: invalid ref %q: %w", ref, err)
		}

		art, err := oci.Pack(skillsPublishOpts.path)
		if err != nil {
			return fmt.Errorf("publish: pack: %w", err)
		}
		defer art.Content.Close()

		client, err := newSkillRegistry()
		if err != nil {
			return fmt.Errorf("publish: %w", err)
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		if !skillsPublishOpts.force {
			if _, err := client.Resolve(ctx, ref); err == nil {
				return fmt.Errorf("publish: tag %s already exists; use --force to overwrite", ref)
			} else if !errors.Is(err, oci.ErrNotFound) {
				return fmt.Errorf("publish: pre-flight check: %w", err)
			}
		}

		desc, err := client.Push(ctx, ref, art)
		if err != nil {
			return fmt.Errorf("publish: push: %w", err)
		}

		cmd.Printf("Published %s\n", ref)
		cmd.Printf("  digest: %s\n", desc.Digest)
		cmd.Printf("  size:   %d bytes\n", desc.Size)

		if skillsPublishOpts.sign {
			if err := signWithCosign(cmd, ref); err != nil {
				return fmt.Errorf("publish: sign: %w", err)
			}
			cmd.Println("  signed: yes")
		}

		return nil
	},
}

func signWithCosign(cmd *cobra.Command, ref string) error {
	c := exec.Command("cosign", "sign", "--yes", ref)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	return c.Run()
}

func init() {
	skillsPublishCmd.Flags().StringVar(&skillsPublishOpts.path, "path", "",
		"Path to the skill directory (required)")
	skillsPublishCmd.Flags().StringVar(&skillsPublishOpts.ref, "ref", "",
		"OCI repository reference without tag (required)")
	skillsPublishCmd.Flags().StringVar(&skillsPublishOpts.version, "version", "",
		"Version to publish; becomes the OCI tag and the skill version (required)")
	skillsPublishCmd.Flags().StringVar(&skillsPublishOpts.tag, "tag", "",
		"Version to publish (deprecated: use --version)")
	_ = skillsPublishCmd.Flags().MarkHidden("tag")
	skillsPublishCmd.Flags().BoolVar(&skillsPublishOpts.force, "force", false,
		"Overwrite the tag if it already exists")
	skillsPublishCmd.Flags().BoolVar(&skillsPublishOpts.sign, "sign", false,
		"Sign the artifact with cosign after publishing (requires cosign in PATH)")

	_ = skillsPublishCmd.MarkFlagRequired("path")
	_ = skillsPublishCmd.MarkFlagRequired("ref")
	// One of --version or its deprecated alias --tag must be given; cobra's
	// required-flag machinery can't express that, so RunE checks it.

	skillsCmd.AddCommand(skillsPublishCmd)
}
