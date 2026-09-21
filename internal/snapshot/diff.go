package snapshot

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Severity ranks a contract change. Major = removal, rename or tightening:
// clients and skills built on the old contract can break.
type Severity int

const (
	None Severity = iota
	Patch
	Minor
	Major
)

func (s Severity) String() string {
	switch s {
	case Patch:
		return "patch"
	case Minor:
		return "minor"
	case Major:
		return "major"
	}
	return "none"
}

// Change is one difference between two snapshots.
type Change struct {
	Severity Severity
	Text     string
}

// Report is the full comparison. captured_at is never compared.
type Report struct {
	Changes []Change
}

// Severity is the highest severity among the changes.
func (r Report) Severity() Severity {
	max := None
	for _, c := range r.Changes {
		if c.Severity > max {
			max = c.Severity
		}
	}
	return max
}

// Drifted reports whether the contract differs at all.
func (r Report) Drifted() bool { return len(r.Changes) > 0 }

// Markdown renders the report for a PR body.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Severity: **%s**\n\n", r.Severity())
	for _, c := range r.Changes {
		fmt.Fprintf(&b, "- `%s` %s\n", c.Severity, c.Text)
	}
	return b.String()
}

// Compare diffs two snapshots.
func Compare(oldS, newS *Snapshot) Report {
	var r Report
	add := func(sev Severity, format string, a ...any) {
		r.Changes = append(r.Changes, Change{sev, fmt.Sprintf(format, a...)})
	}
	o, n := *oldS, *newS
	o.Normalize()
	n.Normalize()

	if o.SchemaVersion != n.SchemaVersion {
		add(Major, "schema_version %q -> %q", o.SchemaVersion, n.SchemaVersion)
	}
	if o.Host != n.Host {
		add(Major, "host %q -> %q", o.Host, n.Host)
	}
	if o.ServerInfo.Name != n.ServerInfo.Name {
		add(Major, "server name %q -> %q", o.ServerInfo.Name, n.ServerInfo.Name)
	}
	if o.ServerInfo.Title != n.ServerInfo.Title {
		add(Patch, "server title %q -> %q", o.ServerInfo.Title, n.ServerInfo.Title)
	}
	if o.ServerInfo.Version != n.ServerInfo.Version {
		add(Patch, "server version %q -> %q", o.ServerInfo.Version, n.ServerInfo.Version)
	}

	// Negotiated revisions: any change, down or up, is recorded (plan D9).
	oneg := map[string]Negotiation{}
	for _, x := range o.Negotiations {
		oneg[x.Requested] = x
	}
	for _, x := range n.Negotiations {
		if p, ok := oneg[x.Requested]; !ok {
			add(Minor, "negotiation for requested %s added (%s -> %s)", x.Requested, x.Path, x.Negotiated)
		} else if p != x {
			add(Major, "requested %s: was %s -> %s, now %s -> %s", x.Requested, p.Path, p.Negotiated, x.Path, x.Negotiated)
		}
		delete(oneg, x.Requested)
	}
	for _, k := range sortedKeys(oneg) {
		add(Major, "negotiation for requested %s removed", k)
	}

	// Tools.
	otools := map[string]Tool{}
	for _, t := range o.Tools {
		otools[t.Name] = t
	}
	for _, t := range n.Tools {
		p, ok := otools[t.Name]
		if !ok {
			add(Minor, "tool `%s` added", t.Name)
			continue
		}
		delete(otools, t.Name)
		if p.InputSchemaDigest != t.InputSchemaDigest {
			switch {
			case reflect.DeepEqual(stripAnnotations(p.InputSchema), stripAnnotations(t.InputSchema)):
				add(Patch, "tool `%s` input schema annotations changed (description/title/examples only)", t.Name)
			case tightens(stripAnnotations(p.InputSchema), stripAnnotations(t.InputSchema)):
				add(Major, "tool `%s` input schema tightened or changed incompatibly (%s -> %s)", t.Name, p.InputSchemaDigest, t.InputSchemaDigest)
			default:
				add(Minor, "tool `%s` input schema widened (%s -> %s)", t.Name, p.InputSchemaDigest, t.InputSchemaDigest)
			}
		}
		if p.DescriptionDigest != t.DescriptionDigest {
			add(Patch, "tool `%s` description changed", t.Name)
		}
	}
	for _, k := range sortedKeys(otools) {
		add(Major, "tool `%s` removed or renamed", k)
	}

	// Resources.
	ores := map[string]Resource{}
	for _, x := range o.Resources {
		ores[x.Name] = x
	}
	for _, x := range n.Resources {
		p, ok := ores[x.Name]
		if !ok {
			add(Minor, "resource `%s` added", x.Name)
			continue
		}
		delete(ores, x.Name)
		if p.URI != x.URI {
			add(Major, "resource `%s` uri %q -> %q", x.Name, p.URI, x.URI)
		}
	}
	for _, k := range sortedKeys(ores) {
		add(Major, "resource `%s` removed or renamed", k)
	}

	// Prompts.
	opr := map[string]Prompt{}
	for _, x := range o.Prompts {
		opr[x.Name] = x
	}
	for _, x := range n.Prompts {
		p, ok := opr[x.Name]
		if !ok {
			add(Minor, "prompt `%s` added", x.Name)
			continue
		}
		delete(opr, x.Name)
		oargs := map[string]PromptArgument{}
		for _, a := range p.Arguments {
			oargs[a.Name] = a
		}
		for _, a := range x.Arguments {
			pa, had := oargs[a.Name]
			switch {
			case !had && a.Required:
				add(Major, "prompt `%s` gained required argument `%s`", x.Name, a.Name)
			case !had:
				add(Minor, "prompt `%s` gained optional argument `%s`", x.Name, a.Name)
			case pa.Required != a.Required && a.Required:
				add(Major, "prompt `%s` argument `%s` became required", x.Name, a.Name)
			case pa.Required != a.Required:
				add(Minor, "prompt `%s` argument `%s` became optional", x.Name, a.Name)
			}
			delete(oargs, a.Name)
		}
		for _, k := range sortedKeys(oargs) {
			add(Major, "prompt `%s` argument `%s` removed or renamed", x.Name, k)
		}
	}
	for _, k := range sortedKeys(opr) {
		add(Major, "prompt `%s` removed or renamed", k)
	}
	return r
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// annotationKeys carry prose, not constraints.
var annotationKeys = map[string]bool{"description": true, "title": true, "examples": true, "example": true, "$comment": true}

// stripAnnotations removes prose keywords. It does not strip a property that
// happens to be NAMED "description": names live under "properties".
func stripAnnotations(v any) any {
	return strip(v, false)
}

func strip(v any, inProperties bool) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if !inProperties && annotationKeys[k] {
				continue
			}
			out[k] = strip(val, !inProperties && k == "properties")
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = strip(val, false)
		}
		return out
	}
	return v
}

