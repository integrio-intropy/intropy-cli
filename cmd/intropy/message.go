package main

import (
	"sort"

	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/xregistry"
	"github.com/spf13/cobra"
)

// messageCmd groups the read-only discovery surface for messages: what the
// read-only xRegistry serves, plus what the workspace's scaffold records
// declare through publishes blocks. It never writes anything.
var messageCmd = &cobra.Command{
	Use:   "message",
	Short: "Discover registry and workspace messages",
	Long: "Discover messages. 'message list' merges the read-only xRegistry with the messages the workspace's scaffold records declare through publishes blocks; " +
		"'message show <ref>' prints one message's definition. Reading the registry requires registryUrl to be configured — there is no default",
}

// resolveRegistryURL layers the --registry-url flag over the config, the
// same precedence every other setting uses.
func resolveRegistryURL(flagURL string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	url, err := cfg.Resolve(config.Flags{RegistryURL: flagURL}).RequireRegistryURL()
	if err != nil {
		return "", newUsageErrorf("%v", err)
	}
	return url, nil
}

// registryClient builds the xRegistry client from the resolved
// --registry-url. An unconfigured registry is a usage error naming every
// way to set it; there is no guessed default.
func registryClient(flagURL string) (*xregistry.Client, error) {
	url, err := resolveRegistryURL(flagURL)
	if err != nil {
		return nil, err
	}
	return newXRegistryClient(url)
}

func newXRegistryClient(url string) (*xregistry.Client, error) {
	return xregistry.New(url, xregistry.WithUserAgent("intropy-cli/"+version))
}

// workspaceMessages indexes the publishes declarations under dir. Scan and
// parse warnings are non-fatal: a malformed sibling record must not hide
// the messages the rest of the workspace declares.
func workspaceMessages(dir string, warnf func(error)) []MessageEntry {
	entries, warnings := template.ListScaffolds(dir)
	for _, w := range warnings {
		warnf(w)
	}
	seen := map[string]bool{}
	var out []MessageEntry
	for _, e := range entries {
		pub, err := template.ReadPublishesBlock(e)
		if err != nil || pub == nil {
			// Malformed block: skipped here — listing must show what is
			// resolvable; assembly is the surface that reports the record.
			continue
		}
		if seen[pub.Message] {
			continue
		}
		seen[pub.Message] = true
		out = append(out, MessageEntry{
			Message:    pub.Message,
			Source:     messageSourceWorkspace,
			Type:       pub.Message,
			DataSchema: pub.Dataschema,
			Contract:   pub.Contract,
			Path:       e.Path,
		})
	}
	sortMessageEntries(out)
	return out
}

// sortMessageEntries orders a merged list: registry messages by ref, then
// workspace messages by name — the stable order both table and JSON echo.
func sortMessageEntries(entries []MessageEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Source != entries[j].Source {
			return entries[i].Source == messageSourceRegistry
		}
		return entries[i].Message < entries[j].Message
	})
}

func init() {
	rootCmd.AddCommand(messageCmd)
}
