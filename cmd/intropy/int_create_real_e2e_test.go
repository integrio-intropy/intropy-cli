//go:build e2e

package main

// The registry-resolved create flow was removed with the scalar message
// DSL: --publishes/--subscribe take the message name directly, and the
// command intake for those flags is pinned by the unit tests in this
// package. The end-to-end scaffold-and-render path against the real
// template library runs in internal/template and internal/system
// (INTROPY_TEMPLATES_DIR).
