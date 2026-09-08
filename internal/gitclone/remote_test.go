package gitclone

import "testing"

func TestSameRepository(t *testing.T) {
	same := [][2]string{
		{"git@gitlab.com:acme/gitops.git", "https://gitlab.com/acme/gitops"},
		{"https://gitlab.com/acme/gitops.git", "https://gitlab.com/acme/gitops/"},
		{"ssh://git@gitlab.com/acme/gitops.git", "git@gitlab.com:acme/gitops"},
		{"https://GitLab.com/acme/gitops", "https://gitlab.com/acme/gitops"},
		{"https://user:token@gitlab.com/acme/gitops.git", "git@gitlab.com:acme/gitops.git"},
		// A written-out default port says nothing a bare URL does not.
		{"https://gitlab.com:443/acme/gitops", "https://gitlab.com/acme/gitops"},
		{"http://gitlab.com:80/acme/gitops", "http://gitlab.com/acme/gitops"},
		{"ssh://git@gitlab.com:22/acme/gitops.git", "git@gitlab.com:acme/gitops"},
		{"https://git.example.com:8443/org/repo", "https://git.example.com:8443/org/repo/"},
		{"/tmp/origin", "/tmp/origin/"},
		{"file:///tmp/origin", "file:///tmp/origin/"},
	}
	for _, pair := range same {
		if !SameRepository(pair[0], pair[1]) {
			t.Errorf("%q and %q should be the same repository", pair[0], pair[1])
		}
	}

	different := [][2]string{
		{"git@gitlab.com:acme/gitops.git", "git@gitlab.com:acme/other.git"},
		{"https://gitlab.com/acme/gitops", "https://github.com/acme/gitops"},
		{"https://gitlab.com/acme/gitops", "https://gitlab.com/other/gitops"},
		{"/tmp/origin", "/tmp/other"},
		// Two ports on one address can be two entirely different servers, so a
		// non-default port is part of the identity.
		{"https://git.example.com:8443/org/repo", "https://git.example.com:9443/org/repo"},
		{"https://git.example.com:8443/org/repo", "https://git.example.com/org/repo"},
		{"ssh://git@git.example.com:2222/org/repo", "ssh://git@git.example.com/org/repo"},
		// scp-form has no port: git reads the whole of "2222/org/repo" as the path.
		{"git@git.example.com:2222/org/repo", "ssh://git@git.example.com:2222/org/repo"},
		// Case matters in a path: hosting providers differ on whether it does, and
		// re-cloning a cache costs a clone whereas conflating two repositories
		// would deploy to the wrong one.
		{"https://gitlab.com/Acme/gitops", "https://gitlab.com/acme/gitops"},
		{"", "https://gitlab.com/acme/gitops"},
	}
	for _, pair := range different {
		if SameRepository(pair[0], pair[1]) {
			t.Errorf("%q and %q should not be the same repository", pair[0], pair[1])
		}
	}
}
