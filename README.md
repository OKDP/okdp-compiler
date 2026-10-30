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

def, err := compiler.LoadDefinition(packageFS) // Chart.yaml, okdp.yaml, values.cel.yaml or values.yaml; each compile reads config.schema.json, <key>.defaults.yaml
if err != nil {
	log.Fatal(err)
}
res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
	Contracts: contractsFS, // the contract schemas, <contract>.schema.json
	Context: []compiler.ContextLayer{ // by level, lowest first, a level's layers in order (helm -f); compiler.ParseValues reads one
		{Level: 0, Values: platformValues}, // the platform's
		{Level: 1, Values: projectValues},  // a project's: it adds values, never changes a lower level's
	},
	Values:    []map[string]any{instanceValues}, // the instance's, last: no global
	Release:   "demo-webapp",
	Namespace: "demo",
	Published: published, // what the other instances of the namespace publish (okdp.info.yaml), by release: a reference may name one of their outputs
})
if err != nil {
	log.Fatal(err) // the whole report, e.g. every schema violation
}
// res.Values:   chart -> final values of the upstream chart (helm template <res.Charts[chart]> -f them)
// res.Charts:   chart -> the upstream chart that renders them (compiler.ChartRef: repository, chart, version):
//               its Chart.yaml dependency, the platform's oidc-dcr chart for the OAuth clients (oidcClients)
// res.Objects:  OKDP's own objects: the credentials Secrets of the provided connections,
//               the mapping's objects section (okdp.objects.yaml)
// res.Info: what the instance publishes: its url, its usage, the connections it provides (okdp.info.yaml)
// res.Dependencies: instances the connection references point to, by naming convention or output (compiler.Dependency), what a deployer orders the releases with

