package template

import (
	"fmt"
	"path"
	"strings"
)

// skeletonFilter decides which skeleton paths a render includes, from the
// template's spec.files rules.
//
// A when depends only on the resolved values, never on the path being tested,
// so each rule is evaluated at most once per render and the result cached.
type skeletonFilter struct {
	rules  []FileRule
	values map[string]any
	cache  map[int]bool
}

func newSkeletonFilter(rules []FileRule, values map[string]any) *skeletonFilter {
	return &skeletonFilter{rules: rules, values: values, cache: make(map[int]bool, len(rules))}
}

// include reports whether rel — a slash-separated path relative to the
// skeleton root, with any .tmpl suffix still attached — should be rendered.
//
// The first rule whose Path matches decides, so a specific rule can override a
// broader one placed after it. A path no rule matches is included, which is
// what keeps a template without spec.files rendering exactly as before.
func (f *skeletonFilter) include(rel string) (bool, error) {
	for i, rule := range f.rules {
		if !matchSkeletonPath(rule.Path, rel) {
			continue
		}
		if got, ok := f.cache[i]; ok {
			return got, nil
		}
		rendered, err := renderExpr(rule.When, f.values)
		if err != nil {
			return false, fmt.Errorf("spec.files[%d] (%s): evaluate when: %w", i, rule.Path, err)
		}
		got := truthy(rendered)
		f.cache[i] = got
		return got, nil
	}
	return true, nil
}

// matchSkeletonPath matches a slash-separated skeleton-relative path against a
// rule pattern.
//
// A trailing "/**" matches the directory itself as well as everything beneath
// it. Matching the directory is what lets the walk prune the subtree before any
// of its bodies are parsed. Otherwise path.Match applies, which does not cross
// separators — so "dapr/*.yaml" cannot silently prune "dapr/nested/x.yaml".
func matchSkeletonPath(pattern, rel string) bool {
	if dir, ok := strings.CutSuffix(pattern, "/**"); ok {
		return rel == dir || strings.HasPrefix(rel, dir+"/")
	}
	ok, err := path.Match(pattern, rel)
	// A malformed pattern cannot match. validate() rejects one at load time, so
	// this is unreachable in practice.
	return err == nil && ok
}

// truthy interprets a rendered when. Anything other than empty, "false", "0" or
// a nil interpolation counts as included, so `{{ eq .pubsub "servicebus" }}`
// and a plain `{{ .someString }}` both read naturally.
func truthy(rendered string) bool {
	switch strings.TrimSpace(rendered) {
	case "", "false", "0", "<no value>":
		return false
	default:
		return true
	}
}
