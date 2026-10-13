// Package okdp compiles an OKDP service instance into the values of its
// upstream charts with a YAML + CEL mapping (internal/compile), and adds the
// OKDP objects of the instance (its descriptor ConfigMap, the credentials
// Secrets of the connections it provides, and the objects section of the
// mapping). It holds OKDP's rules: the package read from a filesystem
// (LoadDefinition), the platform context derived from global.okdp, the
// connections and their contracts, the descriptor, the OAuth clients, the
// okdp chart; and the order of the stages (Compile).
//
// The values are read, merged and checked against the charts' schemas as
// Helm does (internal/chart; testdata/helm holds what `helm template`
// computes on the same inputs), and the mapping's host functions reproduce
// Helm's.
//
// Each chart of service.yaml names the upstream chart that renders its values
// (chart.Spec.Source: repository, chart, version), and Result.Charts maps
// every compiled chart to it. The compiler reads only its defaults and
// schema, from the chart cache (Inputs.Charts); one upstream chart may
// render several charts of service.yaml, each with its own values.
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
package okdp
