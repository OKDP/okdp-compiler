// Package finaltest is test support: a copy of Helm's merge of the final
// values, which okdp-gitops holds (internal/helmvalues), for the tests of a
// compile to see final values. Only tests import it: the compiler ships no
// merge, a host gives it as Inputs.Finalize.
package finaltest

import (
	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

type (
	M             = value.M
	Ref           = chart.Ref
	Spec          = chart.Spec
	Specs         = chart.Specs
	Schema        = chart.Schema
	UpstreamCheck = chart.UpstreamCheck
)
