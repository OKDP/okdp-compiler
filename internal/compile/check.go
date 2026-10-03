// Check: the directives of a document, checked before it is evaluated
// (Load, Doc.Section), in every branch, those no evaluation takes included:
// a misspelled or misplaced directive fails rather than being ignored or
// written to the values as a key.
package compile

import (
	"fmt"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

// place: where a map stands (a map, a list item), with the directive that
// gives its form, and the keys it holds there.
type place struct {
	name  string          // for messages
	keys  bool            // it holds keys
	dirs  map[string]bool // its directives
	holds string          // what it holds, for messages
}

// set: the directives ds.
func set(ds ...string) map[string]bool {
	m := map[string]bool{}
	for _, d := range ds {
		m[d] = true
	}
	return m
}

// The places of a map: a plain map or list item, or one with $for, $value,
// $spread.
var (
	inMap      = &place{"a map", true, set("$if", "$value", "$for", "$merge"), "keys, $if, $value, $for (with $key, $value, $let) and $merge"}
	inItem     = &place{"a list item", true, set("$if", "$value", "$merge", "$for", "$spread"), "keys, $if, $value, $merge, $for (with $each, $let) and $spread"}
	inForMap   = &place{"a map with $for", false, set("$if", "$for", "$key", "$value", "$let"), "$if, $for, $key, $value and $let only"}
	inValueMap = &place{"a map with $value", false, set("$if", "$value"), "$if and $value only"}
	inForItem  = &place{"a list item with $for", false, set("$for", "$each", "$let"), "$for, $each and $let only"}
	inSpread   = &place{"a list item with $spread", false, set("$spread"), "$spread only"}
)

// directives: every directive of the language.
var directives = set("$if", "$value", "$for", "$key", "$let", "$each", "$merge", "$spread")

// literal: k is a key, not a directive: it does not start with $, or it
// starts with $$ (the key is written with one $ less).
func literal(k string) bool { return !strings.HasPrefix(k, "$") || strings.HasPrefix(k, "$$") }

// checkTop: the top level of a document: its keys are sections, not
// directives; let is a map of bindings, errors a list.
func checkTop(top *omap) error {
	for _, k := range top.keys {
		if !literal(k) {
			return atKey(k, fmt.Errorf("not a directive of the top level: its keys are sections"))
		}
	}
	if err := bindings("let", top.vals["let"]); err != nil {
		return err
	}
	switch t := top.vals["errors"].(type) {
	case nil:
	case []any:
		if err := check(t, false); err != nil {
			return atKey("errors", err)
		}
	default:
		return atKey("errors", fmt.Errorf("must be a list of rules, got %s", shape(t)))
	}
	return nil
}

// check: the directives of the node n (a list item when item) and of the
// nodes below it: every key starting with $ is a directive of its place;
// $for has its $key and $value in a map, its $each in a list item; $if,
// $merge and $spread are expressions; $let is a map of bindings.
func check(n any, item bool) error {
	switch t := n.(type) {
	case []any:
		for i, x := range t {
			if err := check(x, true); err != nil {
				return atIndex(i, err)
			}
		}
	case *omap:
		return checkMap(t, item)
	}
	return nil
}

// checkMap: check, for a map.
func checkMap(m *omap, item bool) error {
	p := placeOf(m, item)
	for _, k := range m.keys {
		switch {
		case p.dirs[k], p.keys && literal(k):
		case !literal(k) && !directives[k]:
			return atKey(k, fmt.Errorf("unknown directive (the key %s is written $%s)", k, k))
		default:
			return atKey(k, fmt.Errorf("not in %s, which holds %s", p.name, p.holds))
		}
	}
	switch {
	case p == inForMap && (!has(m, "$key") || !has(m, "$value")):
		return atKey("$for", fmt.Errorf("in a map, needs $key and $value"))
	case p == inForItem && !has(m, "$each"):
		return atKey("$for", fmt.Errorf("in a list item, needs $each"))
	}
	for _, d := range [][2]string{{"$if", "condition"}, {"$merge", "map"}, {"$spread", "list"}} {
		if v, ok := m.vals[d[0]]; ok {
			if err := expression(v, d[1]); err != nil {
				return atKey(d[0], err)
			}
		}
	}
	if err := bindings("$let", m.vals["$let"]); err != nil {
		return err
	}
	// The nodes below: the keys' values, $key, $value, $each.
	for _, k := range m.keys {
		if literal(k) || k == "$key" || k == "$value" || k == "$each" {
			if err := check(m.vals[k], false); err != nil {
				return atKey(k, err)
			}
		}
	}
	return nil
}

// has: m holds the key k.
func has(m *omap, k string) bool {
	_, ok := m.vals[k]
	return ok
}

// placeOf: the place of a map, by its directives, in the order the
// evaluation reads them: $for, then $spread in a list item; $value; else a
// plain map or list item.
func placeOf(m *omap, item bool) *place {
	switch {
	case item && has(m, "$for"):
		return inForItem
	case item && has(m, "$spread"):
		return inSpread
	case has(m, "$for"):
		return inForMap
	case has(m, "$value"):
		return inValueMap
	case item:
		return inItem
	}
	return inMap
}

// expression: v is one expression, ${{ ... }}, with nothing around it.
func expression(v any, what string) error {
	if s, ok := v.(string); ok {
		p, err := scan(s)
		if err != nil {
			return err
		}
		if len(p) == 1 && p[0].expr {
			return nil
		}
	}
	return fmt.Errorf("takes one expression, ${{ <%s> }}, got %s", what, shape(v))
}

// CheckCondition: v is a condition as $if takes it, one expression,
// ${{ <condition> }}, with nothing around it; for a condition written
// outside a document (Condition).
func CheckCondition(v any) error { return expression(v, "condition") }

// bindings: a let or $let map (kind): its names as written, each once,
// none starting with $; its values checked as nodes. Null has none.
func bindings(kind string, v any) error {
	if v == nil {
		return nil
	}
	m, ok := v.(*omap)
	if !ok {
		return atKey(kind, fmt.Errorf("must be a map of bindings, got %s", shape(v)))
	}
	seen := map[string]bool{}
	for _, b := range m.written {
		at := kind + " " + b.name
		switch {
		case strings.HasPrefix(b.name, "$"):
			return atKey(at, fmt.Errorf("a binding name cannot start with $"))
		case seen[b.name]:
			return atKey(at, fmt.Errorf("bound twice"))
		}
		seen[b.name] = true
		if err := check(b.val, false); err != nil {
			return atKey(at, err)
		}
	}
	return nil
}

// shape: a node for a message: a map or a list by its kind, null, else the
// scalar as Go's %#v prints it.
func shape(n any) string {
	switch n.(type) {
	case *omap:
		return "a map"
	case []any:
		return "a list"
	case nil:
		return "null"
	}
	return value.Short(n)
}
