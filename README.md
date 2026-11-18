# okdp-compiler

Compiles an OKDP service instance into the values of its upstream charts,
with a YAML + CEL mapping ([cel2yaml](https://github.com/OKDP/cel2yaml)).
A service is a package, a directory in git, not a chart: its upstream
charts are rendered as published, with the values the compiler writes.

It is a Go library (package `compiler`) that reads everything through
`fs.FS`, and a thin command, `okdp-compile`. It compiles a whole tree:
claims bound to their providers, connections, the deployment graph, the
mandatory rules, and Helm's merge of the final values.

## Library

```go
import compiler "github.com/okdp/okdp-compiler"

rules, err := compiler.LoadRules(rulesFS) // added to the mandatory set
tree, err := compiler.Build(ctx, []compiler.Repository{
	{Name: "db", FS: treeFS, Role: compiler.RoleTree},
}, compiler.BuildOptions{Rules: rules, CostLimit: compiler.DefaultCostLimit})
if problems := tree.Problems(); problems != nil {
	log.Fatal(strings.Join(problems, "\n"))
}
for _, ti := range tree.Instances {
	res := ti.Outcome.Result // Values, Charts, Objects, Info, Dependencies
	files, err := compiler.Write(res)
	// ...
}
```

- `Build` loads a tree and compiles it; a project that does not load is
  refused alone (`Tree.Refused`).
- `CompileAll` compiles instances together, `Compile` a single one; both
  are stateless and safe for concurrent use.
- `Inputs.Finalize` and `BuildOptions.Loaded` are where a host adds what
  the compiler does not do (okdp-gitops: its policy, the chart checks).
- Accessors let a caller check before it compiles, without restating a
  rule: `FieldErrors`, `ConnectionRefs`, `CheckConnection`,
  `CheckProjectName`, `ReadContract`, `MergeValues`...

See [usage](docs/01_usage.md).

## Output

`compiler.Write` gives the files of one instance, for `helm install`:

| File | Holds |
|---|---|
| `values.yaml` | the final values of each dependency of the package |
| `okdp-objects.values.yaml` | OKDP's own objects (credentials Secrets, the mapping's `objects`), when there are any |
| `okdp.info.yaml` | what the instance publishes (URL, usage, connections), when it publishes something |

```bash
helm dependency build <package>
helm install <release>-okdp <okdp-objects chart> -n <namespace> -f okdp-objects.values.yaml
helm install <release> <package> -n <namespace> -f values.yaml
```

Values are read, merged and coalesced as Helm does, so Helm renders the
final values as it would render the layers given as `-f` files
([Helm fidelity](docs/00_architecture.md#helm-fidelity)).

## Rules

Two rule sets apply together, both data, the walkers in Go:

- the mandatory set, embedded
  ([`internal/rules/mandatory`](internal/rules/mandatory)), is what every
  compile needs: workload fragments, OAuth clients, the platform schema,
  labels, names, roles;
- a rule set given (`LoadRules`) adds a platform's policy: okdp-gitops'
  standard set, `rules/v1`, copied in [`testdata/rules`](testdata/rules)
  for the tests.

Each instance is bounded by `Inputs.CostLimit` (CEL cost) and
`Inputs.Timeout`.

## Mapping

The values file (`values.cel.yaml`) and the sections of `okdp.yaml` are
YAML shaped like the upstream values, with `${{ CEL }}` expressions and
`$if`, `$for`, `$merge`, `$spread`, `$vars` directives:
[service definition](docs/specs/02_service_definition.md),
[control flow](docs/specs/03_control_flow.md),
[functions](docs/specs/04_functions.md).

## Command line

```bash
make build
bin/okdp-compile --repository <dir>... [--catalog <dir>]... [--rules <dir>] -o <dir>
bin/okdp-compile --check --repository <dir>... [--catalog <dir>]... [--rules <dir>]
```

It writes the files of [Output](#output) under
`projects/<namespace>/instances/<release>/`. Flux and Argo CD targets,
`--partial`, `--report` and `--charts` are okdp-gitops' `okdp-compile`
([usage](docs/01_usage.md#command-line)).

## Layout

| Path | Description |
|---|---|
| `compiler.go`, `exports.go`, `accessors.go`, `write.go` | the public API |
| `internal/okdp/` | the compile: claims, connections, layers, stages, `CompileAll` |
| `internal/rules/` | the rule engine and the mandatory set |
| `internal/merge/` | Helm's merge of the final values |
| `internal/repository/`, `internal/tree/`, `internal/graph/` | a tree read, compiled, and its deployment graph |
| `internal/compile/` | the mapping language over cel2yaml |
| `chart/`, `value/` | what the compiler reproduces of Helm; the values helpers |
| `cmd/okdp-compile/` | the command line |
| `examples/` | packages from `00_passthrough` to `05_fragments`, each with its expected output, checked by `go test` |
| `docs/` | the [documentation](docs/README.md) |

## Contributing, security, license

[CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md), Apache
License 2.0 ([LICENSE](LICENSE)).
