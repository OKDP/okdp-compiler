// Labels: the labels of the objects OKDP renders itself, from the package
// (Chart.yaml's name) and the release.

package okdp

import (
	"cel.dev/cel-go/cel"
	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/value"
)

// labels: the labels of every object OKDP renders itself, of the service
// name and the release, with app.kubernetes.io/component when component is
// not empty: the selector labels, the stable subset; then, unless selector,
// Helm as manager (the umbrella renders them), part of okdp, and the
// instance and service OKDP finds the objects by. No helm.sh/chart: the
// objects belong to no chart of the service.
// labelFunctions: okdpLabels(component) and okdpSelectorLabels(component),
// the labels with app.kubernetes.io/component.
func labelFunctions(rules *RuleSet, name, release string) []cel.EnvOption {
	fn := func(fname string, selector bool) cel.EnvOption {
		return cel2yaml.Costed(cel.Function(fname, cel.Overload(fname+"_dyn", []*cel.Type{cel.DynType}, cel.DynType,
			cel2yaml.Unary(func(v any) (any, error) { return rules.labels(name, release, value.Str(v), selector), nil }))), fname+"_dyn")
	}
	return []cel.EnvOption{fn("okdpLabels", false), fn("okdpSelectorLabels", true)}
}
