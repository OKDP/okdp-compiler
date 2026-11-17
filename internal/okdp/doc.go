// Package okdp compiles an OKDP service instance into the values of its
// upstream charts. A YAML + CEL mapping computes them (internal/compile,
// over github.com/okdp/cel2yaml). The compile also adds the OKDP objects
// of the instance, and returns what the instance publishes: its URL, its
// usage and the connections it provides.
//
// The package holds OKDP's rules: the package read from a filesystem
// (LoadDefinition), the platform context derived from global.okdp, the
// connections and their contracts, the outputs, the OAuth clients, and
// the order of the stages (Compile). The values are read, merged and
// checked against the charts' schemas as Helm does (internal/chart).
//
// # Packages
//
// A package is a Helm chart (Chart.yaml) plus okdp.yaml (the package
// keys, and the vars, errors and OKDP sections of the mapping) and a
// values file (the charts' values, the mapping's output). Each
// dependency of Chart.yaml is an upstream chart, which renders the
// values under its alias or its name.
//
// A compile reads no chart: Helm coalesces the final values over the
// chart's defaults when it renders it. A check does read the chart's
// defaults and schema, from the chart cache (Inputs.Charts).
//
// # Claims
//
// A package that provisions connections (provisions in okdp.yaml) gets
// the claims bound to its instance (Inputs.Claims) and publishes an
// output for each. The instance that claims one gets its binding
// (Inputs.Bindings). CompileAll (namespace.go) binds each claim to its
// provider, then compiles each instance of a tree after the instances
// it references and the providers of its claims.
//
// # Upstream values
//
// An instance may set any value of a chart that the upstream key of
// okdp.yaml accepts, under upstream.<chart> of its values. The compiler
// merges them over the computed values, refusing the protect paths and
// appending to the append lists. In a path, brackets name a key that
// contains dots, and a final glob names a family of keys:
//
//	annotations["nginx.ingress.kubernetes.io/auth-*"]
package okdp
