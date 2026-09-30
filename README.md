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
// res.Values:   chart -> final values of the upstream chart (helm template vendor/<res.Charts[chart]> -f them)
// res.Charts:   chart -> the vendored chart that renders them: the chart itself, its
//               service.yaml alias (chart:), oidc-dcr for the OAuth clients (oidcClients)
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
template` (`testdata/helm`): every values file is read as Helm
reads it (YAML 1.1), the layers are merged and coalesced as Helm does, the
parameters and the final values are checked against the charts'
`values.schema.json`, and `res.Values[chart]`, rendered by Helm, gives what
Helm gives with the base values, the computed values and the instance's
upstream values as `-f` files
([Helm fidelity](docs/00_architecture.md#helm-fidelity)).

When `service.yaml` declares a chart named `okdp` (`compiler.ObjectsChart`,
the okdp-objects chart), the compiler appends `res.Objects` to its values and
compiles it even when the mapping leaves it out: the descriptor and the
generated Secrets reach the cluster with the upstream charts
([the okdp chart](docs/specs/01_inputs_outputs.md#the-okdp-chart)).

A chart of `service.yaml` is a key, and by default the vendored chart of
the same name (`vendor/<key>`); `chart:` names another one, for a chart
rendered more than once ([aliases](docs/specs/02_service_definition.md#aliases)).
`res.Charts` maps every compiled key to the vendored chart that renders it
([chart index](docs/specs/01_inputs_outputs.md#chart-index)).

## Mapping

`mapping.cel.yaml` is YAML shaped like the upstream values, with `${{ CEL }}`
expressions and `$if`/`$for`/`$merge`/`$spread`/`$let` directives. Sections:
`let`, `errors`, `output` (chart -> values), `instance` (url, usage),
`provides` (the connections the instance publishes), `objects` (OKDP's own
objects, whole manifests) and `oidcClients` (the service's OAuth clients).
Expressions see `release`, `values`, `global.okdp`, `platform`, `chart`,
`labels` and `selectorLabels`, and call host functions, Helm's and OKDP's
(`str`, `sha256`, `toJson`, `okdpFullname`, `okdpGeneratedSecret`,
`oidcClientSecret`...):
[service definition](docs/specs/02_service_definition.md).

The OAuth clients are provisioned by the platform
(`global.okdp.oidc.clientProvisioning: existing`, the default) or registered
by each service (`dcr`): in `dcr` mode, OIDC enabled, the compiler adds one oidc-dcr chart
per client, its values computed by an embedded template in the mapping
language, [`compiler/oidc-dcr.cel.yaml`](compiler/oidc-dcr.cel.yaml)
([oidcClients](docs/specs/02_service_definition.md#oidcclients),
[their charts](docs/specs/01_inputs_outputs.md#oauth-client-charts),
[their platform settings](docs/specs/01_inputs_outputs.md#oauth-client-settings)).

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

`--emit-dir <dir>` writes the final values of each chart,
`<dir>/<chart>.values.yaml`, and their index, `<dir>/okdp.charts.yaml`:
render each with `helm template <release> vendor/<index[chart]> -f
<dir>/<chart>.values.yaml`. `--out <file>` writes the computed values, the
index, the objects and the dependencies ([command line](docs/01_usage.md#command-line)).

## Layout

| Path | Description |
|---|---|
| `compiler/` | the library; what each file owns: [source map](docs/00_architecture.md#source-map) |
| `cmd/okdp-compile/main.go` | binary entrypoint; `--emit-dir` writes the values files and their index |
| `example/` | a complete example service, `webapp`, and its compiled output, checked by `go test` ([example](docs/00_architecture.md#example)) |
| `testdata/` | instance values that must fail on the example, each with its expected error on the first line ([failure cases](docs/00_architecture.md#failure-cases)) |
| `testdata/helm/` | what `helm template` computes on the same inputs, regenerated by `generate.sh` ([Helm fixtures](docs/00_architecture.md#helm-fixtures)) |
| `docs/` | the documentation |

## Documentation

[docs/README.md](docs/README.md) indexes the documentation:

- [Usage](docs/01_usage.md): install, the command line, the Go library, CI and
  GitOps, troubleshooting;
- [Writing a package](docs/02_writing_package.md): a wrapper chart and its
  service definition, step by step;
- [Service definition](docs/specs/02_service_definition.md) and
  [Inputs and outputs](docs/specs/01_inputs_outputs.md): the
  reference;
- [Architecture](docs/00_architecture.md): the compile pipeline, the design
  decisions, the tests.
