# okdp-compiler

Compiles an OKDP service instance into the values of its upstream charts
with a YAML + CEL mapping. A service is a package, a directory in git, not a
chart: its upstream charts are rendered as published, from their own
repositories, with the values the compiler writes.

Two ways to use it:

- **as a Go library** (`github.com/okdp/okdp-compiler/compiler`): no binary to call,
  everything is read through `fs.FS` and in-memory values;
- **as a binary** (`okdp-compile`), for CI and/or scripts.

## Library

```go
import "github.com/okdp/okdp-compiler/compiler"

def, err := compiler.LoadDefinition(packageFS) // service.yaml, mapping.cel.yaml, values.yaml, values.schema.json, base/
res, err := compiler.Compile(def, compiler.Inputs{
	Charts:    chartsFS,    // chart cache: each upstream chart, <chart>-<version>.tgz (helm pull) or <chart>-<version>/
	Instance:  instanceFS,  // optional: the instance's upstream values files (upstream.<chart>.yaml)
	Contracts: contractsFS, // e.g. contracts.FS of github.com/okdp/okdp-lib-chart/contracts
	Values:    []map[string]any{platformValues, instanceValues}, // layers, in order (helm -f); compiler.ParseValues reads one
	Release:   "demo-trino",
	Namespace: "demo",
})
// res.Values:   chart -> final values of the upstream chart (helm template <res.Charts[chart]> -f them)
// res.Charts:   chart -> the upstream chart that renders them (ChartRef: repository, chart, version):
//               its service.yaml source, the platform's objects chart for okdp,
//               the platform's oidc-dcr chart for the OAuth clients (oidcClients)
// res.Computed: chart -> what the mapping computed, before the merges
// res.Objects:  OKDP's own objects: the descriptor ConfigMap, the credentials
//               Secrets of the provided connections, the mapping's objects section
// res.Dependencies: instances the connection references point to (Flux dependsOn, Argo sync waves)
```

`Compile` keeps no state between calls and is safe for concurrent use. Any
`fs.FS` works: `os.DirFS`, `embed.FS`, a git tree, `fstest.MapFS` (see
`compiler/compile_test.go`).

What Helm does, the compiler does the same way, checked against `helm
template` (`testdata/helm`): every values file is read as Helm
reads it (YAML 1.1), the layers are merged and coalesced as Helm does, the
parameters and the final values are checked against the charts'
`values.schema.json`, and `res.Values[chart]`, rendered by Helm, gives what
Helm gives with the base values, the computed values and the instance's
upstream values as `-f` files
([Helm fidelity](docs/00_architecture.md#helm-fidelity)).

A chart of `service.yaml` is a key and its `source`, the published chart
that renders its values: `{repository, chart, version}`, pinned. Two keys
may name one source, for a chart rendered more than once
([sources](docs/specs/02_service_definition.md#sources)). The compiler reads
only the chart's `values.yaml` and `values.schema.json`, from a chart cache
the caller fills with `helm pull`
([chart cache](docs/specs/01_inputs_outputs.md#chart-cache)). `res.Charts`
maps every compiled key to the upstream chart that renders it
([chart index](docs/specs/01_inputs_outputs.md#chart-index)).

Every compile adds the chart `okdp` (`compiler.ObjectsChart`), its values
`res.Objects`: the descriptor and the generated Secrets reach the cluster
with the upstream charts, rendered by the platform's objects chart,
`global.okdp.charts.objects` (okdp-objects)
([the okdp chart](docs/specs/01_inputs_outputs.md#the-okdp-chart),
[platform charts](docs/specs/01_inputs_outputs.md#platform-charts)).

## Mapping

`mapping.cel.yaml` is YAML shaped like the upstream values, with `${{ CEL }}`
expressions and `$if`/`$for`/`$merge`/`$spread`/`$let` directives. Sections:
`let`, `errors`, `output` (chart -> values), `instance` (url, usage),
`provides` (the connections the instance publishes), `objects` (OKDP's own
objects, whole manifests) and `oidcClients` (the service's OAuth clients).
Expressions see `release`, `values`, `global.okdp`, `platform`, `service`,
`labels` and `selectorLabels`, and call host functions, Helm's and OKDP's
(`str`, `sha256`, `toJson`, `okdpFullname`, `okdpGeneratedSecret`,
`oidcClientSecret`...):
[service definition](docs/specs/02_service_definition.md).

The OAuth clients are provisioned by the platform
(`global.okdp.oidc.clientProvisioning: existing`, the default) or registered
by each service (`dcr`): in `dcr` mode, OIDC enabled, the compiler adds one
chart per client, rendered by the oidc-dcr chart (`global.okdp.charts.oidcDcr`,
oidc-dcr 0.5.0 by default), its values computed by an embedded template in
the mapping language, [`compiler/oidc-dcr.cel.yaml`](compiler/oidc-dcr.cel.yaml)
([oidcClients](docs/specs/02_service_definition.md#oidcclients),
[their charts](docs/specs/01_inputs_outputs.md#oauth-client-charts),
[their platform settings](docs/specs/01_inputs_outputs.md#oauth-client-settings)).

## Binary

```bash
make build    # bin/okdp-compile
export OKDP_CONTRACTS=example/contracts   # or --contracts
export OKDP_CHARTS=example/charts         # or --charts
bin/okdp-compile --package example/webapp \
  -f example/platform-values.yaml -f example/values/internal-db.yaml --out /dev/stdout
bin/okdp-compile --package example/webapp \
  -f example/platform-values.yaml -f example/values/internal-db.yaml -f testdata/protected-upstream.yaml
# error: app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed
```

`--emit-dir <dir>` writes the final values of each chart,
`<dir>/<chart>.values.yaml`, and their index, `<dir>/okdp.charts.yaml`:
render each with `helm template <release> <chart> --repo <repository>
--version <version> -f <dir>/<chart>.values.yaml`, the chart named by the
index ([rendering with Helm](docs/01_usage.md#rendering-with-helm)). `--out
<file>` writes the computed values, the index, the objects and the
dependencies ([command line](docs/01_usage.md#command-line)).

## Layout

| Path | Description |
|---|---|
| `compiler/` | the library; what each file owns: [source map](docs/00_architecture.md#source-map) |
| `cmd/okdp-compile/main.go` | binary entrypoint; `--emit-dir` writes the values files and their index |
| `example/` | a complete example: the package `webapp/`, a chart cache `charts/`, the platform and instance values, and the compiled output, checked by `go test` ([example](docs/00_architecture.md#example)) |
| `testdata/` | instance values that must fail on the example, each with its expected error on the first line ([failure cases](docs/00_architecture.md#failure-cases)) |
| `testdata/helm/` | what `helm template` computes on the same inputs, regenerated by `generate.sh` ([Helm fixtures](docs/00_architecture.md#helm-fixtures)) |
| `docs/` | the documentation |

## Documentation

[docs/README.md](docs/README.md) indexes the documentation:

- [Usage](docs/01_usage.md): install, the command line, the Go library, CI and
  GitOps, troubleshooting;
- [Writing a package](docs/02_writing_package.md): a package, its upstream
  charts and its service definition, step by step;
- [Service definition](docs/specs/02_service_definition.md) and
  [Inputs and outputs](docs/specs/01_inputs_outputs.md): the
  reference;
- [Architecture](docs/00_architecture.md): the compile pipeline, the design
  decisions, the tests.
