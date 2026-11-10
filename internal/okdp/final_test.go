package okdp

import (
	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/finaltest"
)

// testFinalize: Inputs.Finalize of the tests: Helm's merge, which a host
// gives in a real compile (internal/finaltest).
func testFinalize(in FinalInput) (map[string]map[string]any, map[string]chart.Ref, []string, error) {
	var cache *finaltest.ChartCache
	if in.Charts != nil {
		cache = finaltest.NewChartCache(in.Charts)
	}
	return finaltest.FinalValues(cache, in.Files, in.Specs, in.ValuesFile, in.Output, in.Values, nil)
}
