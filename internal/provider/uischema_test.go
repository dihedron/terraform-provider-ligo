package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

// The forms of the lab's pg-ledger platform, as ligo shows them.
const pgForms = `{"accessLevelGrants":{"fields":[{"fieldType":"UI_FIELD_TYPE_MULTISELECT","jsonKey":"roles","label":"Roles","options":[{"label":"RO","value":"LEDGER_RO_ROLE"},{"label":"RW","value":"LEDGER_RW_ROLE"}],"required":true}]},
"targetDefinition":{"fields":[{"defaultValue":"ledger","fieldType":"UI_FIELD_TYPE_TEXT","jsonKey":"database","label":"Database","required":true},
{"fieldType":"UI_FIELD_TYPE_TEXT","jsonKey":"schema","label":"Schema","required":true,"validationRegex":"^[A-Za-z_][A-Za-z0-9_]{0,62}$"},
{"fieldType":"UI_FIELD_TYPE_NUMBER","jsonKey":"pool"},{"fieldType":"UI_FIELD_TYPE_BOOLEAN","jsonKey":"tls"},{"fieldType":"UI_FIELD_TYPE_SELECT","jsonKey":"mode","options":[{"value":"a"},{"value":"b"}]}]}}`

func form(t *testing.T, name string) map[string]any {
	t.Helper()
	var all map[string]map[string]any
	if err := json.Unmarshal([]byte(pgForms), &all); err != nil {
		t.Fatal(err)
	}
	return all[name]
}

func TestValidateForm(t *testing.T) {
	def, grants := form(t, "targetDefinition"), form(t, "accessLevelGrants")
	for name, tc := range map[string]struct {
		form   map[string]any
		values string
		want   string // "" is valid
	}{
		"valid":                 {def, `{"database":"ledger","schema":"ledger","pool":3,"tls":true,"mode":"a"}`, ""},
		"required only":         {def, `{"database":"d","schema":"s"}`, ""},
		"missing required":      {def, `{"database":"d"}`, "schema is required"},
		"unknown key":           {def, `{"database":"d","schema":"s","password":"x"}`, "password is not a field"},
		"wrong text type":       {def, `{"database":1,"schema":"s"}`, "database must be a string"},
		"wrong number type":     {def, `{"database":"d","schema":"s","pool":"3"}`, "pool must be a number"},
		"wrong boolean type":    {def, `{"database":"d","schema":"s","tls":"yes"}`, "tls must be a boolean"},
		"regex":                 {def, `{"database":"d","schema":"1bad"}`, "schema does not match"},
		"select option":         {def, `{"database":"d","schema":"s","mode":"c"}`, "mode must be one of"},
		"multiselect ok":        {grants, `{"roles":["LEDGER_RO_ROLE"]}`, ""},
		"multiselect bad value": {grants, `{"roles":["ROOT"]}`, "roles has ROOT"},
		"multiselect not list":  {grants, `{"roles":"LEDGER_RO_ROLE"}`, "roles must be a list"},
		"empty required list":   {grants, `{}`, "roles is required"},
	} {
		t.Run(name, func(t *testing.T) {
			var v map[string]any
			if err := json.Unmarshal([]byte(tc.values), &v); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(validateForm(tc.form, v), "; ")
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A form that is not understood validates nothing: ligo validates anyway.
func TestValidateFormIsLenientWithAnUnknownShape(t *testing.T) {
	for _, f := range []map[string]any{nil, {}, {"fields": "x"}, {"fields": []any{"x", map[string]any{"jsonKey": 1}}}} {
		if p := validateForm(f, map[string]any{"a": 1}); len(p) != 0 {
			t.Errorf("form %v: %v", f, p)
		}
	}
	if p := validateForm(map[string]any{"fields": []any{map[string]any{"jsonKey": "a", "fieldType": "UI_FIELD_TYPE_FUTURE", "validationRegex": "("}}}, map[string]any{"a": 1}); len(p) != 0 {
		t.Errorf("an unknown type or a bad regex: %v", p)
	}
}

func FuzzValidateForm(f *testing.F) {
	f.Add(`{"database":"d","schema":"s"}`)
	f.Add(`{"x":[1,{"y":null}]}`)
	f.Fuzz(func(t *testing.T, values string) {
		var v map[string]any
		if json.Unmarshal([]byte(values), &v) != nil {
			return
		}
		def := form(t, "targetDefinition")
		problems := validateForm(def, v)
		for k := range v {
			known := k == "database" || k == "schema" || k == "pool" || k == "tls" || k == "mode"
			if !known && len(problems) == 0 {
				t.Fatalf("an unknown key %q was accepted", k)
			}
		}
	})
}
