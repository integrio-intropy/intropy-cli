// Package blueprint downloads, validates, describes, and renders Intropy blueprints.
//
// A blueprint is a template.yaml manifest plus a skeleton/ directory. The
// package supports two main workflows:
//   - Describe: fetch a blueprint manifest and return its metadata and parameters.
//   - Create: fetch a blueprint, resolve parameter values, and render files.
package blueprint
