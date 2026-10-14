# Usage

How to run okdp-compiler and integrate its output: the `okdp-compile`
binary, the Go library, CI and GitOps. The file formats it reads and the
objects it writes are specified in
[service definition](specs/02_service_definition.md) and
[inputs and outputs](specs/01_inputs_outputs.md); how it works
inside is in [architecture](00_architecture.md); how to write a package is in
[writing a package](02_writing_package.md).

The commands below run from the repository root, after `make build`.

## Install

`okdp-compile` is a single binary: download a release's, install it with
`go install`, or build it from a checkout. Helm is not needed to
compile, only to render the result.

### Release archives

Each GitHub release attaches the binaries of its version, built by
goreleaser (`.goreleaser.yaml`): `okdp-compile_<version>_<os>_<arch>.tar.gz`,
a `.zip` for `windows`, for `<os>` `linux`, `darwin` or `windows` and
`<arch>` `amd64` or `arm64`, each holding `okdp-compile` (`.exe` on
Windows), `LICENSE` and `README.md`, and
`okdp-compile_<version>_checksums.txt`, their SHA-256 sums. `<version>` is
the tag without its `v`. From the releases of github.com/OKDP/okdp-compiler,
where the repository is to be published:

```bash
version=X.Y.Z os=linux arch=amd64
url=https://github.com/OKDP/okdp-compiler/releases/download/v$version
curl -fsSLO "$url/okdp-compile_${version}_${os}_${arch}.tar.gz"
curl -fsSLO "$url/okdp-compile_${version}_checksums.txt"
sha256sum --ignore-missing -c "okdp-compile_${version}_checksums.txt"
tar -xzf "okdp-compile_${version}_${os}_${arch}.tar.gz" okdp-compile
./okdp-compile --version    # X.Y.Z
```

### `go install`

```bash
go install github.com/okdp/okdp-compiler/cmd/okdp-compile@vX.Y.Z
```

builds a release with Go 1.27 or later and installs it in `$GOBIN`
(`$(go env GOPATH)/bin` by default); `--version` prints `dev`, the version
being set by the `-ldflags` of the release archives and `make build`. The go
command finds a module by its path, through the Go module proxy by
default: this needs the module served at its path,
`github.com/okdp/okdp-compiler` (`go.mod`). The repository is to be
published as github.com/OKDP/okdp-compiler; until the module is served at
its path, download a release archive or build from a checkout.

### From a checkout

Go 1.27 or later (`go 1.27.0` in `go.mod`):

```bash
make build                  # bin/okdp-compile
make test                   # go test ./...
make lint                   # golangci-lint, the version tools/go.mod pins
make check                  # what CI runs: gofmt, vet, lint, tidy, race tests
make                        # every target
```

`make build` sets the version printed by `--version` with
`-ldflags "-X main.version=..."`: `git describe --tags --always --dirty`
without the leading `v` of the tag, the commit hash when there is no tag;
outside a checkout, nothing, and the binary prints `dev` (below).
`VERSION` overrides it:

```console
$ make build VERSION=1.2.3
go build -ldflags "-X main.version=1.2.3" -o bin/okdp-compile ./cmd/okdp-compile
$ bin/okdp-compile --version
1.2.3
```

Without these `-ldflags` (`go build ./cmd/okdp-compile`, or
`go install ./cmd/okdp-compile`, which installs it in `$GOBIN`),
`--version` prints `dev`.

### Versions and releases

