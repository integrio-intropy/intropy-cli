package deploy

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/git"
)

// assertStagedIsExactly checks the index just before a commit is made from it.
//
// Every caller in this package stages a named list of paths and never `add -A`,
// which is what lets a deployment commit be read as a statement about one
// component. That property is worth verifying rather than assuming: the checkout
// is shared, git runs code the repository supplies while building a commit, and
// the difference between what was asked for and what was recorded is one command
// away.
func assertStagedIsExactly(ctx context.Context, g git.Client, expected []string) error {
	staged, err := g.StagedPaths(ctx)
	if err != nil {
		return err
	}
	return assertOnly("staged", expected, staged)
}

// assertCommittedIsExactly checks HEAD, which is the last moment before a push
// makes it everyone's.
//
// Both halves are checked because they answer different questions: the index is
// what the commit is about to be made from, and this is what a push would publish,
// with a hook's opportunity to run in between. It has to be a second call rather
// than a replacement — once the commit exists the index matches HEAD and has
// nothing left to report.
func assertCommittedIsExactly(ctx context.Context, g git.Client, expected []string) error {
	head, err := g.HEAD(ctx)
	if err != nil {
		return err
	}
	committed, err := g.CommitPaths(ctx, head)
	if err != nil {
		return err
	}
	return assertOnly("committed", expected, committed)
}

// assertOnly refuses anything git recorded that was not asked for.
//
// A subset rather than an equality: a file whose content already matches is
// staged and changes nothing, so a shorter list than expected is normal. A longer
// one is not, and there is no benign explanation for it.
func assertOnly(what string, expected, actual []string) error {
	var extra []string
	for _, p := range actual {
		if !slices.Contains(expected, p) {
			extra = append(extra, p)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	slices.Sort(extra)
	return fmt.Errorf("refusing to continue: %d path%s %s that this command did not ask for:\n  %s\n\nNothing has been pushed. A git hook or another process is writing into the cached checkout",
		len(extra), plural(len(extra)), what, strings.Join(extra, "\n  "))
}
