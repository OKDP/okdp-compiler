# okdp-compiler

Compiles an OKDP service instance into the values of its upstream charts
with a YAML + CEL mapping. A service is a package, a directory in git, not a
chart: its upstream charts are rendered as published, from their own
repositories, with the values the compiler writes.

Two ways to use it:

- **as a Go library** (`github.com/okdp/okdp-compiler`, package `compiler`):
  no binary to call,
  everything is read through `fs.FS` and in-memory values;
- **as a binary** (`okdp-compile`), for CI and/or scripts.

## Library

```go
import compiler "github.com/okdp/okdp-compiler"

def, err := compiler.LoadDefinition(packageFS) // service.yaml, values.cel.yaml; each compile reads config.schema.json, <key>.defaults.yaml
if err != nil {
	log.Fatal(err)
}
res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
	Charts:    chartsFS,                         // chart cache: each upstream chart, <repository>/<chart>-<version>.tgz (helm pull) or <repository>/<chart>-<version>/
	Contracts: contractsFS,                      // the contract schemas, <contract>.schema.json
	Platform:  []map[string]any{platformValues}, // the platform's layers, in order (helm -f); compiler.ParseValues reads one
	Project:   []map[string]any{projectValues},  // the project's, after them: they add values, never change the platform's
	Values:    []map[string]any{instanceValues}, // the instance's, last: no global
	Release:   "demo-webapp",
	Namespace: "demo",
})
if err != nil {
	log.Fatal(err) // the whole report, e.g. every schema violation
}
// res.Values:   chart -> final values of the upstream chart (helm template <res.Charts[chart]> -f them)
// res.Charts:   chart -> the upstream chart that renders them (compiler.ChartRef: repository, chart, version):
//               its service.yaml source, the platform's objects chart for okdp,
//               the platform's oidc-dcr chart for the OAuth clients (oidcClients)
// res.Objects:  OKDP's own objects: the descriptor ConfigMap, the credentials
//               Secrets of the provided connections, the mapping's objects section
// res.Dependencies: instances the connection references point to (Flux dependsOn, Argo sync waves)

// What okdp-compile -o <dir> --mkdag writes, file name -> content
// (compiler.Output: --mkdag).
files, err := compiler.Write(res, compiler.Output{Dependencies: true})
if err != nil {
	log.Fatal(err)
}
```

`example_test.go` runs this program on the example (`Example`).
`Compile` keeps no state between calls and is safe for concurrent use. Any
`fs.FS` works: `os.DirFS`, `embed.FS`, a git tree, `fstest.MapFS` (see
`pipeline_test.go`).

