// Objects: the okdp chart, the chart of OKDP's own objects the compiler
// adds to every compile; the compiler's own charts, the okdp chart and the
// OAuth clients' (oidc.go), their keys reserved and their charts added in
// one place (addOwnCharts), and the platform's charts (global.okdp.charts)
// they are rendered with.

package okdp

import (
	"errors"
	"fmt"
	"maps"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// ObjectsChart: the chart key of OKDP's own objects. The compiler adds it to
// every compile, rendered by the platform's objects chart
// (global.okdp.charts.objects: okdp-objects, values `objects: []`), its
// objects Result.Objects: the descriptor ConfigMap, the credentials Secrets
// and the objects section reach the cluster with the upstream charts.
// Neither Chart.yaml nor the values file may list it.
const ObjectsChart = "okdp"

// platformCharts: the charts the platform renders the compiler's own charts
// with (global.okdp.charts of okdp, of the form platform.schema.json gives
// it: its keys, their types; another key is refused there, a misspelled
// oidcDcr would otherwise leave the default in place): objects, the chart
// of OKDP's own objects (okdp-objects, required), and oidcDcr, the chart of
// the OAuth clients in dcr mode (DcrSource by default).
func platformCharts(okdp M) (objects, dcr chart.Ref, err error) {
	charts := value.AsMap(okdp["charts"])
	if charts["objects"] == nil {
		return objects, dcr, errors.New("global.okdp.charts.objects is required: the chart OKDP's own objects are rendered with (okdp-objects: repository, chart, version). " + platformValue)
	}
	if objects, err = platformChart(charts["objects"], "global.okdp.charts.objects"); err != nil {
		return objects, dcr, err
	}
	dcr = DcrSource
	if charts["oidcDcr"] != nil {
		dcr, err = platformChart(charts["oidcDcr"], "global.okdp.charts.oidcDcr")
	}
	return objects, dcr, err
}

// platformChart: a chart of global.okdp.charts, a map of strings
// (platform.schema.json), checked as a source is (chart.Ref.Check); where
// names it in the errors.
func platformChart(v any, where string) (chart.Ref, error) {
	m := value.AsMap(v)
	r := chart.Ref{Repository: value.Str(m["repository"]), Chart: value.Str(m["chart"]), Version: value.Str(m["version"])}
	return r, r.Check(where)
}

// ownChart: a chart key the compiler owns in a compile: the okdp chart,
// and the chart of each OAuth client, in every mode. Neither Chart.yaml
// nor the values file may list it (addOwnCharts). The compiler adds it to
// the compile when it has values, rendered by source: no OKDP defaults, no
// instance upstream values.
type ownChart struct {
	key    string
	of     string    // what it is the chart of, for the messages: OKDP's own objects, oidcClients[<i>] (<name>)
	from   string    // what its values are compiled from, for the messages
	source chart.Ref // the platform's chart that renders it
	values M         // its values; nil: the key is reserved, no chart is added (a client outside dcr mode)
}

// okdpChart: the okdp chart (ObjectsChart) as the compiler owns it, before
// a compile gives its source and values (objectsChart); Chart.yaml may not
// list it, refused when it is loaded (Definition.service).
var okdpChart = ownChart{key: ObjectsChart, of: "OKDP's own objects", from: "the objects section"}

// objectsChart: the okdp chart, rendered by the platform's objects chart
// src, its values the objects, copied.
func objectsChart(objects []map[string]any, src chart.Ref) ownChart {
	list := make([]any, 0, len(objects))
	for _, o := range objects {
		list = append(list, value.Copy(o))
	}
	c := okdpChart
	c.source, c.values = src, M{"objects": list}
	return c
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
