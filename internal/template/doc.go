// Package template downloads, validates, inspects, and renders Intropy
// scaffolding templates. (The package predates the "template" vocabulary;
// it keeps its name to avoid colliding with text/template.)
//
// A template is a template.yaml manifest plus a skeleton/ directory. The
// package supports three main workflows:
//   - List: enumerate the templates published in the library at a release.
//   - Describe: fetch a template manifest and return its metadata and parameters.
//   - Create: fetch a template, resolve parameter values, and render files.
//
// Create also writes a scaffold record (.intropy/scaffold.json) into the
// output directory, pinning the template, version, and resolved values, so
// later commands can re-fetch the exact template a project was built from.
//
// The output directory is the caller's --out-dir, defaulted by convention:
// the kebab-cased --name, or the kebab-cased resolved "name" parameter
// when the run names nothing at all. The convention is fixed — the same
// normalization sys create applies — so a name and its kebab form are one
// component everywhere the CLI looks.
package template
