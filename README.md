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
	Contracts: contractsFS, // e.g. contracts.FS of github.com/okdp/okdp-lib-chart/contracts
	Values:    []map[string]any{instanceValues}, // layers, in order (helm -f); compiler.ParseValues reads one
	Release:   "demo-trino",
	Namespace: "demo",
})
// res.Values:   chart -> final values of the upstream chart
// res.Computed: chart -> what the mapping computed, before the merges
// res.Objects:  OKDP's own objects (the descriptor ConfigMap)
// res.Dependencies: instances the connection references point to (Flux dependsOn, Argo sync waves)
```

`Compile` keeps no state between calls and is safe for concurrent use. Any
`fs.FS` works: `os.DirFS`, `embed.FS`, a git tree, `fstest.MapFS` (see
`compiler/compile_test.go`). `compiler.Compare` checks a result against a
`helm template` output of the wrapper chart.

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
| `compiler/compile.go` | public API: `LoadDefinition`, `Compile`, `Inputs`, `Result` |
| `compiler/context.go` | what a mapping sees: the values layers, the platform requirements, the platform context derived from `global.okdp` |
| `compiler/connections.go` | connection references declared in the parameter schema (`x-okdp-connection-ref`), resolved to an external connection or an instance of the namespace and completed against their contract |
| `compiler/cel.go` | the mapping engine: YAML shaped like the upstream values, with `${{ CEL }}` expressions and `$if`/`$for`/`$merge`/`$spread`/`$let` directives |
| `compiler/dcr.go` | `okdpDcr`, the host function computing the oidc-dcr chart values of an OAuth client |
| `compiler/merge.go` | final values of each upstream chart: upstream defaults < fixed values < computed values < instance `upstream:` values |
| `compiler/descriptor.go` | the descriptor ConfigMap `<release>-okdp`, from the mapping's `instance` and `provides` sections, checked against the contracts |
| `compiler/values.go` | YAML/JSON read as Helm values (`ParseValues`) and Helm's merge rules |
| `compiler/compare.go` | `Compare`: a result against a `helm template` output of the wrapper chart |
| `cmd/okdp-compile/main.go` | binary entrypoint |
| `example/` | a complete example service, `webapp`: `service.yaml` and `mapping.cel.yaml` (the service definition), `webapp/` (its wrapper chart and vendored chart defaults), `contracts/`, `platform-values.yaml`, `values/` (instances) and `compiled/` (their compiled output, checked by `go test`; `go test ./compiler -update` rewrites it) |
| `testdata/` | instance values that must fail on the example, each with its expected error on the first line (`# error: ...`): malformed connections (wrong contract, unknown or secret field, no secretRef), mapping error, protected upstream path, missing platform value |
