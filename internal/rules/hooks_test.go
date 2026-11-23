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

// TestAddedTables: a set added gives tables of its own, which Go and the
// documents read, its hooks among them; the mandatory set gives none, and
// refuses no release.
func TestAddedTables(t *testing.T) {
	r := hookedSet(t)
	ctx := context.Background()
	if !slices.Contains(r.List("colours"), "green") || !slices.Equal(r.List("shapes"), []string{"circle"}) {
		t.Errorf("the tables of the set added: %v %v", r.List("colours"), r.List("shapes"))
	}
	if err := r.Refuse(ctx, "colour", M{"value": "circle"}); err != nil {
		t.Errorf("a value of a table read as a list: %v", err)
	}
	err := r.Refuse(ctx, "colour", M{"value": "blue"})
	if want := "blue: neither red, green nor circle"; err == nil || err.Error() != want || !errors.Is(err, ErrRefused) {
		t.Errorf("a document of the set added, called by name: got %v, want %q", err, want)
	}
	if names := r.List("objectNames"); !slices.Equal(names, []string{"extra-object"}) {
		t.Errorf("the table of the release hook: %v", names)
	}
	if Mandatory().List("objectNames") != nil || len(Mandatory().Hooks(HookRelease)) != 0 {
		t.Error("the mandatory set gives the table, or a release hook")
	}
	for release, want := range map[string]string{
		"extra":  `Inputs.Release "extra": the name of an object the platform writes in every namespace (extra-object)`,
		"chosen": `Inputs.Release "chosen": the name of an object the platform writes in every namespace (chosen)`,
		"other":  "",
	} {
		err := r.RunHook(ctx, HookRelease, M{"field": "Inputs.Release", "release": release, "settings": M{"extra": M{"objectName": "chosen"}}})
		if want == "" && err != nil || want != "" && (err == nil || !strings.HasPrefix(err.Error(), want)) {
			t.Errorf("release %s: got %v, want %q", release, err, want)
		}
	}
	if err := Mandatory().RunHook(ctx, HookRelease, M{"field": "f", "release": "chosen", "settings": M{"extra": M{"objectName": "chosen"}}}); err != nil {
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
		"unknown table": {func(s string) string {
			return strings.Replace(s, "  colours:\n", "  sizes:\n    add: [small]\n  colours:\n", 1)
		}, "rules.yaml: tables: sizes: add: no rule set read before gives the table"},
		"add and values": {func(s string) string {
			return strings.Replace(s, "    values: [red, green]\n", "    values: [red, green]\n    add: [a]\n", 1)
		}, "doesn't meet the specifications of rules.schema.json"},
		"unknown hook": {func(s string) string {
			return strings.Replace(s, "hooks:\n", "hooks:\n  other: [colour]\n", 1)
		}, "doesn't meet the specifications of rules.schema.json"},
		"hook of no document": {func(s string) string {
			return strings.Replace(s, "[labelled]", "[labelled, missing]", 1)
		}, "rules.yaml: hooks: objects: missing is none of documents"},
		"a document listed twice": {func(s string) string {
			return strings.Replace(s, "[reserved]", "[reserved, namespace]", 1)
		}, "the rule set hooked: rules.yaml: hooks: namespace: namespace: given by a rule set read before (core): a rule set adds rules, it replaces none"},
		"a checks section": {func(s string) string {
			return s + "checks:\n  colour: colour.cel.yaml\n"
		}, "doesn't meet the specifications of rules.schema.json"},
		"an input not declared": {func(s string) string {
			return strings.Replace(s, "inputs: [raw]\n", "", 1)
		}, "helpers.cel.yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadRules(copyFS(t, os.DirFS("testdata/hooked"), tc.edit))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// A set added marks its own table extensible for the sets after it.
	fsys := copyFS(t, os.DirFS("testdata/hooked"), func(s string) string {
		return strings.Replace(s, "  shapes:\n", "  shapes:\n    extensible: true\n", 1)
	})
	if r, err := LoadRules(fsys); err != nil || !slices.Equal(r.List("shapes"), []string{"circle"}) {
		t.Errorf("an extensible table of a set added: %v", err)
	}
}

// TestHooks: the documents of each hook, those of the sets in the order
// they are read, the first message refusing as the document writes it.
func TestHooks(t *testing.T) {
	r := hookedSet(t)
	ctx := context.Background()
	if !slices.Equal(r.hooks[HookObjects], []string{"labelled"}) || !slices.Equal(r.hooks[HookFinalValues], []string{"bounded"}) {
		t.Errorf("the hooks: %v %v", r.hooks[HookObjects], r.hooks[HookFinalValues])
	}
	if !slices.Equal(r.Hooks(HookNamespace), []string{"namespace", "reserved"}) || !slices.Equal(Mandatory().Hooks(HookNamespace), []string{"namespace"}) {
		t.Errorf("the namespace hook: %v, the mandatory set's %v", r.Hooks(HookNamespace), Mandatory().Hooks(HookNamespace))
	}
	if len(Mandatory().hooks[HookObjects]) != 0 || len(Mandatory().hooks[HookFinalValues]) != 0 {
		t.Error("the mandatory set has objects or finalValues hooks")
	}
	for ns, want := range map[string]string{
		"kube-system": `f "kube-system": the namespace is reserved: kube-* is Kubernetes' own`,
		"team":        `f "team": reserved by the settings`,
		"demo":        "",
	} {
		err := r.RunHook(ctx, HookNamespace, M{"field": "f", "ns": ns, "settings": M{"reserved": []any{"team", "kube-system"}}})
		if want == "" && err != nil || want != "" && (err == nil || !strings.HasPrefix(err.Error(), want)) {
			t.Errorf("namespace %s: got %v, want %q", ns, err, want)
		}
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
	broken := copyFS(t, os.DirFS("testdata/hooked"), nil)
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