// What okdp-compile -o <dir> writes, path -> content.
files, err := compiler.Write(res)
if err != nil {
	log.Fatal(err)
}
```

`example_test.go` runs this program on the example (`Example`).
`compiler.CompileAll` compiles instances together: each instance of a
namespace after the instances whose outputs it references, with what they
publish as `Published`. `compiler.NewGraph` checks their dependencies and
orders their releases, and a target writes them for a deployer: the
umbrella chart of each (`compiler.Helm`), with its Flux HelmRelease
(`compiler.Flux`) or its Argo CD Application (`compiler.ArgoCD`).
`Compile` keeps no state between calls and is safe for concurrent use. Any
`fs.FS` works: `os.DirFS`, `embed.FS`, a git tree, `fstest.MapFS` (see
`internal/okdp/pipeline_test.go`).

What Helm does, the compiler does the same way, checked against `helm
template` (`testdata/helm`): every values file is read as Helm
reads it (YAML 1.1), the layers are merged and coalesced as Helm does, the
parameters are checked against the package's `config.schema.json`, and
`res.Values[chart]`, OKDP's defaults, the computed values and the
instance's upstream values merged, rendered by Helm, gives what Helm gives
with them as `-f` files: Helm applies the chart's own defaults, which
`res.Values` leaves out
([Helm fidelity](docs/00_architecture.md#helm-fidelity)).

A package is a Helm `Chart.yaml` and OKDP's files: each of its
`dependencies` is a key, its `alias` or its `name`, and the published chart
that renders its values, `{name, repository, version}`, pinned. Two
dependencies may name one chart, each with its alias, for a chart rendered
more than once
([dependencies](docs/specs/02_service_definition.md#dependencies)). A
compile reads no chart. A check (`Inputs.Charts`, `okdp-compile --check
--charts`) reads the chart's `values.yaml` and `values.schema.json`, from a
chart cache the caller fills with `helm pull --untar`, each chart unpacked
under its repository's directory, the URL without its scheme
(`quay.io/adaltas/oidc-dcr-0.5.0/oidc-dcr/`): the final values, over the
chart's defaults, must meet its schema
([chart cache](docs/specs/01_inputs_outputs.md#chart-cache)). `res.Charts`
maps every compiled key to the upstream chart that renders it
([chart index](docs/specs/01_inputs_outputs.md#chart-index)).

`res.Objects`, OKDP's own objects, the generated Secrets among them,
reach the cluster with the upstream charts: `Write` gives them as a YAML
stream, `okdp.objects.yaml`, which the umbrella's own template renders as
written, in the instance's release
([the objects file](docs/specs/01_inputs_outputs.md#the-objects-file),
[OKDP's own objects](docs/03_okdp_objects.md)). `res.Info`, what the
instance publishes, its URL, its usage and the connections it provides,
stays in git, `okdp.info.yaml`, for a control plane or a console to
read ([the info file](docs/specs/01_inputs_outputs.md#the-info-file)).

## Mapping

The mapping is YAML shaped like the upstream values, with `${{ CEL }}`
expressions and `$if`/`$for`/`$merge`/`$spread`/`$vars` directives: the
values file, `values.cel.yaml` (chart -> values; `values.yaml` without
expressions), and the sections of `okdp.yaml`, after its package keys
(`require`, `roles`, `dependsOn`, `defaults`, `upstream`): `vars`,
`errors`, `instance` (url, usage), `provides` (the connections the
instance publishes), `objects` (OKDP's own objects, whole manifests) and
`oidcClients` (the service's OAuth clients).
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
[`internal/okdp/oidc-dcr.cel.yaml`](internal/okdp/oidc-dcr.cel.yaml)
([oidcClients](docs/specs/02_service_definition.md#oidcclients), [their
charts](docs/specs/01_inputs_outputs.md#oauth-client-charts), [their
platform settings](docs/specs/01_inputs_outputs.md#oauth-client-settings)).

## Binary

Each GitHub release attaches `okdp-compile` for Linux, macOS and Windows,
amd64 and arm64, as archives with their SHA-256 sums
(`okdp-compile_<version>_<os>_<arch>.tar.gz`, `.zip` for Windows), built by
goreleaser ([releases](https://github.com/OKDP/okdp-compiler/releases)).
`go install` installs a release by its module path:
`go install github.com/okdp/okdp-compiler/cmd/okdp-compile@vX.Y.Z`. From a
checkout, `make build` ([install](docs/01_usage.md#install)):

```bash
make build    # bin/okdp-compile
db=examples/02_advanced/input/db
export OKDP_CONTRACTS=$db/contracts   # or --contracts
compile() { bin/okdp-compile --package $db/catalog/webapp/1.0.0 --release demo-webapp --namespace demo \
  --context $db/platform.yaml -f $db/projects/demo/instances/demo-webapp/config.yaml "$@"; }
compile -o out/webapp
# depends on demo-db (database, database)
# out/webapp now holds examples/02_advanced/output/projects/demo/instances/demo-webapp/, byte for byte
compile -f testdata/protected-upstream.yaml -o out/protected
# error: app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed
```

`--context [<level>:]<file>` gives the context: at level 0, no prefix, the
platform values, `global.okdp` among them, whose settings the compiler reads
are described, typed and defaulted by
[`internal/okdp/platform.schema.json`](internal/okdp/platform.schema.json);
`-f` the instance's: an instance values file that sets `connections`, or
`global` but for the keys the package's parameter schema declares under
it, fails
([context values](docs/specs/01_inputs_outputs.md#context-values)).
A higher level, `--context 1:<file>`, gives what the instance's project
(its namespace) adds to them, its settings under `global` and its
connections, and one above it, `--context 2:<file>`, the instance's own
connections; files of one level merge in order, whatever the order of the
flags, and a file that changes a value of a lower level, or one of its
connections, fails
([levels](docs/specs/01_inputs_outputs.md#levels)):

```bash
db=examples/03_full/input/db
i=$db/projects/demo/instances/demo-workspace
full() { bin/okdp-compile --package $db/catalog/workspace/3.2.0-1.4.0 \
  --contracts $db/contracts --release demo-workspace --namespace demo \
  --context $db/platform.yaml --context 1:$db/projects/demo/project.yaml \
  --context 2:$i/connections.yaml -f $i/config.yaml "$@"; }
