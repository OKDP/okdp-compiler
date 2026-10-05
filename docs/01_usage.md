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
(`$(go env GOPATH)/bin` by default); `--version` prints `X.Y.Z`. The go
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
outside a checkout, nothing, and the binary reads its build info (below).
`VERSION` overrides it:

```console
$ make build VERSION=1.2.3
go build -ldflags "-X main.version=1.2.3" -o bin/okdp-compile ./cmd/okdp-compile
$ bin/okdp-compile --version
1.2.3
```

Without these `-ldflags` (`go build ./cmd/okdp-compile`, or
`go install ./cmd/okdp-compile`, which installs it in `$GOBIN`),
`--version` prints the version Go records in the binary, without its
leading `v`: in a checkout, a pseudo-version Go derives from the last tag
and the commit, `0.1.1-0.20261012192101-1872c2523100`, `+dirty` with
uncommitted changes; `dev` when Go records none (`-buildvcs=false`,
sources outside git).

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
okdp-compile --package <dir> [--charts <dir>] [--contracts <dir>] [--instance <dir>]
             [--platform <platform.yaml>]... [-f <values.yaml>]...
             --release <name> --namespace <name>
             [--out <file>] [--emit-dir <dir>] [--umbrella <dir>] [--indent <n>]
okdp-compile --version
```

| Flag | Default | |
|---|---|---|
| `--package` | required | package directory: `service.yaml`, `mapping.cel.yaml`, `values.yaml`, `values.schema.json` at its root, and the base files `service.yaml` names ([package files](specs/01_inputs_outputs.md#package-files)) |
| `--charts` | `$OKDP_CHARTS` | chart cache directory: each upstream chart in its repository's directory, the URL without its scheme, as `helm pull -d <dir>/<repository>` writes it, `<repository>/<chart>-<version>.tgz`, or unpacked, `<repository>/<chart>-<version>/` (`quay.io/adaltas/oidc-dcr-0.5.0.tgz`); else `<chart>-<version>` at its root ([chart cache](specs/01_inputs_outputs.md#chart-cache)); required, by flag or variable |
| `--contracts` | `$OKDP_CONTRACTS` | contract schemas directory: `<contract>.schema.json` ([contracts](specs/01_inputs_outputs.md#contracts)); required, by flag or variable |
| `--instance` | none | instance directory: the upstream values files `service.yaml` names (`upstream.<key>.yaml`, by chart key; [instance values](specs/01_inputs_outputs.md#instance-values)) |
| `--platform` | none | a platform values file: `global`, `global.okdp` among them ([platform values](specs/01_inputs_outputs.md#platform-values)); repeatable, merged in order as `helm -f` files, before the `-f` files |
| `-f` | none | an instance values file; repeatable, merged in order as `helm -f` files, after the `--platform` files. It sets no `global` but the keys the parameter schema declares under it ([instance values](specs/01_inputs_outputs.md#instance-values), [merge precedence](specs/01_inputs_outputs.md#merge-precedence)) |
| `--release` | required | release name: the Helm release the values are compiled for, which renders them; a DNS-1123 label (lowercase letters, digits and `-`) of 53 characters at most, as Helm accepts it |
| `--namespace` | required | namespace: the release's; a DNS-1123 label of 63 characters at most |
| `--out` | none | write the computed values, the chart index, the objects and the dependencies to one YAML file, out of the directories below |
| `--emit-dir` | none | write the final values of each chart (OKDP's objects in `okdp.values.yaml`), the chart index and the dependencies to a directory, one file each; it holds only these files |
| `--umbrella` | none | write the instance as one chart to a directory: `Chart.yaml`, an umbrella over the upstream charts, and `instance.values.yaml`, its values; a directory other than `--emit-dir`'s, holding only these files |
| `--indent` | `2` | the indentation of the YAML files `--out`, `--emit-dir` and `--umbrella` write, 2 to 9 spaces |
| `--version` | | print the version and exit |

Flags take one or two dashes, and every input is a flag: an argument that
is not one is refused, where it would end the flags and those after it be
ignored (a values file without its `-f`). The directories of `--package`,
`--charts`, `--contracts` and `--instance` must exist. Without `--out`,
`--emit-dir` or `--umbrella`, the command only compiles: exit status 0
means the instance compiles. Every output is built before any is written:
a compile or an output that fails writes nothing.

### `--out`

One YAML document, keys sorted: `charts` (`Result.Charts`, the
[chart index](specs/01_inputs_outputs.md#chart-index): chart key ->
the upstream chart that renders it, `repository`, `chart`, `version`),
`dependencies` (`Result.Dependencies`),
`objects` (`Result.Objects`) and `values` (`Result.Computed`: chart key ->
what the mapping computed, before the merge with the chart defaults, the
base values and the instance's upstream values; in `dcr` mode, also the
values of the [OAuth client charts](specs/01_inputs_outputs.md#oauth-client-charts)).
The final values are written by `--emit-dir`, not by `--out`.

```console
$ export OKDP_CONTRACTS=example/input/contracts OKDP_CHARTS=example/input/charts
$ bin/okdp-compile --package example/input/package --instance example/input/instance \
    --release demo-webapp --namespace demo \
    --platform example/input/platform.yaml -f example/input/instance.yaml --out /dev/stdout
