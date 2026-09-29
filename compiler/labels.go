// Labels: the labels of the objects OKDP renders itself, as okdp.labels and
// okdp.selectorLabels of okdp-lib-chart compute them from the wrapper chart
// (Chart.yaml) and the release.
package compiler

import (
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// selectorLabels: okdp.selectorLabels, the stable subset of the labels, for
// selectors; with app.kubernetes.io/component when component is not empty.
func selectorLabels(chart chartMeta, release, component string) M {
	out := M{
		"app.kubernetes.io/name":     chart.Name,
		"app.kubernetes.io/instance": release,
	}
	if component != "" {
		out["app.kubernetes.io/component"] = component
	}
	return out
}

// labels: okdp.labels, the labels of every object OKDP renders itself:
// helm.sh/chart, the selector labels (and component), the application
// version when Chart.yaml has one, Helm as manager, part of okdp, and the
// instance and service OKDP finds the objects by.
func labels(chart chartMeta, release, component string) M {
	out := selectorLabels(chart, release, component)
	out["helm.sh/chart"] = strings.TrimSuffix(trunc(strings.ReplaceAll(chart.Name+"-"+chart.Version, "+", "_"), 63), "-")
	if chart.AppVersion != "" {
		out["app.kubernetes.io/version"] = chart.AppVersion
	}
	out["app.kubernetes.io/managed-by"] = "Helm"
	out["app.kubernetes.io/part-of"] = "okdp"
	out["okdp.io/instance"] = release
	out["okdp.io/service"] = chart.Name
	return out
}

// chartContext: what the mapping sees of the wrapper chart: chart (name,
// version, appVersion), labels and selectorLabels (without component).
func chartContext(chart chartMeta, release string) M {
	return M{
		"chart":          M{"name": chart.Name, "version": chart.Version, "appVersion": chart.AppVersion},
		"labels":         labels(chart, release, ""),
		"selectorLabels": selectorLabels(chart, release, ""),
	}
}

// labelFunctions: okdpLabels(component) and okdpSelectorLabels(component),
// the labels with app.kubernetes.io/component.
func labelFunctions(chart chartMeta, release string) []cel.EnvOption {
	fn := func(name string, f func(chartMeta, string, string) M) cel.EnvOption {
		return cel.Function(name, cel.Overload(name+"_dyn", []*cel.Type{cel.DynType}, cel.DynType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				return types.DefaultTypeAdapter.NativeToValue(f(chart, release, text(v)))
			})))
	}
	return []cel.EnvOption{fn("okdpLabels", labels), fn("okdpSelectorLabels", selectorLabels)}
}
