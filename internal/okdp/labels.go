// Labels: the labels of the objects OKDP renders itself, from the package
// (service.yaml's name and appVersion) and the release.
package okdp

import (
	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"

	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// serviceMeta: what the mapping and OKDP's objects see of the package:
// service.yaml's name, version and appVersion.
type serviceMeta struct {
	Name       string
	Version    string
	AppVersion string
}

// ctxService: the package of a mapping context.
func ctxService(ctx M) serviceMeta {
	return serviceMeta{Name: value.Str(value.Dig(ctx, "service.name")), Version: value.Str(value.Dig(ctx, "service.version")), AppVersion: value.Str(value.Dig(ctx, "service.appVersion"))}
}

// selectorLabels: the stable subset of the labels, for selectors; with
// app.kubernetes.io/component when component is not empty.
func selectorLabels(svc serviceMeta, release, component string) M {
	out := M{
		"app.kubernetes.io/name":     svc.Name,
		"app.kubernetes.io/instance": release,
	}
	if component != "" {
		out["app.kubernetes.io/component"] = component
	}
	return out
}

// labels: the labels of every object OKDP renders itself: the selector
// labels (and component), the application version when service.yaml has
// one, Helm as manager (the objects chart renders them), part of okdp, and
// the instance and service OKDP finds the objects by. No helm.sh/chart: the
// objects belong to no chart of the service.
func labels(svc serviceMeta, release, component string) M {
	out := selectorLabels(svc, release, component)
	if svc.AppVersion != "" {
		out["app.kubernetes.io/version"] = svc.AppVersion
	}
	out["app.kubernetes.io/managed-by"] = "Helm"
	out["app.kubernetes.io/part-of"] = "okdp"
	out["okdp.io/instance"] = release
	out["okdp.io/service"] = svc.Name
	return out
}

// serviceContext: what the mapping sees of the package: service (name,
// version, appVersion), labels and selectorLabels (without component).
func serviceContext(svc serviceMeta, release string) M {
	return M{
		"service":        M{"name": svc.Name, "version": svc.Version, "appVersion": svc.AppVersion},
		"labels":         labels(svc, release, ""),
		"selectorLabels": selectorLabels(svc, release, ""),
	}
}

// labelFunctions: okdpLabels(component) and okdpSelectorLabels(component),
// the labels with app.kubernetes.io/component.
func labelFunctions(svc serviceMeta, release string) []cel.EnvOption {
	fn := func(name string, f func(serviceMeta, string, string) M) cel.EnvOption {
		return cel.Function(name, cel.Overload(name+"_dyn", []*cel.Type{cel.DynType}, cel.DynType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				return types.DefaultTypeAdapter.NativeToValue(f(svc, release, compile.Text(v)))
			})))
	}
	return []cel.EnvOption{fn("okdpLabels", labels), fn("okdpSelectorLabels", selectorLabels)}
}
