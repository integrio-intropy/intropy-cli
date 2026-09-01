package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/huandu/xstrings"

	"github.com/integrio-intropy/intropy-cli/internal/system"
	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// syncSystemRequest is the optional POST /api/systems/{path} body.
type syncSystemRequest struct {
	// Force lets an update overwrite diverged declaration files (sys
	// update --force) or a create render into a non-empty host directory.
	Force bool `json:"force,omitempty"`
}

// syncSystemResponse reports what a host sync did. Action says which verb
// ran: "update" folded orphans into an existing host, "create" assembled a
// new one, "none" found an up-to-date host and wrote nothing.
type syncSystemResponse struct {
	Action      string   `json:"action"`
	HostDir     string   `json:"hostDir,omitempty"`
	System      string   `json:"system,omitempty"`
	Added       []string `json:"added,omitempty"`
	Kept        []string `json:"kept,omitempty"`
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// syncSystem is POST /api/systems/{path...}: re-assemble one system's host
// the way the CLI would — `sys update` when the directory already has a
// host, `sys create` when it does not. The path is the system directory,
// never the workspace root of a multi-system workspace: sys update's
// single-host contract holds per system.
func (s *apiServer) syncSystem(w http.ResponseWriter, r *http.Request) {
	dir, err := s.resolveSystemDir(r.PathValue("path"))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	var req syncSystemRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxCreateBodyBytes))
	if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// The same serialization bargain template creates make: two concurrent
	// syncs of one host would race on its files, and each run may clone the
	// template library.
	s.createMu.Lock()
	defer s.createMu.Unlock()

	// The host renders from the release the dashboard holds, so assembling a
	// system and scaffolding into it can never pin different ones.
	tag, err := s.libraryTag(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	hosts, _ := template.ListSystemHosts(dir)
	if len(hosts) == 0 {
		s.createHost(w, r, dir, tag, req.Force)
		return
	}
	s.updateHost(w, r, dir, tag, req.Force)
}

// updateHost runs `sys update` against the system directory: fold every
// scaffolded-but-undeclared component into the existing host. No orphans is
// a successful no-op (action "none"), matching the command's own contract.
func (s *apiServer) updateHost(w http.ResponseWriter, r *http.Request, dir, tag string, force bool) {
	var out, logs bytes.Buffer
	err := system.Update(r.Context(), system.UpdateOptions{
		StartDir:   dir,
		Force:      force,
		Version:    tag,
		OutputJSON: "-",
		Stdout:     &out,
		Stderr:     &logs,
		UserAgent:  s.templates.userAgent,
		Owner:      s.templates.owner,
		Repo:       s.templates.repo,
		Source:     s.templates.source,
	})
	if err != nil {
		writeError(w, syncErrorStatus(err), err.Error())
		return
	}

	resp := syncSystemResponse{Action: "none", Diagnostics: diagnosticLines(logs.String())}
	// The result document is only written when the update added something;
	// an empty stdout is the no-orphans path.
	var res system.UpdateResult
	if out.Len() > 0 && json.Unmarshal(out.Bytes(), &res) == nil {
		resp.Action = "update"
		resp.HostDir = s.relPath(res.HostDir)
		resp.System = res.System
		resp.Added = res.Added
		resp.Kept = res.Kept
	}
	writeJSON(w, http.StatusOK, resp)
}

// createHost runs `sys create` for a system directory that has no host yet:
// the system is named after its directory (the same rule that labels the
// workspace pseudo-system) and the host renders inside it as <name>-host,
// a sibling of the components and the contracts project.
func (s *apiServer) createHost(w http.ResponseWriter, r *http.Request, dir, tag string, force bool) {
	name := hostSystemName(dir)
	var out, logs bytes.Buffer
	err := system.Create(r.Context(), system.CreateOptions{
		Name:       name,
		StartDir:   dir,
		OutputDir:  filepath.Join(dir, xstrings.ToKebabCase(name)+"-host"),
		Version:    tag,
		Force:      force,
		OutputJSON: "-",
		Stdout:     &out,
		Stderr:     &logs,
		UserAgent:  s.templates.userAgent,
		Owner:      s.templates.owner,
		Repo:       s.templates.repo,
		Source:     s.templates.source,
	})
	if err != nil {
		writeError(w, syncErrorStatus(err), err.Error())
		return
	}

	resp := syncSystemResponse{Action: "create", Diagnostics: diagnosticLines(logs.String())}
	var res system.CreateResult
	if out.Len() > 0 && json.Unmarshal(out.Bytes(), &res) == nil {
		resp.HostDir = s.relPath(res.OutputDir)
		resp.System = res.System.Name
		for _, c := range res.System.Components {
			resp.Added = append(resp.Added, c.AppID)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolveSystemDir confines a root-relative system path to the workspace,
// under the same segment rules a create request's dir obeys; "" and "."
// mean the workspace root itself. The directory must exist — the endpoint
// syncs systems, it does not invent them.
func (s *apiServer) resolveSystemDir(rel string) (string, error) {
	segments, err := splitCreateDir(rel)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(append([]string{s.root}, segments...)...)
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		return "", fmt.Errorf("directory %q does not exist under the workspace root", rel)
	}
	return dir, nil
}

// hostSystemName names a system created for dir after the directory itself,
// resolved to absolute first so the workspace root ("." from a relative
// invocation) still yields its folder name.
func hostSystemName(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Base(dir)
}

// syncErrorStatus maps a host sync failure onto the status the banner acts
// on: a divergence the command refuses to overwrite is a 409 (the retry
// offers force), a workspace-shape problem is a 422, and an upstream
// template fetch failure is a 502.
func syncErrorStatus(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "--force"), strings.Contains(msg, "not empty"):
		return http.StatusConflict
	case strings.Contains(msg, "fetch"), strings.Contains(msg, "download"):
		return http.StatusBadGateway
	case strings.Contains(msg, "system host"),
		strings.Contains(msg, "not a workspace"),
		strings.Contains(msg, "scaffold"),
		strings.Contains(msg, "component"):
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}
