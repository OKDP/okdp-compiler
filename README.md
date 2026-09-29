# okdp-compiler

Compiles an OKDP service instance into the values of its upstream charts
with a YAML + CEL mapping.

Two ways to use it:

- **as a Go library** (`github.com/okdp/okdp-compiler/compiler`): no binary to call,
  everything is read through `fs.FS` and in-memory values;
- **as a binary** (`okdp-compile`), for CI and/or scripts.

## Library

```go
import "github.com/okdp/okdp-compiler/compiler"

def, err := compiler.LoadDefinition(serviceFS) // service.yaml, mapping.cel.yaml
res, err := compiler.Compile(def, compiler.Inputs{
	Chart:     chartFS,     // wrapper chart: Chart.yaml, values.yaml, values.schema.json, vendor/, vendor-values/
	Instance:  instanceFS,  // optional: the instance's upstream values files (upstream.<chart>.yaml)
	Contracts: contractsFS, // e.g. contracts.FS of github.com/okdp/okdp-lib-chart/contracts
	Values:    []map[string]any{instanceValues}, // layers, in order (helm -f); compiler.ParseValues reads one
	Release:   "demo-trino",
	Namespace: "demo",
})
// res.Values:   chart -> final values of the upstream chart (helm template vendor/<chart> -f them)
// res.Computed: chart -> what the mapping computed, before the merges
// res.Objects:  OKDP's own objects: the descriptor ConfigMap, the credentials
//               Secrets of the provided connections, the mapping's objects section
// res.Dependencies: instances the connection references point to (Flux dependsOn, Argo sync waves)
```

`Compile` keeps no state between calls and is safe for concurrent use. Any
`fs.FS` works: `os.DirFS`, `embed.FS`, a git tree, `fstest.MapFS` (see
`compiler/compile_test.go`). `compiler.Compare` checks a result against a
`helm template` output of the wrapper chart.

What Helm did, the compiler does the same way, checked against `helm
template` (`compiler/testdata/helm`):

- every values file is read as Helm reads it (`sigs.k8s.io/yaml`, YAML 1.1:
  `yes`, `no`, `on`, `off` are booleans; every document of a file), and the
  layers are merged and coalesced as Helm does: a `null` deletes a default,
  and stays where there is no default. The mapping's scalars and keys are
  read the same way (YAML 1.1), except the names of `let` and `$let`
  bindings;
- the merged parameters are checked against the wrapper's
  `values.schema.json` before the mapping runs, and the final values of each
  chart against `vendor/<chart>/values.schema.json` when it has one: every
  violation is reported with its path, as Helm reports it;
- `res.Values[chart]`, rendered by Helm, gives what Helm gives with the base
  values, the computed values and the instance's upstream values as `-f`
  files: a `null` that deletes a default stays `null`, the chart's `null`
  defaults are left out.

When `service.yaml` declares a chart named `okdp` (`compiler.ObjectsChart`,
the okdp-objects chart, values `objects: []`), the compiler appends
`res.Objects` to the objects the mapping computes for it
(`output.okdp.objects`, kept first) and compiles it even when the mapping
leaves it out: the descriptor and the generated Secrets reach the cluster
with the upstream charts.

## Mapping

