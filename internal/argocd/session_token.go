package argocd

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// errNoSessionToken marks every way mintSessionToken can fail quietly; the
// caller falls back to the static configuration rather than surfacing it.
var errNoSessionToken = errors.New("no session token from the argocd CLI")

// sessionTokenTimeout bounds the argocd CLI call. A healthy one answers in
// well under a second; an SSO refresh adds one round trip to the issuer.
const sessionTokenTimeout = 10 * time.Second

// sessionTokenCommand is replaced in tests to avoid needing a real argocd
// binary and a live server.
var sessionTokenCommand = func(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, sessionTokenTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "argocd", "account", "session-token").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// mintSessionToken asks the argocd CLI for a session token.
//
// The argocd CLI owns everything this CLI would otherwise have to reimplement:
// reading its own configuration, noticing the stored token has expired, and
// redeeming the SSO refresh token for a fresh one (writing the rotated refresh
// token back so the next reader starts valid). Calling it means this CLI never
// stores a credential of its own — the token lives in memory for one run and
// the only at-rest store remains argocd's own config file.
//
// The error is deliberately unadorned: a missing argocd binary, a missing
// login, or a dead refresh token are all reported by LoadCredentials falling
// back to the static configuration, which has the better message.
func mintSessionToken(ctx context.Context) (string, error) {
	token, err := sessionTokenCommand(ctx)
	if err != nil {
		return "", errNoSessionToken
	}
	if token == "" {
		return "", errNoSessionToken
	}
	return token, nil
}
