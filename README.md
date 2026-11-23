# okdp-compiler

Compiles an OKDP service instance into the values of its upstream charts,
with a YAML + CEL mapping ([cel2yaml](https://github.com/OKDP/cel2yaml)).
A service is a package, a directory in git, not a chart: its upstream
charts are rendered as published, with the values the compiler writes.

It is a Go library (package `compiler`) that reads everything through
`fs.FS`, and a thin command, `okdp-compile`. It compiles instances
together, read from input documents laid out in any files: claims bound
to their providers, connections, the mandatory rules, and Helm's merge of
the final values. How a deployment's files are laid out, what each of
its values layers may set, and the order its releases deploy in, are a
host's.

## Library

```go
import compiler "github.com/okdp/okdp-compiler"

rules, err := compiler.LoadRules(securityFS, ownFS) // added to the mandatory set, in order
cat, err := compiler.LoadCatalog([]compiler.CatalogFiles{{Name: "charts", FS: catalogFS}})
instances, err := compiler.LoadInputs(ctx, []compiler.InputFiles{
	{Name: "config", FS: configFS}, // every .yaml file: Settings, Project, Instance
}, cat, rules)
for i := range instances {
	instances[i].Inputs.Rules = rules
	instances[i].Inputs.CostLimit = compiler.DefaultCostLimit
}
for _, out := range compiler.CompileAll(ctx, instances) {
	if out.Err != nil {
		log.Fatal(out.Err)
	}
	res := out.Result // Values, Charts, Objects, Info, Dependencies
	files, err := compiler.Write(res)
	// ...
}
```

- `LoadCatalog` reads the packages and contracts of catalog directories,
  `LoadInputs` the instances of input documents, each of a `kind`,
  `Settings`, `Project` or `Instance`.
- `CompileAll` compiles instances together, `Compile` a single one; both
  are stateless and safe for concurrent use.
- `Inputs.Finalize` is where a host adds what the compiler does not do:
  a host may fetch the upstream charts, check the final values against
  them, and add the security set's policy on upstream values.
- `Inputs.Settings` gives a host's own settings beside the values
  layers: the rule sets read them as they read `global.okdp`, and no
  chart receives them (a namespace's groups, say). A layer that gives a
  path they give too is refused.
- Accessors let a caller check before it compiles, without restating a
  rule: `FieldErrors`, `ConnectionRefs`, `CheckConnection`,
  `CheckNamespace`, `ReadContracts`...

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
helm install <release>-okdp ../okdp-objects-chart -n <namespace> -f okdp-objects.values.yaml
helm install <release> <package> -n <namespace> -f values.yaml
```

Values are read, merged and coalesced as Helm does, so Helm renders the
final values as it would render the layers given as `-f` files
([Helm fidelity](docs/00_architecture.md#helm-fidelity)).

## Rules

Rule sets apply together, all data, the walkers in Go:

- the mandatory set, embedded
  ([`internal/rules/mandatory`](internal/rules/mandatory)), is what every
  compile needs: the platform schema, labels, names, the namespaces
  OKDP keeps (`platform`, `kube-*`, its namespace hook), the mapping
  schema, the generated Secrets, the secret store's objects, the
  default objects chart;
- the rule sets given (`LoadRules`, in order; `okdp-compile --rules`
  gives one) add a platform's own rules to it, each to those before it,
  and replace none: tables, documents, functions, sections, keys of the
  schemas, what a mapping sees as `platform`, and the documents of the
  hooks the compiler calls, for every project, on its namespace and
  release, on every object and on the final values.

The platform's policy (no cluster-scoped object, no other namespace, no
credential in clear, but in the platform's privileged projects; the
namespaces it reserves; the hosts a project serves), the groups of each
role, the OAuth clients, the hardened pod and the policy on upstream
values are such a set,
[okdp-compiler-security](https://github.com/OKDP/okdp-compiler-security),
which a host gives to every compile: a compile without it accepts what
it refuses. What a host keeps, the namespaces of its deployer engines
and the names of the objects it writes in every namespace, is its own
set's. A platform that relies on a set requires it
(`global.okdp.ruleSets`), and a compile without it is refused.

Every hook, function and setting: [the rule set](docs/specs/05_rules.md).

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

Each [release](https://github.com/OKDP/okdp-compiler/releases) attaches
`okdp-compile` for Linux, macOS and Windows, amd64 and arm64, with the
SHA-256 sums signed keyless by the release workflow, an SBOM of each
archive and its build provenance, all verified in
[install](docs/01_usage.md#install):

```bash
gh release download -R OKDP/okdp-compiler \
  -p 'okdp-compile_*_linux_amd64.tar.gz' -p 'okdp-compile_*_checksums.txt'
sha256sum -c --ignore-missing okdp-compile_*_checksums.txt
tar xzf okdp-compile_*_linux_amd64.tar.gz okdp-compile
```

`go install github.com/okdp/okdp-compiler/cmd/okdp-compile@latest`
builds the latest release too, and `make build` gives `bin/okdp-compile`
from a checkout.

```bash
okdp-compile -f <file|dir>... --catalog <dir>... [--rules <dir>] -o <dir>
okdp-compile --check -f <file|dir>... --catalog <dir>... [--rules <dir>]
okdp-compile -f examples/04_claims/input/config --catalog examples/04_claims/input -o out
```

It reads the input documents of each `-f`, the packages and contracts of
each `--catalog`, and writes the files of [Output](#output) under
`projects/<namespace>/instances/<release>/`. It writes no file for a
deployer engine such as Flux or Argo CD and reads no upstream chart: a
host does ([usage](docs/01_usage.md#command-line)).

## Layout

| Path | Description |
|---|---|
| `compiler.go`, `exports.go`, `accessors.go`, `write.go` | the public API |
| `internal/okdp/` | the compile: claims, connections, layers, stages, `CompileAll` |
| `internal/rules/` | the rule engine and the mandatory set |
| `internal/merge/` | Helm's merge of the final values |
| `internal/catalog/`, `internal/inputs/` | the catalog read, the input documents read as instances |
| `internal/compile/` | the mapping language over cel2yaml |
| `chart/`, `value/` | what the compiler reproduces of Helm; the values helpers |
| `cmd/okdp-compile/` | the command line |
| `examples/` | `00_passthrough`, `01_minimal`, `02_advanced` and `04_claims`, each its `input/` (`catalog/`, `contracts/`, `config/inputs.yaml`) and its expected output, checked by `go test`; those of the security set are in okdp-compiler-security |
| `docs/` | the [documentation](docs/README.md) |

## Contributing, security, license

[CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md), Apache
License 2.0 ([LICENSE](LICENSE)).