charts:
  app:
    repository: https://charts.example.org
    chart: app
    version: 1.8.0
  okdp:
    repository: oci://registry.example.org/okdp
    chart: okdp-objects
    version: 1.0.0
dependencies:
  - instance: demo-db
    contract: database
    field: database
objects:
  - apiVersion: v1
    # ... ConfigMap demo-webapp-okdp, the descriptor
values:
  app:
    # ...
    replicaCount: 2
  okdp:
    objects:
      # ... the descriptor
```

The computed values of a chart hold only what the mapping sets:

```console
$ bin/okdp-compile --package example/input/package --instance example/input/instance \
    --release demo-webapp --namespace demo \
    --platform example/input/platform.yaml -f example/input/instance.yaml \
    --out compiled.yaml --emit-dir out
$ yq '.values.app.image' compiled.yaml
tag: "2.3"
$ yq '.image' out/app.values.yaml
repository: example/webapp
tag: "2.3"
```

### `--emit-dir`

The directory is created if needed, and holds these files only:

| File | Content |
|---|---|
| `<key>.values.yaml` | `Result.Values[key]`: the final values of the chart key, one file per chart the mapping outputs, for the [okdp chart](specs/01_inputs_outputs.md#the-okdp-chart) (OKDP's own objects, `Result.Objects`, under `objects`), and per [OAuth client chart](specs/01_inputs_outputs.md#oauth-client-charts) in `dcr` mode |
| `okdp.charts.yaml` | `Result.Charts`, the [chart index](specs/01_inputs_outputs.md#chart-index): `<key>: {repository, chart, version}`, one entry per values file |
| `okdp.dependencies.yaml` | `Result.Dependencies`: a YAML list, `[]` when there is none |

The example's, `out/` here, are `example/output/files/`:

```console
$ bin/okdp-compile --package example/input/package --instance example/input/instance \
    --release demo-webapp --namespace demo \
    --platform example/input/platform.yaml -f example/input/instance.yaml --emit-dir out
$ ls out
app.values.yaml
okdp.charts.yaml
okdp.dependencies.yaml
okdp.values.yaml
$ cat out/okdp.charts.yaml
app:
  repository: https://charts.example.org
  chart: app
  version: 1.8.0
okdp:
  repository: oci://registry.example.org/okdp
  chart: okdp-objects
  version: 1.0.0
```

In `dcr` mode (`global.okdp.oidc.clientProvisioning: dcr`), the webapp's
OAuth client adds the chart `oidc-dcr`, which `service.yaml` does not list,
any more than `okdp`: `oidc-dcr.values.yaml`, and its entry in the index.
The example's, with `platform-dcr.yaml` after `platform.yaml`, are
`example/output/dcr/`:

```console
$ bin/okdp-compile --package example/input/package --instance example/input/instance \
    --release demo-webapp --namespace demo \
    --platform example/input/platform.yaml --platform example/input/platform-dcr.yaml \
    -f example/input/instance.yaml --emit-dir out-dcr
$ ls out-dcr
app.values.yaml
oidc-dcr.values.yaml
okdp.charts.yaml
okdp.dependencies.yaml
okdp.values.yaml
```

A chart the mapping leaves out (`$if`) has no file and no index entry.

Before anything is written, the files of the directory are checked: those
of a previous compile are overwritten, and removed when this one does not
write them (a chart no longer compiled); any other file or directory, or a
file of these names that is not a regular file (a symbolic link), fails the
compile, so that a directory given by mistake loses nothing:

```
refusing to write in out: notes.txt is not an output of okdp-compile
```

### `--umbrella`

The instance as one chart, for one Helm release per instance
([GitOps](#gitops)). The directory is created if needed, and holds these
files only, checked as `--emit-dir`'s are; it cannot be the directory of
`--emit-dir`:

| File | Content |
|---|---|
| `Chart.yaml` | an umbrella chart, named and versioned after `service.yaml` (`name`, `version`, `appVersion`), with one dependency per key of the index: its upstream chart (repository, name, version), aliased to the key when the key is not the chart's name |
| `instance.values.yaml` | `Result.Values`: the final values of each key, under the key |

The chart has no `values.yaml`: its values are given to Helm as user
values, with `-f`. As the chart's `values.yaml` they would be its defaults,
and Helm drops the nulls of defaults, where a null of the final values
deletes an upstream default
([layers and nulls](specs/01_inputs_outputs.md#layers-and-nulls)).

The example's are `example/output/umbrella/`:

```console
$ bin/okdp-compile --package example/input/package --instance example/input/instance \
    --release demo-webapp --namespace demo \
    --platform example/input/platform.yaml -f example/input/instance.yaml --umbrella umbrella
$ cat umbrella/Chart.yaml
apiVersion: v2
name: webapp
version: 1.0.0
appVersion: "2.3"
dependencies:
  - name: app
    repository: https://charts.example.org
    version: 1.8.0
  - name: okdp-objects
    alias: okdp
    repository: oci://registry.example.org/okdp
    version: 1.0.0
