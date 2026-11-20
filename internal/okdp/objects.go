// What the compiler adds to an instance besides the dependencies of
// Chart.yaml: the objects of the rule set's sections, and the chart that
// renders the instance's objects, whose key is reserved.

package okdp

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// ObjectsChart is the key of the chart of the instance's objects in the
// umbrella. Every compile reserves it. ObjectsValue is the value of that
// chart that holds the objects, as one YAML stream.
const (
	ObjectsChart = "okdp-objects"
	ObjectsValue = "manifests"
)

// platformCharts returns the charts the platform renders the compiler's
// own charts with: each chart of the rule set, or the one that
// global.okdp.charts of okdp sets under the same name.
func platformCharts(rules *RuleSet, okdp M) (map[string]chart.Ref, error) {
	given := value.AsMap(okdp["charts"])
	out := map[string]chart.Ref{}
	for _, name := range slices.Sorted(maps.Keys(rules.Charts())) {
		out[name] = rules.Charts()[name]
		if given[name] != nil {
			r, err := platformChart(given[name], "global.okdp.charts."+name)
			if err != nil {
				return nil, err
			}
			out[name] = r
		}
	}
	return out, nil
}

// ownObjects returns the objects that the sections of the mapping's
// result res add (the rule set's ownObjects). The list document reads the
// section's items, the release, platform, privileged and settings; its
// first message refuses.
func ownObjects(ctx context.Context, session *cel2yaml.Session, inst *instance, res M) ([]any, error) {
	var objects []any
	for _, spec := range inst.rules.OwnObjects() {
		items := []any{}
		items = append(items, value.AsList(res[spec.Section])...)
		listed, err := inst.rules.Values(ctx, spec.List, M{
			"items": items, "release": inst.scope["release"], "platform": inst.scope["platform"],
			"privileged": privileged(inst.okdp, inst.namespace), "settings": settingsOr(inst.okdp),
		})
		if err != nil {
			return nil, err
		}
		for _, v := range value.AsList(listed["items"]) {
			c := value.AsMap(v)
			if !value.Truthy(c["render"]) {
				continue
			}
			of := value.Str(c["of"])
			scope := maps.Clone(inst.scope)
			scope["client"], scope["names"] = c["client"], c["names"]
			out, err := evalTemplate(ctx, session, spec.Objects, scope)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", of, err)
			}
			objects = append(objects, value.AsList(out["objects"])...)
		}
	}
	return objects, nil
}

// platformChart reads and checks a chart of global.okdp.charts, found at
// where.
func platformChart(v any, where string) (chart.Ref, error) {
	m := value.AsMap(v)
	r := chart.Ref{Repository: value.Str(m["repository"]), Chart: value.Str(m["chart"]), Version: value.Str(m["version"])}
	return r, r.Check(where)
}

// checkObjectsChart reserves the key of the chart of the instance's
// objects: neither the dependencies of Chart.yaml nor the output of the
// mapping may list it. The writer of the umbrella adds the chart.
func checkObjectsChart(deps []chart.Dependency, file string, output M) error {
	for i, d := range deps {
		if d.Key() == ObjectsChart {
			return fmt.Errorf("%s: dependencies[%d] (%s): the chart of the instance's objects is added by the compiler: remove it", chart.MetadataFile, i, ObjectsChart)
		}
	}
	if _, ok := output[ObjectsChart]; ok {
		return fmt.Errorf("%s: %s: the chart of the instance's objects is compiled from the objects section: remove it", file, ObjectsChart)
	}
	return nil
}
