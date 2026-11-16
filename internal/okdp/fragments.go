// Fragments: the workloads OKDP provides to packages as CEL functions
// returning the objects, a pod spec, a Job, a sign-in proxy, a web
// workload, instead of each package writing its own: one hardening, one
// proxy, reviewed once, in git with the compiler, never a chart to publish.
// Each is a template of the compiler (fragment-*.cel.yaml, the mapping
// language) evaluated at each call on the arguments, which
// mapping.schema.json checks (okdpPodSpec, okdpJob, okdpSignInProxy,
// okdpWebWorkload); the objects are a package's own, the compile's policy
// over its objects applies to them: namespace, hosts, plain Secrets.

package okdp

import (
	"context"
	"fmt"

	"cel.dev/cel-go/cel"
	"github.com/okdp/cel2yaml"

	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// call: the value of the fragment f for the arguments args (okdpPodSpec(args)...), in
// the compile's session: args checked, the template evaluated with args, the
// instance's release, labels, platform and global.
func callFragment(ctx context.Context, f ruleset.Fragment, session *cel2yaml.Session, inst *instance, args any) (any, error) {
	checked, err := schemaValues(inst.rules.MappingSchema(), f.Name+": the arguments don't meet the specifications of mapping.schema.json", M{f.Name: args})
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

// fragmentFunctions: okdpPodSpec(args), okdpJob(args), okdpSignInProxy(args)
// and okdpWebWorkload(args) for the instance inst, in the session *session.
func fragmentFunctions(ctx context.Context, inst *instance, session **cel2yaml.Session) []cel.EnvOption {
	var opts []cel.EnvOption
	for _, f := range inst.rules.Fragments() {
		id := f.Name + "_map"
		opts = append(opts, cel2yaml.Costed(cel.Function(f.Name, cel.Overload(id, []*cel.Type{cel.DynType}, cel.DynType,
			cel2yaml.Unary(func(v any) (any, error) {
				if _, ok := v.(M); !ok {
					return nil, fmt.Errorf("%s: the arguments are a map, got %s", f.Name, value.ShortAt("args", v))
				}
				return callFragment(ctx, f, *session, inst, v)
			}))), id))
	}
	return opts
}
