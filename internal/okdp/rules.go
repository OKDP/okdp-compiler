package okdp

import (
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// RuleSet: what the compiler refuses and what it knows of a platform's
// names, as data (internal/rules).
type RuleSet = ruleset.RuleSet

// LoadRules: the rule set of a file system.
var LoadRules = ruleset.LoadRules

// defaultRules: the rule set of a compile that gives none. Temporary: a
// compile without one is refused once the hosts give it.
var defaultRules = ruleset.Default

// schemaValues: ruleset.SchemaValues.
var schemaValues = ruleset.SchemaValues

// rules: the rule set of the compile.
func (in Inputs) rules() *RuleSet {
	if in.Rules != nil {
		return in.Rules
	}
	return defaultRules()
}

// objectInput: an object as the checks read it: what its fields are as
// text, whatever a mapping wrote, and the object as it is (raw).
func objectInput(o M) M {
	meta := value.AsMap(o["metadata"])
	kind, name, version := value.Str(o["kind"]), value.Str(meta["name"]), value.Str(o["apiVersion"])
	return M{"id": kind + "/" + name, "kind": kind, "name": name, "apiVersion": version, "group": apiGroup(version), "ns": meta["namespace"], "raw": o}
}
