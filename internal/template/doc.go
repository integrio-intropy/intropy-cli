// Package template downloads, validates, describes, and renders Intropy
// scaffolding templates. (The package predates the "template" vocabulary;
// it keeps its name to avoid colliding with text/template.)
//
// A template is a template.yaml manifest plus a skeleton/ directory. The
// package supports two main workflows:
//   - Describe: fetch a template manifest and return its metadata and parameters.
//   - Create: fetch a template, resolve parameter values, and render files.
//
// Create also writes a scaffold record (.intropy/scaffold.json) into the
// output directory, pinning the template, version, and resolved values, so
// later commands can re-fetch the exact template a project was built from.
package template
