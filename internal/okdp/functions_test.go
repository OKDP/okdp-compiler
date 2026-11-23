package okdp

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/value"
)

// functionRules: testdata/functions over the mandatory set: the fragments
// exampleConfigMap (result objects) and exampleSettings (no result), the
// function exampleName a document computes, and the section notes, each
// checked against the mapping schema the set adds.
func functionRules(t *testing.T) *RuleSet {
	t.Helper()
	r, err := LoadRules(os.DirFS("testdata/functions"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// functionCompile: def with this okdp.yaml and values file, compiled with
// rules.
func functionCompile(rules *RuleSet, okdp, values string) (*Result, error) {
	d := *def
	d.Okdp, d.Values = []byte(okdp), []byte(values)
	in := inputs()
	in.Rules = rules
	return Compile(context.Background(), &d, in)
}

// TestFragmentFunctions: a fragment of a rule set, called by a mapping, is
// evaluated on its arguments, checked against the mapping schema under the
// function's name, and on what a fragment sees
// (the release, OKDP's labels, platform, global); result names the value
// it returns, every value without it. A function a document computes
// returns the document's value of that name.
func TestFragmentFunctions(t *testing.T) {
	res, err := functionCompile(functionRules(t), `
objects:
  - $spread: "${{ exampleConfigMap({'name': 'settings', 'data': {'a': 'b'}}) }}"
instance: {url: "https://${{ exampleName('web') }}", usage: demo}
`, "app:\n  settings: \"${{ exampleSettings({}) }}\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Objects) != 1 {
		t.Fatalf("objects: %#v", res.Objects)
	}
	o := res.Objects[0]
	if value.Dig(o, "metadata.name") != "demo-app-settings" || value.Dig(o, "metadata.namespace") != "demo" || value.AsMap(value.Dig(o, "metadata.labels"))["okdp.io/instance"] != "demo-app" {
		t.Errorf("the ConfigMap's metadata: %#v", o["metadata"])
	}
	if want := (M{"a": "b", "replicas": "1", "domain": "example.org"}); !reflect.DeepEqual(o["data"], want) {
		t.Errorf("the ConfigMap's data: got %#v, want %#v", o["data"], want)
	}
	if want := (M{"name": "app-demo-app", "viewers": []any{}}); !reflect.DeepEqual(res.Values["app"]["settings"], want) {
		t.Errorf("a fragment without result: got %#v, want %#v", res.Values["app"]["settings"], want)
	}
	if res.Info.URL != "https://demo-app-web" {
		t.Errorf("a function of a document: %q", res.Info.URL)
	}
}

// TestFragmentErrors: a fragment's arguments that are not a map, or that
// its schema refuses, fail the call; the objects a fragment returns are
// the package's, and the compile's checks apply to them. A compile without
// the set that gives the function fails when it loads the mapping, the
// call in any branch.
func TestFragmentErrors(t *testing.T) {
	rules := functionRules(t)
	for name, tc := range map[string]struct{ args, want string }{
		"not a map":     {`'settings'`, "exampleConfigMap: the arguments are a map, got"},
		"no name":       {`{}`, "exampleConfigMap: the arguments don't meet the specifications of mapping.schema.json:\n- at '/exampleConfigMap': missing property 'name'"},
		"a minimum":     {`{'name': 'a', 'replicas': 0}`, "- at '/exampleConfigMap/replicas': minimum: got 0, want 1"},
		"unknown":       {`{'name': 'a', 'nmae': 'b'}`, "- at '/exampleConfigMap': additional properties 'nmae' not allowed"},
		"another space": {`{'name': 'a', 'namespace': 'other'}`, "demo: objects: ConfigMap/demo-app-a:"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := functionCompile(rules, "objects:\n  - $spread: \"${{ exampleConfigMap("+tc.args+") }}\"\n", "app: {}\n")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	_, err := functionCompile(Mandatory(), "vars:\n  m: \"${{ values.replicas > 5 ? exampleConfigMap({'name': 'a'}) : [] }}\"\n", "app: {}\n")
	if err == nil || !strings.Contains(err.Error(), "okdp.yaml: vars m: ${{ values.replicas > 5 ? exampleConfigMap({'name': 'a'}) : [] }}: ERROR: <input>:1:40: undeclared reference to 'exampleConfigMap'") {
		t.Errorf("without the set: got %v", err)
	}
}
