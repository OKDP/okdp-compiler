package rules

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestZeroValue: a RuleSet no LoadRules made holds no rule and panics on
// nothing: its tables are empty, its checks fail, naming ErrNotLoaded.
func TestZeroValue(t *testing.T) {
	var r RuleSet
	if r.Loaded() || r.Has(TableProjectRoles, "viewer") || r.List(TableProjectRoles) != nil || len(r.Added(TableProjectRoles, M{})) != 0 {
		t.Errorf("the zero value holds something: %+v", r)
	}
	if err := r.Refuse(context.Background(), CheckNamespaceRule, M{"ns": "demo"}); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("Refuse: %v", err)
	}
	if _, err := r.Values(context.Background(), "scope", M{}); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("Values: %v", err)
	}
	if !Mandatory().Loaded() {
		t.Error("the empty set is not loaded")
	}
}

// TestFailure: a rule that refuses is a *Failure, ErrRefused, whose message
// is the rule's; a check cancelled is not.
func TestFailure(t *testing.T) {
	r, err := LoadRules(os.DirFS(standardDir))
	if err != nil {
		t.Fatal(err)
	}
	err = r.Refuse(context.Background(), CheckNamespaceRule, M{"ns": "kube-system"})
	var f *Failure
	if !errors.As(err, &f) || !errors.Is(err, ErrRefused) || f.Check != CheckNamespaceRule || f.Message != err.Error() {
		t.Fatalf("a refusal: %v (%+v)", err, f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = r.Refuse(ctx, CheckNamespaceRule, M{"ns": "kube-system"})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrRefused) {
		t.Errorf("a check cancelled: %v", err)
	}
}

// TestSchemaError: the values a schema refuses, a *SchemaError, whose
// message is the heading, then the violations.
func TestSchemaError(t *testing.T) {
	r, err := LoadRules(os.DirFS(standardDir))
	if err != nil {
		t.Fatal(err)
	}
	_, err = SchemaValues(r.PlatformSchema(), "heading", M{"global": M{"okdp": M{"policy": "x"}}})
	var se *SchemaError
	if !errors.As(err, &se) || err.Error() != "heading:\n"+se.Violations || se.Violations == "" {
		t.Errorf("got %v", err)
	}
}