```

Rendered, under the release and namespace of the compile (the example's
charts are fictional: its umbrella compiles, it does not render):

```bash
helm dependency build <dir>
helm template <release> <dir> --namespace <namespace> -f <dir>/instance.values.yaml
```

`helm dependency build` adds `Chart.lock` and `charts/` to the directory,
which the next compile into it refuses: build a copy, or remove them
first.

Each upstream chart renders as a subchart, with the final values of its
key, as it renders alone ([rendering with Helm](#rendering-with-helm)), but
for what Helm does to every subchart (checked with Helm 4.3.0 and the
versions the deployers embed, [GitOps](#gitops),
[umbrella chart](specs/01_inputs_outputs.md#umbrella-chart)):

- an aliased subchart is named after its alias: its `.Chart.Name` is the
  key, not the chart's name. A name the chart derives from `.Chart.Name`
  changes, unless the values set it (`fullnameOverride`, `nameOverride`).
  The key `okdp` (chart `okdp-objects`) is aliased: the objects chart writes
  its objects as they are;
- every subchart has a `global` map, empty when nothing sets one;
- a null default nested in a map of the chart's `values.yaml`, which the
  values leave unset, stays null, where the chart alone drops it. A template
  reads both the same, except `hasKey`, `toYaml`, and `range` over the map.

With Helm 3.19, that null default is dropped, a key below the top level
that the chart's `values.yaml` and the values both set to null is deleted,
not kept null, and only the first document of a `values.yaml` is read: an
upstream chart whose `values.yaml` has several documents needs Helm 4. A
null of the final values deletes the upstream default with every version.

### Exit status and errors

| Status | When |
|---|---|
| 0 | compiled; `--version`, `--help` |
| 1 | any error: `error: <message>` on stderr |
| 2 | unknown flag, a flag without its argument, an argument that is not a flag: the message and the usage on stderr |

A message is a whole report and may span several lines: every schema
violation, every message of the mapping's `errors` section.

```console
$ bin/okdp-compile --package example/input/package \
    --release demo-webapp --namespace demo \
    --platform example/input/platform.yaml -f testdata/schema-violation.yaml
