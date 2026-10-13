package snapshot

import "reflect"

// compareOutputSchema compares what a tool promises to return. The direction
// is the reverse of an input schema: a client reads these fields, so a
// removed or narrowed field breaks it and an added one does not.
func compareOutputSchema(p, t Tool, add func(Severity, string, ...any)) {
	hadOutput, hasOutput := p.OutputSchema != nil, t.OutputSchema != nil
	switch {
	case !hadOutput && !hasOutput:
	case !hadOutput:
		add(Minor, "tool `%s` output schema pinned (%s)", t.Name, t.OutputSchemaDigest)
	case !hasOutput:
		add(Major, "tool `%s` output schema removed", t.Name)
	case p.OutputSchemaDigest != t.OutputSchemaDigest:
		switch {
		case reflect.DeepEqual(stripAnnotations(p.OutputSchema), stripAnnotations(t.OutputSchema)):
			add(Patch, "tool `%s` output schema annotations changed (description/title/examples only)", t.Name)
		case outputNarrows(stripAnnotations(p.OutputSchema), stripAnnotations(t.OutputSchema)):
			add(Major, "tool `%s` output schema narrowed or a field removed (%s -> %s)", t.Name, p.OutputSchemaDigest, t.OutputSchemaDigest)
		default:
			add(Minor, "tool `%s` output schema widened (%s -> %s)", t.Name, p.OutputSchemaDigest, t.OutputSchemaDigest)
		}
	}
}

// outputNarrows reports whether n drops something o promised: a property or
// definition, a required entry, an enum member, or a changed type. Anything
// it cannot prove additive counts as narrowing. Added properties, added
// required entries and added enum members are additive.
func outputNarrows(o, n any) bool {
	om, ook := o.(map[string]any)
	nm, nok := n.(map[string]any)
	if !ook || !nok {
		return !reflect.DeepEqual(o, n)
	}
	for k, ov := range om {
		nv, present := nm[k]
		switch k {
		case "properties", "$defs", "definitions":
			op, _ := ov.(map[string]any)
			np, _ := nv.(map[string]any)
			for name, sub := range op {
				nsub, has := np[name]
				if !has || outputNarrows(sub, nsub) {
					return true
				}
			}
		case "required":
			if !subsetStrings(ov, nv) {
				return true
			}
		case "enum":
			if present && !subsetAny(ov, nv) {
				return true
			}
		case "type", "const", "$ref":
			if !present || !reflect.DeepEqual(ov, nv) {
				return true
			}
		default:
			if present && outputNarrows(ov, nv) {
				return true
			}
		}
	}
	return false
}
