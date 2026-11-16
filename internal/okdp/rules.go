package okdp

import (
	"io/fs"

	"github.com/okdp/okdp-compiler/chart"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// RuleSet: what the compiler refuses and what it knows of a platform's
// names, as data (internal/rules).
type RuleSet = ruleset.RuleSet

// LoadRules: the rule set of a file system.
func LoadRules(fsys fs.FS) (*RuleSet, error) { return ruleset.LoadRules(fsys) }

// schemaValues: ruleset.SchemaValues.
func schemaValues(schema *chart.Schema, heading string, user M) (M, error) {
	return ruleset.SchemaValues(schema, heading, user)
}

// rules: the rule set of the compile.
func (in Inputs) rules() *RuleSet { return rulesOr(in.Rules) }

// rulesOr: r, or the rule set of a compile that gives none.
func rulesOr(r *RuleSet) *RuleSet {
	if r != nil {
		return r
	}
	return ruleset.Empty()
}

// objectInput: an object as the checks read it: what its fields are as
// text, whatever a mapping wrote, and the object as it is (raw).
func objectInput(o M) M {
	meta := value.AsMap(o["metadata"])
	kind, name, version := value.Str(o["kind"]), value.Str(meta["name"]), value.Str(o["apiVersion"])
	return M{"id": kind + "/" + name, "kind": kind, "name": name, "apiVersion": version, "group": apiGroup(version), "ns": meta["namespace"], "raw": o}
}