// tightens reports whether n accepts less than o, or whether that cannot be
// ruled out. Conservative: an unknown difference counts as tightening.
func tightens(o, n any) bool {
	om, ook := o.(map[string]any)
	nm, nok := n.(map[string]any)
	if !ook || !nok {
		return !reflect.DeepEqual(o, n)
	}
	for k, ov := range om {
		nv, present := nm[k]
		switch k {
		case "properties":
			op, _ := ov.(map[string]any)
			np, _ := nv.(map[string]any)
			for name, sub := range op {
				nsub, has := np[name]
				if !has || tightens(sub, nsub) {
					return true
				}
			}
		case "required":
			if !subsetStrings(nv, ov) {
				return true
			}
		case "enum":
			if present && !subsetAny(ov, nv) {
				return true
			}
		default:
			if present && tightens(ov, nv) {
				return true
			}
		}
	}
	for k, nv := range nm {
		if _, had := om[k]; had {
			continue
		}
		switch k {
		case "properties":
			// New properties are widening unless listed in required (below).
		case "required":
			if !subsetStrings(nv, nil) {
				return true
			}
		default:
			return true // a constraint that did not exist before
		}
	}
	return false
}

// subsetStrings: every string in a is in b.
func subsetStrings(a, b any) bool {
	as, _ := a.([]any)
	bs, _ := b.([]any)
	set := map[string]bool{}
	for _, x := range bs {
		if s, ok := x.(string); ok {
			set[s] = true
		}
	}
	for _, x := range as {
		if s, ok := x.(string); !ok || !set[s] {
			return false
		}
	}
	return true
}

// subsetAny: every value in a appears in b (b may have more).
func subsetAny(a, b any) bool {
	as, _ := a.([]any)
	bs, _ := b.([]any)
	for _, x := range as {
		found := false
		for _, y := range bs {
			if reflect.DeepEqual(x, y) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
