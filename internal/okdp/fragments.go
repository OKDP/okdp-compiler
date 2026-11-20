// Fragments: the functions a rule set provides to packages, each a
// template evaluated on the arguments of the call, typically objects a
// package calls for instead of writing its own, so that they are written
// and reviewed once. The objects they return belong to the package, so
// the compile's policy over objects applies.

package okdp

import (
	"context"
	"fmt"

	"cel.dev/cel-go/cel"
	"github.com/okdp/cel2yaml"

	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// callFragment evaluates the fragment f, a template of the rule set, on
// args in the compile's session. It checks args against
// mapping.schema.json.
func callFragment(ctx context.Context, f ruleset.Fragment, session *cel2yaml.Session, inst *instance, args any) (any, error) {
	checked, err := ruleset.SchemaValues(inst.rules.MappingSchema(), f.Name+": the arguments don't meet the specifications of mapping.schema.json", M{f.Name: args})
	if err != nil {
		return nil, err
	}
	scope := M{
		"args":     checked[f.Name],
		"release":  M{"name": inst.release, "namespace": inst.namespace},
		"labels":   inst.rules.Labels(inst.svc.Name, inst.release, "", false),
		"platform": inst.scope["platform"],
		"global":   inst.scope["global"],
	}
	v, err := evalTemplate(ctx, session, f.Template, scope)
	if err != nil {
		return nil, err
	}
	if f.Result == "" {
		return v, nil
	}
	return v[f.Result], nil
}

// fragmentFunctions returns the fragment functions of the rule set for
// inst. They run in *session, which is read at each call.
func fragmentFunctions(ctx context.Context, inst *instance, session **cel2yaml.Session) []cel.EnvOption {
	var opts []cel.EnvOption
	for _, f := range inst.rules.Fragments() {
		opts = append(opts, unary(f.Name, f.Name+"_map", func(v any) (any, error) {
			if _, ok := v.(M); !ok {
				return nil, fmt.Errorf("%s: the arguments are a map, got %s", f.Name, value.ShortAt("args", v))
			}
			return callFragment(ctx, f, *session, inst, v)
		}))
	}
	return opts
}
