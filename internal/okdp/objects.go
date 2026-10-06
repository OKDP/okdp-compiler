// Objects: the okdp chart, the chart of OKDP's own objects the compiler
// adds to every compile, and the platform's charts (global.okdp.charts) the
// compiler's own charts are rendered with; no two of OKDP's own objects are
// one object of the cluster.
package okdp

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// ObjectsChart: the chart key of OKDP's own objects. The compiler adds it to
// every compile, rendered by the platform's objects chart
// (global.okdp.charts.objects: okdp-objects, values `objects: []`), its
// objects Result.Objects: the descriptor ConfigMap, the credentials Secrets
// and the objects section reach the cluster with the upstream charts.
// Neither service.yaml nor the mapping's output may list it.
const ObjectsChart = "okdp"

// platformCharts: the charts the platform renders the compiler's own charts
// with (global.okdp.charts of okdp): objects, the chart of OKDP's own objects
// (okdp-objects, required unless the compile renders no okdp chart,
// WithoutObjectsChart), and oidcDcr, the chart of the OAuth clients in dcr
// mode (DcrSource by default). Another key is refused: a misspelled oidcDcr
// would otherwise leave the default in place.
func platformCharts(okdp M, needObjects bool) (objects, dcr chart.Ref, err error) {
	charts, ok := okdp["charts"].(M)
	if !ok && okdp["charts"] != nil {
		return objects, dcr, fmt.Errorf("global.okdp.charts must be a map (objects, oidcDcr), got %s", value.Short(okdp["charts"]))
	}
	var unknown []string
	for k := range charts {
		if k != "objects" && k != "oidcDcr" {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return objects, dcr, fmt.Errorf("global.okdp.charts: unknown key(s) %s (allowed: objects, oidcDcr)", strings.Join(unknown, ", "))
	}
	if charts["objects"] == nil && needObjects {
		return objects, dcr, errors.New("global.okdp.charts.objects is required: the chart OKDP's own objects are rendered with (okdp-objects: repository, chart, version). " + platformValue)
	}
	if charts["objects"] != nil {
		if objects, err = platformChart(charts["objects"], "global.okdp.charts.objects"); err != nil {
			return objects, dcr, err
		}
	}
	dcr = DcrSource
	if charts["oidcDcr"] != nil {
		dcr, err = platformChart(charts["oidcDcr"], "global.okdp.charts.oidcDcr")
	}
	return objects, dcr, err
}

// platformChart: a chart of global.okdp.charts, a map of strings, checked.
func platformChart(v any, where string) (chart.Ref, error) {
	var r chart.Ref
	m, ok := v.(M)
	if !ok {
		return r, fmt.Errorf("%s must be a map (repository, chart, version), got %s", where, value.Short(v))
	}
	for _, f := range []struct {
		key string
		to  *string
	}{{"repository", &r.Repository}, {"chart", &r.Chart}, {"version", &r.Version}} {
		if x, set := m[f.key]; set {
			s, ok := x.(string)
			if !ok {
				return r, fmt.Errorf("%s.%s must be a string, got %s", where, f.key, value.Short(x))
			}
			*f.to = s
		}
	}
	return r, r.Check(where)
}

// objectsChart: the charts of svc plus the okdp chart (ObjectsChart),
// rendered by the platform's objects chart, its values the objects (no OKDP
// defaults, no instance values), added to the mapping's output, which may not
// list it. svc is not changed.
func objectsChart(svc *Service, objects []map[string]any, output M, src chart.Ref) (*Service, error) {
	if _, ok := output[ObjectsChart]; ok {
		return nil, fmt.Errorf("mapping output %q: the chart of OKDP's own objects is compiled from the objects section: remove it from output", ObjectsChart)
	}
	list := make([]any, 0, len(objects))
	for _, o := range objects {
		list = append(list, value.Copy(o))
	}
	output[ObjectsChart] = M{"objects": list}
	out := *svc
	out.Charts = maps.Clone(svc.Charts)
	if out.Charts == nil {
		out.Charts = chart.Specs{}
	}
	out.Charts[ObjectsChart] = chart.Spec{Source: src}
	return &out, nil
}

// ownObject: one of OKDP's own objects, and what gives it, for the errors:
// the descriptor, the credentials Secret of provides "<name>" (<contract>),
// objects[<i>].
type ownObject struct {
	from   string
	object M
}

// uniqueObjects: the first two objects that are one object of the cluster,
// refused: the same API group (of apiVersion, whatever its version), kind,
// namespace (namespace, the release's, for an object without one) and
// name. Applied, the second would replace the first.
func uniqueObjects(objects []ownObject, namespace string) error {
	seen := map[[4]string]string{}
	for _, o := range objects {
		group, _, grouped := strings.Cut(value.Str(o.object["apiVersion"]), "/")
		if !grouped {
			group = ""
		}
		md := value.AsMap(o.object["metadata"])
		kind, ns, name := value.Str(o.object["kind"]), value.Str(md["namespace"]), value.Str(md["name"])
		if ns == "" {
			ns = namespace
		}
		id := [4]string{group, kind, ns, name}
		if other, dup := seen[id]; dup {
			if group != "" {
				kind += "." + group
			}
			return fmt.Errorf("%s: %s is the same object, %s %q in namespace %q: give each object its own name", o.from, other, kind, name, ns)
		}
		seen[id] = o.from
	}
	return nil
}
