package rules

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestZeroValue: a RuleSet no LoadRules made holds no rule and panics on
// nothing: its tables are empty, its documents and hooks fail, naming
// ErrNotLoaded.
func TestZeroValue(t *testing.T) {
	var r RuleSet
	if r.Loaded() || r.List("engineNamespaces") != nil {
		t.Errorf("the zero value holds something: %+v", r)
	}
	if err := r.Refuse(context.Background(), "namespace", M{"field": "f", "ns": "demo", "settings": M{}}); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("Refuse: %v", err)
	}
	if err := r.RunHook(context.Background(), HookNamespace, M{"field": "f", "ns": "demo", "settings": M{}}); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("RunHook: %v", err)
	}
	if _, err := r.Values(context.Background(), "scope", M{}); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("Values: %v", err)
	}
	if !Mandatory().Loaded() {
		t.Error("the mandatory set is not loaded")
	}
}

// TestFailure: a rule that refuses is a *Failure, ErrRefused, whose message
// is the rule's; a document cancelled is not.
func TestFailure(t *testing.T) {
	r := Mandatory()
	err := r.Refuse(context.Background(), "namespace", M{"field": "f", "ns": "kube-system", "settings": M{}})
	var f *Failure
	if !errors.As(err, &f) || !errors.Is(err, ErrRefused) || f.Message != err.Error() {
		t.Fatalf("a refusal: %v (%+v)", err, f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = r.Refuse(ctx, "namespace", M{"field": "f", "ns": "kube-system", "settings": M{}})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrRefused) {
		t.Errorf("a document cancelled: %v", err)
	}
}

// TestSchemaError: the values a schema refuses, an error whose message is
// the heading, then the violations.
func TestSchemaError(t *testing.T) {
	r := Mandatory()
	_, err := SchemaValues(r.PlatformSchema(), "heading", M{"global": M{"okdp": M{"proxy": "x"}}})
	if err == nil || !strings.HasPrefix(err.Error(), "heading:\n") || err.Error() == "heading:\n" {
		t.Errorf("got %v", err)
	}
}