full -o out/full
# depends on demo-db (database, catalogs[1].database)
# depends on demo-db (database, database)
# depends on demo-alerts (http, notifications.webhook)
# out/full now holds examples/03_full/output/projects/demo/instances/demo-workspace/, byte for byte
full --check --charts examples/03_full/.charts
# nothing written: the compile checked, and its final values against the upstream charts
```

`--repository <dir>`, repeatable, compiles every instance of a tree
together, each directory any part of it: each instance with what the
others of its namespace publish, compiled after them, their dependencies
checked, written for `--target`: `helm` (the default), each instance's
umbrella chart as `-o` writes one, `flux` with its HelmRelease or `argocd`
with its Application
([`--repository`](docs/01_usage.md#--repository)):

```bash
bin/okdp-compile --repository examples/03_full/input/db -o out/tree
# demo/demo-workspace: depends on demo-db (database, catalogs[1].database)
# demo/demo-workspace: depends on demo-db (database, database)
# demo/demo-workspace: depends on demo-alerts (http, notifications.webhook)
# out/tree now holds examples/03_full/output/, byte for byte
bin/okdp-compile --repository examples/03_full/input/db -o out/flux --target flux
# out/flux holds the same charts, with each instance's HelmRelease and the Kustomizations
```

`-o <dir>` (`--output`), required, writes the instance as one chart, for
one Helm release per instance: `<dir>/Chart.yaml`, an umbrella over the
upstream charts of the index, each a dependency under its key, and
`<dir>/instance.values.yaml`, the final values of each chart under its
key, given to Helm with `-f` ([`--output`](docs/01_usage.md#--output),
[rendering with Helm](docs/01_usage.md#rendering-with-helm)),
`<dir>/okdp.objects.yaml`, OKDP's own objects, which the umbrella renders
as written (`<dir>/templates/okdp.yaml`), when the instance has any, and
`<dir>/okdp.info.yaml`, what the instance publishes, when it publishes
something. The directory is created if needed; a compile that fails writes
nothing, the files of a successful one overwrite those of the same name.
The instances the connection references point to are printed on stderr,
one line each, `depends on <instance> (<contract>, <field>)`; a deployer
that orders the releases reads them from `Result.Dependencies`
([okdp-gitops](https://github.com/OKDP/okdp-gitops),
[GitOps](docs/01_usage.md#gitops)). `--published <dir>` gives what the
other instances of the namespace publish, each instance's
`<dir>/<release>/okdp.info.yaml` (`Inputs.Published`): a reference of a
contract without a naming convention may name one of their outputs,
`depends on demo-pg (database-server demo-pg-hive, db)`
([resolution](docs/specs/01_inputs_outputs.md#resolution)). `--release`
and `--namespace` are required, names Kubernetes and Helm accept (a
release of 53 characters at most): the values are compiled for them.

`--check`, without `-o`, compiles and writes nothing: what fails a compile
fails it, its warnings printed. With it, `--charts <dir>` (`$OKDP_CHARTS`),
a chart cache, checks the final values against each upstream chart: over
its defaults, they must meet its `values.schema.json`, as Helm checks them
when it renders; an instance list appended to (`append`) that leaves out
items of the chart's default list is warned of, Helm replacing that list.
A compile reads no chart: `--charts` without `--check` fails
([`--check`](docs/01_usage.md#--check)).

## Layout

| Path | Description |
|---|---|
| `compiler.go` | the Go library's public API, package `compiler`: `LoadDefinition`, `Compile`, `LoadRepository`, `CompileAll`, `NewGraph`, the targets, `Write`... |
| `example_test.go`, `compiler_test.go`, `examples_test.go` | the Go programs of the documentation, run on the example as examples; the concurrent use, raced; `examples/` compiled through the public API |
| `internal/okdp/` | OKDP's rules (`global.okdp`, connections and contracts, the outputs, the OAuth clients), `LoadDefinition` and `Compile`, the order of the stages |
| `internal/repository/` | the tree of a deployment, in one or more repositories, read as the instances `CompileAll` compiles |
| `internal/graph/` | the deployment graph of compiled instances: their dependencies checked, what each release waits for, its sync wave |
| `internal/target/` | the output formats of compiled instances (`Target`), what they share: the tree, `projects/<namespace>/instances/<release>/` |
| `internal/target/flux/`, `internal/target/argocd/` | the flux and argocd targets: the umbrella charts with each instance's HelmRelease and Kustomizations, or its Application, okdp-gitops' format |
| `internal/target/helm/` | the helm target; the files a compile is written as (`Write`, `-o`): the umbrella chart, its values, OKDP's own objects, what the instance publishes |
| `internal/compile/` | the mapping language: YAML + CEL templating, generic ([source map](docs/00_architecture.md#source-map)) |
| `internal/chart/` | what the compiler reproduces of Helm: values, schemas, `Chart.yaml` and the charts of a package, the final values, their check against the chart cache |
| `internal/value/` | the values every package shares, and their helpers |
| `cmd/okdp-compile/main.go` | binary entrypoint: flags, and the files written to disk |
| `examples/<example>/` | each example a repository tree ([repository layout](docs/01_usage.md#repository-layout)): its sources under `input/db/` (`platform.yaml`, `contracts/`, the packages in `catalog/<service>/<version>/`, the instances of the project `demo` in `projects/demo/instances/<instance>/`), what `-o <dir>` writes for each instance in `output/projects/demo/instances/<instance>/`, checked by `go test` ([example](docs/00_architecture.md#example)) |
| `examples/00_passthrough/` | a package that computes nothing, `echo`: `Chart.yaml`, one chart, and a plain `values.yaml`, the chart's values the platform sets; no `okdp.yaml`, no parameter, no instance values |
| `examples/01_minimal/` | the smallest package with a mapping, `hello`: one chart, a mapping that sets three of its values and the instance's usage; no platform values, none needed |
| `examples/02_advanced/` | an example compile, `webapp`: the package, the contracts, the platform values (`platform.yaml`: `global.okdp` in `dcr` mode and a connection every instance may reference, the instance's audit database), the instance's values (`config.yaml`, given with `-f`); `demo-db`, the database it references, a minimal package, `db` |
| `examples/03_full/` | a package, `workspace`, that uses every part of the mapping language, each commented: five charts (two renders of one, one left out by `$if`), three OAuth clients in `dcr` mode, contracts with derived and conditional defaults; the platform's values (`platform.yaml`) and a project's (`project.yaml`, `--context 1:project.yaml`), each with connections, the instance's own connections (`connections.yaml`, `--context 2:connections.yaml`) and its values (`config.yaml`); the instances it references, `demo-db` and `demo-alerts`, minimal packages, `db` and `alerts`; its chart cache in `.charts/`, for `--check --charts` |
| `testdata/` | instance values that must fail on the example, context values of level `<l>` in `testdata/context/<l>/`, each with its expected error on the first line ([failure cases](docs/00_architecture.md#failure-cases)) |
| `testdata/cli/` | the binary run as a process (testscript): exit statuses, `--check`, `--published`, `--repository` and its targets, `$OKDP_CHARTS` and `$OKDP_CONTRACTS`, stdout and stderr |
| `testdata/targets/` | the files of each target for three instances (`go test ./internal/target -update`) |
| `testdata/helm/` | what `helm template` computes on the same inputs, regenerated by `generate.sh` ([Helm fixtures](docs/00_architecture.md#helm-fixtures)) |
| `docs/` | the documentation |

## Documentation

[docs/README.md](docs/README.md) indexes the documentation

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md): the requirements, the `make` targets,
the tests and their data, the Helm fixtures, the commits and releases.

## License

Apache License 2.0: [LICENSE](LICENSE).
