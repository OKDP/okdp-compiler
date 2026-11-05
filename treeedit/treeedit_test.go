package treeedit

import (
	"reflect"
	"strings"
	"testing"
)

// TestDescriptionKeepsTheRest: the description is a key of project.yaml,
// first when new, the rest of the file kept: its head, its comments and
// its other keys.
func TestDescriptionKeepsTheRest(t *testing.T) {
	for _, c := range []struct{ raw, description, want string }{
		{"", "d", "description: d\n"},
		{"", "", "{}\n"},
		{"# licence\n\nglobal: {}\n", "d", "# licence\n\ndescription: d\nglobal: {}\n"},
		{"# licence\n{}\n", "d", "# licence\ndescription: d\n"},
		{"description: old # why\nconnections: {}\n", "d  e\n", "description: d e # why\nconnections: {}\n"},
		{"global: {}\ndescription: old\n", "", "global: {}\n"},
		{"description: old\n", "", "{}\n"},
		{"global: {} # kept\n", "", "global: {} # kept\n"},
	} {
		got, err := WriteDescription([]byte(c.raw), "project.yaml", c.description)
		if err != nil || string(got) != c.want {
			t.Errorf("%q, %q: %q %v, want %q", c.raw, c.description, got, err, c.want)
		}
		if d := ReadDescription(got); d != strings.Join(strings.Fields(c.description), " ") {
			t.Errorf("%q: read back %q", got, d)
		}
	}
	if _, err := WriteDescription([]byte("[a]\n"), "projects/p/project.yaml", "d"); err == nil || err.Error() != "projects/p/project.yaml is not a YAML mapping: fix it in Git" {
		t.Errorf("a list: %v", err)
	}
}

// TestInstance: an instance.yaml written and read back, its claimers
// absent, none or listed, each kept apart.
func TestInstance(t *testing.T) {
	for _, c := range []struct {
		spec InstanceSpec
		want string
	}{
		{InstanceSpec{Service: "app", Version: "1.0.0"}, "service: app\nversion: 1.0.0\n"},
		{InstanceSpec{Service: "app", Version: "1.0.0", Claimers: []string{}}, "service: app\nversion: 1.0.0\nclaimers: []\n"},
		{InstanceSpec{Service: "app", Version: "1.0.0", Claimers: []string{"demo", "*"}}, "service: app\nversion: 1.0.0\nclaimers:\n  - demo\n  - '*'\n"},
	} {
		raw, err := EncodeInstance(c.spec)
		if err != nil || string(raw) != c.want {
			t.Errorf("%+v: %q %v, want %q", c.spec, raw, err, c.want)
		}
		got, err := ParseInstance(raw)
		if err != nil || !reflect.DeepEqual(got, c.spec) || (got.Claimers == nil) != (c.spec.Claimers == nil) {
			t.Errorf("%q: read back %#v %v", raw, got, err)
		}
	}
}

// TestValues: a values file written ({} for none) and read as the compile
// reads it, every number a float64; an empty file or a null document {}.
func TestValues(t *testing.T) {
	if raw, err := EncodeValues(nil); err != nil || string(raw) != "{}\n" {
		t.Errorf("none: %q %v", raw, err)
	}
	for raw, want := range map[string]map[string]any{
		"":                    {},
		"null\n":              {},
		"a: 1\nb: {c: [x]}\n": {"a": 1.0, "b": map[string]any{"c": []any{"x"}}},
	} {
		if got, err := DecodeValues([]byte(raw)); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %v %v", raw, got, err)
		}
	}
	if _, err := DecodeValues([]byte("- a\n")); err == nil {
		t.Error("a list read as values")
	}
	if got := RoundTrip(map[string]any{"n": 2, "s": []string{"a"}}); !reflect.DeepEqual(got, map[string]any{"n": 2.0, "s": []any{"a"}}) {
		t.Errorf("round trip: %v", got)
	}
}

// TestEditEntry: an entry of a top-level key set, replaced and removed,
// the head, the comments and the other keys kept; the key goes with its
// last entry; one that is not a mapping refused.
func TestEditEntry(t *testing.T) {
	raw := []byte("# licence\nglobal: {}\nconnections:\n  # the shared one\n  ext: {contract: database}\n")
	got, err := EditEntry(raw, "p.yaml", "connections", "ext", map[string]any{"contract": "s3"})
	if want := "# licence\nglobal: {}\nconnections:\n  # the shared one\n  ext:\n    contract: s3\n"; err != nil || string(got) != want {
		t.Errorf("replaced: %q %v, want %q", got, err, want)
	}
	got, err = EditEntry(got, "p.yaml", "connections", "new", map[string]any{"contract": "database"})
	if !strings.HasSuffix(string(got), "  new:\n    contract: database\n") || err != nil {
		t.Errorf("added: %q %v", got, err)
	}
	got, _ = EditEntry(got, "p.yaml", "connections", "ext", nil)
	got, err = EditEntry(got, "p.yaml", "connections", "new", nil)
	if want := "# licence\nglobal: {}\n"; err != nil || string(got) != want {
		t.Errorf("removed: %q %v, want %q", got, err, want)
	}
	if same, err := EditEntry(raw, "p.yaml", "claims", "x", nil); err != nil || string(same) != string(raw) {
		t.Errorf("removing nothing: %q %v", same, err)
	}
	if got, err := EditEntry([]byte(""), "p.yaml", "claims", "x", map[string]any{"contract": "db"}); err != nil || string(got) != "claims:\n  x:\n    contract: db\n" {
		t.Errorf("an empty file: %q %v", got, err)
	}
	if _, err := EditEntry([]byte("claims: [a]\n"), "p.yaml", "claims", "x", map[string]any{}); err == nil || err.Error() != "p.yaml: claims is not a mapping: fix it in Git" {
		t.Errorf("a list: %v", err)
	}
}

// TestMergeDocument: the keys that do not change keep their nodes and
// comments, a changed one its comments, those removed go, those added
// come last, in name order; a document that is not a mapping replaced.
func TestMergeDocument(t *testing.T) {
	raw := []byte("# The catalogs\nrepositories:\n  - {name: a, url: x} # kept\n# the menu\ncategories: []\nold: 1\n")
	got, err := MergeDocument(raw, map[string]any{"repositories": []any{map[string]any{"name": "a", "url": "x"}}, "categories": []any{"db"}, "z": 1, "b": true})
	want := "# The catalogs\nrepositories:\n  - {name: a, url: x} # kept\n# the menu\ncategories:\n  - db\nb: true\nz: 1\n"
	if err != nil || string(got) != want {
		t.Errorf("merged:\n%s%v\nwant:\n%s", got, err, want)
	}
	if got, err := MergeDocument([]byte("[a]\n"), map[string]any{"k": "v"}); err != nil || string(got) != "k: v\n" {
		t.Errorf("replaced: %q %v", got, err)
	}
}