`mapping.cel.yaml` is YAML shaped like the upstream values, with `${{ CEL }}`
expressions and `$if`/`$for`/`$merge`/`$spread`/`$let` directives (see
`compiler/cel.go`). Sections: `let`, `errors`, `output` (chart -> values),
`instance` (url, usage), `provides` (the connections the instance
publishes; `secret: {generate, stringData}` or `{stringData}` renders their
credentials Secret, `secretRef` defaulting to `<name>-<contract>-credentials`)
and `objects` (OKDP's own objects, whole manifests).

| Context | |
|---|---|
| `release` | `{name, namespace}` |
| `values` | the parameters, connection references resolved |
| `global.okdp` | the platform values, as written |
| `platform` | derived from them: `oidc`, `oidcClientSecret`, `ingressAnnotations`, `proxyEnv` |
| `chart` | `{name, version, appVersion}` of `Chart.yaml` |
| `labels`, `selectorLabels` | `okdp.labels`, `okdp.selectorLabels` (a `let` of the same name wins) |

| Function | As |
|---|---|
| `str(x)`, `first(list)` | Go templates' printing, Helm's `default` |
| `sha256(x)` | `sha256sum` of `str(x)` |
| `toJson(x)`, `toPrettyJson(x)`, `toYaml(x)`, `quote(x)` | Helm's |
| `okdpFullname(suffix)` | `okdp.fullname`: `<release>-<suffix>` cut to 63 characters |
| `okdpLabels(component)`, `okdpSelectorLabels(component)` | the labels with `app.kubernetes.io/component` |
| `okdpGeneratedSecret({name, keys, ...})` | the objects of `okdp.generatedSecret`, for `$spread` in `objects` |
| `okdpDcr({grantTypes, ...})` | the oidc-dcr chart values of an OAuth client (`okdp.vendor.oidcDcr`) |

## Binary

```bash
make build    # bin/okdp-compile
export OKDP_CONTRACTS=example/contracts   # or --contracts
bin/okdp-compile --chart example/webapp --service example \
  -f example/platform-values.yaml -f example/values/internal-db.yaml --out /dev/stdout
bin/okdp-compile --chart example/webapp --service example \
  -f example/platform-values.yaml -f example/values/internal-db.yaml -f testdata/protected-upstream.yaml
# error: app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed
```

## Layout

| Path | Description |
|---|---|
| `compiler/compile.go` | public API: `LoadDefinition`, `Compile`, `Inputs`, `Result`, `ObjectsChart` |
| `compiler/context.go` | what a mapping sees: the values layers (checked against the parameter schema), the platform requirements, the platform context derived from `global.okdp`, the chart and its labels |
| `compiler/connections.go` | connection references declared in the parameter schema (`x-okdp-connection-ref`), resolved to an external connection or an instance of the namespace and completed against their contract |
| `compiler/cel.go` | the mapping engine: YAML shaped like the upstream values (read as YAML 1.1), with `${{ CEL }}` expressions and `$if`/`$for`/`$merge`/`$spread`/`$let` directives |
| `compiler/functions.go` | the host functions `sha256`, `toJson`, `toPrettyJson`, `toYaml`, `quote`, `okdpFullname` |
| `compiler/labels.go` | `okdp.labels` and `okdp.selectorLabels`: the `chart`, `labels` and `selectorLabels` of the context, `okdpLabels`, `okdpSelectorLabels` |
| `compiler/secrets.go` | `okdpGeneratedSecret` (`okdp.generatedSecret`) and the credentials Secrets of `provides[].secret` |
| `compiler/dcr.go` | `okdpDcr`, the host function computing the oidc-dcr chart values of an OAuth client |
| `compiler/merge.go` | final values of each upstream chart: upstream defaults < base values < computed values < instance `upstream.<chart>.yaml` files < inline instance `upstream:` values, merged as Helm merges `-f` files |
| `compiler/schema.go` | values checked against a `values.schema.json` as Helm checks them |
| `compiler/descriptor.go` | the descriptor ConfigMap `<release>-okdp`, from the mapping's `instance` and `provides` sections, checked against the contracts |
| `compiler/values.go` | YAML/JSON read as Helm values (`ParseValues`), and Helm's merge and coalescing of values |
| `compiler/compare.go` | `Compare`: a result against a `helm template` output of the wrapper chart |
| `compiler/testdata/helm/` | what `helm template` computes on the same inputs (values, schemas, functions, labels, generated Secrets, descriptor), regenerated by `generate.sh` |
| `cmd/okdp-compile/main.go` | binary entrypoint |
| `example/` | a complete example service, `webapp`: `service.yaml` and `mapping.cel.yaml` (the service definition), `webapp/` (its wrapper chart and vendored chart defaults, the okdp chart included), `contracts/`, `platform-values.yaml`, `values/` (instances) and `compiled/` (their compiled output, checked by `go test`; `go test ./compiler -update` rewrites it) |
| `testdata/` | instance values that must fail on the example, each with its expected error on the first line (`# error: ...`, `\n` for a line break): malformed connections (wrong contract, unknown or secret field, no secretRef), mapping error, protected upstream path, missing platform value, parameter schema violation |