A version is a tag `vX.Y.Z`, the version of the Go module and of the CLI
(`make build` takes it from `git describe`; `--version` prints it without
the `v`). Releases are made by release-please
(`.github/workflows/release-please.yml`): each push to `main` updates a
release pull request with the next version and `CHANGELOG.md`, computed
from the [conventional commits](https://www.conventionalcommits.org) since
the last release (`fix:` a patch; `feat:` a minor version; `!`, a breaking
change, a minor version before 1.0, then a major one); merging it tags the
version and publishes the GitHub release. The same workflow then runs
goreleaser on the tag: it builds the [release archives](#release-archives),
`-X main.version=X.Y.Z`, and attaches them to the release, whose notes it
keeps (a tag created with the workflow's token triggers no other
workflow).
`.release-please-manifest.json` holds the current version,
`release-please-config.json` the settings, `.goreleaser.yaml` the
binaries'.

## Command line

```text
okdp-compile --package <dir> [--charts <dir>] [--contracts <dir>]
             [--context [<level>:]<context.yaml>]...
             [-f <values.yaml>]...
             --release <name> --namespace <name> -o <dir>
okdp-compile --version
```

| Flag | Default | |
|---|---|---|
| `--package` | required | package directory: `Chart.yaml`, `okdp.yaml` (optional), `values.cel.yaml` (optional), `config.schema.json` (optional) at its root, and the defaults files `okdp.yaml` names ([package files](specs/01_inputs_outputs.md#package-files)) |
| `--charts` | `$OKDP_CHARTS` | chart cache directory: each upstream chart unpacked under its repository's directory, the URL without its scheme, as `helm pull --untar -d <dir>/<repository>/<chart>-<version>` writes it, `<repository>/<chart>-<version>/<chart>/` (`quay.io/adaltas/oidc-dcr-0.5.0/oidc-dcr/`) ([chart cache](specs/01_inputs_outputs.md#chart-cache)); required, by flag or variable |
| `--contracts` | `$OKDP_CONTRACTS` | contract schemas directory: `<contract>.schema.json` ([contracts](specs/01_inputs_outputs.md#contracts)); required, by flag or variable |
| `--context` | none | a context values file, `[<level>:]<file>`: `global`, `global.okdp` and `connections` among them ([context values](specs/01_inputs_outputs.md#context-values)); repeatable, before the `-f` files. No prefix is level 0, the platform's; a higher level, `1:` a project's, `2:` an instance's own connections..., adds values. The files are merged by level, lowest first, whatever the order of the flags, a level's in order as `helm -f` files; a file changes no value of a lower level, nor a connection it gives, and holds no `{{` above level 0 ([levels](specs/01_inputs_outputs.md#levels)) |
| `-f` | none | an instance values file; repeatable, merged in order as `helm -f` files, after the `--context` files. It sets no `global` but the keys the parameter schema declares under it, and no `connections` ([instance values](specs/01_inputs_outputs.md#instance-values), [merge precedence](specs/01_inputs_outputs.md#merge-precedence)) |
| `--release` | required | release name: the Helm release the values are compiled for, which renders them; a DNS-1123 label (lowercase letters, digits and `-`) of 53 characters at most, as Helm accepts it |
| `--namespace` | required | namespace: the release's; a DNS-1123 label of 63 characters at most |
| `--output`, `-o` | required | the output directory: the instance as one chart, `Chart.yaml`, an umbrella over the upstream charts, `instance.values.yaml`, its values, and `okdp.dependencies.yaml`, the instances the connection references point to ([`--output`](#--output)) |
| `--version` | | print the version and exit |

Flags take one or two dashes, and every input is a flag: an argument that
is not one is refused, where it would end the flags and those after it be
ignored (a values file without its `-f`). The directories of `--package`,
`--charts` and `--contracts` must exist. Every output is built before any
is written: a compile or an output that fails writes nothing; an error
while writing may leave some of the files written.

### `--output`

The instance as one chart, for one Helm release per instance
([GitOps](#gitops)):

| File | Content |
|---|---|
| `Chart.yaml` | an umbrella chart: the package's `Chart.yaml`, its metadata kept (`name`, `version`...), with one dependency per key of the [chart index](specs/01_inputs_outputs.md#chart-index): the rendered dependencies of the package, in `Chart.yaml` order, as written; then the OAuth client charts, by key, and `okdp`, each its upstream chart (repository, name, version), aliased to the key when the key is not the chart's name ([umbrella chart](specs/01_inputs_outputs.md#umbrella-chart)) |
| `instance.values.yaml` | `Result.Values`: the final values of each key, under the key, after the line that marks it generated, `# Code generated by okdp-compile. DO NOT EDIT.`; OKDP's own objects (`Result.Objects`) under `okdp.objects` |
| `okdp.dependencies.yaml` | `Result.Dependencies`: the instances the connection references point to ([`okdp.dependencies.yaml`](#okdpdependenciesyaml)) |

The keys are the dependencies of the package, the
[okdp chart](specs/01_inputs_outputs.md#the-okdp-chart), and in `dcr` mode
one per [OAuth client chart](specs/01_inputs_outputs.md#oauth-client-charts).
A dependency the values file leaves out (its key left out by `$if`, or
null) is no dependency of the umbrella; one it does not write is, with its
defaults
([rendered dependencies](specs/02_service_definition.md#rendered-dependencies)).

The chart has no `values.yaml`: its values are given to Helm as user
values, with `-f`. As the chart's `values.yaml` they would be its defaults,
and Helm drops the nulls of defaults, where a null of the final values
deletes an upstream default
([layers and nulls](specs/01_inputs_outputs.md#layers-and-nulls)).

The example's, `out/` here, are `examples/02_advanced/output-no-dcr/`:

```console
$ bin/okdp-compile --package examples/02_advanced/input/service \
    --release demo-webapp --namespace demo \
    --context examples/02_advanced/input/platform.yaml -f examples/02_advanced/input/config.yaml -o out
$ ls out
Chart.yaml
instance.values.yaml
okdp.dependencies.yaml
$ cat out/Chart.yaml
apiVersion: v2
name: webapp
version: 1.0.0
dependencies:
  - name: app
    repository: https://charts.example.org
    version: 1.8.0
  - name: okdp-objects
    alias: okdp
    repository: oci://registry.example.org/okdp
    version: 1.0.0
```

In `dcr` mode (`global.okdp.oidc.clientProvisioning: dcr`), the webapp's
OAuth client adds the key `oidc-dcr`, which `Chart.yaml` does not list,
any more than `okdp`: a dependency between `app` and `okdp`, the
platform's oidc-dcr chart.
The example's, with `platform-dcr.yaml` after `platform.yaml`, are
`examples/02_advanced/output-dcr/`.

The full example compiles an instance of a project: `demo.yaml`, a project
file at level 1 (`--context 1:demo.yaml`), between the platform's file and
the instance's, adds the project's settings and connections, and may change
none of the platform's; `connections.yaml`, at level 2 (`--context
2:connections.yaml`), adds the instance's own connection, and may change
none of theirs ([levels](specs/01_inputs_outputs.md#levels)).
Its outputs, `examples/03_full/output/`, list as dependencies those of the
package the values file renders, in `Chart.yaml` order (`api`,
`scheduler`, `gateway`, `metrics`: `backup`, left out, is not among them),
then the chart of each OAuth client in `dcr` mode, by key, then `okdp`:

```console
$ in=examples/03_full/input
$ bin/okdp-compile --package $in/service --charts $in/charts --contracts $in/contracts \
    --release demo-workspace --namespace demo \
    --context $in/platform.yaml --context 1:$in/demo.yaml --context 2:$in/connections.yaml \
    -f $in/config.yaml -o out-full
$ ls out-full
Chart.yaml
instance.values.yaml
okdp.dependencies.yaml
```

The directory is created if needed, and the three files written into it,
overwriting those of the same name; nothing else in it is read, removed
or checked. A directory an earlier version wrote may hold files this one
no longer writes, a values file per chart, `<key>.values.yaml` (without
`--umbrella`), and `install.sh` (`--mkinstall`): remove them, or write to
another directory.

### `okdp.dependencies.yaml`

In the output directory, the chart's directory: Helm ignores it when it
renders, and packages it with the chart. It holds `Result.Dependencies`,
a YAML list, `[]` when there is
none: one item per connection reference to another instance of the
namespace, `instance` (its release name), `contract` and `field` (the
parameter that references it)
([dependencies](specs/01_inputs_outputs.md#dependencies)). Helm does not
read it: it is for a deployer that installs the referenced instances first
([GitOps](#gitops)).

The example's, the instance referencing `demo-db` in `database`:

```console
$ bin/okdp-compile --package examples/02_advanced/input/service \
    --release demo-webapp --namespace demo \
    --context examples/02_advanced/input/platform.yaml -f examples/02_advanced/input/config.yaml \
    -o out
$ cat out/okdp.dependencies.yaml
- instance: demo-db
  contract: database
  field: database
```

### Exit status and errors

| Status | When |
|---|---|
| 0 | compiled; `--version`, `--help` |
| 1 | any error: `error: <message>` on stderr |
| 2 | unknown flag, a flag without its argument, an argument that is not a flag: the message and the usage on stderr |

A message is a whole report and may span several lines: every schema
violation, every message of the mapping's `errors` section.

```console
$ bin/okdp-compile --package examples/02_advanced/input/service \
    --release demo-webapp --namespace demo \
    --context examples/02_advanced/input/platform.yaml -f testdata/schema-violation.yaml
error: values don't meet the specifications of the schema(s) in the following chart(s):
webapp:
- at '/database': got number, want string
- at '/replicas': minimum: got 0, want 1
```

### Rendering with Helm

Render the umbrella under the release and namespace of the compile, its
dependencies built first:

```bash
helm dependency build <dir>
helm template <release> <dir> --namespace <namespace> -f <dir>/instance.values.yaml
```

`helm dependency build` adds `Chart.lock` and `charts/` to the directory,
which the next compile into it refuses: build a copy, or remove them
first. Do not
add OKDP's defaults or the values files of the compile: the final values
already merge the chart's own defaults, OKDP's defaults, the computed
values and the instance's upstream values ([merge
precedence](specs/01_inputs_outputs.md#merge-precedence)). The example's
charts are fictional, and its chart cache,
`examples/02_advanced/input/charts/`, holds only their `values.yaml`: its
umbrella compiles, it does not render. The okdp chart writes each object
of its values (`okdp.objects` of `instance.values.yaml`) as a manifest:
for the example, the descriptor ConfigMap `demo-webapp-okdp`.

Installed by hand, as one release, its dependencies built in a copy of
`Chart.yaml`; the OAuth client charts need no order, their Job being a
pre-install hook:

```bash
chart=$(mktemp -d) && cp <dir>/Chart.yaml "$chart" && helm dependency build "$chart"
helm upgrade --install <release> "$chart" --namespace <namespace> -f <dir>/instance.values.yaml
```

Each upstream chart renders as a subchart, with the final values of its
key, as it renders alone, but for what Helm does to every subchart
(checked with Helm 4.3.0 and the versions the deployers embed,
[GitOps](#gitops), [umbrella chart](specs/01_inputs_outputs.md#umbrella-chart)):

- an aliased subchart is named after its alias: its `.Chart.Name` is the
  key, not the chart's name. A name the chart derives from `.Chart.Name`
  changes, unless the values set it (`fullnameOverride`, `nameOverride`).
  The key `okdp` (chart `okdp-objects`) is aliased: the objects chart writes
  its objects as they are;
- every subchart has a `global` map, empty when nothing sets one;
- a null default nested in a map of the chart's `values.yaml`, which the
  values leave unset, stays null, where the chart alone drops it. A template
  reads both the same, except `hasKey`, `toYaml`, and `range` over the map.

With Helm 3.19, that null default is dropped, and a key below the top
level that the chart's `values.yaml` and the values both set to null is
deleted, not kept null. A null of the final values deletes the upstream
default with every version.

One key renders alone with its upstream chart, `Chart.yaml`'s dependency
of that alias (or name), and its values, the key's map of
`instance.values.yaml`, which holds its own `global`:

```bash
yq '.app' <dir>/instance.values.yaml > app.values.yaml
helm template <release> app --repo https://charts.example.org --version 1.8.0 --namespace <namespace> -f app.values.yaml
```

## Go library

`go get github.com/okdp/okdp-compiler@<version>` where the module can be
fetched at its path; from a checkout next to the module:

```bash
go mod edit -replace github.com/okdp/okdp-compiler=../okdp-compiler
go mod tidy
```

From a checkout, with `os.DirFS` (run from the repository root):

```go
package main

import (
	"context"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"

	compiler "github.com/okdp/okdp-compiler"
)

func main() {
	def, err := compiler.LoadDefinition(os.DirFS("examples/02_advanced/input/service")) // the package
	if err != nil {
		log.Fatal(err)
	}
	in := compiler.Inputs{
		Charts:    os.DirFS("examples/02_advanced/input/charts"), // the chart cache
		Contracts: os.DirFS("examples/02_advanced/input/contracts"),
		Release:   "demo-webapp",
		Namespace: "demo",
	}
	// The values layers, as helm -f files: the platform's, then the
	// instance's.
	read := func(f string) map[string]any {
		b, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		v, err := compiler.ParseValues(b)
		if err != nil {
			log.Fatalf("%s: %v", f, err)
		}
		return v
	}
	in.Context = []compiler.ContextLayer{{Values: read("examples/02_advanced/input/platform.yaml")}}
	in.Values = []map[string]any{read("examples/02_advanced/input/config.yaml")}
	res, err := compiler.Compile(context.Background(), def, in)
	if err != nil {
		log.Fatal(err) // the whole report, e.g. every schema violation
	}
	fmt.Println("charts:", len(res.Values), "objects:", len(res.Objects))
	fmt.Println("replicaCount:", res.Values["app"]["replicaCount"])
	// Each chart key is rendered by the upstream chart res.Charts[key].
	for _, key := range slices.Sorted(maps.Keys(res.Charts)) {
		c := res.Charts[key]
		fmt.Printf("%s: %s %s %s\n", key, c.Repository, c.Chart, c.Version)
	}
	for _, d := range res.Dependencies {
		fmt.Printf("dependency: %s -> %s (%s)\n", d.Field, d.Instance, d.Contract)
	}
}
```

```text
charts: 2 objects: 1
replicaCount: 2
app: https://charts.example.org app 1.8.0
okdp: oci://registry.example.org/okdp okdp-objects 1.0.0
dependency: database -> demo-db (database)
```

From memory, with `fstest.MapFS`: no file is read, and the result is
written as `-o` writes it, in memory.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"testing/fstest"

	compiler "github.com/okdp/okdp-compiler"
)

func main() {
	def, err := compiler.LoadDefinition(fstest.MapFS{
		"Chart.yaml": {Data: []byte(`apiVersion: v2
name: demo
version: 1.2.3
dependencies:
  - {name: app, repository: https://charts.example.org, version: 1.0.0}
`)},
		"okdp.yaml":          {Data: []byte("defaults: {app: app.defaults.yaml}\n")},
		"values.cel.yaml":    {Data: []byte("app:\n  replicaCount: ${{ values.replicas }}\n")},
		"config.schema.json": {Data: []byte(`{"properties": {"replicas": {"type": "integer", "default": 1}}}`)},
		"app.defaults.yaml":  {Data: []byte("image: {tag: \"4.5\"}\n")},
	})
	if err != nil {
		log.Fatal(err)
	}
	platform := map[string]any{"global": map[string]any{"okdp": map[string]any{"charts": map[string]any{
		"objects": map[string]any{"repository": "oci://registry.example.org/okdp", "chart": "okdp-objects", "version": "1.0.0"},
	}}}}
	res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
		Charts: fstest.MapFS{ // the defaults of each upstream chart
			"charts.example.org/app-1.0.0/app/values.yaml":                          {Data: []byte("replicaCount: 1\nimage: {tag: latest}\n")},
			"registry.example.org/okdp/okdp-objects-1.0.0/okdp-objects/values.yaml": {Data: []byte("objects: []\n")},
		},
		Contracts: fstest.MapFS{}, // no connection, no provides: no contract read
		Context:   []compiler.ContextLayer{{Values: platform}},
		Values:    []map[string]any{{"replicas": 3}},
		Release:   "demo-app",
		Namespace: "demo",
	})
	if err != nil {
		log.Fatal(err)
	}
	files, err := compiler.Write(res) // the umbrella chart
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(files["Chart.yaml"]))
}
```

```text
apiVersion: v2
name: demo
version: 1.2.3
dependencies:
  - name: app
    repository: https://charts.example.org
    version: 1.0.0
  - name: okdp-objects
    alias: okdp
    repository: oci://registry.example.org/okdp
    version: 1.0.0
```

Both programs are examples of `example_test.go` (`ExampleCompile`,
`ExampleWrite`, as README's is `Example`): `go test` runs them, and checks
that they are the programs above and print what they show
(`TestDocPrograms`).

Any `fs.FS` works: `embed.FS` (`fs.Sub` for a subdirectory), a git tree.

`compiler.LoadDefinition(fsys)` reads the package at the root of `fsys`:
`Chart.yaml`, `okdp.yaml` and `values.cel.yaml`, then keeps `fsys`
(`Definition.Files`) for the files each compile reads, `config.schema.json`
and the defaults files. A `Definition` can also be built in Go: the
`Chart.yaml` metadata (`Chart`), the package keys of `okdp.yaml`
(`Package`; `Package.Require` is a list of keys), and `okdp.yaml` and the
values file as bytes (`Okdp`, `Values`, nil when absent).

`compiler.Inputs` takes what the flags give: `Charts` (`--charts`),
`Contracts` (`--contracts`; it may be empty when nothing references a
contract), `Context` (the `--context` files, parsed, in order, each a
`ContextLayer` of its level), `Values` (the `-f` files, parsed, in order),
`Release` and `Namespace`; all but `Context` and `Values` are required
([inputs and outputs](specs/01_inputs_outputs.md)). `Result.Values` is what
`-o` writes in `instance.values.yaml`, `Result.Charts` the index the
umbrella's `Chart.yaml` names the charts from, `Result.Dependencies` what
it writes in `okdp.dependencies.yaml` and `Result.Objects` OKDP's objects (the values of the
`okdp` chart) ([outputs](specs/01_inputs_outputs.md#outputs)).
`Result.Values` renders as in [rendering with Helm](#rendering-with-helm),
`Result.Charts` being the index.

`compiler.Write(res)` gives the files `okdp-compile -o` writes (name ->
content), for a caller to write where it keeps them: the umbrella chart
(the package's `Chart.yaml`, `Result.Chart`, its dependencies the compiled
charts), its
values (`compiler.UmbrellaValues`) and the dependencies
(`compiler.DependenciesFile`).

- `compiler.ParseValues(b)` reads one values file as Helm 3 does (YAML 1.1,
  the first document of the file), for `Inputs.Context` and
  `Inputs.Values`. A layer built in Go may hold other types than
  `map[string]any` and `[]any` (`map[string]string`, `[]string`, `int32`,
  a struct by its `json` tags): it is read as the values it would be once
  written to a file and read back
  ([values files](specs/01_inputs_outputs.md#values-files)); what a file
  cannot hold fails, its layer and path named.
- `compiler.ChartRef` is an upstream chart, `{Repository, Chart, Version}`:
  a dependency of `Chart.yaml` (its `repository`, `name`, `version`), an
  entry of `Result.Charts`. `compiler.ChartMetadata` is a `Chart.yaml`, the
  package's in `Result.Chart`.
- `okdp` is the chart key of
  [OKDP's own objects](specs/01_inputs_outputs.md#the-okdp-chart),
  `oidc-dcr` the chart key of a service's main
  [OAuth client](specs/01_inputs_outputs.md#oauth-client-charts), and the
  suffix of the others; oidc-dcr 0.5.0 of `oci://quay.io/adaltas` is the
  chart they are rendered by unless the platform names another
  ([platform charts](specs/01_inputs_outputs.md#platform-charts)): the
  chart a chart cache needs for a `dcr` platform.
- `Compile` keeps no state between calls and is safe for concurrent use: one
  `Definition` and the same values layers can be shared between goroutines;
  the inputs are not changed (`TestConcurrentCompile`, under `go test
  -race`).
- `compiler.Compile(ctx, def, in)` is cancelled with `ctx`: between the
  stages, and inside an expression at the next check of a comprehension
  (every 100 iterations); it then returns `ctx.Err()`.
  `Inputs.CostLimit` bounds the total CEL cost (cel-go's runtime cost:
  about one unit per operation, list element and character compared) of
  the compile's expressions, the mapping's and the compiler's templates'
  (OAuth clients, generated Secrets, the descriptor; 0: no limit): once
  the total passes it, the compile fails with
  `the expressions of the compile exceed its CEL cost limit (n)`. The
  total is checked after each expression, which may itself cost up to the
  limit: a compile can pass the limit by one expression's cost. A
  server compiling packages it does not review sets one, as an instance's
  values can make an expression iterate over them. The example costs under
  1000, the descriptor's template most of it (`ExampleCompile_server`).
- Errors are messages for the user, returned as they are; the package
  defines no error types. A message carries its context: the file, the
  parameter path, the chart key, the mapping section (see
  [troubleshooting](#troubleshooting)).

## Integration

### CI

Compile every instance of a package, each under its release name and
namespace; any error fails the job:

```bash
for f in instances/*.yaml; do
  name=$(basename "$f" .yaml)   # the instance's release name
  bin/okdp-compile --package package --charts charts --contracts contracts \
    --context platform.yaml -f "$f" --release "$name" --namespace demo \
    -o "compiled/$name" || exit 1
done
test -z "$(git status --porcelain compiled/)" || { git status --short compiled/; exit 1; }
```

The emitted files can be committed and checked against the commit, as
`go test .` checks the example's outputs: `git status
--porcelain` lists the changed, removed and new files, where
`git diff --exit-code` misses a new, untracked file (those of a new
instance).

### GitOps

The compiler writes values and objects, not HelmRelease or Application
objects. The values of every key are compiled for the instance's release
name and namespace: a deployment renders the umbrella under these, one
release per instance, one Flux HelmRelease or one Argo CD Application. OKDP
publishes no chart of the service: the upstream charts come from their own
repositories, the umbrella's dependencies.

**The umbrella.** Its directory is committed with the instance's compiled
files. Flux builds the chart from the deployments repository, and takes the
values from a ConfigMap of `instance.values.yaml` (a kustomize
`configMapGenerator`):

```yaml
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: demo-webapp
  namespace: demo
spec:
  releaseName: demo-webapp
  chart:
    spec:
      chart: ./compiled/demo-webapp
      sourceRef: {kind: GitRepository, name: deployments, namespace: flux-system}
      reconcileStrategy: Revision
  valuesFrom:
    - kind: ConfigMap
      name: demo-webapp-values
      valuesKey: instance.values.yaml
```

`reconcileStrategy: Revision` rebuilds the chart at every revision of the
repository: its version, the package's, does not change when the platform
changes the version of a dependency. `chart.spec.valuesFiles` would make the
values the chart's defaults, and drop their nulls. Argo CD takes the values
file with `-f`:

```yaml
spec:
  source:
    repoURL: https://git.example.org/deployments.git
    path: compiled/demo-webapp
    helm:
      releaseName: demo-webapp
      valueFiles: [instance.values.yaml]
```

Both fetch the dependencies from their repositories: a private repository
needs its credentials there. Each renders with the Helm it embeds (latest
patch releases, October 2026): Flux 2.9 Helm 4.2.4 (helm-controller 1.6), Flux 2.8 Helm 4.2.0
(helm-controller 1.5), Flux 2.7 Helm 3.19.2 (helm-controller 1.4); Argo CD
3.5 Helm 4.2.1, Argo CD 3.4 and 3.3 Helm 3.19.4. The umbrella, the final
values and the schema reports are checked with each ([rendering with
Helm](#rendering-with-helm), [Helm fixtures](00_architecture.md#helm-fixtures)).

**OAuth clients.** In `dcr` mode the index lists one chart per OAuth client
(`oidc-dcr`, `<name>-oidc-dcr`, rendered by the platform's oidc-dcr chart;
[OAuth client charts](specs/01_inputs_outputs.md#oauth-client-charts)),
dependencies of the umbrella like the others. Their Job, a Helm pre-install/pre-upgrade hook
(an Argo CD `PreSync` hook), writes the client's Secret; the pods that read
it do not start before it exists.

**OKDP's own objects.** `Result.Objects` are the values of the `okdp` chart,
which every compile adds
([the okdp chart](specs/01_inputs_outputs.md#the-okdp-chart)): the
umbrella's `okdp` dependency, the platform's objects chart
(`global.okdp.charts.objects`), brings the descriptor ConfigMap, the
credentials Secrets and the mapping's objects to the cluster. They are its
`objects`, a YAML list: `yq '.okdp.objects[] | split_doc'
out/instance.values.yaml` turns them into a multi-document stream.

**Dependencies.** `Result.Dependencies` lists the instances of the
namespace the connection references point to, one item per parameter
([dependencies](specs/01_inputs_outputs.md#dependencies)), written to
[`okdp.dependencies.yaml`](#okdpdependenciesyaml). The compiler does not
check that an instance exists: the deployment waits for it.

- Flux: on the instance's HelmRelease objects, one `spec.dependsOn` entry per
  referenced instance. With HelmReleases named after the instances:

  ```console
  $ yq '{"dependsOn": [.[].instance] | unique | map({"name": .})}' out/okdp.dependencies.yaml
  dependsOn:
    - name: demo-db
  ```

- Argo CD: give the instance's Application a sync wave
  (`argocd.argoproj.io/sync-wave`) greater than the waves of the instances
  it depends on, e.g. one more than the greatest. An app-of-apps waits for a
  child Application to be healthy only with a health check for Application
  resources configured in Argo CD.

## Troubleshooting

`<field>` is the parameter path (`database`, `backends[1].database`, a key
that holds a dot in brackets: `databases["eu.main"]`),
`<key>` a chart key (of a `Chart.yaml` dependency, `okdp`, or an OAuth
client chart), `<chart>-<version>` the upstream chart that renders it,
`<service>` the `name` of `Chart.yaml`. Every error exits with status 1, a
usage error with status 2.

### Inputs and instance values

| Message | Cause | Fix |
|---|---|---|
| `--package is required`, `--charts (or $OKDP_CHARTS) is required`, `--contracts (or $OKDP_CONTRACTS) is required`, `--release is required`, `--namespace is required` | a required flag is missing | give it |
| `Inputs.Release "Demo_App": a release name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 53 characters at most (Helm's limit)`, `Inputs.Namespace "<name>": a namespace name, ...` | `--release` or `--namespace` is not a name Kubernetes and Helm accept | give one of that form ([inputs](specs/01_inputs_outputs.md)) |
| `database: the database contract cannot be read: database.schema.json: no such file or directory`, `app: stat charts.example.org/app-1.8.0/app: not a directory` | a directory flag names no directory (here `--contracts`) or a file (here `--charts`): it fails at the first file read from it | fix the path |
| `unexpected argument "instance.yaml": every input is a flag (-f before each values file)` | an argument that is not a flag, here a second values file after one `-f`; exit status 2, nothing compiled | one `-f` per values file |
| `--output (-o) is required` | no output directory | give one: the command writes the compile there |
| `open Chart.yaml: no such file or directory` | `--package` is not the package's directory | point it at the directory of `Chart.yaml` |
| `Chart.yaml: apiVersion "v1": v2, the only one the compiler reads`, `Chart.yaml: name is required`, `Chart.yaml: version is required`, `Chart.yaml: type "library": a package is an application chart` | the package metadata is incomplete, or not that of an application chart of `apiVersion: v2` | set them ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: version "1.0": SemVer (1.2.3), the version of the umbrella chart` | a package version Helm does not accept as a chart's | a SemVer `version` in `Chart.yaml` |
| `Chart.yaml: yaml: unmarshal errors:` then `line 9: field condtion not found in type chart.Dependency`; `Chart.yaml: one YAML document` | a field Helm's `Chart.yaml` does not define, here a misspelt `condition` in a dependency, one line per unknown field; a second YAML document | fix the name; one document ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: dependencies[1] (api): condition: the values decide what is rendered ($if on api in values.cel.yaml)` | a dependency with `condition`, `tags`, `enabled` or `import-values` | remove it; leave the dependency out with `$if` on its key in the values file ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0] (my_app): key "my_app": a DNS label (lowercase letters, digits and -, a letter or digit at each end, 63 characters at most): the key names the umbrella's dependency alias` | a dependency key (its `alias`, else its `name`) that is not a DNS label: `../app`, an upper case letter, `_` or `.` | give it an `alias` that is one, and rename the key in `okdp.yaml` and the values file ([chart keys](specs/02_service_definition.md#chart-keys)) |
| `Chart.yaml: dependencies[2] (api): the key of dependencies[1] too: give one of them an alias` | two dependencies of one key: a chart rendered twice, one of them without its alias | an alias each ([recipe](02_writing_package.md#render-a-chart-twice)) |
| `Chart.yaml: dependencies[0].name is required` | a dependency without its chart's name | name the published chart that renders it ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0].repository "charts.example.org": the URL of a chart repository (https://... or oci://...)`, `Chart.yaml: dependencies[0].name "a/b": the name of a chart of the repository` | a repository that is not a URL (`https://`, `http://`, `oci://`) or whose host and path are no directory of the chart cache (`https://`, `https://x/../y`), a name that is not a chart's name | fix the dependency ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0].version "^1.8": a pinned chart version (1.2.3), not a range` | a version range | pin the version the chart cache holds |
| `Chart.yaml: dependencies[0] (okdp): the chart of OKDP's own objects is added by the compiler: remove it` | `Chart.yaml` lists `okdp` | remove it; the platform names its chart ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
| `okdp.yaml: yaml: unmarshal errors:` then `line 3: field requires not found in type okdp.Package` | a key `okdp.yaml` does not define, here a misspelt `require`; one line per unknown key | fix the name ([okdp.yaml](specs/02_service_definition.md#okdpyaml)) |
| `okdp.yaml: output: the charts' values are in values.cel.yaml`, `okdp.yaml: name: the name, version and charts are Chart.yaml's` | a key of the former package format | the values in `values.cel.yaml`, without the `output:` wrapper; the name, version and charts in `Chart.yaml` ([service definition](specs/02_service_definition.md)) |
| `okdp.yaml: defaults.web: not a dependency of Chart.yaml`, `okdp.yaml: upstream.web: not a dependency of Chart.yaml` | a key of `defaults` or `upstream` that is no dependency's key | fix the key, or add the dependency ([okdp.yaml](specs/02_service_definition.md#okdpyaml)) |
| `okdp.yaml: upstream: true, false or a map by dependency`, `okdp.yaml: upstream.app: true, false or {protect, append}` | `upstream`, or a dependency's value in it, of another form: a string, a number, a list; a map with another key than `protect` and `append` (a misspelt `protect`) | `true`, `false`, or a map by key, each `true`, `false` or `{protect, append}` ([upstream](specs/02_service_definition.md#upstream)) |
| `okdp.yaml: upstream.app.protect[0] "a[": not a path: ...` | a `protect` or `append` path out of the grammar | fix the path ([protect and append](specs/02_service_definition.md#protect-and-append)) |
| `okdp.yaml: yaml: unmarshal errors:` then `line 9: cannot unmarshal !!map into string` | a `require` entry of another form than a key (`{key, $if}` included) | a key; check a value of one mode in the mapping's `errors` ([require](specs/02_service_definition.md#require)) |
| `app: chart app-1.8.0 is not in the chart cache: helm pull app --repo https://charts.example.org --version 1.8.0 --untar -d <chart cache>/charts.example.org/app-1.8.0` | a chart the compile renders (a dependency the values file leaves out, by `$if` or null, is not read) is not unpacked in its directory (`charts.example.org/app-1.8.0/app/`), even when the chart cache holds its archive (`helm pull -d` without `--untar`) or the chart elsewhere; for an `oci://` repository the command is `helm pull oci://<repository>/<chart> --version <version> --untar -d <chart cache>/<repository>/<chart>-<version>` | run the command, `<chart cache>` the chart cache, or check `--charts` / `$OKDP_CHARTS` ([chart cache](specs/01_inputs_outputs.md#chart-cache)) |
| `global.okdp.charts.objects is required: the chart OKDP's own objects are rendered with (okdp-objects: repository, chart, version). It is a context value: set it with --context (Inputs.Context)` | the context names no objects chart | pass the platform values with `--context`; set it ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
| `global.okdp.charts.objects.version "1.x": a pinned chart version (1.2.3), not a range` | a platform chart checked as a source is (its repository, chart and version, the same messages); also `global.okdp.charts.oidcDcr`; its form is checked by the platform schema (below) | fix the platform values |
| `database: the database contract cannot be read: database.schema.json: no such file or directory` | no `<contract>.schema.json` in the contracts directory | check `--contracts` / `$OKDP_CONTRACTS` |
| `database: the database contract cannot be read: database.schema.json: unexpected EOF` | the contract file cannot be read, is not JSON, not an object or not a valid JSON Schema; also `provides "<name>" (<contract>): the <contract> contract cannot be read: ...` | fix the contract, or point `--contracts` at the contract schemas ([contracts](specs/01_inputs_outputs.md#location)) |
| `<file>: cannot unmarshal yaml document: error converting YAML to JSON: yaml: line 1: did not find expected node content` | a values file is not valid YAML | fix the file |
| `global.okdp.ingress.className is required. It is a context value: set it with --context (Inputs.Context)` | `require` in `okdp.yaml` lists it, and no context layer sets it (or a later one of its level sets it to `null`) | pass the platform values with `--context`; set the key ([context values](specs/01_inputs_outputs.md#context-values)) |
| `context layer 2 (level 1): global.okdp.ingress.suffix is set at level 0 (--context, Inputs.Context): a level adds values, it does not change a lower level's`, `context layer 2 (level 1): connections.platform-db is set at level 0 ...` | a context file, the second `--context` flag (`Inputs.Context[1]`), of level 1, sets a value a file of a lower level sets (the same value and `null` included, or a parent of it to a non-map), or gives a connection of a lower level's connection's name, even by a field it leaves out | remove it from the file: a level only adds keys and connections; change the value in the lower level's files ([levels](specs/01_inputs_outputs.md#levels)) |
| `values layer 1: global.okdp is set by the context (--context, Inputs.Context)`, `values layer 2: global is set by the context ...` | an instance file, the first, second... `-f` file (`Inputs.Values[0]`, `[1]`...), sets `global`: a platform value, a key of `global` the parameter schema does not declare, or `global` itself to a non-map | give platform values with `--context`; a package lets instances set a key of `global` by declaring it in `values.schema.json` ([context values](specs/01_inputs_outputs.md#context-values)) |
| `values layer 1: connections are given by the context (--context, Inputs.Context): an instance's own in a context layer of its level` | an instance file (`-f`) sets `connections`, to any value | give the instance's own connections in a context file of its level (`--context 2:<file>`); a connection of a lower level is not changed ([context values](specs/01_inputs_outputs.md#context-values)) |
| `values layer 1: database: the value contains "{{"`, `values layer 2: upstream.app.podAnnotations.{{ x }}: the key contains "{{"` | template code in an instance file (`-f`), in a key or a value under any key: parameters, upstream values; `context layer <n> (level <l>): ...` in a context file above level 0 (`--context 1:<file>`); the platform's files, level 0, are not checked | remove it ([layers](specs/01_inputs_outputs.md#layers)) |
| `values layer 2: json: cannot marshal from Go func() within "/hooks/0"`, `context layer 1 (level 0): json: cannot marshal from Go float64 within "/global/proxy/no.proxy": unsupported value: NaN` | a values layer built in Go, the second of `Inputs.Values`, the first of `Inputs.Context`, holds what a values file cannot: a func, a chan, a complex, a NaN or an infinity, text that is not UTF-8, a key that is not text or a number, two keys that read as the same, a value that contains itself | build the layer of values a file can hold ([values files](specs/01_inputs_outputs.md#values-files)) |
| `values don't meet the specifications of the schema(s) in the following chart(s):` then `<service>:` and `- at '/replicas': minimum: got 0, want 1` | the parameters violate the package's `config.schema.json`; every violation is listed | fix the instance values ([parameters](specs/01_inputs_outputs.md#parameters)) |
| `compiled values don't meet the specifications of the schema(s) in the following chart(s):` then `<key> (<chart>-<version>):` (`app (app-1.8.0):`) and `- at '/replicaCount': maximum: got 3, want 2` | the final values violate the `values.schema.json` of the upstream chart | fix the instance's upstream values; if they are not involved, OKDP's defaults or the mapping |
| `database: connection "store" has contract "s3", "database" is expected` | the parameter references a `connections` entry of another contract | reference a connection of the expected contract |
| `database: connection "shared-db": field "user" is not part of the database contract` | an external connection has a field its contract does not declare | remove it ([connections](specs/01_inputs_outputs.md#connections)) |
| `database: connection "shared-db": "password" is a secret field, put it in the credentials Secret` | a secret field written in the connection | put it in a Secret named by `secretRef` |
| `database (connection "shared-db"): field "dbName" is required by the database contract` | a required contract field is missing; one line per missing field | set it |
| `database (connection "shared-db"): the fields violate the database contract:` then `- at '/engine': value must be one of 'postgresql', 'mysql'`, `- at '/port': got string, want integer` | a field of an external connection the contract's schema refuses; one line per violation | give a value of the contract's type (`port: 5432`, not `"5432"`) and among its values ([checks](specs/01_inputs_outputs.md#checks)) |
| `<field>: "<name>" is not a connection, and the <contract> contract has no internal naming convention` | the name is not in `connections`, and the contract cannot resolve an instance | add the connection under `connections` of a context file (`--context 2:<file>` for the instance's own) |
| `<field>: "<name>" is not a connection, nor a release name: lowercase letters, digits and "-", a letter or digit at each end, 53 characters at most` | the name is not in `connections`, and cannot name an instance of the namespace (`Shared_DB`, a name longer than 53 characters) | fix the name, or add the connection under `connections` of a context file |
| `database: a connection reference must be a string, got map[string]interface {}{"host":"db.example.org"}` | a connection reference given as a map, a number or a list (the parameter schema does not say `"type": "string"`) | give the name of a connection or an instance; put the connection itself under `connections` |
| `database (connection "shared-db"): field "host": the value contains "{{"` | template code in a connection given by the platform's values, or in a contract default (a context file above level 0 fails earlier, `context layer <n> (level <l>): ...`) | fix the platform values or the contract |
| several lines of the connection messages above, one `<field>` each | several references do not resolve: every one is reported, in parameter path order (keys sorted at each level), with its first error (the fields of a connection in alphabetical order), or all its missing required fields, or all its schema violations | fix each, then compile again: a fixed field may reveal the next one ([connection errors](specs/01_inputs_outputs.md#connection-errors)) |
| `webapp: database connection "shared-db" has no secretRef, the password cannot be read` | not a compiler check: a message of the example mapping's `errors` section | add `secretRef: {name: ...}` to the connection |
| `app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed` | the instance's upstream values set a path `okdp.yaml` protects (`upstream.<key>.protect`, and always `global`) | remove the value; set it through the parameters when the package offers one |
| `app: upstream.app.podAnnotations.x: the value contains "{{"`, `... the key contains "{{"` | template code in upstream values the platform's values or the package's defaults give (in an instance's, its values file fails before: `values layer <n>: ...`) | remove it |
| `app: upstream.app.env must be a list` | a path of `append` given as a non-list | give a list |
| `context values (--context, Inputs.Context) don't meet the specifications of the schema of global.okdp:` then `- at '/global/okdp/oidc': got boolean, want object`, `- at '/global/okdp/oidc/clientProvisioning': value must be one of 'existing', 'dcr'` | a platform setting the compiler reads, or a map on the way to it, of another type (`oidc: false`, `proxy: <url>`, a list for a string), or not among its values; it would otherwise read as not set. Every violation is listed | fix the platform values against `platform.schema.json`; `null` is not set ([fields the compiler reads](specs/01_inputs_outputs.md#fields-the-compiler-reads)) |
| `failing loading "<url>": no URLLoader registered for "<url>"` | a `config.schema.json`, or an upstream chart's `values.schema.json`, references a file, a URL or a `urn:` | inline the referenced schema |

### Mapping

The errors of the mapping, the sections of `okdp.yaml` and the values file,
mostly for package authors, are listed
with their causes in [service definition](specs/02_service_definition.md#errors):
the messages of its `errors` section, one per line, as written; CEL errors
prefixed with the file, then the section, key and list position path
(`values.cel.yaml: app: env[2]: value: ${{ ... }}: no such key: host`,
`okdp.yaml: vars db: ...`); a key of the values file that is no
dependency's (`values.cel.yaml: web: not a dependency of Chart.yaml`);
sections of the wrong shape, against `mapping.schema.json`,
every violation at once
(`the mapping's sections don't meet the specifications of mapping.schema.json:`
then `- at '/output/app': got string, want null or object`); arguments of
`okdpGeneratedSecret` unknown or of the wrong type
(`- at '/okdpGeneratedSecret/keys/0': additional properties 'lenght' not allowed`);
YAML errors (`values.cel.yaml: yaml: line <n>: ...`,
`okdp.yaml: yaml: line <n>: ...`). The `provides` checks are in [descriptor ConfigMap](specs/01_inputs_outputs.md#descriptor-configmap),
the OAuth client errors [below](#oauth-clients).

### OAuth clients

The messages of the `oidcClients` checks are listed with the mapping's
([compile errors](specs/02_service_definition.md#compile-errors)),
those of the platform's settings `global.okdp.oidc.dcr` in
[OAuth client settings](specs/01_inputs_outputs.md#oauth-client-settings).
The mapping schema names a client by its path, `/oidcClients/<i>`, and
reports every violation at once; another message names it
`oidcClients[<i>]`, its position in the evaluated list, then ` (<name>)`
for a named client, the first failing check stopping the compile. Where
the cause or the fix is not in the message:

| Message | Cause | Fix |
|---|---|---|
| `context values (--context, Inputs.Context) don't meet ...` then `- at '/global/okdp/oidc/dcr/ttlSeconds': got string, want integer` | a platform setting of the wrong type, here a quoted number; the types of the settings are checked in every mode, with or without a client | fix the platform values; `null` takes the default |
| `- at '/oidcClients/0': additional properties 'redirectUri' not allowed` | a typo, or a registration field written as a client key | use a client key (`mapping.schema.json`); a registration field goes under `request` |
| `global.okdp.oidc.dcr.authMethod "" is not supported: OKDP registers its OAuth clients anonymously` | `dcr` mode, at least one client, and `authMethod` unset (`""`, as when the platform sets no `dcr` settings) or not `anonymous` | `authMethod: anonymous` in the platform values |
| `global.okdp.oidc.dcr.registrationUrl is required (clientProvisioning: dcr): the endpoint the oidc-dcr Jobs register the OAuth clients at` | `dcr` mode, at least one client, and no `registrationUrl` (`testdata/context/0/dcr-without-registration-url.yaml`) | set it in the platform values |
| `Chart.yaml: dependencies[1] (oidc-dcr): the chart of oidcClients[0] is added by the compiler: remove it` | `Chart.yaml` lists the chart key of a client | remove it: the compiler adds the client charts; give the dependency another key (`alias`), or rename the client, when it is another chart |
| `values.cel.yaml: oidc-dcr: the chart of oidcClients[0] is compiled from oidcClients: remove it` | the values file computes values for the chart key of a client | remove it; set registration fields with the client's `request` |
| `oidc-dcr: chart oidc-dcr-0.5.0 is not in the chart cache: helm pull oci://quay.io/adaltas/oidc-dcr --version 0.5.0 --untar -d <chart cache>/quay.io/adaltas/oidc-dcr-0.5.0` | `dcr` mode, at least one client, and the chart cache lacks the platform's oidc-dcr chart (`existing` mode does not need it) | pull it into the chart cache ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
