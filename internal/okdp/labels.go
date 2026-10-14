// Labels: the labels of the objects OKDP renders itself, from the package
// (Chart.yaml's name) and the release.

package okdp

import (
	"cel.dev/cel-go/cel"

	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// labels: the labels of every object OKDP renders itself, of the service
// name and the release, with app.kubernetes.io/component when component is
// not empty: the selector labels, the stable subset; then, unless selector,
// Helm as manager (the umbrella renders them), part of okdp, and the
// instance and service OKDP finds the objects by. No helm.sh/chart: the
// objects belong to no chart of the service.
func labels(name, release, component string, selector bool) M {
	out := M{"app.kubernetes.io/name": name, "app.kubernetes.io/instance": release}
	if component != "" {
		out["app.kubernetes.io/component"] = component
	}
	if !selector {
		out["app.kubernetes.io/managed-by"] = "Helm"
		out["app.kubernetes.io/part-of"] = "okdp"
		out["okdp.io/instance"] = release
		out["okdp.io/service"] = name
	}
	return out
}

// labelFunctions: okdpLabels(component) and okdpSelectorLabels(component),
// the labels with app.kubernetes.io/component.
func labelFunctions(name, release string) []cel.EnvOption {
	fn := func(fname string, selector bool) cel.EnvOption {
		return cel.Function(fname, cel.Overload(fname+"_dyn", []*cel.Type{cel.DynType}, cel.DynType,
			compile.Unary(func(v any) (any, error) { return labels(name, release, value.Str(v), selector), nil })))
	}
	return []cel.EnvOption{fn("okdpLabels", false), fn("okdpSelectorLabels", true)}
}
