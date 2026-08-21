package gitops

import (
	"net/url"
	"strings"
)

// SameRepository reports whether two remote URLs name the same repository.
//
// One repository has many spellings — SSH and HTTPS, scp-form and ssh://, with
// and without .git, with and without a trailing slash — and a cached checkout's
// origin is compared against a URL a human typed into config.yaml. Comparing the
// strings would re-clone the cache on a cosmetic difference.
//
// Deliberately not used to derive the cache directory: CheckoutDir hashes the URL
// exactly as given, so the two spellings get two caches. That is a wasted clone
// at worst, whereas treating two repositories as one would be a deploy to the
// wrong place. This function only ever decides "is this cache still ours".
func SameRepository(a, b string) bool {
	return normalizeRepoURL(a) == normalizeRepoURL(b)
}

// normalizeRepoURL reduces a remote URL to host, port and path.
//
// Credentials and scheme are dropped: neither changes which repository is named,
// and both differ routinely between the SSH form a clone records and the HTTPS
// form a human writes down. A port is kept unless it is the scheme's default,
// which is the only way to drop it without conflating two hosts: :8443 and :9443
// on one address can be entirely different servers, while :443 and no port on an
// https URL cannot be. An unparseable URL is returned trimmed rather than
// rejected, so it compares equal only to itself.
func normalizeRepoURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return ""
	}

	// scp-form: git@host:owner/repo, which is not a URL and would parse as the
	// "git@host" scheme with an opaque body. It carries no port — git reads
	// everything after the colon as the path, so git@host:2222/o/r is a repository
	// called 2222/o/r and ssh:// is the only way to name a port.
	if !strings.Contains(s, "://") {
		if host, path, ok := strings.Cut(s, ":"); ok {
			_, host, _ = strings.Cut(host, "@") // drop any user
			return strings.ToLower(host) + "/" + strings.Trim(path, "/")
		}
		// A local path, which the tests and a bare filesystem remote both use.
		return s
	}

	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		// file:///path — the path is the whole identity.
		return strings.TrimRight(u.Path, "/")
	}
	if port := u.Port(); port != "" && port != defaultPort(u.Scheme) {
		host += ":" + port
	}
	return host + "/" + strings.Trim(u.Path, "/")
}

// defaultPort is the port a scheme implies, so writing it out compares equal to
// leaving it off. An unrecognised scheme has no default, which keeps whatever port
// it was given rather than guessing that it does not matter.
func defaultPort(scheme string) string {
	switch strings.ToLower(scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	case "ssh":
		return "22"
	case "git":
		return "9418"
	}
	return ""
}
