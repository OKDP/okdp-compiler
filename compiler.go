// Package compiler compiles an OKDP service instance into the values of its
// upstream charts. A package (service.yaml, values.cel.yaml, its parameters
// and OKDP's defaults for its charts) is read from any fs.FS
// (LoadDefinition), compiled for one instance (Compile), and written as the
// files of okdp-compile (Write):
//
//	def, err := compiler.LoadDefinition(os.DirFS("packages/myservice"))
//	res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
//		Charts:    os.DirFS("charts"),
//		Contracts: os.DirFS("contracts"),
//		Platform:  []map[string]any{platformValues},
//		Values:    []map[string]any{instanceValues},
//		Release:   "demo-myservice",
//		Namespace: "demo",
//	})
//	files, err := compiler.Write(res, compiler.Output{Dependencies: true}) // -o <dir> --mkdag
//
// The compile runs a YAML + CEL mapping (internal/compile) and adds the OKDP
// objects of the instance (its descriptor ConfigMap, the credentials Secrets
// of the connections it provides, and the objects section of the mapping).
// This package holds OKDP's rules: the platform context derived from
// global.okdp, the connections and their contracts, the descriptor, the
// OAuth clients, the okdp chart; the order of the stages (Compile); and the
// edges, the package read from a filesystem (load.go) and the files a
// compile is written as (output.go).
//
// The values are read, merged and checked against the charts' schemas as
// Helm does (internal/chart; testdata/helm holds what `helm template`
// computes on the same inputs), and the mapping's host functions reproduce
// Helm and okdp-lib-chart functions.
//
// Each chart of service.yaml names the upstream chart that renders its values
// (ChartSpec.Source: repository, chart, version), and Result.Charts maps
// every compiled chart to it: a deployer renders the upstream chart from its
// repository. The compiler reads only its defaults and schema, from the chart
// cache (Inputs.Charts); one upstream chart may render several charts of
// service.yaml, each with its own values.
//
// A mapping's oidcClients section declares the service's OAuth clients
// (oidc.go): when the platform registers them by dynamic client
// registration, the compiler adds one chart per client, rendered by the
// oidc-dcr chart (oidc-dcr for the main client, <name>-oidc-dcr for a named
// one), which service.yaml does not list; their values come from a template
// in the mapping language embedded in the compiler (oidc-dcr.cel.yaml).
//
// An instance may set any value of a chart whose service.yaml entry accepts
// it (upstream), under upstream.<chart> of its values. The mapping sees them
// (values.upstream.<chart>), so that a rule of its errors section covers
// them; the compiler then merges them over the computed values, refusing
// the protect paths and appending to the append lists. A path names a key
// that contains dots in brackets, and a family of keys with a final glob:
// annotations["nginx.ingress.kubernetes.io/auth-*"].
//
// Everything is read through fs.FS and in-memory values, so a caller (a
// server, a CLI) can compile from a checkout, an embedded filesystem or a
// git tree without touching the disk.
package compiler

import "github.com/okdp/okdp-compiler/internal/chart"

// The charts of service.yaml.
type (
	// ChartSpec: a chart of service.yaml, by key.
	ChartSpec = chart.Spec
	// ChartRef: an upstream chart, pinned: what renders the values of a key.
	ChartRef = chart.Ref
)

// ParseValues: a values file as Helm reads it (YAML 1.1, every document of
// the file merged in order, string keys, every number a float64), for
// Inputs.Platform, Inputs.Project and Inputs.Values.
func ParseValues(b []byte) (map[string]any, error) {
	return chart.ParseValues(b)
}
