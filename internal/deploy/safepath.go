package deploy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// destTree is the boundary a scaffold may write inside: the GitOps checkout, and
// nothing else on the machine.
//
// Every read and write goes through os.Root, so a path that leaves the directory
// is refused by the kernel rather than by our own string handling. That alone is
// not enough, for two reasons:
//
//   - os.Root still follows a symlink whose target stays inside the root, and
//     .git/hooks is inside the root — a link there turns a manifest write into
//     code that runs on the next commit;
//   - the paths come from a GitOps repository and from --domain/--system, neither
//     of which this CLI controls.
//
// So a symlink on any component of a destination path is a hard refusal. A
// GitOps tree has no legitimate use for one, and following it would mean writing
// somewhere the plan never named.
type destTree struct {
	root *os.Root

	// path is the directory as the user knows it, for messages. os.Root's own
	// errors are relative to the root and read as if the file were at /.
	path string
}

func openDestTree(dir string) (*destTree, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	return &destTree{root: root, path: dir}, nil
}

func (t *destTree) Close() error { return t.root.Close() }

// assertRelIsLocal refuses a repository-relative path that does not stay inside
// the repository.
//
// filepath.IsLocal is the whole check: it rejects an absolute path, an empty one,
// anything that climbs out with .., and the Windows spellings (drive-relative,
// UNC, reserved device names) that a Unix-only rule would miss.
func assertRelIsLocal(rel string) error {
	if rel == "" {
		return errors.New("empty repository-relative path")
	}
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return fmt.Errorf("%s is not a path inside the repository", rel)
	}
	return nil
}

// assertWritable reports whether rel may be written, without writing anything.
//
// It is called during classification as well as before the write, so a hostile
// tree is refused before the first byte lands — including under --plan, where
// nothing would be written at all.
func (t *destTree) assertWritable(rel string) error {
	if err := assertRelIsLocal(rel); err != nil {
		return err
	}

	// Top down, leaf last. The order is the point: a parent symlink has to be
	// caught before an Lstat of anything beneath it would traverse it.
	segments := strings.Split(path.Clean(rel), "/")
	for i := range segments {
		prefix := path.Join(segments[:i+1]...)
		leaf := i == len(segments)-1

		info, err := t.root.Lstat(prefix)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Nothing from here down exists yet, so there is nothing left to
				// follow: the write will create it.
				return nil
			}
			return fmt.Errorf("inspect %s: %w", filepath.Join(t.path, filepath.FromSlash(prefix)), err)
		}

		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink; refusing to write %s through it, because a symlink on a path this command writes can redirect the write out of the repository",
				filepath.Join(t.path, filepath.FromSlash(prefix)), rel)
		case !leaf && !info.IsDir():
			return fmt.Errorf("%s is not a directory, so %s cannot be written", filepath.Join(t.path, filepath.FromSlash(prefix)), rel)
		case leaf && !info.Mode().IsRegular():
			return fmt.Errorf("%s is not a regular file", filepath.Join(t.path, filepath.FromSlash(prefix)))
		}
	}
	return nil
}

// read returns the file's current contents, reporting fs.ErrNotExist unchanged
// so a caller can tell "not there yet" from a failure to read it.
func (t *destTree) read(rel string) ([]byte, error) {
	return t.root.ReadFile(rel)
}

// write creates the file at rel and refuses one that appeared after
// classification. O_EXCL preserves create-only semantics and cannot follow a
// link, so the write does not depend on the earlier path check still holding.
func (t *destTree) write(rel string, data []byte) error {
	if dir := path.Dir(rel); dir != "." {
		if err := t.root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	f, err := t.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", rel, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return f.Close()
}

// assertPathSegment refuses a name that is not a single directory or file name.
//
// --domain, --system and a component name from the topology all become tree
// segments. destTree already makes a traversal impossible at the write, but the
// error there names a rendered path; this one names the input that was wrong.
// They also become part of a branch name, where a slash would silently nest a
// ref rather than fail.
func assertPathSegment(what, name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%s is empty", what)
	case name == "." || name == "..":
		return fmt.Errorf("invalid %s %q", what, name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("invalid %s %q (must not start with a dot)", what, name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("invalid %s %q (must be a single path segment)", what, name)
	}
	return nil
}
