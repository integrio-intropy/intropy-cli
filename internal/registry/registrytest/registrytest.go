// Package registrytest provides an in-memory OCI distribution registry for
// tests. It implements only the endpoints oras-go exercises: the version
// check, monolithic blob upload (with cross-repo mount), blob fetch, manifest
// push/fetch by tag or digest, and tag listing. Chunked uploads, the referrers
// API, pagination, and authentication challenges are not implemented.
package registrytest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type manifest struct {
	mediaType string
	data      []byte
}

// Registry is an in-memory OCI distribution registry.
type Registry struct {
	mu        sync.Mutex
	blobs     map[string]map[string][]byte   // repository → digest → content
	manifests map[string]map[string]manifest // repository → digest → manifest
	tags      map[string]map[string]string   // repository → tag → digest
	uploads   map[string]string              // upload id → repository
	nextID    int

	userAgents []string
}

// Server is a Registry served over HTTP.
type Server struct {
	*httptest.Server
	Registry *Registry
	// Host is the host:port of the server, suitable for building references.
	Host string
}

func New() *Registry {
	return &Registry{
		blobs:     map[string]map[string][]byte{},
		manifests: map[string]map[string]manifest{},
		tags:      map[string]map[string]string{},
		uploads:   map[string]string{},
	}
}

// NewServer starts a Registry on a local HTTP listener. The caller must
// Close the returned server.
func NewServer() *Server {
	reg := New()
	srv := httptest.NewServer(reg)
	return &Server{
		Server:   srv,
		Registry: reg,
		Host:     strings.TrimPrefix(srv.URL, "http://"),
	}
}

// UserAgents returns the User-Agent header of every request received.
func (r *Registry) UserAgents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.userAgents...)
}

// HasManifest reports whether the repository holds a manifest with digest.
func (r *Registry) HasManifest(repo, digest string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.manifests[repo][digest]
	return ok
}

func (r *Registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.userAgents = append(r.userAgents, req.UserAgent())
	r.mu.Unlock()

	if req.URL.Path == "/v2/" || req.URL.Path == "/v2" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
		return
	}

	rest, ok := strings.CutPrefix(req.URL.Path, "/v2/")
	if !ok {
		writeError(w, http.StatusNotFound, "UNSUPPORTED", "not a registry endpoint")
		return
	}

	// Repository names contain slashes, so split on the last operation
	// segment rather than a fixed position.
	switch {
	case strings.HasSuffix(rest, "/tags/list"):
		repo := strings.TrimSuffix(rest, "/tags/list")
		r.handleTags(w, req, repo)
	case strings.Contains(rest, "/blobs/uploads/"):
		repo, id, _ := strings.Cut(rest, "/blobs/uploads/")
		r.handleUpload(w, req, repo, id)
	case strings.Contains(rest, "/blobs/"):
		repo, digest, _ := strings.Cut(rest, "/blobs/")
		r.handleBlob(w, req, repo, digest)
	case strings.Contains(rest, "/manifests/"):
		repo, ref, _ := strings.Cut(rest, "/manifests/")
		r.handleManifest(w, req, repo, ref)
	default:
		writeError(w, http.StatusNotFound, "UNSUPPORTED", "not a registry endpoint")
	}
}

// handleTags serves the tag listing. An unknown repository is a 404 rather
// than an empty list, matching a real registry: a caller cannot otherwise tell
// "never pushed" from "pushed and since emptied". Tags come back sorted so
// tests do not depend on map iteration order. Pagination is not implemented,
// so the n and last parameters are ignored.
func (r *Registry) handleTags(w http.ResponseWriter, req *http.Request, repo string) {
	if req.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "tag listing is read-only")
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	repoTags, ok := r.tags[repo]
	if !ok {
		writeError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository unknown")
		return
	}

	tags := make([]string, 0, len(repoTags))
	for tag := range repoTags {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags})
}

