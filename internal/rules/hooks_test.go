package rules

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// hookedSet loads testdata/hooked over the mandatory set.
func hookedSet(t *testing.T) *RuleSet {
	t.Helper()
	r, err := LoadRules(os.DirFS("testdata/hooked"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// hookedCopy: the files of testdata/hooked, rules.yaml edited by edit.
func hookedCopy(t *testing.T, edit func(string) string) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	entries, err := os.ReadDir("testdata/hooked")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile("testdata/hooked/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = &fstest.MapFile{Data: b}
	}
	out[RulesFile] = &fstest.MapFile{Data: []byte(edit(string(out[RulesFile].Data)))}
	return out
}

// TestAddedTables: a set added gives tables of its own, which Go and the
// documents read, and adds values to the extensible tables of the
// mandatory set, which its checks then apply.
func TestAddedTables(t *testing.T) {
	r := hookedSet(t)
	ctx := context.Background()
	if !r.Has("colours", "green") || !slices.Equal(r.List("shapes"), []string{"circle"}) {
		t.Errorf("the tables of the set added: %v %v", r.List("colours"), r.List("shapes"))
	}
	if err := r.Refuse(ctx, "colour", M{"value": "circle"}); err != nil {
		t.Errorf("a value of a table read as a list: %v", err)
	}
	err := r.Refuse(ctx, "colour", M{"value": "blue"})
	if want := "blue: neither red, green nor circle"; err == nil || err.Error() != want || !errors.Is(err, ErrRefused) {
		t.Errorf("a document of the set added, called by name: got %v, want %q", err, want)
	}
	names := r.List(TablePlatformObjectNames)
	if names[len(names)-1] != "extra-object" || !r.Has(TablePlatformObjectNames, "okdp-project") || len(Mandatory().List(TablePlatformObjectNames))+1 != len(names) {
		t.Errorf("the values added to an extensible table: %v", names)
	}
	if slices.Contains(Mandatory().List(TablePlatformObjectNames), "extra-object") {
		t.Error("the mandatory set changed")
	}
	for release, want := range map[string]string{
		"extra":  `Inputs.Release "extra": the name of an object the platform writes in every namespace (extra-object)`,
		"chosen": `Inputs.Release "chosen": the name of an object the platform writes in every namespace (chosen)`,
		"other":  "",
	} {
		err := r.Refuse(ctx, CheckReleaseRule, M{"field": "Inputs.Release", "release": release, "settings": M{"extra": M{"objectName": "chosen"}}})
		if want == "" && err != nil || want != "" && (err == nil || !strings.HasPrefix(err.Error(), want)) {
			t.Errorf("release %s: got %v, want %q", release, err, want)
		}
	}
	if err := Mandatory().Refuse(ctx, CheckReleaseRule, M{"field": "f", "release": "chosen", "settings": M{"extra": M{"objectName": "chosen"}}}); err != nil {
		t.Errorf("the mandatory set reads a setting of the set added: %v", err)
	}
	// A nested refusal is the caller's message, as written.
	if err := r.Refuse(ctx, "calling", M{"value": "blue"}); err == nil || err.Error() != "blue: neither red, green nor circle" {
		t.Errorf("a refusal through rule: got %v", err)
	}
	if err := r.Refuse(ctx, "calling", M{"value": "red"}); err != nil {
		t.Errorf("a document through rule that passes: %v", err)
	}
}

// TestAddedTablesRefused: a set adds to the extensible tables only, to
// tables a set read before gives, and replaces no table.
func TestAddedTablesRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(string) string
		want string
	}{
		"closed table": {func(s string) string {
			return strings.Replace(s, "  colours:\n", "  projectRoles:\n    add: [owner]\n  colours:\n", 1)
		}, "rules.yaml: tables: projectRoles: add: the table is not extensible: a rule set adds values to the tables marked extensible only"},
		"unknown table": {func(s string) string {
			return strings.Replace(s, "  colours:\n", "  sizes:\n    add: [small]\n  colours:\n", 1)
		}, "rules.yaml: tables: sizes: add: no rule set read before gives the table"},
		"values on an extensible table": {func(s string) string {
			return strings.Replace(s, "  colours:\n", "  engineNamespaces:\n    values: [other]\n  colours:\n", 1)
		}, "tables: engineNamespaces: given by another rule set"},
		"add and values": {func(s string) string {
			return strings.Replace(s, "    add: [extra-object]\n", "    add: [extra-object]\n    values: [a]\n", 1)
		}, "doesn't meet the specifications of rules.schema.json"},
		"unknown hook": {func(s string) string {
			return strings.Replace(s, "hooks:\n", "hooks:\n  other: [colour]\n", 1)
		}, "doesn't meet the specifications of rules.schema.json"},
		"hook of no document": {func(s string) string {
			return strings.Replace(s, "[labelled]", "[labelled, missing]", 1)
		}, "rules.yaml: hooks: objects: missing is none of documents"},
		"hook of a check": {func(s string) string {
			return strings.Replace(s, "[labelled]", "[labelled, object]", 1)
		}, "rules.yaml: hooks: objects: object is none of documents"},
		"check of a set added": {func(s string) string {
			return s + "checks:\n  colour: colour.cel.yaml\n"
		}, "rules.yaml: checks: unknown colour"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadRules(hookedCopy(t, tc.edit))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// The mandatory set marks a table extensible for the sets after it; a
	// set added marks its own.
	fsys := hookedCopy(t, func(s string) string {
		return strings.Replace(s, "  shapes:\n", "  shapes:\n    extensible: true\n", 1)
	})
	if r, err := LoadRules(fsys); err != nil || !slices.Equal(r.List("shapes"), []string{"circle"}) {
		t.Errorf("an extensible table of a set added: %v", err)
	}
}

// TestHooks: the documents of each hook, in order, the first message
// refusing as the document writes it.
func TestHooks(t *testing.T) {
	r := hookedSet(t)
	ctx := context.Background()
	if !slices.Equal(r.Hooks(HookObjects), []string{"labelled"}) || !slices.Equal(r.Hooks(HookFinalValues), []string{"bounded"}) {
		t.Errorf("the hooks: %v %v", r.Hooks(HookObjects), r.Hooks(HookFinalValues))
	}
	if len(Mandatory().Hooks(HookObjects)) != 0 || len(Mandatory().Hooks(HookFinalValues)) != 0 {
		t.Error("the mandatory set has hooks")
	}
	object := func(labels M) M {
		return M{"id": "ConfigMap/a", "kind": "ConfigMap", "name": "a", "raw": M{"metadata": M{"name": "a", "labels": labels}}}
	}
	err := r.RunHook(ctx, HookObjects, M{"svc": "web", "ns": "demo", "object": object(M{}), "settings": M{}})
	if want := "web: objects: ConfigMap/a: metadata.labels: no label team in demo"; err == nil || err.Error() != want || !errors.Is(err, ErrRefused) {
		t.Errorf("objects: got %v, want %q", err, want)
	}
	if err := r.RunHook(ctx, HookObjects, M{"svc": "web", "ns": "demo", "object": object(M{"owner": "x"}), "settings": M{"label": "owner"}}); err != nil {
		t.Errorf("objects, the label of the settings: %v", err)
	}
	value := M{"a": M{"count": 3}, "b": []any{M{"count": 9}}}
	err = r.RunHook(ctx, HookFinalValues, M{"svc": "web", "ns": "demo", "key": "app", "value": value, "settings": M{}})
	if want := "app: b.0.count: above the most (web in demo)"; err == nil || err.Error() != want {
		t.Errorf("finalValues: got %v, want %q", err, want)
	}
	if err := r.RunHook(ctx, HookFinalValues, M{"svc": "web", "ns": "demo", "key": "app", "value": value, "settings": M{"maxCount": 10}}); err != nil {
		t.Errorf("finalValues, the most of the settings: %v", err)
	}
	if err := Mandatory().RunHook(ctx, HookObjects, M{}); err != nil {
		t.Errorf("no hook: %v", err)
	}
	if err := (&RuleSet{}).RunHook(ctx, HookObjects, M{}); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("the zero RuleSet: got %v", err)
	}
	broken := hookedCopy(t, func(s string) string { return s })
	broken["labelled.cel.yaml"] = &fstest.MapFile{Data: []byte("errors:\n  - ${{ object.missing.deep }}\n")}
	b, err := LoadRules(broken)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.RunHook(ctx, HookObjects, M{"svc": "web", "ns": "demo", "object": object(M{}), "settings": M{}}); err == nil || errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "the rule set:") {
		t.Errorf("a hook that fails to evaluate: got %v", err)
	}
}

