// Labels: the labels of the objects OKDP renders itself.

package okdp

import (
	"cel.dev/cel-go/cel"
	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/value"
)

// labelFunctions returns okdpLabels(component) and
// okdpSelectorLabels(component), the latter being the stable subset.
// Both add app.kubernetes.io/component when component is not empty.
// There is no helm.sh/chart label: the objects belong to no chart of the
// service.
func labelFunctions(rules *RuleSet, name, release string) []cel.EnvOption {
	fn := func(fname string, selector bool) cel.EnvOption {
		return cel2yaml.Costed(cel.Function(fname, cel.Overload(fname+"_dyn", []*cel.Type{cel.DynType}, cel.DynType,
			cel2yaml.Unary(func(v any) (any, error) { return rules.Labels(name, release, value.Str(v), selector), nil }))), fname+"_dyn")
	}
	return []cel.EnvOption{fn("okdpLabels", false), fn("okdpSelectorLabels", true)}
}