error: values don't meet the specifications of the schema(s) in the following chart(s):
webapp:
- at '/database': got number, want string
- at '/replicas': minimum: got 0, want 1
```

### Rendering with Helm

Render each key of the index with its `<key>.values.yaml` alone, with the
upstream chart the index names for it, `index[key]`, from its repository,
and the release and namespace of the compile:

```bash
helm template <release> <chart> --repo <repository> --version <version> --namespace <namespace> -f <dir>/<key>.values.yaml
helm template <release> oci://<repository>/<chart> --version <version> --namespace <namespace> -f <dir>/<key>.values.yaml
```

The second form is for an `oci://` repository. A chart the chart cache holds
as an archive renders from it too: `helm template <release>
<cache>/<repository>/<chart>-<version>.tgz ...`. `index[key]` is the key's `source` in
`service.yaml` ([sources](specs/02_service_definition.md#sources)), the
platform's oidc-dcr chart for an OAuth client chart, the platform's objects
chart for `okdp` ([platform charts](specs/01_inputs_outputs.md#platform-charts)):
one upstream chart may render several keys, each with its own values file.
Do not add the base values, the instance files or the values files of the
compile: the final values already merge the chart's own defaults, the base
values, the computed values and the instance's upstream values
([merge precedence](specs/01_inputs_outputs.md#merge-precedence)), and give
what Helm gives with them. A chart the index does not list is not rendered.

The example's chart cache, `example/input/charts/`, holds only the charts'
`values.yaml`, enough to compile but not to render, and its `app` and
`okdp-objects` charts are fictional. Every key of the index, from the `out`
directory above:

```bash
yq 'to_entries[] | .key + " " + .value.repository + " " + .value.chart + " " + .value.version' out/okdp.charts.yaml |
while read -r key repository chart version; do
  case $repository in
    oci://*) set -- "${repository%/}/$chart" ;;
    *) set -- "$chart" --repo "$repository" ;;
  esac
  helm template demo-webapp "$@" --version "$version" --namespace demo -f "out/$key.values.yaml"
done > manifests.yaml
```

The okdp chart writes each object of its values (`objects` of
`out/okdp.values.yaml`) as a manifest: here the descriptor ConfigMap
`demo-webapp-okdp`.

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
	"fmt"
	"log"
	"maps"
	"os"
	"slices"

	compiler "github.com/okdp/okdp-compiler"
)

func main() {
	def, err := compiler.LoadDefinition(os.DirFS("example/input/package")) // the package
	if err != nil {
		log.Fatal(err)
	}
	in := compiler.Inputs{
		Charts:    os.DirFS("example/input/charts"),   // the chart cache
		Instance:  os.DirFS("example/input/instance"), // the instance's upstream values files
		Contracts: os.DirFS("example/input/contracts"),
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
	in.Platform = []map[string]any{read("example/input/platform.yaml")}
	in.Values = []map[string]any{read("example/input/instance.yaml")}
	res, err := compiler.Compile(def, in)
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

From memory, with `fstest.MapFS`: no file is read.

```go
package main

import (
	"fmt"
	"log"
	"testing/fstest"

	compiler "github.com/okdp/okdp-compiler"
)

func main() {
	def, err := compiler.LoadDefinition(fstest.MapFS{
		"service.yaml": {Data: []byte(`name: demo
version: 1.2.3
appVersion: "4.5"
charts:
  app:
    source: {repository: https://charts.example.org, chart: app, version: 1.0.0}
    base: base/app.yaml
`)},
		"mapping.cel.yaml":   {Data: []byte("output:\n  app:\n    replicaCount: ${{ values.replicas }}\n")},
		"values.yaml":        {Data: []byte("replicas: 1\n")},
		"values.schema.json": {Data: []byte(`{"properties": {"replicas": {"type": "integer"}}}`)},
		"base/app.yaml":      {Data: []byte("image: {tag: \"4.5\"}\n")},
	})
	if err != nil {
		log.Fatal(err)
	}
	platform := map[string]any{"global": map[string]any{"okdp": map[string]any{"charts": map[string]any{
		"objects": map[string]any{"repository": "oci://registry.example.org/okdp", "chart": "okdp-objects", "version": "1.0.0"},
	}}}}
	res, err := compiler.Compile(def, compiler.Inputs{
		Charts: fstest.MapFS{ // the defaults of each upstream chart
			"charts.example.org/app-1.0.0/values.yaml":                 {Data: []byte("replicaCount: 1\nimage: {tag: latest}\n")},
			"registry.example.org/okdp/okdp-objects-1.0.0/values.yaml": {Data: []byte("objects: []\n")},
		},
		Contracts: fstest.MapFS{}, // no connection, no provides: no contract read
		Platform:  []map[string]any{platform},
		Values:    []map[string]any{{"replicas": 3}},
		Release:   "demo-app",
		Namespace: "demo",
	})
	if err != nil {
		log.Fatal(err)
	}
	files, err := compiler.Files(res, compiler.DefaultIndent)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(files["app.values.yaml"]))
}
```

```text
global:
  okdp:
    charts:
      objects:
        chart: okdp-objects
        repository: oci://registry.example.org/okdp
        version: 1.0.0
image:
  tag: "4.5"
replicaCount: 3
```

Both programs are examples of `example_test.go` (`ExampleCompile`,
`ExampleFiles`, as README's is `Example`): `go test` runs them, and checks
that they are the programs above and print what they show
(`TestDocPrograms`).

Any `fs.FS` works: `embed.FS` (`fs.Sub` for a subdirectory), a git tree.

`compiler.LoadDefinition(fsys)` reads the package at the root of `fsys`:
`service.yaml` and `mapping.cel.yaml`, then keeps `fsys` (`Definition.Files`)
for the files each compile reads, `values.yaml`, `values.schema.json` and the
base files. A `Definition{Service, Mapping, Files}` can also be built in Go;
an entry of `Service.Require` is a `compiler.Requirement`, `{Key, If}`,
`If` empty for a key required in every case.

`compiler.Inputs` takes what the flags give: `Charts` (`--charts`),
`Instance` (`--instance`, `nil` when there is none), `Contracts`
(`--contracts`; it may be empty when nothing references a contract),
`Platform` (the `--platform` files, parsed, in order), `Values` (the `-f`
files, likewise), `Release` and `Namespace`; all but `Instance`,
`Platform` and `Values` are required
([inputs and outputs](specs/01_inputs_outputs.md)). `Result.Values` is what
`--emit-dir` writes, `Result.Computed` what `--out` writes under `values`,
`Charts` (the index), `Objects` and `Dependencies` what both write
([outputs](specs/01_inputs_outputs.md#outputs)):
`compiler.Files(res, indent)` gives the files of `--emit-dir` (name ->
content), `compiler.Document(res, indent)` the file of `--out`, for a caller
to write where it keeps them. `Result.Values` renders as in [rendering with
Helm](#rendering-with-helm), `Result.Charts` being the index.

- `compiler.ParseValues(b)` reads one values file as Helm does (YAML 1.1,
  every document of the file merged in order), for `Inputs.Platform` and
  `Inputs.Values`. A layer built in Go may hold other types than
  `map[string]any` and `[]any` (`map[string]string`, `[]string`, `int32`,
  a struct by its `json` tags): it is read as the values it would be once
  written to a file and read back
  ([values files](specs/01_inputs_outputs.md#values-files)); what a file
  cannot hold fails, its layer and path named.
- `compiler.ChartRef` is an upstream chart, `{Repository, Chart, Version}`:
  a `source` of `service.yaml`, an entry of `Result.Charts`.
- `compiler.ObjectsChart` (`okdp`) is the chart key of
  [OKDP's own objects](specs/01_inputs_outputs.md#the-okdp-chart),
  `compiler.DcrChart` (`oidc-dcr`) the chart key of a service's main
  [OAuth client](specs/01_inputs_outputs.md#oauth-client-charts), and the
  suffix of the others; `compiler.DcrSource()` is the oidc-dcr chart they
  are rendered by unless the platform names another
  ([platform charts](specs/01_inputs_outputs.md#platform-charts)): the
  chart a chart cache needs for a `dcr` platform.
- `Compile` keeps no state between calls and is safe for concurrent use: one
  `Definition` and the same values layers can be shared between goroutines;
  the inputs are not changed (`TestConcurrentCompile`, under `go test
  -race`).
- `compiler.CompileContext(ctx, def, in, opts...)` is `Compile` cancelled
  with `ctx`: between the stages, and inside an expression at the next
  check of a comprehension (every 100 iterations); it then returns
  `ctx.Err()`. Its options (`ExampleCompileContext` compiles the example
  with both):
  - `compiler.WithCostLimit(n)` bounds the total CEL cost (cel-go's runtime
    cost: about one unit per operation, list element and character
    compared) of the compile's expressions, the conditions of `require`,
    the mapping's and the OAuth client templates': past it, the compile
    fails with `the expressions of the compile exceed its CEL cost limit
    (n)`. A server compiling packages
    it does not review sets one, as an instance's values can make an
    expression iterate over them. The example costs under 100.
  - `compiler.WithoutObjectsChart()` leaves the
    [okdp chart](specs/01_inputs_outputs.md#the-okdp-chart) out of
    `Result.Charts` and `Result.Values`, for a caller that deploys
    `Result.Objects` otherwise; `global.okdp.charts.objects` is then not
    required.
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
    --platform platform.yaml -f "$f" --release "$name" --namespace demo \
    --emit-dir "compiled/$name" || exit 1
done
test -z "$(git status --porcelain compiled/)" || { git status --short compiled/; exit 1; }
```

The emitted files can be committed and checked against the commit, as
`go test ./internal/fsio` checks `example/output/`: `git status
--porcelain` lists the changed, removed and new files, where
`git diff --exit-code` misses a new, untracked file (a chart now
compiled).

### GitOps

The compiler writes values and objects, not HelmRelease or Application
objects. The values of every key are compiled for the instance's release
name and namespace: a deployment renders them under these, in one of two
ways. OKDP publishes no chart of the service in either: the upstream charts
come from their own repositories.

- **One release per instance**, the umbrella (`--umbrella`): one Flux
  HelmRelease, or one Argo CD Application. Helm keeps one release of a name
  per namespace, and Flux deploys Helm releases: with Flux, deploy the
  umbrella.
- **One source per key** (`--emit-dir`), as in
  [rendering with Helm](#rendering-with-helm): one Argo CD Application
  source per key, whose chart is the key's entry in `okdp.charts.yaml` (the
  repository, the chart's name in it, the version) and whose values are
  `<key>.values.yaml`. Argo CD renders each source with `helm template` and
  creates no Helm release: the sources share the release name. Two Flux
  HelmReleases cannot.

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
3.5 Helm 4.2.1, Argo CD 3.4 and 3.3 Helm 3.19.4. The umbrella is checked
with each ([`--umbrella`](#--umbrella)).

**OAuth clients.** In `dcr` mode the index lists one chart per OAuth client
(`oidc-dcr`, `<name>-oidc-dcr`, rendered by the platform's oidc-dcr chart;
[OAuth client charts](specs/01_inputs_outputs.md#oauth-client-charts)):
deploy them with the others. Their Job, a Helm pre-install/pre-upgrade hook
(an Argo CD `PreSync` hook), writes the client's Secret; the pods that read
it do not start before it exists.

**OKDP's own objects.** `Result.Objects` are the values of the `okdp` chart,
which every compile adds
([the okdp chart](specs/01_inputs_outputs.md#the-okdp-chart)): deploying the
platform's objects chart (`global.okdp.charts.objects`) with
`okdp.values.yaml`, like the other charts, brings the descriptor ConfigMap,
the credentials Secrets and the mapping's objects to the cluster. They are
its `objects`, a YAML list: `yq '.objects[] | split_doc'
out/okdp.values.yaml` turns them into a multi-document stream.

**Dependencies.** `Result.Dependencies` lists the instances of the
namespace the connection references point to, one item per parameter
([dependencies](specs/01_inputs_outputs.md#dependencies)). The
compiler does not check that an instance exists: the deployment waits for
it.

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

`<field>` is the parameter path (`database`, `backends[1].database`),
`<key>` a chart key (of `service.yaml`, `okdp`, or an OAuth client chart),
`<chart>-<version>` the upstream chart that renders it, `<service>` the
`name` of `service.yaml`. Every error exits with status 1, a usage error
with status 2.

### Inputs and instance values

| Message | Cause | Fix |
|---|---|---|
| `--package is required`, `--charts (or $OKDP_CHARTS) is required`, `--contracts (or $OKDP_CONTRACTS) is required`, `--release is required`, `--namespace is required` | a required flag is missing | give it |
| `Inputs.Release "Demo_App": a release name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 53 characters at most (Helm's limit)`, `Inputs.Namespace "<name>": a namespace name, ...` | `--release` or `--namespace` is not a name Kubernetes and Helm accept | give one of that form ([inputs](specs/01_inputs_outputs.md)) |
| `--instance: stat instances/demo: no such file or directory`, `--charts charts.tgz: not a directory` | a directory flag (`--package`, `--charts`, `--contracts`, `--instance`) names no directory | fix the path |
| `unexpected argument "instance.yaml": every input is a flag (-f before each values file)` | an argument that is not a flag, here a second values file after one `-f`; exit status 2, nothing compiled | one `-f` per values file |
| `refusing to write in out: notes.txt is not an output of okdp-compile` | the `--emit-dir` or `--umbrella` directory holds a file the flag does not write (`Chart.lock` and `charts/` of `helm dependency build` included); nothing is written | another directory, or remove the file |
| `--emit-dir and --umbrella name the same directory, out: give each its own` | both flags name one directory | give each its own |
| `open service.yaml: no such file or directory`, `open mapping.cel.yaml: ...` | `--package` is not the package's directory, or the package has no mapping | point it at the directory of `service.yaml` |
| `open values.yaml: no such file or directory`, `open values.schema.json: ...` | the package has no parameter defaults, or no parameter schema; both are always read (connection references are declared in the schema) | add them ([package files](specs/01_inputs_outputs.md#package-files)) |
| `service.yaml: name is required`, `service.yaml: version is required` | the package metadata is incomplete | set them ([service](specs/02_service_definition.md#service)) |
| `service.yaml: line 7: unknown field protcet (allowed: source, base, protect, append, upstream)` | a field `service.yaml` does not define, here a misspelt `protect` in a chart; one line per unknown field | fix the name ([service.yaml](specs/02_service_definition.md#serviceyaml)) |
| `service.yaml: line 12: a second YAML document: service.yaml is one document` | a `---` followed by a second document | one document |
| `service.yaml: charts: key "my_app": a DNS label (lowercase letters, digits and -, a letter or digit at each end, 63 characters at most): the key names files (<key>.values.yaml) and the umbrella's dependency alias` | a chart key that is not a DNS label: `""`, `../app`, an upper case letter, `_` or `.` | rename the key in `service.yaml` and the mapping ([chart keys](specs/02_service_definition.md#chart-keys)) |
| `service.yaml: line 19: upstream off: write true or false (service.yaml is read as YAML 1.2, where off is not a boolean; quote a path of that name)` | a chart's `upstream` written as a YAML 1.1 boolean (`no`, `off`, `yes`, `on`...), a string in `service.yaml` | `true` or `false`; quote a file of that name ([upstream](specs/02_service_definition.md#upstream)) |
| `service.yaml: charts.app.source is required: the upstream chart (repository, chart, version)` | a chart without its `source` | name the published chart that renders it ([sources](specs/02_service_definition.md#sources)) |
| `service.yaml: charts.app.source.repository "charts.example.org": the URL of a chart repository (https://... or oci://...)`, `service.yaml: charts.app.source.chart "a/b": the name of a chart of the repository` | a repository that is not a URL (`https://`, `http://`, `oci://`) or whose host and path are no directory of the chart cache (`https://`, `https://x/../y`), a chart that is not a chart's name | fix the source ([sources](specs/02_service_definition.md#sources)) |
| `service.yaml: charts.app.source.version "^1.8": a pinned chart version (1.2.3), not a range` | a version range | pin the version the chart cache holds |
| `service.yaml: require[1]: unknown field if: an entry is a key, or {key: <key>, $if: ${{ <condition> }}}`, `service.yaml: require[1] (oidc.issuerUri): $if: takes one expression, ${{ <condition> }}, got "platform.oidc.enabled"` | a `require` entry of another form | a key, or `{key, $if}` with one expression ([require](specs/02_service_definition.md#require)) |
| `service.yaml: charts.okdp: the chart of OKDP's own objects is added by the compiler: remove it from service.yaml` | `service.yaml` lists `okdp` | remove it; the platform names its chart ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
| `app: chart app-1.8.0 is not in the chart cache: mkdir -p <chart cache>/charts.example.org && helm pull app --repo https://charts.example.org --version 1.8.0 -d <chart cache>/charts.example.org` | a chart the compile renders (a chart left out by `$if` is not read) is neither in its repository's directory (`charts.example.org/app-1.8.0/` or `.tgz`) nor at the root of the chart cache (`app-1.8.0/` or `.tgz`); for an `oci://` repository the commands are `mkdir -p <chart cache>/<repository> && helm pull oci://<repository>/<chart> --version <version> -d <chart cache>/<repository>` | run the commands, `<chart cache>` the chart cache, or check `--charts` / `$OKDP_CHARTS` ([chart cache](specs/01_inputs_outputs.md#chart-cache)) |
| `app: chart app-1.8.0 comes from 2 repositories, and the chart cache holds it only as app-1.8.0, which cannot tell them apart: pull each into its repository's directory: mkdir -p <chart cache>/charts.example.org && helm pull app --repo https://charts.example.org --version 1.8.0 -d <chart cache>/charts.example.org; mkdir -p <chart cache>/fork.example.org && helm pull app --repo https://fork.example.org --version 1.8.0 -d <chart cache>/fork.example.org` | the compile renders one chart name and version from two repositories (a fork, a generic name), and the chart cache holds it at its root only, not in this key's repository's directory | run the commands ([chart cache](specs/01_inputs_outputs.md#chart-cache)) |
| `app: charts.example.org/app-1.8.0.tgz: chart illegally references parent directory`, `... chart illegally contains absolute paths`, `... chart illegally contains content outside the base directory: "values.yaml"`, `... chart contains illegally named files`, `... decompressed chart is larger than the maximum size 104857600`, `app: charts.example.org/app-1.8.0/values.yaml: chart exceeds maximum decompressed size of 104857600 bytes` | the chart's archive has an entry out of the chart's directory (`..`, an absolute path, a Windows drive, a file at the archive's root), or the chart is over 100 MiB decompressed: Helm refuses it too | pull the chart again; a chart Helm cannot load does not render ([chart cache](specs/01_inputs_outputs.md#chart-cache)) |
| `global.okdp.charts.objects is required: the chart OKDP's own objects are rendered with (okdp-objects: repository, chart, version). It is a platform value: set it in platform/platform-values.yaml` | the platform values name no objects chart | pass the platform values with `--platform`; set it ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
| `global.okdp.charts.objects.version "1.x": a pinned chart version (1.2.3), not a range` | a platform chart checked as a source is (its repository, chart and version, the same messages); also `global.okdp.charts.oidcDcr`, and `... must be a map (repository, chart, version)`, `... must be a string` for its form | fix the platform values |
| `database: unknown contract "database"` | no `<contract>.schema.json` in the contracts directory | check `--contracts` / `$OKDP_CONTRACTS` |
| `database: the database contract cannot be read: database.schema.json: unexpected end of JSON input` | the contract file cannot be read, is not JSON, not an object or not a valid JSON Schema; also `provides "<name>" (<contract>): the <contract> contract cannot be read: ...` | fix the contract, or point `--contracts` at okdp-lib-chart's ([contracts](specs/01_inputs_outputs.md#location)) |
| `<file>: cannot unmarshal yaml document: error converting YAML to JSON: yaml: line 1: did not find expected node content` | a values file is not valid YAML | fix the file |
| `global.okdp.ingress.className is required. It is a platform value: set it in platform/platform-values.yaml` | `require` in `service.yaml` lists it, in every case or under a condition that holds, and no platform layer sets it (or a later one sets it to `null`) | pass the platform values with `--platform`; set the key ([platform values](specs/01_inputs_outputs.md#platform-values)) |
| `service.yaml: require[1] (oidc.issuerUri): $if: ${{ platform.oidc.enabled }}: must be true or false, got null` | the condition of a `require` entry is not a boolean, or its evaluation fails (`no such key: ...`) | write a condition of `true` or `false` (`platform.oidc.enabled == true`), reading optional values with `.?` ([require](specs/02_service_definition.md#require)) |
| `values layer 1: global.okdp is set by the platform (--platform, Inputs.Platform)`, `values layer 2: global is set by the platform ...` | an instance file, the first, second... `-f` file (`Inputs.Values[0]`, `[1]`...), sets `global`: a platform value, a key of `global` the parameter schema does not declare, or `global` itself to a non-map | give platform values with `--platform`; a package lets instances set a key of `global` by declaring it in `values.schema.json` ([platform values](specs/01_inputs_outputs.md#platform-values)) |
| `values layer 1: connections.shared-db.host: the value contains "{{"`, `values layer 2: upstream.app.podAnnotations.{{ x }}: the key contains "{{"` | template code in an instance file (`-f`), in a key or a value under any key: parameters, connections, inline upstream values; the platform's files are not checked | remove it ([layers](specs/01_inputs_outputs.md#layers)) |
| `values layer 2: hooks[0]: a func() cannot be a value`, `platform layer 1: global.proxy["no.proxy"]: not a finite number: NaN` | a values layer built in Go, the second of `Inputs.Values`, the first of `Inputs.Platform`, holds what a values file cannot: a func, a chan, a complex, a NaN or an infinity, text that is not UTF-8, a key other than a string, a number or a boolean, two keys that read as the same, a value that contains itself | build the layer of values a file can hold ([values files](specs/01_inputs_outputs.md#values-files)) |
| `values don't meet the specifications of the schema(s) in the following chart(s):` then `<service>:` and `- at '/replicas': minimum: got 0, want 1` | the parameters violate the package's `values.schema.json`; every violation is listed | fix the instance values ([parameters](specs/01_inputs_outputs.md#parameters)) |
| `compiled values don't meet the specifications of the schema(s) in the following chart(s):` then `<key> (<chart>-<version>):` (`app (app-1.8.0):`) and `- at '/replicaCount': maximum: got 3, want 2` | the final values violate the `values.schema.json` of the upstream chart | fix the instance's upstream values; if they are not involved, the base values or the mapping |
| `database: connection "store" has contract "s3", "database" is expected` | the parameter references a `connections` entry of another contract | reference a connection of the expected contract |
| `database: connection "shared-db": field "user" is not part of the database contract` | an external connection has a field its contract does not declare | remove it ([connections](specs/01_inputs_outputs.md#connections)) |
| `database: connection "shared-db": "password" is a secret field` | a secret field written in the connection | put it in a Secret named by `secretRef` |
| `database (connection "shared-db"): field "dbName" is required by the database contract` | a required contract field is missing; one line per missing field | set it |
| `database (connection "shared-db"): field "port" violates the database contract: got string, want integer`, `... field "engine" violates the database contract: value must be one of 'postgresql', 'mysql'` | a field of an external connection the contract's schema refuses; one line per violation | give a value of the contract's type (`port: 5432`, not `"5432"`) and among its values ([checks](specs/01_inputs_outputs.md#checks)) |
| `<field>: "<name>" is not a connection, and the <contract> contract has no internal naming convention` | the name is not in `connections`, and the contract cannot resolve an instance | add the connection under `connections` |
| `<field>: "<name>" is not a connection, nor a release name: lowercase letters, digits and "-", a letter or digit at each end, 53 characters at most` | the name is not in `connections`, and cannot name an instance of the namespace (`Shared_DB`, a name longer than 53 characters) | fix the name, or add the connection under `connections` |
| `database: a connection reference must be a string, got map[string]interface {}{"host":"db.example.org"}` | a connection reference given as a map, a number or a list (the parameter schema does not say `"type": "string"`) | give the name of a connection or an instance; put the connection itself under `connections` |
| `database (connection "shared-db"): field "host": the value contains "{{"` | template code in a connection given by the platform's values, or in a contract default (an instance's files fail earlier, `values layer <n>: ...`) | fix the platform values or the contract |
| several lines of the connection messages above, one `<field>` each | several references do not resolve: every one is reported, sorted by parameter path, with its first error (the fields of a connection in alphabetical order), or all its missing required fields, or all its schema violations | fix each, then compile again: a fixed field may reveal the next one ([connection errors](specs/01_inputs_outputs.md#connection-errors)) |
| `webapp: database connection "shared-db" has no secretRef, the password cannot be read` | not a compiler check: a message of the example mapping's `errors` section | add `secretRef: {name: ...}` to the connection |
| `app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed` | the instance's upstream values set a path `service.yaml` protects (`protect`, and always `global`); a file name replaces `upstream.<key>` for an instance file | remove the value; set it through the parameters when the package offers one |
| `app: upstream.app.yaml: podAnnotations.x: the value contains "{{"`, `... the key contains "{{"` | template code in an instance's upstream values file (in the inline upstream values of an instance file, that file fails before: `values layer <n>: ...`) | remove it |
| `app: upstream.app.env must be a list` | a path of `append` given as a non-list | give a list |
| `service.yaml: version "1.0": the version of the umbrella chart, SemVer (1.2.3)` | `--umbrella` with a package version Helm does not accept as a chart's | a SemVer `version` in `service.yaml` |
| `<key>: upstream file <file>: no instance files (Inputs.Instance)` | `service.yaml` names upstream files, and no instance directory is given | pass `--instance` / `Inputs.Instance` |
| `global.okdp.oidc.clientProvisioning "manual" is not one of existing, dcr` | wrong platform value | `existing` or `dcr` |
| `global.okdp.oidc must be a map, got false`, `global.okdp.proxy.noProxy must be a string, got []interface {}{".svc"}` | a platform setting the compiler reads, or a map on the way to it, of another type (`oidc: false`, `proxy: <url>`, a list for a string); it would otherwise read as not set | fix the platform values; `null` is not set ([fields the compiler reads](specs/01_inputs_outputs.md#fields-the-compiler-reads)) |
| `$ref <url>: the schema may only reference itself (the compile reads no file or URL)` | a `values.schema.json` references a file or a URL | inline the referenced schema |

### Mapping

The errors of `mapping.cel.yaml`, mostly for package authors, are listed
with their causes in [service definition](specs/02_service_definition.md#errors):
the messages of its `errors` section, one per line; CEL errors prefixed with
the section, key and list position path (`output: app: env[2]: value: ${{ ... }}: no such key: host`);
sections of the wrong shape
(`output: app: must be a map of the chart's values, got "on"`,
`instance: url: must be a string, got 8080`); arguments of
`okdpGeneratedSecret` unknown or of the wrong type
(`okdp.generatedSecret: keys[0]: unknown field(s) lenght (allowed: key, length, digits, symbols, noUpper, transform)`);
YAML errors (`yaml: line <n>: ...`), which name no file. The `provides`
checks are in [descriptor ConfigMap](specs/01_inputs_outputs.md#descriptor-configmap),
the OAuth client errors [below](#oauth-clients).

### OAuth clients

The messages of the `oidcClients` checks are listed with the mapping's
([compile errors](specs/02_service_definition.md#compile-errors)),
those of the platform's settings `global.okdp.oidc.dcr` in
[OAuth client settings](specs/01_inputs_outputs.md#oauth-client-settings).
A message names a client `oidcClients[<i>]`, its position in the evaluated
list, then ` (<name>)` for a named client; the first failing check stops
the compile. Where the cause or the fix is not in the message:

| Message | Cause | Fix |
|---|---|---|
| `global.okdp.oidc.dcr.ttlSeconds must be a whole number >= 0, got "600"` | a platform setting of the wrong type, here a quoted number; the types of the settings are checked in every mode, with or without a client | fix the platform values; `null` takes the default |
| `oidcClients[0]: unknown key(s) redirectUri (allowed: name, grantTypes, redirectUris, scope, clientName, public, request, secret, caSecret)` | a typo, or a registration field written as a client key | use a client key; a registration field goes under `request` |
| `global.okdp.oidc.dcr.authMethod "" is not supported: OKDP registers its OAuth clients anonymously` | `dcr` mode, at least one client, and `authMethod` unset (`""`, as when the platform sets no `dcr` settings) or not `anonymous` | `authMethod: anonymous` in the platform values |
| `global.okdp.oidc.dcr.registrationUrl is required (clientProvisioning: dcr): the endpoint the oidc-dcr Jobs register the OAuth clients at` | `dcr` mode, at least one client, and no `registrationUrl` (`testdata/platform/dcr-without-registration-url.yaml`) | set it in the platform values |
| `service.yaml: charts.oidc-dcr: the chart of oidcClients[0] is added by the compiler: remove it from service.yaml` | `service.yaml` lists the chart key of a client | remove it: the compiler adds the client charts; rename the chart key or the client when it is another chart |
| `mapping output "oidc-dcr": the chart of oidcClients[0] is compiled from oidcClients: remove it from output` | `output` computes values for the chart key of a client | remove it; set registration fields with the client's `request` |
| `oidc-dcr: chart oidc-dcr-0.5.0 is not in the chart cache: mkdir -p <chart cache>/quay.io/adaltas && helm pull oci://quay.io/adaltas/oidc-dcr --version 0.5.0 -d <chart cache>/quay.io/adaltas` | `dcr` mode, at least one client, and the chart cache lacks the platform's oidc-dcr chart (`existing` mode does not need it) | pull it into the chart cache ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
