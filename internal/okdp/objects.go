// Own charts: the charts the compiler adds beside the dependencies of
// Chart.yaml, those of the OAuth clients (oidc.go), their keys reserved and
// their charts added in one place (addOwnCharts), and the platform's charts
// (global.okdp.charts) they are rendered with.

package okdp

import (
	"fmt"
	"maps"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// platformCharts: the chart the platform renders the compiler's own charts
// with (global.okdp.charts of okdp, of the form platform.schema.json gives
// it: its keys, their types; another key is refused there, a misspelled
// oidcDcr would otherwise leave the default in place): oidcDcr, the chart
// of the OAuth clients in dcr mode, DcrSource by default.
func platformCharts(okdp M) (chart.Ref, error) {
	charts := value.AsMap(okdp["charts"])
	if charts["oidcDcr"] == nil {
		return DcrSource, nil
	}
	return platformChart(charts["oidcDcr"], "global.okdp.charts.oidcDcr")
}

// platformChart: a chart of global.okdp.charts, a map of strings
// (platform.schema.json), checked as a source is (chart.Ref.Check); where
// names it in the errors.
func platformChart(v any, where string) (chart.Ref, error) {
	m := value.AsMap(v)
	r := chart.Ref{Repository: value.Str(m["repository"]), Chart: value.Str(m["chart"]), Version: value.Str(m["version"])}
	return r, r.Check(where)
}

// ownChart: a chart key the compiler owns in a compile: the chart of each
// OAuth client, in every mode. Neither Chart.yaml nor the values file may
// list it (addOwnCharts). The compiler adds it to the compile when it has
// values, rendered by source: no OKDP defaults, no instance upstream
// values.
type ownChart struct {
	key    string
	of     string    // what it is the chart of, for the messages: oidcClients[<i>] (<name>)
	from   string    // what its values are compiled from, for the messages
	source chart.Ref // the platform's chart that renders it
	values M         // its values; nil: the key is reserved, no chart is added (a client outside dcr mode)
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
