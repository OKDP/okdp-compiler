package compiler

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestRequirementsYAML: require in service.yaml, each entry a key or {key,
// $if}; an entry of another form fails with its line.
func TestRequirementsYAML(t *testing.T) {
	var svc Service
	src := `require:
  - ingress.suffix
  - key: oidc.issuerUri
    $if: ${{ platform.oidc.enabled }}
  - {key: proxy.httpProxy, $if: "${{ values.replicas > 1 }}"}
`
	if err := yaml.Unmarshal([]byte(src), &svc); err != nil {
		t.Fatal(err)
	}
	want := Requirements{
		{Key: "ingress.suffix"},
		{Key: "oidc.issuerUri", If: "${{ platform.oidc.enabled }}"},
		{Key: "proxy.httpProxy", If: "${{ values.replicas > 1 }}"},
	}
	if !reflect.DeepEqual(svc.Require, want) {
		t.Errorf("got %#v, want %#v", svc.Require, want)
	}
	for src, want := range map[string]string{
		"require: a":        "line 1: cannot unmarshal !!str `a` into compiler.Requirements",
		"require: [a, [b]]": "line 1: cannot unmarshal !!seq into string",
		"require: [{key: a, $if: '${{ c }}', when: x}]": "line 1: field when not found in type compiler.Requirement",
		"require: [{key: [a], $if: '${{ c }}'}]":        "line 1: cannot unmarshal !!seq into string",
	} {
		var svc Service
		if err := yaml.Unmarshal([]byte(src), &svc); err == nil || err.Error() != "yaml: unmarshal errors:\n  "+want {
			t.Errorf("%s: got %v, want %q", src, err, want)
		}
	}
}

// TestRequirementsCheck: a requirement built in memory is checked as one
// read from service.yaml, before every compile.
func TestRequirementsCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		require Requirements
		want    string
	}{
		"an empty key":           {Requirements{{Key: "a"}, {}}, "service.yaml: require[1]: an empty key: a key under global.okdp (ingress.suffix)"},
		"an empty key, with $if": {Requirements{{If: "${{ true }}"}}, "service.yaml: require[0]: an empty key"},
		"not an expression":      {Requirements{{Key: "a", If: "true"}}, `service.yaml: require[0] (a): $if: takes one expression, ${{ <condition> }}, got "true"`},
	} {
		d := *def
		d.Service.Require = tc.require
		if err := d.Service.Check(); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
		if _, err := Compile(context.Background(), &d, inputs()); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: compile: got %v, want %q", name, err, tc.want)
		}
	}
}

// TestRequireWhen: a requirement under a condition, evaluated on the
// mapping's context (variables and host functions), within the compile's
// budget: when it holds, the key is required as in every case. Every entry
// is checked once the context is built (its errors first), before the
// platform charts and the mapping.
func TestRequireWhen(t *testing.T) {
	compile := func(cond string, limit uint64, layers ...map[string]any) error {
		d := *def
		d.Service.Require = Requirements{{Key: "ingress.suffix"}, {Key: "oidc.issuerUri", If: cond}}
		in := inputs(layers...)
		in.CostLimit = limit
		_, err := Compile(context.Background(), &d, in)
		return err
	}
	missing := "global.okdp.oidc.issuerUri is required. It is a platform value: set it in the platform's values (--platform, Inputs.Platform)"
	issuer := M{"global": M{"okdp": M{"oidc": M{"issuerUri": "https://sso.example.org"}}}}
	disabled := M{"global": M{"okdp": M{"oidc": M{"enabled": false}}}}
	for name, tc := range map[string]struct {
		cond   string
		layers []map[string]any
		want   string // "": no error
	}{
		"holds, missing":          {"${{ platform.oidc.enabled }}", nil, missing},
		"holds, set":              {"${{ platform.oidc.enabled }}", []map[string]any{issuer}, ""},
		"does not hold":           {"${{ platform.oidc.enabled }}", []map[string]any{disabled}, ""},
		"on the parameters":       {"${{ values.replicas > 1 }}", []map[string]any{{"replicas": 2}}, missing},
		"on the parameters, not":  {"${{ values.replicas > 1 }}", nil, ""},
		"host functions":          {"${{ okdpFullname() == 'demo-app' && service.name == 'demo' }}", nil, missing},
		"before the mapping":      {"${{ true }}", []map[string]any{{"replicas": 11}}, missing},
		"before platform charts":  {"${{ true }}", []map[string]any{{"global": M{"okdp": M{"charts": nil}}}}, missing},
		"after the context":       {"${{ true }}", []map[string]any{{"global": M{"okdp": M{"ingress": M{"suffix": nil}, "oidc": M{"enabled": "yes"}}}}}, "platform values (--platform, Inputs.Platform) don't meet the specifications of the schema of global.okdp:\n- at '/global/okdp/oidc/enabled': got string, want boolean"},
		"not true or false":       {"${{ values.replicas }}", nil, "service.yaml: require[1] (oidc.issuerUri): $if: ${{ values.replicas }}: must be true or false, got 1"},
		"null":                    {"${{ platform.oidc.enabled }}", []map[string]any{{"global": M{"okdp": M{"oidc": M{"enabled": nil}}}}}, "service.yaml: require[1] (oidc.issuerUri): $if: ${{ platform.oidc.enabled }}: must be true or false, got null"},
		"an evaluation error":     {"${{ values.nope }}", nil, "service.yaml: require[1] (oidc.issuerUri): $if: ${{ values.nope }}: no such key: nope"},
		"no let binding in scope": {"${{ host != '' }}", nil, "service.yaml: require[1] (oidc.issuerUri): $if: ${{ host != '' }}: ERROR: <input>:1:2: undeclared reference to 'host'"},
	} {
		err := compile(tc.cond, 0, tc.layers...)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)):
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
	// The compile's budget: its cost limit and its context.
	cond := "${{ platform.oidc.enabled }}"
	if err := compile(cond, 1); err == nil || err.Error() != "service.yaml: require[1] (oidc.issuerUri): $if: ${{ platform.oidc.enabled }}: the expressions of the compile exceed its CEL cost limit (1)" {
		t.Errorf("cost limit: got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := *def
	d.Service.Require = Requirements{{Key: "oidc.issuerUri", If: cond}}
	if _, err := Compile(ctx, &d, inputs(issuer)); !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "service.yaml: require[0] (oidc.issuerUri): $if: ") {
		t.Errorf("cancelled: got %v, want context.Canceled at the condition", err)
	}
}
