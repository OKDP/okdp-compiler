# okdp-compiler

Compiles an OKDP service instance into the values of its upstream charts
with a YAML + CEL mapping. A service is a package, a directory in git, not
a chart: its upstream charts are rendered as published, from their own
repositories, with the values the compiler writes.

It is a Go library (`github.com/okdp/okdp-compiler`, package `compiler`),
everything read through `fs.FS` and in-memory values, and a thin command
over it, `okdp-compile`, for `helm install` ([command
line](#command-line)). It holds the claims and their providers, the
compile of a whole tree they require, the mandatory rules every compile
applies, Helm's merge of the final values and the errors, on
[cel2yaml](https://github.com/OKDP/cel2yaml). The Flux
and Argo CD outputs, the chart cache that pulls charts, the edits of a
tree and the standard rule set are
[okdp-gitops](https://github.com/OKDP/okdp-gitops)'s.

## Library

```go
import compiler "github.com/okdp/okdp-compiler"

rules, err := compiler.LoadRules(rulesFS) // a rule set added to the mandatory one; none, and no platform rule applies
if err != nil {
	log.Fatal(err)
}
tree, err := compiler.Build(ctx, []compiler.Repository{
	{Name: "db", FS: treeFS, Role: compiler.RoleTree}, // the tree, in one or more repositories: platform.yaml, contracts/, catalog/, projects/; a catalog of its own, RoleCatalog, catalog/ and contracts/ only
}, compiler.BuildOptions{Rules: rules, CostLimit: compiler.DefaultCostLimit})
if err != nil {
	log.Fatal(err) // a tree whose platform does not load: every problem, each naming its path
}
if problems := tree.Problems(); problems != nil {
	log.Fatal(strings.Join(problems, "\n")) // why each instance does not deploy: every schema violation, each claim that binds to nothing
}
for _, ti := range tree.Instances {
	res := ti.Outcome.Result
	// res.Values:   chart -> final values of the upstream chart (helm template <res.Charts[chart]> -f them)
	// res.Charts:   chart -> the upstream chart that renders them (compiler.ChartRef: repository, chart, version)
	// res.Objects:  OKDP's own objects: the credentials Secrets of the provided connections, the mapping's objects section
	// res.Info:     what the instance publishes: its url, its usage, the connections it provides
	// res.Dependencies: the instances the connection references point to (compiler.Dependency): the graph's edges

	files, err := compiler.Write(res) // values.yaml, okdp-objects.values.yaml, okdp.info.yaml
	if err != nil {
		log.Fatal(err)
	}
	_ = files
}
// tree.Graph: every dependency checked, the releases ordered
```

`compiler.Build` is the load of a tree, then `CompileTree`: a project that
does not load is `Tree.Refused`, the others compiled, and a tree whose
platform does not load a `compiler.LoadError`. Underneath,
`compiler.LoadRepository` reads a tree and `compiler.CompileAll` compiles
its instances together: each claim bound to its provider, which a claim or
a class names by its instance or its release (`data/postgresql`,
`data/data-postgresql`), one of another project through a class of
`platform.yaml` or with its consent, the `claimers` of its `instance.yaml`
([providers](docs/specs/01_inputs_outputs.md#providers)), each instance of
a namespace after those whose outputs it references, with what they
publish as `Published`. `compiler.NewGraph` checks their dependencies and
orders the releases. `compiler.Compile` compiles one instance, given its
inputs, as `CompileAll` does each; it keeps no state between calls and is
safe for concurrent use. Any `fs.FS` works: `os.DirFS`, `embed.FS`, a git
tree, `fstest.MapFS` (see `internal/okdp/pipeline_test.go`).

A program that answers before it compiles asks the compile's rules of the
library, none written again: the connection references of a schema
(`compiler.ConnectionRefs`), the contracts as the compile reads them
(`compiler.ReadContract`), a connection at its declaration
(`compiler.CheckConnection`), the layering of values
(`compiler.MergeValues`, `compiler.CoalesceValues`), the names of the
claims (`compiler.NamedClaimID`, `IsClaimID`, `CheckProvider`), a
compile's error field by field (`compiler.FieldErrors`). The names of the
tree (`compiler.ProjectsDir`, `compiler.InstanceFile`...) and its release
rule (`compiler.ReleaseName`) are exported: a caller does not spell them;
the rule of a project's name, `compiler.CheckProjectName`, is the
compile's, for a program that creates a project before any compile.

## Output

`compiler.Write` gives the files of a compile, for `helm install`, without
a chart: the catalog's package is installed as it is.

| File | Holds |
|---|---|
| `values.yaml` | the final values of each dependency of the package, `enabled: false` for one the mapping leaves out, which must declare `condition: <key>.enabled` |
| `okdp-objects.values.yaml` | the values of the `okdp-objects` chart: `Result.Objects` as one YAML stream, `manifests`; written when the instance has objects |
| `okdp.info.yaml` | what the instance publishes: its URL, its usage, the connections it provides; written when it publishes something |

```bash
helm dependency build <package>
helm install <release>-okdp <okdp-objects chart> -n <namespace> -f okdp-objects.values.yaml
helm install <release> <package> -n <namespace> -f values.yaml
```

The objects go first: the instance's pods read the Secrets they create.
`Result.ObjectsSource` names the chart
([OKDP's own objects](docs/03_okdp_objects.md)). None of the objects holds
credentials in clear, a Secret with values, a `provides` secret without
`generate`, unless the caller sets `Inputs.AllowPlainSecrets`
([credentials
Secrets](docs/specs/01_inputs_outputs.md#credentials-secrets)). The one
release a Flux HelmRelease or an Argo CD Application deploys, an umbrella
over both charts, is okdp-gitops'
([output](https://github.com/OKDP/okdp-gitops/blob/main/docs/specs/03_output.md)).

What Helm does, the compiler does the same way: every values file is read
as Helm reads it (YAML 1.1), the layers are merged and coalesced as Helm
does, the parameters are checked against the package's
`config.schema.json`, and `res.Values[chart]`, OKDP's defaults, the
computed values and the instance's upstream values merged, rendered by
Helm, gives what Helm gives with them as `-f` files: Helm applies the
chart's own defaults, which `res.Values` leaves out
([Helm fidelity](docs/00_architecture.md#helm-fidelity)).

A package is a Helm `Chart.yaml` and OKDP's files: each of its
`dependencies` is a key, its `alias` or its `name`, and the published chart
that renders its values, `{name, repository, version}`, pinned. Two
dependencies may name one chart, each with its alias, for a chart rendered
more than once
([dependencies](docs/specs/02_service_definition.md#dependencies)). A
compile reads no chart. A check (`Inputs.Charts`) reads the chart's
`values.yaml` and `values.schema.json`, from a chart cache the caller
fills with `helm pull --untar`, each chart unpacked under its repository's
directory, the URL without its scheme
(`quay.io/adaltas/oidc-dcr-0.6.0/oidc-dcr/`): the final values, over the
chart's defaults, must meet its schema
([chart cache](docs/specs/01_inputs_outputs.md#chart-cache)).

## Rules

What a compile knows of a platform's names and what a platform refuses
are data, two rule sets a compile applies together. The walkers that
apply them stay in Go.

The mandatory set, embedded in the compiler
([`internal/rules/mandatory`](internal/rules/mandatory)), is what a
compile needs to write values a chart installs: the workload fragments,
the OAuth clients, the platform's schema, what a mapping sees as
`platform`, the labels, the names, the roles and the objects of the
secret store. Every compile applies it.

A rule set given (`compiler.RuleSet`, `compiler.LoadRules`, as
`Inputs.Rules` or `BuildOptions.Rules`) is added to it: its tables, its
checks, the schema of a mapping and the charts the compiler adds. It
replaces nothing the mandatory set gives, and the two together hold every
table and check. okdp-gitops holds the standard set, `rules/v1`, and
gives it to its compiles; [`testdata/rules`](testdata/rules) is its copy,
for this module's tests.

With no rule set a compile applies the mandatory one alone: no namespace,
object or host is refused and no schema reads a mapping's sections.

A host adds what depends on the merged values with `Inputs.Finalize` and
`Inputs.CheckGlobals`: okdp-gitops' `deploy.Build` sets the upstream and
ingress policy there. A compile without them applies Helm's merge alone.

The settings of `global.okdp` the compiler reads are described, typed
and defaulted by the mandatory set's
[`schemas/platform.schema.json`](internal/rules/mandatory/schemas/platform.schema.json):
the classes of the claims
([classes](docs/specs/01_inputs_outputs.md#classes)), the platform's
secret store (`global.okdp.secrets`), through which the credentials of
claims cross namespaces, under `<provider namespace>/<claim ID>`, keys a
path-based store scopes per namespace ([what the store must
enforce](docs/specs/01_inputs_outputs.md#what-the-store-must-enforce)),
and the platform's policy (`global.okdp.policy`): its own projects,
`privileged`, and the namespaces no project may have, `reserved`, beside
those the standard set reserves: `platform`, `kube-*`, and the deployer
engines' and the targets', `flux-system`, `argocd`, `okdp-releases`
([upstream policy](docs/specs/01_inputs_outputs.md#upstream-policy)).

Whatever the rule set, an instance's `config.yaml` that sets `connections`
or `claims`, or `global` but for the keys the package's parameter schema
declares under it, fails
([context values](docs/specs/01_inputs_outputs.md#context-values)), and a
context file that changes a value of a lower level, one of its
connections, named claims or classes, or its default class, fails
([levels](docs/specs/01_inputs_outputs.md#levels)). The compiler reads the
platform's settings of the context's files alone, never of a package's
defaults: a `config.schema.json` that declares `global.okdp` or gives it a
default fails ([defaults](docs/specs/01_inputs_outputs.md#defaults)).

What a package computes is bounded: `Inputs.CostLimit`, the CEL cost of
each instance's expressions, the bytes a host function reads and builds
counted, past which the instance is refused, and `Inputs.Timeout`, one
instance's compile.

## Mapping

The mapping is YAML shaped like the upstream values, with `${{ CEL }}`
expressions and `$if`/`$for`/`$merge`/`$spread`/`$vars` directives: the
values file, `values.cel.yaml` (chart -> values; `values.yaml` without
expressions), and the sections of `okdp.yaml`, after its package keys
(`require`, `roles`, `dependsOn`, `defaults`, `upstream`): `vars`,
`errors`, `instance` (url, usage), `provides` (the connections the
instance publishes), `objects` (OKDP's own objects, whole manifests) and
what the rule sets add, `oidcClients` (the service's OAuth clients) in the
mandatory set. Expressions see `release`, `values`, `global.okdp`,
`platform`, `service`, `labels` and `selectorLabels`, and call host
functions, Helm's and OKDP's (`str`, `sha256`, `toJson`, `okdpFullname`;
`okdpGeneratedSecret`, `oidcClientSecret` and the fragments of the
mandatory set...):
[service definition](docs/specs/02_service_definition.md),
[control flow](docs/specs/03_control_flow.md),
[functions](docs/specs/04_functions.md).

The OAuth clients are provisioned by the platform
(`global.okdp.oidc.clientProvisioning: existing`, the default) or
registered by each service (`dcr`): in `dcr` mode, OIDC enabled, each
client is registered by a Job among the instance's objects, computed by
a template in the mapping language,
[`charts/oidc-dcr.cel.yaml`](internal/rules/mandatory/charts/oidc-dcr.cel.yaml)
([oidcClients](docs/specs/02_service_definition.md#oidcclients), [their
platform settings](docs/specs/01_inputs_outputs.md#oauth-client-settings)).

## Command line

`okdp-compile` (`make build`, `bin/okdp-compile`) compiles a tree from
directories and writes the files of [Output](#output) under
`projects/<namespace>/instances/<release>/`, with neither okdp-gitops nor
a control plane:

```bash
okdp-compile --repository <dir>... [--catalog <dir>]... \
  [--rules <rule set>] -o <dir>
okdp-compile --check [--charts <dir>] --repository <dir>... \
  [--catalog <dir>]... [--rules <rule set>]
```

It takes the flags of okdp-gitops' `okdp-compile`, whose other targets,
`--partial` and `--report` need okdp-gitops, and `--rules`, a rule set
added to the mandatory one (okdp-gitops' `rules/v1`, or its copy here,
`testdata/rules`): none, and the mandatory rules alone apply
([usage](docs/01_usage.md#command-line)).

## Layout

| Path | Description |
|---|---|
| `compiler.go`, `exports.go` | the public API, package `compiler`: `LoadDefinition`, `Compile`, `LoadRepository`, `CompileAll`, `CompileTree`, `NewGraph`, `Build`, `Partial`, `LoadRules`, what okdp-gitops needs of a compile |
| `accessors.go` | the compile's rules for a caller that answers before it writes: `FieldErrors`, `ConnectionRefs`, `ReadContract`, `CheckConnection`, `MergeValues`, `CoalesceValues`, the names of the claims |
| `write.go` | the files of a compile: `Write`, `ChartValues`, `ObjectsChartValues` |
| `internal/okdp/` | the claims, classes and providers, the connections and contracts, the outputs, the layers, `LoadDefinition` and `Compile`, the order of the stages, `CompileAll`; the walkers the rule set drives |
| `internal/rules/` | the rule engine: `RuleSet`, `LoadRules`, `Mandatory`, `rules.schema.json`; `mandatory/`, the mandatory rule set, embedded |
| `internal/merge/` | Helm's merge of the final values, the instance's upstream values, the check against a chart cache |
| `internal/repository/` | the tree of a deployment, in one or more repositories, read as the instances `CompileAll` compiles; the problems of one that does not load |
| `internal/tree/` | a tree compiled (`CompileTree`), its instances read as a caller asks of them; its projects written and kept (`Partial`) |
| `internal/graph/` | the deployment graph of compiled instances: their dependencies checked, what each release waits for, its sync wave |
| `internal/compile/` | the top level of a mapping over github.com/okdp/cel2yaml, the YAML + CEL language: `vars`, `errors`, the sections, read from one file or several ([source map](docs/00_architecture.md#source-map)) |
| `internal/testrules/` | the rule set of this module's tests |
| `cmd/okdp-compile/` | the command line, over `Build` and `Write` |
| `internal/dirfs/`, `internal/outdir/` | the directories the command reads, bounded; its output directory: copies of okdp-gitops' |
| `chart/` | what the compiler reproduces of Helm: values, schemas, `Chart.yaml` and the charts of a package, the references to charts |
| `value/` | the values every package shares, and their helpers |
| `examples/<example>/` | each example: `input/catalog/` the package or its services, `input/contracts/`, `input/config/` the platform, projects and instances, `output/` what `Write` gives, checked by `go test` ([examples](docs/00_architecture.md#examples)) |
| `examples/00_passthrough/` | a package that computes nothing, `echo`: `Chart.yaml`, one chart, and a plain `values.yaml`; no `okdp.yaml`, no parameter |
| `examples/01_minimal/` | the smallest package with a mapping, `hello` |
| `examples/02_advanced/` | `webapp`: the contracts, the platform's values, a claim of a database bound to `demo-db`, OAuth clients in `dcr` mode |
| `examples/03_full/` | `workspace`, every part of the mapping language, commented: five charts, three OAuth clients, connections at each level, two claims |
| `examples/04_claims/` | claims across namespaces: a provider in the project `data`, its claimers in `demo`, the classes and the consent, the platform's secret store |
| `examples/05_fragments/` | workloads written with the fragments of the mandatory set |
| `testdata/rules/` | the copy of okdp-gitops' standard rule set |
| `docs/` | the documentation |

## Documentation

[docs/README.md](docs/README.md) indexes the documentation

## Security

[SECURITY.md](SECURITY.md): how to report a vulnerability, the versions
supported, the disclosure process.

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md): the requirements, the `make` targets,
the tests and their data, the commits and releases.

## License

Apache License 2.0: [LICENSE](LICENSE).
