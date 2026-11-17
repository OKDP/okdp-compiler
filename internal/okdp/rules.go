package okdp

import (
	"io/fs"

	"github.com/okdp/okdp-compiler/chart"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// RuleSet holds, as data, what the compiler refuses and what it knows of
// a platform's names (internal/rules).
type RuleSet = ruleset.RuleSet

// LoadRules loads the rule set of a file system and adds it to the
// mandatory one.
func LoadRules(fsys fs.FS) (*RuleSet, error) { return ruleset.LoadRules(fsys) }

// Mandatory returns the mandatory rule set alone.
func Mandatory() *RuleSet { return ruleset.Mandatory() }

func schemaValues(schema *chart.Schema, heading string, user M) (M, error) {
	return ruleset.SchemaValues(schema, heading, user)
}

func (in Inputs) rules() *RuleSet { return rulesOr(in.Rules) }

// rulesOr returns r, or the mandatory rule set alone when r is nil.
func rulesOr(r *RuleSet) *RuleSet {
	if r != nil {
		return r
	}
	return Mandatory()
}

// objectInput returns an object as the checks read it: its fields as
// text, whatever a mapping wrote in them, and the object itself (raw).
func objectInput(o M) M {
	meta := value.AsMap(o["metadata"])
	kind, name, version := value.Str(o["kind"]), value.Str(meta["name"]), value.Str(o["apiVersion"])
	return M{"id": kind + "/" + name, "kind": kind, "name": name, "apiVersion": version, "group": apiGroup(version), "ns": meta["namespace"], "raw": o}
}
