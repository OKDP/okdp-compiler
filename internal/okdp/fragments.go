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
	_ "embed"
	"fmt"

	"cel.dev/cel-go/cel"

	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

//go:embed fragment-pod.cel.yaml
var podTemplate []byte

//go:embed fragment-job.cel.yaml
var jobTemplate []byte

//go:embed fragment-proxy.cel.yaml
var proxyTemplate []byte

//go:embed fragment-web.cel.yaml
var webTemplate []byte

// fragment: a function of OKDP's templates: its arguments checked against
// mappingSchema under the function's name, then the template evaluated
// on them and the instance's context; result names the value it returns.
type fragment struct {
	name     string // the function: okdpPodSpec...
	file     string // the template's file, the first segment of its errors
	template []byte
	result   string // the key of the template's values it returns
}

// fragments: the functions.
var fragments = []fragment{
	{"okdpPodSpec", "fragment-pod.cel.yaml", podTemplate, "spec"},
	{"okdpJob", "fragment-job.cel.yaml", jobTemplate, "objects"},
	{"okdpSignInProxy", "fragment-proxy.cel.yaml", proxyTemplate, ""},
	{"okdpWebWorkload", "fragment-web.cel.yaml", webTemplate, "objects"},
}

// call: the value of the fragment f for the arguments args (okdpPodSpec(args)...), in
// the compile's session: args checked, the template evaluated with args, the
// instance's release, labels, platform and global.
func (f fragment) call(session *compile.Session, inst *instance, args any) (any, error) {
	checked, err := schemaValues(mappingSchema, f.name+": the arguments don't meet the specifications of mapping.schema.json", M{f.name: args})
	if err != nil {
		return nil, err
	}
	scope := M{
		"args":     checked[f.name],
		"release":  M{"name": inst.release, "namespace": inst.namespace},
		"labels":   labels(inst.svc.Name, inst.release, "", false),
		"platform": inst.scope["platform"],
		"global":   inst.scope["global"],
	}
	v, err := evalTemplate(session, f.file, f.template, scope)
	if err != nil {
		return nil, err
	}
	if f.result == "" {
		return v, nil
	}
	return v[f.result], nil
}

// fragmentFunctions: okdpPodSpec(args), okdpJob(args), okdpSignInProxy(args)
// and okdpWebWorkload(args) for the instance inst, in the session *session.
func fragmentFunctions(inst *instance, session **compile.Session) []cel.EnvOption {
	var opts []cel.EnvOption
	for _, f := range fragments {
		id := f.name + "_map"
		opts = append(opts, compile.Costed(cel.Function(f.name, cel.Overload(id, []*cel.Type{cel.DynType}, cel.DynType,
			compile.Unary(func(v any) (any, error) {
				if _, ok := v.(M); !ok {
					return nil, fmt.Errorf("%s: the arguments are a map, got %s", f.name, value.ShortAt("args", v))
				}
				return f.call(*session, inst, v)
			}))), id))
	}
	return opts
}
