// Package merge computes the final values of a compile. It merges OKDP's
// defaults, what the mapping computed and the instance's upstream values,
// as Helm merges -f files. It reads no upstream chart: a host checks the
// result against them.
package merge

import (
	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

type (
	M             = value.M
	Ref           = chart.Ref
	Spec          = chart.Spec
	Specs         = chart.Specs
	UpstreamCheck = chart.UpstreamCheck
)
