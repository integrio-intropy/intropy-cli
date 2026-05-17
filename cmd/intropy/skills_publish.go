package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/intropy/intropy-cli/internal/skill/oci"
	"github.com/spf13/cobra"
)

type skillsPublishFlags struct {
	path  string
	ref   string
	tag   string
	force bool
	sign  bool
}

var skillsPublishOpts skillsPublishFlags

var skillsPublishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish a skill to an OCI registry",
	Long: `Packages a skill directory as an OCI artifact and pushes it to a
registry. --ref is the OCI repository path (without tag); --tag is the version
to publish. The tag becomes the skill version in the OCI config.

Example:
  intropy skills publish --path ./skills/pr-review --ref ghcr.io/example/skills/pr-review --tag 1.2.0`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ref := skillsPublishOpts.ref + ":" + skillsPublishOpts.tag
		if _, err := oci.ParseReference(ref); err != nil {
			return fmt.Errorf("publish: invalid ref %q: %w", ref, err)
		}

		art, err := oci.Pack(skillsPublishOpts.path)
		if err != nil {
			return fmt.Errorf("publish: pack: %w", err)
		}
		defer art.Content.Close()

		client, err := oci.NewClient()
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
	skillsPublishCmd.Flags().StringVar(&skillsPublishOpts.tag, "tag", "",
		"Version tag to publish (required)")
	skillsPublishCmd.Flags().BoolVar(&skillsPublishOpts.force, "force", false,
		"Overwrite the tag if it already exists")
	skillsPublishCmd.Flags().BoolVar(&skillsPublishOpts.sign, "sign", false,
		"Sign the artifact with cosign after publishing (requires cosign in PATH)")

	_ = skillsPublishCmd.MarkFlagRequired("path")
	_ = skillsPublishCmd.MarkFlagRequired("ref")
	_ = skillsPublishCmd.MarkFlagRequired("tag")

	skillsCmd.AddCommand(skillsPublishCmd)
}
