// Package okdp compiles an OKDP service instance into the values of its
// upstream charts with a YAML + CEL mapping (internal/compile), and adds the
// OKDP objects of the instance (the credentials Secrets of the connections
// it provides, and the objects section of the mapping) and what it
// publishes (its URL, its usage, the connections it provides). It holds
// OKDP's rules: the package read from a filesystem (LoadDefinition), the
// platform context derived from global.okdp, the connections and their
// contracts, the outputs, the OAuth clients; and the order of the stages
// (Compile).
//
// The values are read, merged and checked against the charts' schemas as
// Helm does (internal/chart; testdata/helm holds what `helm template`
// computes on the same inputs), and the mapping's host functions reproduce
// Helm's.
//
// A package that provisions connections (okdp.yaml's provisions) gets the
// claims bound to its instance (Inputs.Claims) and publishes an output for
// each; the instance that claims one (a reference that takes claims) gets its
// binding (Inputs.Bindings). CompileAll (namespace.go) binds each claim to
// its provider, the one it names or the provider of its class, the default
// class of the context's global.okdp.provisioning unless it names one, and
// compiles the instances of a tree together, each after the instances it
// references and the providers of its claims, with what they publish.
//
// A package is a Helm chart, Chart.yaml, plus OKDP's files: okdp.yaml (its
// package keys and the mapping's vars, errors and OKDP sections) and a
// values file, values.cel.yaml or values.yaml (the charts' values, the
// mapping's output). Each dependency of Chart.yaml is an upstream chart
// that renders the values of its key, its alias or its name
// (chart.Spec.Source: repository, chart, version), and Result.Charts maps
// every compiled chart to it. A compile reads no chart: Helm coalesces the
// final values over the chart's defaults when it renders it. A check reads
// its defaults and schema, from the chart cache (Inputs.Charts). One
// upstream chart may render several dependencies, each under an alias with
// its own values.
//
// A mapping's oidcClients section declares the service's OAuth clients
// (oidc.go): when the platform registers them by dynamic client
// registration, the compiler adds one chart per client, rendered by the
// oidc-dcr chart (oidc-dcr for the main client, <name>-oidc-dcr for a named
// one), which Chart.yaml does not list; their values come from a template
// in the mapping language embedded in the compiler (oidc-dcr.cel.yaml).
//
// An instance may set any value of a chart that okdp.yaml's upstream
// accepts, under upstream.<chart> of its values. The mapping sees them
// (values.upstream.<chart>), so that a rule of its errors section covers
// them; the compiler then merges them over the computed values, refusing
// the protect paths and appending to the append lists. A path names a key
// that contains dots in brackets, and a family of keys with a final glob:
// annotations["nginx.ingress.kubernetes.io/auth-*"].
package okdp