// TestHelpers: leaves, fields and urlParse, as the documents call them.
func TestHelpers(t *testing.T) {
	r := hookedSet(t)
	got, err := r.Values(context.Background(), "helpers", M{
		"value": M{"b": []any{M{"y": 1, "x": nil}, "s", []any{}}, "a": true, "c": M{}},
		"raw":   "https://user:pw@Example.org:8443/p", "field": ", ",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The values of a document are as YAML reads them: numbers float64.
	leaves := []any{
		M{"path": []any{"a"}, "value": true},
		M{"path": []any{"b", float64(0), "x"}, "value": nil},
		M{"path": []any{"b", float64(0), "y"}, "value": float64(1)},
		M{"path": []any{"b", float64(1)}, "value": "s"},
	}
	if !reflect.DeepEqual(got["leaves"], leaves) {
		t.Errorf("leaves:\n got %#v\nwant %#v", got["leaves"], leaves)
	}
	if want := []any{"https://user:pw@Example.org:8443/p"}; !reflect.DeepEqual(got["fields"], want) {
		t.Errorf("fields: got %#v", got["fields"])
	}
	if want := (M{"ok": true, "scheme": "https", "host": "Example.org", "user": true}); !reflect.DeepEqual(got["url"], want) {
		t.Errorf("urlParse: got %#v", got["url"])
	}
	got, err = r.Values(context.Background(), "helpers", M{"value": "top", "raw": " a,,b c\n", "field": ", \n"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{M{"path": []any{}, "value": "top"}}; !reflect.DeepEqual(got["leaves"], want) {
		t.Errorf("leaves of a scalar: got %#v", got["leaves"])
	}
	if want := []any{"a", "b", "c"}; !reflect.DeepEqual(got["fields"], want) {
		t.Errorf("fields: got %#v", got["fields"])
	}
	if want := (M{"ok": false, "scheme": "", "host": "", "user": false}); !reflect.DeepEqual(got["url"], want) {
		t.Errorf("urlParse of what does not parse: got %#v", got["url"])
	}
	got, err = r.Values(context.Background(), "helpers", M{"value": M{}, "raw": "relative/path", "field": ""})
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"ok": true, "scheme": "", "host": "", "user": false}); !reflect.DeepEqual(got["url"], want) {
		t.Errorf("urlParse of a relative URL: got %#v", got["url"])
	}
	if !reflect.DeepEqual(got["leaves"], []any{}) || !reflect.DeepEqual(got["fields"], []any{"relative/path"}) {
		t.Errorf("leaves of an empty map, fields without separators: %#v %#v", got["leaves"], got["fields"])
	}
}
