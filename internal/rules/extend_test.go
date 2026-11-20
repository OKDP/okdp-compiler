package rules

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// extraSet: the files of testdata/extra, a set to add to the mandatory one,
// with files replaced or added by name.
func extraSet(t *testing.T, files map[string]string) fstest.MapFS {
	t.Helper()
	dir := os.DirFS("testdata/extra")
	out := fstest.MapFS{}
	if err := fs.WalkDir(dir, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(dir, p)
		out[p] = &fstest.MapFile{Data: b}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		out[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return out
}

// extraRules: the rules.yaml of testdata/extra, with old replaced by by.
func extraRules(t *testing.T, old, by string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/extra/rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("rules.yaml holds no %q", old)
	}
	return strings.Replace(string(b), old, by, 1)
}

func loadExtra(t *testing.T) *RuleSet {
	t.Helper()
	r, err := LoadRules(extraSet(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestSchemasAdd: an added set's schemas are merged into the mandatory
// ones. A key added under a map the mandatory schema closes is accepted,
// the defaults of an added subtree apply, a null still deletes a default,
// and the mandatory rules hold as before.
func TestSchemasAdd(t *testing.T) {
	r := loadExtra(t)
	added := M{"global": M{"okdp": M{"policy": M{"extraNames": []any{"a"}}}}}
	if _, err := SchemaValues(Mandatory().PlatformSchema(), "h", added); err == nil {
		t.Fatal("the mandatory schema alone accepts the added key")
	}
	if _, err := SchemaValues(r.PlatformSchema(), "h", added); err != nil {
		t.Errorf("an added key under a closed map: %v", err)
	}
	other := M{"global": M{"okdp": M{"policy": M{"other": true}}}}
	if _, err := SchemaValues(r.PlatformSchema(), "h", other); err == nil || !strings.Contains(err.Error(), "additional properties 'other' not allowed") {
		t.Errorf("the closed map takes another key: %v", err)
	}
	v, err := SchemaValues(r.PlatformSchema(), "h", M{})
	if err != nil {
		t.Fatal(err)
	}
	extra := v["global"].(M)["okdp"].(M)["extra"].(M)
	if extra["level"] != float64(3) || extra["label"] != "plain" {
		t.Errorf("the defaults of the added subtree: %v", extra)
	}
	if v["global"].(M)["okdp"].(M)["secrets"].(M)["serviceAccount"] != "okdp-secrets" {
		t.Errorf("a default of the mandatory schema: %v", v)
	}
	v, err = SchemaValues(r.PlatformSchema(), "h", M{"global": M{"okdp": M{"extra": M{"level": nil}, "secrets": M{"serviceAccount": nil}}}})
	if err != nil {
		t.Fatal(err)
	}
	okdp := v["global"].(M)["okdp"].(M)
	if _, set := okdp["extra"].(M)["level"]; set {
		t.Errorf("a null does not delete an added default: %v", okdp)
	}
	if _, set := okdp["secrets"].(M)["serviceAccount"]; set {
		t.Errorf("a null does not delete a mandatory default: %v", okdp)
	}
	if _, err := SchemaValues(r.PlatformSchema(), "h", M{"global": M{"okdp": M{"extra": M{"level": "high"}}}}); err == nil {
		t.Error("the added subtree's types do not hold")
	}
	if _, err := SchemaValues(r.PlatformSchema(), "h", M{"global": M{"okdp": M{"access": M{"other": 1}}}}); err == nil {
		t.Error("a map the mandatory schema closes, and the added one does not touch, takes another key")
	}
	mapping := r.MappingSchema()
	if _, err := SchemaValues(mapping, "h", M{"extraItems": []any{M{"name": "a"}}}); err != nil {
		t.Errorf("an added section, through an added $defs: %v", err)
	}
	if _, err := SchemaValues(mapping, "h", M{"extraItems": []any{M{"name": ""}}}); err == nil {
		t.Error("an added $defs does not apply")
	}
	if _, err := SchemaValues(mapping, "h", M{"provides": []any{M{}}}); err == nil {
		t.Error("the mandatory mapping schema does not apply")
	}
}

// TestSchemasCollide: an added schema adds keys and changes none: a
// property or a $defs the mandatory schema holds as a leaf (without
// properties nor $defs), and any other keyword of a node it merges, are
// refused.
func TestSchemasCollide(t *testing.T) {
	for name, tc := range map[string]struct {
		file, schema, want string
	}{
		"a property": {"platform.schema.json", `{"properties": {"global": {"properties": {"okdp": {"properties": {"policy": {"properties": {"privileged": {"type": "string"}}}}}}}}}`,
			"schemas: platform: #/properties/global/properties/okdp/properties/policy/properties/privileged: given by another rule set"},
		"a keyword": {"platform.schema.json", `{"properties": {"global": {"properties": {"okdp": {"properties": {"policy": {"additionalProperties": true}}}}}}}`,
			"schemas: platform: #/properties/global/properties/okdp/properties/policy/additionalProperties: given by another rule set"},
		"a required": {"platform.schema.json", `{"properties": {"global": {"properties": {"okdp": {"required": ["extra"]}}}}}`,
			"schemas: platform: #/properties/global/properties/okdp/required: given by another rule set"},
		"a leaf's default": {"platform.schema.json", `{"properties": {"global": {"properties": {"okdp": {"properties": {"secrets": {"properties": {"serviceAccount": {"default": "other"}}}}}}}}}`,
			"schemas: platform: #/properties/global/properties/okdp/properties/secrets/properties/serviceAccount: given by another rule set"},
		"a default": {"platform.schema.json", `{"properties": {"global": {"properties": {"okdp": {"properties": {"secrets": {"default": {}}}}}}}}`,
			"schemas: platform: #/properties/global/properties/okdp/properties/secrets/default: given by another rule set"},
		"a type": {"platform.schema.json", `{"type": "array"}`, "schemas: platform: #/type: given by another rule set"},
		"a $defs": {"mapping.schema.json", `{"$defs": {"generatedKey": {"type": "string"}}}`,
			"schemas: mapping: #/$defs/generatedKey/type: given by another rule set"},
		"a boolean schema": {"mapping.schema.json", `{"properties": {"output": true}}`, "schemas: mapping: #/properties/output: given by another rule set"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadRules(extraSet(t, map[string]string{tc.file: tc.schema}))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "replaces none") {
				t.Errorf("got %v, want an error of %q", err, tc.want)
			}
		})
	}
	if _, err := LoadRules(extraSet(t, map[string]string{"platform.schema.json": `{"properties": {"global": {"type": "object"}}}`})); err != nil {
		t.Errorf("a keyword of the mandatory schema given again, alike: %v", err)
	}
	if _, err := LoadRules(extraSet(t, map[string]string{"platform.schema.json": `{"properties": `})); err == nil || !strings.Contains(err.Error(), "platform.schema.json") {
		t.Errorf("an added schema that does not parse: %v", err)
	}
	bad := `{"properties": {"global": {"properties": {"okdp": {"properties": {"extra": {"type": "nothing"}}}}}}}`
	if _, err := LoadRules(extraSet(t, map[string]string{"platform.schema.json": bad})); err == nil || !strings.Contains(err.Error(), "schemas: platform, with what the added rule sets give") {
		t.Errorf("an added schema that does not compile: %v", err)
	}
}

// TestScopes: the scope documents of every set are evaluated in order and
// their platform merged; a value two of them give is refused, naming both.
// With the mandatory set alone, the platform is its scope document's.
func TestScopes(t *testing.T) {
	ctx := context.Background()
	input := M{"settings": M{"extra": M{"level": 2}, "access": M{"admins": []any{"ops"}}}, "release": M{"name": "rel", "namespace": "demo"}, "items": M{"team": "editor"}}
	values, err := Mandatory().Values(ctx, "scope", input)
	if err != nil {
		t.Fatal(err)
	}
	alone, err := Mandatory().Scope(ctx, input)
	if err != nil || !reflect.DeepEqual(alone, values["platform"]) {
		t.Errorf("the mandatory set alone: %v, %v, want %v", alone, err, values["platform"])
	}
	both, err := loadExtra(t).Scope(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(both["extra"]) != "map[level:2 release:rel]" {
		t.Errorf("the added set's key: %#v", both["extra"])
	}
	access := both["access"].(M)
	if !reflect.DeepEqual(access["extraGroups"], []any{"team"}) || !reflect.DeepEqual(access["viewers"], alone["access"].(M)["viewers"]) {
		t.Errorf("the access maps merged: %v", access)
	}
	for k, v := range alone {
		if k != "access" && !reflect.DeepEqual(both[k], v) {
			t.Errorf("%s: %v, the mandatory set's %v", k, both[k], v)
		}
	}
	if _, set := alone["extra"]; set {
		t.Error("the added set's scope changed the mandatory set's own result")
	}
	collide := extraSet(t, map[string]string{"scope.cel.yaml": "values:\n  platform:\n    access:\n      viewers: []\n"})
	r, err := LoadRules(collide)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Scope(ctx, input); err == nil || err.Error() != "the rule set: scopes: platform.access.viewers is given by both scope and extraScope" {
		t.Errorf("a value two documents give: %v", err)
	}
	collide["scope.cel.yaml"] = &fstest.MapFile{Data: []byte("values:\n  platform:\n    access: none\n")}
	if r, err = LoadRules(collide); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Scope(ctx, input); err == nil || !strings.Contains(err.Error(), "platform.access is given by both scope and extraScope") {
		t.Errorf("a map one document gives, a scalar the other: %v", err)
	}
	collide["scope.cel.yaml"] = &fstest.MapFile{Data: []byte("errors: [refused here]\n")}
	if r, err = LoadRules(collide); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Scope(ctx, input); !errors.Is(err, ErrRefused) || err.Error() != "refused here" {
		t.Errorf("a scope document's message: %v", err)
	}
	if _, err := new(RuleSet).Scope(ctx, input); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("the zero value: %v", err)
	}
	for name, tc := range map[string]struct{ rules, want string }{
		"no document":         {extraRules(t, "scopes: [extraScope]", "scopes: [missing]"), "rules.yaml: scopes: missing is none of documents"},
		"a check":             {extraRules(t, "scopes: [extraScope]", "scopes: [namespace]"), "rules.yaml: scopes: namespace is none of documents"},
		"the mandatory scope": {extraRules(t, "scopes: [extraScope]", "scopes: [scope]"), "rules.yaml: scopes: scope: given by another rule set"},
		"listed twice":        {extraRules(t, "scopes: [extraScope]", "scopes: [extraScope, extraScope]"), "rules.yaml: scopes: extraScope: given by another rule set"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadRules(extraSet(t, map[string]string{RulesFile: tc.rules})); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestSetNames: each set names itself, the mandatory one core; an added
// set without a name, one of a name taken and one of a version that is
// none are refused.
func TestSetNames(t *testing.T) {
	r := loadExtra(t)
	if !slices.Equal(r.Names(), []string{CoreSet, "extra"}) || !slices.Equal(Mandatory().Names(), []string{CoreSet}) || len(new(RuleSet).Names()) != 0 {
		t.Errorf("the names: %v, %v", r.Names(), Mandatory().Names())
	}
	if v, ok := r.SetVersion("extra"); !ok || v != "1.2.0" || r.Version() != "1.2.0" || Mandatory().Version() != "1.0.0" {
		t.Errorf("the versions: %q %v, %q", v, ok, r.Version())
	}
	if v, ok := r.SetVersion(CoreSet); !ok || v != "1.0.0" {
		t.Errorf("the mandatory set's version: %q %v", v, ok)
	}
	if _, ok := r.SetVersion("other"); ok {
		t.Error("a set not loaded has a version")
	}
	for name, tc := range map[string]struct{ rules, want string }{
		"unnamed":        {extraRules(t, "name: extra\n", ""), "rules.yaml: name: required: a rule set added to the mandatory one names itself"},
		"duplicate name": {extraRules(t, "name: extra", "name: core"), "rules.yaml: name: core: given by another rule set, the compiler's mandatory one: a rule set adds rules, it replaces none"},
		"bad name":       {extraRules(t, "name: extra", "name: Extra_Set"), "rules.schema.json"},
		"bad version":    {extraRules(t, "version: 1.2.0", "version: \"1.2\""), `rules.yaml: version: "1.2" is no version MAJOR.MINOR.PATCH, with an optional -prerelease`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadRules(extraSet(t, map[string]string{RulesFile: tc.rules})); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	if _, err := loadEdited(t, func(s string) string { return strings.Replace(s, "name: core", "name: base", 1) }); err == nil || !strings.Contains(err.Error(), `name: the mandatory rule set is core, not "base"`) {
		t.Errorf("the mandatory set of another name: %v", err)
	}
}

// TestRequire: a tree requires a set by its name and a constraint on its
// version.
func TestRequire(t *testing.T) {
	r := loadExtra(t)
	for _, c := range []string{"", "1.2.0", "=1.2.0", "^1.0.0", "~1.2.0", ">=1.0.0, <2.0.0", "!=1.1.0", ">1.1.9", "<= 1.2.0"} {
		if err := r.Require("extra", c); err != nil {
			t.Errorf("%q: %v", c, err)
		}
	}
	if err := r.Require(CoreSet, "^1.0.0"); err != nil {
		t.Errorf("the mandatory set: %v", err)
	}
	if err := r.Require("other", ""); err == nil || err.Error() != "the platform requires the rule set other, which the compile was not given: give it with okdp-compile --rules (compiler.LoadRules)" {
		t.Errorf("a set not loaded: %v", err)
	}
	if err := r.Require("extra", "^2.0.0"); err == nil || err.Error() != "the platform requires the rule set extra ^2.0.0, the compile was given its version 1.2.0: give a matching one with okdp-compile --rules (compiler.LoadRules)" {
		t.Errorf("a version refused: %v", err)
	}
	if err := r.Require("extra", "^1.x"); err == nil || !strings.Contains(err.Error(), `the rule set extra: version "^1.x": "1.x" is no version`) {
		t.Errorf("a constraint that is none: %v", err)
	}
}

// TestConstraints: the grammar of a constraint on a version.
func TestConstraints(t *testing.T) {
	for _, tc := range []struct {
		constraint, version string
		want                bool
	}{
		{"1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.4", false},
		{"=1.2.3", "1.2.3", true},
		{"!=1.2.3", "1.2.3", false},
		{">1.2.3", "1.2.4", true},
		{">1.2.3", "1.2.3", false},
		{">=1.2.3", "1.2.3", true},
		{"<1.2.3", "1.2.2", true},
		{"<1.2.3", "1.2.3", false},
		{"<=1.2.3", "1.2.3", true},
		{"~1.2.3", "1.2.9", true},
		{"~1.2.3", "1.3.0", false},
		{"~1.2.3", "1.2.2", false},
		{"^1.2.3", "1.9.0", true},
		{"^1.2.3", "2.0.0", false},
		{"^1.2.3", "1.2.2", false},
		{"^0.2.3", "0.2.9", true},
		{"^0.2.3", "0.3.0", false},
		{">=1.0.0, <2.0.0", "1.5.0", true},
		{">=1.0.0, <2.0.0", "2.0.0", false},
		{"<1.0.0", "1.0.0-rc.1", true},
		{">1.0.0-rc.1", "1.0.0-rc.2", true},
		{">1.0.0-rc.9", "1.0.0-rc.10", true},
		{">1.0.0-alpha", "1.0.0-alpha.1", true},
		{">1.0.0-alpha.1", "1.0.0-alpha.beta", true},
		{">1.0.0-beta.11", "1.0.0-rc.1", true},
		{"=1.0.0-1", "1.0.0-1", true},
		{"<1.0.0-alpha", "1.0.0-1", true},
	} {
		match, err := parseConstraint(tc.constraint)
		if err != nil {
			t.Errorf("%q: %v", tc.constraint, err)
			continue
		}
		v, err := parseVersion(tc.version)
		if err != nil {
			t.Errorf("%q: %v", tc.version, err)
			continue
		}
		if match(v) != tc.want {
			t.Errorf("%q of %s: %v, want %v", tc.constraint, tc.version, !tc.want, tc.want)
		}
	}
	for _, c := range []string{"x", "1.2", "1.2.3.4", "01.2.3", "1.2.3-", "1.2.3-01", "1.2.3+build", ">=", "1.2.3,", "=>1.2.3", "v1.2.3", "1.2.3-a..b"} {
		if _, err := parseConstraint(c); err == nil {
			t.Errorf("%q: no error", c)
		}
	}
}
