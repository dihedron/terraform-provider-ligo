// Package jsonnorm gives the free-form parts of a resource (a target's
// definition, a binding's parameters) one text form, so that a reformatting
// is not a difference in a plan (spec §3.5).
package jsonnorm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Normalise returns the one JSON text of s: keys sorted, no insignificant
// white space, numbers kept as they are written (no loss of precision), and
// no HTML escaping. s must be exactly one JSON value, without duplicate keys
// (the server would keep only one of them, and the plan would lie).
func Normalise(s string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("not JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", errors.New("not JSON: more than one value")
	}
	if err := noDuplicates(strings.NewReader(s)); err != nil {
		return "", err
	}
	return encode(v)
}

// Of is the normal form of a value that the admin client decoded; the empty
// string for an absent one.
func Of(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("not JSON: %w", err)
	}
	return Normalise(string(raw))
}

func encode(v any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b) // Encode sorts the keys of a map
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// noDuplicates fails on an object that repeats a key, at any depth.
func noDuplicates(r io.Reader) error {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		d, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch d {
		case '{':
			seen := []string{}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return err
				}
				key := k.(string)
				i := sort.SearchStrings(seen, key)
				if i < len(seen) && seen[i] == key {
					return fmt.Errorf("not JSON: the key %q is repeated", key)
				}
				seen = append(seen[:i], append([]string{key}, seen[i:]...)...)
				if err := walk(); err != nil {
					return err
				}
			}
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
		}
		_, err = dec.Token() // the closing delimiter
		return err
	}
	return walk()
}
