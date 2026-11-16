package jsonnorm

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

func stringsReader(s string) io.Reader { return strings.NewReader(s) }

func TestNormalise(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"b": 1, "a": [3, 2, {"d": null, "c": "x"}]}`, `{"a":[3,2,{"c":"x","d":null}],"b":1}`},
		{"  [ ]  ", `[]`},
		{`{"n": 12345678901234567890, "f": 1.50, "e": 1e3}`, `{"e":1e3,"f":1.50,"n":12345678901234567890}`},
		{`"a<b>&"`, `"a<b>&"`},
		{`null`, `null`},
	} {
		got, err := Normalise(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Normalise(%s) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestNormaliseRefuses(t *testing.T) {
	for _, in := range []string{``, `{`, `{"a":1} {"b":2}`, `{"a":1,"a":2}`, `nope`} {
		if got, err := Normalise(in); err == nil {
			t.Errorf("Normalise(%q) = %q, want an error", in, got)
		}
	}
}

func TestOf(t *testing.T) {
	got, err := Of(map[string]any{"z": []int{1}, "a": "x"})
	if err != nil || got != `{"a":"x","z":[1]}` {
		t.Errorf("Of = %q, %v", got, err)
	}
	if got, err := Of(nil); err != nil || got != "" {
		t.Errorf("Of(nil) = %q, %v: an absent value is the empty string", got, err)
	}
}

// FuzzNormalise: a normal form is stable (normalising it changes nothing),
// and it says the same as the input.
func FuzzNormalise(f *testing.F) {
	for _, s := range []string{`{"b":1,"a":[1,2]}`, `[]`, `"x"`, `{"a":{"b":{"c":null}}}`, `1e400`, `{"é":"é"}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		once, err := Normalise(in)
		if err != nil {
			return
		}
		twice, err := Normalise(once)
		if err != nil || twice != once {
			t.Fatalf("not idempotent: %q -> %q -> %q (%v)", in, once, twice, err)
		}
		var a, b any
		d1, d2 := json.NewDecoder(stringsReader(in)), json.NewDecoder(stringsReader(once))
		d1.UseNumber()
		d2.UseNumber()
		if d1.Decode(&a) != nil || d2.Decode(&b) != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("lost something: %q -> %q", in, once)
		}
	})
}
