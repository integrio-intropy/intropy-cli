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
	renders, err := f.expand(rel)
	return len(renders) > 0, err
}

// expand returns the value sets rel renders with: none when a rule excludes
// it, the render's own values when it is included, and one set per element
// when the deciding rule expands over a list (Each), each binding its element
// and index. The first rule whose Path matches decides, as for include.
func (f *skeletonFilter) expand(rel string) ([]map[string]any, error) {
	for i, rule := range f.rules {
		if !matchSkeletonPath(rule.Path, rel) {
			continue
		}
		if rule.When != "" {
			got, ok := f.cache[i]
			if !ok {
				rendered, err := renderExpr(rule.When, f.values)
				if err != nil {
					return nil, fmt.Errorf("spec.files[%d] (%s): evaluate when: %w", i, rule.Path, err)
				}
				got = truthy(rendered)
				f.cache[i] = got
			}
			if !got {
				return nil, nil
			}
		}
		if rule.Each == "" {
			return []map[string]any{f.values}, nil
		}
		return f.eachRenders(i, rule)
	}
	return []map[string]any{f.values}, nil
}

// eachRenders binds every element of the rule's list parameter into its own
// copy of the values. A missing or empty list yields no renders; a value
// that is not a list is an error naming the rule.
func (f *skeletonFilter) eachRenders(i int, rule FileRule) ([]map[string]any, error) {
	as := rule.As
	if as == "" {
		as = "item"
	}
	raw, ok := f.values[rule.Each]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("spec.files[%d] (%s): each %q is a %T, not a list", i, rule.Path, rule.Each, raw)
	}
	renders := make([]map[string]any, len(items))
	for n, item := range items {
		v := make(map[string]any, len(f.values)+2)
		for k, val := range f.values {
			v[k] = val
		}
		v[as] = item
		v[as+"Index"] = n
		renders[n] = v
	}
	return renders, nil
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