What Helm does, the compiler does the same way, checked against `helm
template` (`testdata/helm`): every values file is read as Helm
reads it (YAML 1.1), the layers are merged and coalesced as Helm does, the
parameters are checked against the package's `config.schema.json` and the
final values against the charts' `values.schema.json`, and `res.Values[chart]`, rendered by Helm, gives what
Helm gives with OKDP's defaults, the computed values and the instance's
upstream values as `-f` files
([Helm fidelity](docs/00_architecture.md#helm-fidelity)).

A chart of `service.yaml` is a key and its `source`, the published chart
that renders its values: `{repository, chart, version}`, pinned. Two keys
may name one source, for a chart rendered more than once
([sources](docs/specs/02_service_definition.md#sources)). The compiler reads
only the chart's `values.yaml` and `values.schema.json`, from a chart cache
the caller fills with `helm pull`, each chart in its repository's
directory, the URL without its scheme (`quay.io/adaltas/oidc-dcr-0.5.0.tgz`)
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

`values.cel.yaml` is YAML shaped like the upstream values, with `${{ CEL }}`
expressions and `$if`/`$for`/`$merge`/`$spread`/`$let` directives. Sections:
`let`, `errors`, `output` (chart -> values), `instance` (url, usage),
`provides` (the connections the instance publishes), `objects` (OKDP's own
objects, whole manifests) and `oidcClients` (the service's OAuth clients).
Expressions see `release`, `values`, `global.okdp`, `platform`, `service`,
`labels` and `selectorLabels`, and call host functions, Helm's and OKDP's
(`str`, `sha256`, `toJson`, `okdpFullname`, `okdpGeneratedSecret`,
`oidcClientSecret`...):
[service definition](docs/specs/02_service_definition.md),
[control flow](docs/specs/03_control_flow.md),
[functions](docs/specs/04_functions.md).

The OAuth clients are provisioned by the platform
(`global.okdp.oidc.clientProvisioning: existing`, the default) or registered
by each service (`dcr`): in `dcr` mode, OIDC enabled, the compiler adds one
chart per client, rendered by the oidc-dcr chart
(`global.okdp.charts.oidcDcr`, oidc-dcr 0.5.0 by default), its values
computed by an embedded template in the mapping language,
[`oidc-dcr.cel.yaml`](oidc-dcr.cel.yaml)
([oidcClients](docs/specs/02_service_definition.md#oidcclients), [their
charts](docs/specs/01_inputs_outputs.md#oauth-client-charts), [their
platform settings](docs/specs/01_inputs_outputs.md#oauth-client-settings)).

## Binary

Each GitHub release attaches `okdp-compile` for Linux, macOS and Windows,
amd64 and arm64, as archives with their SHA-256 sums
(`okdp-compile_<version>_<os>_<arch>.tar.gz`, `.zip` for Windows), built by
goreleaser. Once the module is served at its path,
`github.com/okdp/okdp-compiler` (the repository is to be published as
github.com/OKDP/okdp-compiler), `go install` installs a release:
`go install github.com/okdp/okdp-compiler/cmd/okdp-compile@vX.Y.Z`. From a
checkout, `make build` ([install](docs/01_usage.md#install)):

```bash
make build    # bin/okdp-compile
in=examples/02_advanced/input
export OKDP_CONTRACTS=$in/contracts   # or --contracts
export OKDP_CHARTS=$in/charts         # or --charts
compile() { bin/okdp-compile --package $in/service --release demo-webapp --namespace demo -f $in/config.yaml "$@"; }
dcr="--platform $in/platform-dcr.yaml"
compile --platform $in/platform.yaml -o out/output-no-dcr
compile --platform $in/platform.yaml $dcr -o out/output-dcr
# out/ now holds the outputs of examples/02_advanced/, byte for byte
compile --platform $in/platform.yaml -f testdata/protected-upstream.yaml -o out/protected
# error: app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed
```

`--platform` gives the platform values, `global.okdp` among them, whose
settings the compiler reads are described, typed and defaulted by
[`platform.schema.json`](platform.schema.json);
`-f` the instance's: an instance values file that sets `global` fails, but
for the keys the package's parameter schema declares under it
([platform values](docs/specs/01_inputs_outputs.md#platform-values)).
`--project`, between them, gives what the instance's project (its
namespace) adds to them, its settings under `global` and its connections: a
project file that changes a value of the platform's, or one of its
connections, fails
([project values](docs/specs/01_inputs_outputs.md#project-values)):

```bash
in=examples/03_full/input
bin/okdp-compile --package $in/service --charts $in/charts --contracts $in/contracts \
  --release demo-workspace --namespace demo \
  --platform $in/platform.yaml --project $in/demo.yaml -f $in/config.yaml \
  -o out/full --mkdag
# out/full now holds examples/03_full/output/, byte for byte
```

`-o <dir>` (`--output`), required, writes the instance as one chart, for
one Helm release per instance: `<dir>/Chart.yaml`, an umbrella over the
upstream charts of the index, each a dependency under its key, and
`<dir>/instance.values.yaml`, the final values of each chart under its key
(OKDP's own objects under `okdp`), given to Helm with `-f`
([`--output`](docs/01_usage.md#--output),
[rendering with Helm](docs/01_usage.md#rendering-with-helm)); with
`--mkdag`, also
`<dir>/okdp.dependencies.yaml`, the instances the connection references
point to, for a deployer that orders the releases
([`--mkdag`](docs/01_usage.md#--mkdag)). Every output is built before any is
written, and the directory holds only what `okdp-compile` writes there:
the files of a previous compile no longer written are removed, a
directory holding any other file is refused. `--release` and
`--namespace` are required, names Kubernetes and Helm accept (a release of
53 characters at most): the values are compiled for them.

## Layout

| Path | Description |
|---|---|
| `*.go` | the Go library, package `compiler`: `LoadDefinition`, `Compile`, `Write`...; OKDP's rules (`global.okdp`, connections and contracts, the descriptor, the OAuth clients, the `okdp` chart), the order of the stages, the files a compile is written as (`-o`, `--mkdag`) |
| `example_test.go`, `compiler_test.go` | the Go programs of the documentation, run on the example as examples; the concurrent use, raced |
| `internal/compile/` | the mapping language: YAML + CEL templating, generic ([source map](docs/00_architecture.md#source-map)) |
| `internal/chart/` | what the compiler reproduces of Helm: values, schemas, the chart cache, the `charts` section of `service.yaml`, the final values |
| `` | OKDP's rules (`global.okdp`, connections and contracts, the descriptor, the OAuth clients, the `okdp` chart) and `Compile` |
| `` | the edges: `LoadDefinition`, and the files a compile is written as (`Write`: `-o`, `--mkdag`) |
| `internal/helmfn/` | the Helm, sprig and Kubernetes rules the compiler reproduces: template functions, the names Kubernetes and Helm accept |
| `internal/value/` | the values every package shares, and their helpers |
| `cmd/okdp-compile/main.go` | binary entrypoint: flags, and the files written to disk |
| `examples/01_minimal/` | the smallest package: one chart, a mapping that sets three of its values and the instance's usage, the platform values the okdp chart needs; its outputs in `output/`, what `-o <dir>` writes, checked by `go test` ([example](docs/00_architecture.md#example)) |
| `examples/02_advanced/input/` | an example compile's inputs: the package (`service/`), the chart cache (`charts/`), the contracts, the platform values (`platform.yaml`: `global.okdp` and a connection every instance may reference, the instance's audit database; `platform-dcr.yaml` after it for `dcr` mode), the instance's values (`config.yaml`, given with `-f`) ([example](docs/00_architecture.md#example)) |
| `examples/02_advanced/output-*/` | what `okdp-compile` writes for it, checked by `go test`: `output-no-dcr/` with `-o <dir>`, `output-dcr/` likewise in `dcr` mode |
| `examples/03_full/` | a package that uses every part of the mapping language, each commented: five charts (two renders of one, one left out by `$if`), three OAuth clients in `dcr` mode, contracts with derived and conditional defaults; the platform's values (`platform.yaml`), a project's (`demo.yaml`, `--project`) and an instance's (`config.yaml`) with connections of each; its outputs in `output/`, what `-o <dir> --mkdag` writes, checked by `go test` ([example](docs/00_architecture.md#example)) |
| `testdata/` | instance values that must fail on the example, platform values in `testdata/platform/`, project values in `testdata/project/`, each with its expected error on the first line ([failure cases](docs/00_architecture.md#failure-cases)) |
| `testdata/cli/` | the binary run as a process (testscript): exit statuses, `$OKDP_CHARTS` and `$OKDP_CONTRACTS`, stdout and stderr |
| `testdata/helm/` | what `helm template` computes on the same inputs, regenerated by `generate.sh` ([Helm fixtures](docs/00_architecture.md#helm-fixtures)) |
| `docs/` | the documentation |

## Documentation

[docs/README.md](docs/README.md) indexes the documentation

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md): the requirements, the `make` targets,
the tests and their data, the Helm fixtures, the commits and releases.

## License

Apache License 2.0: [LICENSE](LICENSE).
