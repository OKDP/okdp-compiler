// Own charts: the charts the compiler adds beside the dependencies of
// Chart.yaml, those a section of the rule set adds and the instance's
// objects', their keys reserved and added in one place (addOwnCharts), and
// the platform's charts (global.okdp.charts) they are rendered with.

package okdp

import (
	"fmt"
	"maps"
	"slices"

	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// ObjectsChart: the chart key of the instance's objects, in the umbrella,
// reserved in every compile; ObjectsValue: its value that holds them, one
// YAML stream.
const (
	ObjectsChart = "okdp-objects"
	ObjectsValue = "manifests"
)

// platformCharts: the charts the platform renders the compiler's own charts
// with: each of the rule set's, unless global.okdp.charts of okdp, of the
// form the platform schema gives it, sets it. A misspelled key is refused
// there: it would otherwise leave the default in place.
func platformCharts(rules *RuleSet, okdp M) (map[string]chart.Ref, error) {
	given := value.AsMap(okdp["charts"])
	out := map[string]chart.Ref{}
	for _, name := range slices.Sorted(maps.Keys(rules.charts)) {
		out[name] = rules.charts[name]
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

// ownCharts: the charts the sections of the mapping's result res add (the
// rule set's ownCharts): each section's checked, then the values of those
// it renders, in the compile's session; the source of each from sources.
func ownCharts(session *cel2yaml.Session, inst *instance, res M, sources map[string]chart.Ref) ([]ownChart, error) {
	var own []ownChart
	for _, spec := range inst.rules.ownCharts {
		items := []any{}
		items = append(items, value.AsList(res[spec.section])...)
		listed, err := inst.rules.values(spec.list, M{"items": items, "release": inst.scope["release"], "platform": inst.scope["platform"]})
		if err != nil {
			return nil, err
		}
		for _, v := range value.AsList(listed["charts"]) {
			c := value.AsMap(v)
			o := ownChart{key: value.Str(c["key"]), of: value.Str(c["of"]), from: value.Str(c["from"]), source: sources[spec.chart]}
			if value.Truthy(c["render"]) {
				scope := maps.Clone(inst.scope)
				scope["client"], scope["names"] = c["client"], c["names"]
				if o.values, err = evalTemplate(session, spec.values, scope); err != nil {
					return nil, fmt.Errorf("%s: %w", o.of, err)
				}
			}
			own = append(own, o)
		}
	}
	return own, nil
}

// platformChart: a chart of global.okdp.charts, a map of strings
// (platform.schema.json), checked as a source is (chart.Ref.Check); where
// names it in the errors.
func platformChart(v any, where string) (chart.Ref, error) {
	m := value.AsMap(v)
	r := chart.Ref{Repository: value.Str(m["repository"]), Chart: value.Str(m["chart"]), Version: value.Str(m["version"])}
	return r, r.Check(where)
}

// ownChart: a chart key the compiler owns in a compile: one a section of
// the rule set adds, and the instance's objects'. Neither
// Chart.yaml nor the values file may list it (addOwnCharts). The compiler
// adds it to the compile when it has values, rendered by source: no OKDP
// defaults, no instance upstream values.
type ownChart struct {
	key    string
	of     string    // what it is the chart of, for the messages
	from   string    // what its values are compiled from, for the messages
	source chart.Ref // the platform's chart that renders it
	values M         // its values; nil: the key is reserved, no chart is added
}

// listed: the error of the chart c listed by Chart.yaml (deps, its
// dependencies), then by the mapping's output (file, the values file);
// nil when neither lists it.
func (c ownChart) listed(deps []chart.Dependency, file string, output M) error {
	for i, d := range deps {
		if d.Key() == c.key {
			return fmt.Errorf("%s: dependencies[%d] (%s): the chart of %s is added by the compiler: remove it", chart.MetadataFile, i, c.key, c.of)
		}
	}
	if _, ok := output[c.key]; ok {
		return fmt.Errorf("%s: %s: the chart of %s is compiled from %s: remove it", file, c.key, c.of, c.from)
	}
	return nil
}

// addOwnCharts: the dependencies of Chart.yaml, deps, specs by key, plus
// the charts of own that have values, each rendered by its source, their
// values added to the mapping's output, read from the values file file:
// what chart.FinalValues compiles. A chart of own that Chart.yaml or the
// output lists fails (listed), the first in the order of own. specs is not
// changed.
func addOwnCharts(deps []chart.Dependency, specs chart.Specs, file string, output M, own []ownChart) (chart.Specs, error) {
	for _, c := range own {
		if err := c.listed(deps, file, output); err != nil {
			return nil, err
		}
	}
	all := chart.Specs{}
	maps.Copy(all, specs)
	for _, c := range own {
		if c.values != nil {
			all[c.key] = chart.Spec{Source: c.source}
			output[c.key] = c.values
		}
	}
	return all, nil
}