func (r *Registry) handleUpload(w http.ResponseWriter, req *http.Request, repo, id string) {
	switch {
	case req.Method == http.MethodPost && id == "":
		// Cross-repo mount: satisfy it directly when the source repository
		// already holds the blob, otherwise fall through to a session.
		if mount := req.URL.Query().Get("mount"); mount != "" {
			from := req.URL.Query().Get("from")
			r.mu.Lock()
			data, ok := r.blobs[from][mount]
			if ok {
				r.putBlobLocked(repo, mount, data)
			}
			r.mu.Unlock()
			if ok {
				w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", repo, mount))
				w.Header().Set("Docker-Content-Digest", mount)
				w.WriteHeader(http.StatusCreated)
				return
			}
		}

		r.mu.Lock()
		r.nextID++
		uploadID := strconv.Itoa(r.nextID)
		r.uploads[uploadID] = repo
		r.mu.Unlock()

		w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", repo, uploadID))
		w.WriteHeader(http.StatusAccepted)

	case req.Method == http.MethodPut && id != "":
		r.mu.Lock()
		_, known := r.uploads[id]
		r.mu.Unlock()
		if !known {
			writeError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "unknown upload")
			return
		}

		data, err := io.ReadAll(req.Body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
			return
		}
		want := req.URL.Query().Get("digest")
		if got := digestOf(data); want != got {
			writeError(w, http.StatusBadRequest, "DIGEST_INVALID", fmt.Sprintf("digest %s does not match content %s", want, got))
			return
		}

		r.mu.Lock()
		r.putBlobLocked(repo, want, data)
		delete(r.uploads, id)
		r.mu.Unlock()

		w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", repo, want))
		w.Header().Set("Docker-Content-Digest", want)
		w.WriteHeader(http.StatusCreated)

	default:
		writeError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "unsupported upload method")
	}
}

func (r *Registry) handleBlob(w http.ResponseWriter, req *http.Request, repo, digest string) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "unsupported blob method")
		return
	}

	r.mu.Lock()
	data, ok := r.blobs[repo][digest]
	r.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if req.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func (r *Registry) handleManifest(w http.ResponseWriter, req *http.Request, repo, ref string) {
	switch req.Method {
	case http.MethodPut:
		data, err := io.ReadAll(req.Body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
			return
		}
		digest := digestOf(data)
		mediaType := req.Header.Get("Content-Type")

		r.mu.Lock()
		if r.manifests[repo] == nil {
			r.manifests[repo] = map[string]manifest{}
		}
		r.manifests[repo][digest] = manifest{mediaType: mediaType, data: data}
		if !strings.HasPrefix(ref, "sha256:") {
			if r.tags[repo] == nil {
				r.tags[repo] = map[string]string{}
			}
			r.tags[repo][ref] = digest
		}
		r.mu.Unlock()

		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Location", fmt.Sprintf("/v2/%s/manifests/%s", repo, digest))
		w.WriteHeader(http.StatusCreated)

	case http.MethodGet, http.MethodHead:
		r.mu.Lock()
		digest := ref
		if !strings.HasPrefix(ref, "sha256:") {
			digest = r.tags[repo][ref]
		}
		m, ok := r.manifests[repo][digest]
		r.mu.Unlock()
		if !ok {
			writeError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
			return
		}

		w.Header().Set("Content-Type", m.mediaType)
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Content-Length", strconv.Itoa(len(m.data)))
		w.WriteHeader(http.StatusOK)
		if req.Method == http.MethodGet {
			_, _ = w.Write(m.data)
		}

	default:
		writeError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "unsupported manifest method")
	}
}

func (r *Registry) putBlobLocked(repo, digest string, data []byte) {
	if r.blobs[repo] == nil {
		r.blobs[repo] = map[string][]byte{}
	}
	r.blobs[repo][digest] = data
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{{"code": code, "message": message}},
	})
}
