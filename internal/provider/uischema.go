package provider

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
)

// validateForm checks values against a platform plugin's form (the shape of
// GetPlatform's uiSchemas: a list of fields with a jsonKey, a fieldType,
// options and a validationRegex), so that a plan fails before any filing. It
// is lenient about what it does not understand (ligo validates anyway) and
// strict about what it does: a key that no field names is a problem, which is
// also how a secret put in a definition by mistake is caught (T-P10).
func validateForm(form map[string]any, values map[string]any) []string {
	fields, _ := form["fields"].([]any)
	if len(fields) == 0 {
		return nil
	}
	known := map[string]map[string]any{}
	for _, f := range fields {
		if m, ok := f.(map[string]any); ok {
			if k, ok := m["jsonKey"].(string); ok {
				known[k] = m
			}
		}
	}
	if len(known) == 0 {
		return nil
	}
	var problems []string
	keys := make([]string, 0, len(known))
	for k := range known {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f := known[k]
		v, present := values[k]
		if !present {
			if req, _ := f["required"].(bool); req {
				problems = append(problems, k+" is required")
			}
			continue
		}
		problems = append(problems, checkField(k, f, v)...)
	}
	extra := make([]string, 0)
	for k := range values {
		if _, ok := known[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		problems = append(problems, k+" is not a field of the platform's form")
	}
	return problems
}

func checkField(key string, f map[string]any, v any) []string {
	switch f["fieldType"] {
	case "UI_FIELD_TYPE_TEXT", "UI_FIELD_TYPE_TEXTAREA":
		s, ok := v.(string)
		if !ok {
			return []string{key + " must be a string"}
		}
		if re, _ := f["validationRegex"].(string); re != "" {
			if c, err := regexp.Compile(re); err == nil && !c.MatchString(s) {
				return []string{fmt.Sprintf("%s does not match %s", key, re)}
			}
		}
	case "UI_FIELD_TYPE_NUMBER":
		if _, ok := v.(float64); !ok {
			return []string{key + " must be a number"}
		}
	case "UI_FIELD_TYPE_BOOLEAN":
		if _, ok := v.(bool); !ok {
			return []string{key + " must be a boolean"}
		}
	case "UI_FIELD_TYPE_SELECT":
		s, ok := v.(string)
		if !ok {
			return []string{key + " must be a string"}
		}
		if opts := optionValues(f); len(opts) > 0 && !slices.Contains(opts, s) {
			return []string{fmt.Sprintf("%s must be one of %v", key, opts)}
		}
	case "UI_FIELD_TYPE_MULTISELECT":
		l, ok := v.([]any)
		if !ok {
			return []string{key + " must be a list"}
		}
		opts := optionValues(f)
		for _, e := range l {
			s, ok := e.(string)
			if !ok || (len(opts) > 0 && !slices.Contains(opts, s)) {
				return []string{fmt.Sprintf("%s has %v, which is not one of %v", key, e, opts)}
			}
		}
	}
	return nil
}

func optionValues(f map[string]any) []string {
	var out []string
	opts, _ := f["options"].([]any)
	for _, o := range opts {
		if m, ok := o.(map[string]any); ok {
			if s, ok := m["value"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
