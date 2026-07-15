// Package blueprint downloads, validates, describes, and renders Intropy
// scaffolding templates. (The package predates the "template" vocabulary;
// it keeps its name to avoid colliding with text/template.)
//
// A template is a template.yaml manifest plus a skeleton/ directory. The
// package supports two main workflows:
//   - Describe: fetch a template manifest and return its metadata and parameters.
//   - Create: fetch a template, resolve parameter values, and render files.
//
// Create also writes a scaffold record (.intropy/scaffold.json) into the
// output directory, pinning the template, version, and resolved values.
//
// A template may additionally carry a manifests/ directory — a second
// template.yaml + skeleton/ pair with the same contract — holding Kubernetes
// deployment manifest templates. It is consumed by the deploy package
// (`intropy manifests create`), which re-fetches the pinned version from the
// scaffold record and renders manifests/skeleton with values seeded from the
// record.
package blueprint
