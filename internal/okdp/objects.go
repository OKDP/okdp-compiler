// What the compiler adds to an instance beside the dependencies of
// Chart.yaml: the objects a section of the rule set adds (ownObjects), and
// the chart that renders the instance's objects, its key reserved
// (checkObjectsChart), of the platform's charts (global.okdp.charts).

package okdp

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"slices"

	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
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

// ownObjects: the objects the sections of the mapping's result res add (the
// rule set's ownObjects): each section's items checked, then the objects of
// those it renders, in the compile's session. The URLs an item names are
// served by the project first (checkItemURLs).
func ownObjects(ctx context.Context, session *cel2yaml.Session, inst *instance, res M) ([]any, error) {
	var objects []any
	for _, spec := range inst.rules.OwnObjects() {
		items := []any{}
		items = append(items, value.AsList(res[spec.Section])...)
		listed, err := inst.rules.Values(ctx, spec.List, M{"items": items, "release": inst.scope["release"], "platform": inst.scope["platform"]})
		if err != nil {
			return nil, err
		}
		for _, v := range value.AsList(listed["items"]) {
			c := value.AsMap(v)
			if !value.Truthy(c["render"]) {
				continue
			}
			of := value.Str(c["of"])
			if err := checkItemURLs(ctx, inst, of, value.AsList(c["urls"])); err != nil {
				return nil, err
			}
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

// checkItemURLs: the URLs an item of a section registers with an identity
// provider, of an instance that is not privileged: each http or https, with
// a host and no user information (url.Parse takes the host from what a
// browser reads), a host the project serves (hostRules): a redirect to
// another's host hands it the authorization code. of names the item.
func checkItemURLs(ctx context.Context, inst *instance, of string, urls []any) error {
	if privileged(inst.okdp, inst.namespace) {
		return nil
	}
	hosts := newHostRules(inst.rules, inst.okdp, inst.namespace)
	for i, v := range urls {
		raw, at := value.Str(v), fmt.Sprintf("%s: redirectUris[%d]", of, i)
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			return fmt.Errorf("%s: %q: an http or https URL with a host and no user information", at, raw)
		}
		if err := hosts.check(ctx, at, u.Hostname()); err != nil {
			return err
		}
	}
	return nil
}

// platformChart: a chart of global.okdp.charts, a map of strings
// (platform.schema.json), checked as a source is (chart.Ref.Check); where
// names it in the errors.
func platformChart(v any, where string) (chart.Ref, error) {
	m := value.AsMap(v)
	r := chart.Ref{Repository: value.Str(m["repository"]), Chart: value.Str(m["chart"]), Version: value.Str(m["version"])}
	return r, r.Check(where)
}

// checkObjectsChart: the key of the chart of the instance's objects is the
// compiler's: neither Chart.yaml (deps, its dependencies) nor the mapping's
// output (file, the values file) may list it. The writer of the umbrella
// adds the chart.
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
