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

Go 1.25 or later (`go 1.25.0` in `go.mod`). Helm is not needed to compile,
only to render the result.

```bash
make build                  # bin/okdp-compile
make test                   # go test ./...
make lint                   # golangci-lint run ./...
```

`make build` sets the version printed by `--version` with
`-ldflags "-X main.version=..."`: `git describe --tags --always --dirty`
without the leading `v` of the tag, the commit hash when there is no tag,
`dev` outside a git checkout. `VERSION` overrides it:

```console
$ make build VERSION=1.2.3
go build -ldflags "-X main.version=1.2.3" -o bin/okdp-compile ./cmd/okdp-compile
$ bin/okdp-compile --version
1.2.3
```

`go install ./cmd/okdp-compile` from a checkout installs it in `$GOBIN`; without
the `-ldflags` above, `--version` prints `dev`. The module path is
`github.com/okdp/okdp-compiler` (`go.mod`):
`go install github.com/okdp/okdp-compiler/cmd/okdp-compile@<version>` works
where the module can be fetched at that path.

## Command line

```text
okdp-compile --package <dir> [--charts <dir>] [--contracts <dir>] [--instance <dir>]
             [-f <values.yaml>]... [--release <name>] [--namespace <name>]
             [--out <file>] [--emit-dir <dir>]
okdp-compile --version
```

| Flag | Default | |
|---|---|---|
| `--package` | required | package directory: `service.yaml`, `mapping.cel.yaml`, `values.yaml`, `values.schema.json` at its root, and the base files `service.yaml` names ([package files](specs/01_inputs_outputs.md#package-files)) |
| `--charts` | `$OKDP_CHARTS` | chart cache directory: each upstream chart as `helm pull -d <dir>` writes it, `<chart>-<version>.tgz`, or unpacked, `<chart>-<version>/` ([chart cache](specs/01_inputs_outputs.md#chart-cache)); required, by flag or variable |
| `--contracts` | `$OKDP_CONTRACTS` | contract schemas directory: `<contract>.schema.json` ([contracts](specs/01_inputs_outputs.md#contracts)); required, by flag or variable |
| `--instance` | none | instance directory: the upstream values files `service.yaml` names (`upstream.<key>.yaml`, by chart key; [instance values](specs/01_inputs_outputs.md#instance-values)) |
| `-f` | none | a values file; repeatable, merged in order as `helm -f` files. The platform values come first ([platform values](specs/01_inputs_outputs.md#platform-values), [merge precedence](specs/01_inputs_outputs.md#merge-precedence)) |
| `--release` | `demo-<name>` | release name; `<name>` is the `name` of `service.yaml` |
| `--namespace` | `demo` | namespace |
| `--out` | none | write the computed values, the chart index, the objects and the dependencies to one YAML file |
| `--emit-dir` | none | write the final values of each chart, the chart index, the objects and the dependencies to a directory, one file each |
| `--version` | | print the version and exit |

Flags take one or two dashes. Without `--out` or `--emit-dir`, the command
only compiles: exit status 0 means the instance compiles. When both are
given, `--emit-dir` is written first, then `--out`.

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
$ export OKDP_CONTRACTS=example/contracts OKDP_CHARTS=example/charts
$ bin/okdp-compile --package example/webapp \
    -f example/platform-values.yaml -f example/values/internal-db.yaml --out /dev/stdout
charts:
  app:
    repository: https://charts.example.org
    chart: app
    version: 1.8.0
  metrics:
    repository: oci://registry.example.org/charts
    chart: metrics
    version: 0.4.2
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
    # ... ConfigMap demo-webapp-okdp (the descriptor), ConfigMap demo-webapp-settings,
    # Password demo-webapp-session-session-key, ExternalSecret demo-webapp-session
values:
  app:
    # ...
    replicaCount: 3
  metrics:
    interval: 30s
    target: http://demo-webapp-app.demo.svc:8080/metrics
  okdp:
    objects:
      # ... the four objects above
```

The computed values of a chart hold only what the mapping sets:

```console
$ bin/okdp-compile --package example/webapp \
    -f example/platform-values.yaml -f example/values/internal-db.yaml \
    --out compiled.yaml --emit-dir out
$ yq '.values.app.image' compiled.yaml
tag: "2.3"
$ yq '.image' out/app.values.yaml
pullPolicy: IfNotPresent
repository: example/webapp
tag: "2.3"
```

### `--emit-dir`

The directory is created if needed, and its files are overwritten:

| File | Content |
|---|---|
| `<key>.values.yaml` | `Result.Values[key]`: the final values of the chart key, one file per chart the mapping outputs, for the [okdp chart](specs/01_inputs_outputs.md#the-okdp-chart), and per [OAuth client chart](specs/01_inputs_outputs.md#oauth-client-charts) in `dcr` mode |
| `okdp.charts.yaml` | `Result.Charts`, the [chart index](specs/01_inputs_outputs.md#chart-index): `<key>: {repository, chart, version}`, one entry per values file |
| `okdp.objects.yaml` | `Result.Objects`: a YAML list of whole manifests |
| `okdp.dependencies.yaml` | `Result.Dependencies`: a YAML list, `[]` when there is none |

With `example/values/dcr.yaml` (the platform registers the OAuth clients by
DCR), the webapp's client adds the chart `oidc-dcr`, which `service.yaml`
does not list, any more than `okdp`:

```console
$ bin/okdp-compile --package example/webapp \
    -f example/platform-values.yaml -f example/values/dcr.yaml --emit-dir dcr
$ ls dcr
app.values.yaml
oidc-dcr.values.yaml
okdp.charts.yaml
okdp.dependencies.yaml
okdp.objects.yaml
okdp.values.yaml
$ cat dcr/okdp.charts.yaml
app:
    repository: https://charts.example.org
    chart: app
    version: 1.8.0
oidc-dcr:
    repository: oci://quay.io/adaltas
    chart: oidc-dcr
    version: 0.5.0
okdp:
    repository: oci://registry.example.org/okdp
    chart: okdp-objects
    version: 1.0.0
```

A chart the mapping leaves out has no file and no index entry: metrics are
off in `example/values/dcr.yaml`, so there is no `metrics.values.yaml`; in
`existing` mode (`example/values/internal-db.yaml`) there is no
`oidc-dcr.values.yaml`. Files of a previous run are not removed: emit into
an empty directory.

### Exit status and errors

| Status | When |
|---|---|
| 0 | compiled; `--version`, `--help` |
| 1 | any error: `error: <message>` on stderr |
| 2 | unknown flag, or a flag without its argument: the message and the usage on stderr |

A message is a whole report and may span several lines: every schema
violation, every message of the mapping's `errors` section.

```console
$ bin/okdp-compile --package example/webapp \
    -f example/platform-values.yaml -f testdata/schema-violation.yaml
error: values don't meet the specifications of the schema(s) in the following chart(s):
webapp:
- at '/metrics/enabled': got string, want boolean
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
<cache>/<chart>-<version>.tgz ...`. `index[key]` is the key's `source` in
`service.yaml` ([sources](specs/02_service_definition.md#sources)), the
platform's oidc-dcr chart for an OAuth client chart, the platform's objects
chart for `okdp` ([platform charts](specs/01_inputs_outputs.md#platform-charts)):
one upstream chart may render several keys, each with its own values file.
Do not add the base values, the instance files or the values files of the
compile: the final values already merge the chart's own defaults, the base
values, the computed values and the instance's upstream values
([merge precedence](specs/01_inputs_outputs.md#merge-precedence)), and give
what Helm gives with them. A chart the index does not list is not rendered.

The example's chart cache, `example/charts/`, holds only the charts'
`values.yaml`, enough to compile but not to render, and its `app`, `metrics`
and `okdp-objects` charts are fictional. Every key of the index, from the
`dcr` directory above:

```bash
yq 'to_entries[] | .key + " " + .value.repository + " " + .value.chart + " " + .value.version' dcr/okdp.charts.yaml |
while read -r key repository chart version; do
  case $repository in
    oci://*) set -- "${repository%/}/$chart" ;;
    *) set -- "$chart" --repo "$repository" ;;
  esac
  helm template demo-webapp "$@" --version "$version" --namespace demo -f "dcr/$key.values.yaml"
done > manifests.yaml
```

The oidc-dcr chart is published:

```console
$ helm template demo-webapp oci://quay.io/adaltas/oidc-dcr --version 0.5.0 --namespace demo -f dcr/oidc-dcr.values.yaml | yq -N '.kind + " " + .metadata.name'
ServiceAccount demo-webapp-oidc-dcr
ConfigMap demo-webapp-oidc-dcr
Role demo-webapp-oidc-dcr
RoleBinding demo-webapp-oidc-dcr
Service demo-webapp-oidc-dcr-headless
Job demo-webapp-oidc-dcr
```

The okdp chart writes each object of its values as a manifest: the
descriptor ConfigMap `demo-webapp-okdp`, the ConfigMap
`demo-webapp-settings`, the ExternalSecret `demo-webapp-session` and the
Password `demo-webapp-session-session-key` (`dcr/okdp.objects.yaml`).

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

	"github.com/okdp/okdp-compiler/compiler"
)

func main() {
	def, err := compiler.LoadDefinition(os.DirFS("example/webapp")) // the package
	if err != nil {
		log.Fatal(err)
	}
	in := compiler.Inputs{
		Charts:    os.DirFS("example/charts"), // the chart cache
		Contracts: os.DirFS("example/contracts"),
		Release:   "demo-webapp",
		Namespace: "demo",
	}
	// The values layers, in order, as helm -f files.
	for _, f := range []string{"example/platform-values.yaml", "example/values/internal-db.yaml", "example/values/dcr.yaml"} {
		b, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		v, err := compiler.ParseValues(b)
		if err != nil {
			log.Fatalf("%s: %v", f, err)
		}
		in.Values = append(in.Values, v)
	}
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
charts: 4 objects: 4
replicaCount: 3
app: https://charts.example.org app 1.8.0
metrics: oci://registry.example.org/charts metrics 0.4.2
oidc-dcr: oci://quay.io/adaltas oidc-dcr 0.5.0
okdp: oci://registry.example.org/okdp okdp-objects 1.0.0
dependency: database -> demo-db (database)
```

From memory, with `fstest.MapFS`: no file is read.

```go
package main

import (
	"log"
	"os"
	"testing/fstest"

	"gopkg.in/yaml.v3"

	"github.com/okdp/okdp-compiler/compiler"
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
			"app-1.0.0/values.yaml":          {Data: []byte("replicaCount: 1\nimage: {tag: latest}\n")},
			"okdp-objects-1.0.0/values.yaml": {Data: []byte("objects: []\n")},
		},
		Contracts: fstest.MapFS{}, // no connection, no provides: no contract read
		Values:    []map[string]any{platform, {"replicas": 3}},
		Release:   "demo-app",
		Namespace: "demo",
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := yaml.NewEncoder(os.Stdout).Encode(res.Values["app"]); err != nil {
		log.Fatal(err)
	}
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

Any `fs.FS` works: `embed.FS` (`fs.Sub` for a subdirectory), a git tree.

`LoadDefinition(fsys)` reads the package at the root of `fsys`:
`service.yaml` and `mapping.cel.yaml`, then keeps `fsys` (`Definition.Files`)
for the files each compile reads, `values.yaml`, `values.schema.json` and the
base files. A `Definition{Service, Mapping, Files}` can also be built in Go.

`Inputs` takes what the flags give: `Charts` (`--charts`), `Instance`
(`--instance`, `nil` when there is none), `Contracts` (`--contracts`; it may
be empty when nothing references a contract), `Values` (the `-f` files,
parsed, in order; the platform values first), `Release` and `Namespace`;
all but `Instance` and `Values` are required
([inputs and outputs](specs/01_inputs_outputs.md)).
`Result.Values` is what `--emit-dir` writes, `Result.Computed` what `--out`
writes under `values`, `Charts` (the index), `Objects` and `Dependencies`
what both write ([outputs](specs/01_inputs_outputs.md#outputs)).
`Result.Values` renders as in [rendering with Helm](#rendering-with-helm),
`Result.Charts` being the index.

- `compiler.ParseValues(b)` reads one values file as Helm does (YAML 1.1,
  every document of the file merged in order), for `Inputs.Values`.
- `compiler.ChartRef` is an upstream chart, `{Repository, Chart, Version}`:
  a `source` of `service.yaml`, an entry of `Result.Charts`.
- `compiler.ObjectsChart` (`okdp`) is the chart key of
  [OKDP's own objects](specs/01_inputs_outputs.md#the-okdp-chart),
  `compiler.DcrChart` (`oidc-dcr`) the chart key of a service's main
  [OAuth client](specs/01_inputs_outputs.md#oauth-client-charts), and the
  suffix of the others; `compiler.DcrSource` is the oidc-dcr chart they
  are rendered by unless the platform names another
  ([platform charts](specs/01_inputs_outputs.md#platform-charts)): the
  chart a chart cache needs for a `dcr` platform.
- `Compile` keeps no state between calls and is safe for concurrent use: one
  `Definition` and the same values layers can be shared between goroutines;
  the inputs are not changed.
- Errors are messages for the user, returned as they are; the package
  defines no error types. A message carries its context: the file, the
  parameter path, the chart key, the mapping section (see
  [troubleshooting](#troubleshooting)).

## Integration

### CI

Compile every values file of a package; any error fails the job:

```bash
for f in example/values/*.yaml; do
  name=$(basename "$f" .yaml)
  bin/okdp-compile --package example/webapp --charts example/charts \
    -f example/platform-values.yaml -f "$f" --emit-dir "ci-out/$name" || exit 1
done
```

The emitted files can be committed and checked with `git diff --exit-code`,
as `go test ./compiler` does with `example/compiled/`.

### GitOps

The compiler writes values and objects, not HelmRelease or Application
objects. A deployment renders each key of the index under the instance's
release name and namespace, as in [rendering with Helm](#rendering-with-helm):
one Flux HelmRelease, or one Argo CD Application source, per key, whose
chart is the key's entry in `okdp.charts.yaml` (the repository, the chart's
name in it, the version) and whose values are `<key>.values.yaml`. The
chart comes from the index, not from the key, and from its own repository:
OKDP publishes no chart of the service.

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
also in `okdp.objects.yaml`, a YAML list: `yq '.[] | split_doc'
out/okdp.objects.yaml` turns it into a multi-document stream.

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
`name` of `service.yaml`. Every error exits with status 1.

### Inputs and instance values

| Message | Cause | Fix |
|---|---|---|
| `--package is required`, `--charts (or $OKDP_CHARTS) is required`, `--contracts (or $OKDP_CONTRACTS) is required` | a required flag is missing | give it |
| `open service.yaml: no such file or directory`, `open values.yaml: ...` | `--package` is not the package's directory | point it at the directory of `service.yaml` |
| `open values.schema.json: no such file or directory` | the package has no parameter schema; it is always read (connection references are declared there) | add `values.schema.json` ([package files](specs/01_inputs_outputs.md#package-files)) |
| `service.yaml: name is required`, `service.yaml: version is required` | the package metadata is incomplete | set them ([service](specs/02_service_definition.md#service)) |
| `service.yaml: charts.metrics.source is required: the upstream chart (repository, chart, version)` | a chart without its `source` | name the published chart that renders it ([sources](specs/02_service_definition.md#sources)) |
| `service.yaml: charts.metrics.source.version "^0.4": a pinned chart version (1.2.3), not a range` | a version range, or a field of `source` that is not a repository URL (`https://`, `oci://`), a chart name or a version | pin the version the chart cache holds |
| `service.yaml: charts.okdp: the chart of OKDP's own objects is added by the compiler: remove it from service.yaml` | `service.yaml` lists `okdp` | remove it; the platform names its chart ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
| `app: chart app-1.8.0 is not in the chart cache: helm pull app --repo https://charts.example.org --version 1.8.0 -d <chart cache>` | a chart the compile renders (a chart left out by `$if` is not read) is neither `<chart>-<version>/` nor `<chart>-<version>.tgz` of the chart cache; for an `oci://` repository the command is `helm pull oci://<repository>/<chart> --version <version>` | run the command with `-d` the chart cache, or check `--charts` / `$OKDP_CHARTS` ([chart cache](specs/01_inputs_outputs.md#chart-cache)) |
| `global.okdp.charts.objects is required: the chart OKDP's own objects are rendered with (okdp-objects: repository, chart, version). It is a platform value: set it in platform/platform-values.yaml` | the platform values name no objects chart | pass the platform values as the first `-f`; set it ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
| `global.okdp.charts.objects.version "1.x": a pinned chart version (1.2.3), not a range` | a platform chart that is not a repository URL, a chart name and a pinned version; also `global.okdp.charts.oidcDcr` | fix the platform values |
| `database: unknown contract "database"` | no `<contract>.schema.json` in the contracts directory | check `--contracts` / `$OKDP_CONTRACTS` |
| `<file>: cannot unmarshal yaml document: error converting YAML to JSON: yaml: line 1: did not find expected node content` | a values file is not valid YAML | fix the file |
| `global.okdp.ingress.className is required. It is a platform value: set it in platform/platform-values.yaml` | `require` in `service.yaml` lists it, and no layer sets it (or a later one sets it to `null`) | pass the platform values as the first `-f`; set the key ([platform values](specs/01_inputs_outputs.md#platform-values)) |
| `values don't meet the specifications of the schema(s) in the following chart(s):` then `<service>:` and `- at '/replicas': minimum: got 0, want 1` | the parameters violate the package's `values.schema.json`; every violation is listed | fix the instance values ([parameters](specs/01_inputs_outputs.md#parameters)) |
| `compiled values don't meet the specifications of the schema(s) in the following chart(s):` then `<key> (<chart>-<version>):` (`app (app-1.8.0):`) and `- at '/replicaCount': maximum: got 3, want 2` | the final values violate the `values.schema.json` of the upstream chart | fix the instance's upstream values; if they are not involved, the base values or the mapping |
| `database: connection "store" has contract "s3", "database" is expected` | the parameter references a `connections` entry of another contract | reference a connection of the expected contract |
| `database: connection "shared-db": field "user" is not part of the database contract` | an external connection has a field its contract does not declare | remove it ([connections](specs/01_inputs_outputs.md#connections)) |
| `database: connection "shared-db": "password" is a secret field` | a secret field written in the connection | put it in a Secret named by `secretRef` |
| `database (connection "shared-db"): field "dbName" is required by the database contract` | a required contract field is missing | set it |
| `<field>: "<name>" is not a connection, and the <contract> contract has no internal naming convention` | the name is not in `connections`, and the contract cannot resolve an instance | add the connection under `connections` |
| `webapp: database connection "shared-db" has no secretRef, the password cannot be read` | not a compiler check: a message of the example mapping's `errors` section | add `secretRef: {name: ...}` to the connection |
| `app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed` | the instance's upstream values set a path `service.yaml` protects (`protect`, and always `global`); a file name replaces `upstream.<key>` for an instance file | remove the value; set it through the parameters when the package offers one |
| `app: upstream.app: a key or value contains "{{"` | template code in the instance's upstream values | remove it |
| `app: upstream.app.env must be a list` | a path of `append` given as a non-list | give a list |
| `<key>: upstream file <file>: no instance files (Inputs.Instance)` | `service.yaml` names upstream files, and no instance directory is given | pass `--instance` / `Inputs.Instance` |
| `global.okdp.oidc.clientProvisioning "manual" is not one of existing, dcr` | wrong platform value | `existing` or `dcr` |
| `$ref <url>: the schema may only reference itself (the compile reads no file or URL)` | a `values.schema.json` references a file or a URL | inline the referenced schema |

### Mapping

The errors of `mapping.cel.yaml`, mostly for package authors, are listed
with their causes in [service definition](specs/02_service_definition.md#errors):
the messages of its `errors` section, one per line; CEL errors prefixed with
the section, key and list position path (`output: app: env[2]: value: ${{ ... }}: no such key: host`);
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
| `global.okdp.oidc.dcr.registrationUrl is required (clientProvisioning: dcr): the endpoint the oidc-dcr Jobs register the OAuth clients at` | `dcr` mode, at least one client, and no `registrationUrl` (`testdata/dcr-without-registration-url.yaml`) | set it in the platform values |
| `service.yaml: charts.oidc-dcr: the chart of oidcClients[0] is added by the compiler: remove it from service.yaml` | `service.yaml` lists the chart key of a client | remove it: the compiler adds the client charts; rename the chart key or the client when it is another chart |
| `mapping output "oidc-dcr": the chart of oidcClients[0] is compiled from oidcClients: remove it from output` | `output` computes values for the chart key of a client | remove it; set registration fields with the client's `request` |
| `oidc-dcr: chart oidc-dcr-0.5.0 is not in the chart cache: helm pull oci://quay.io/adaltas/oidc-dcr --version 0.5.0 -d <chart cache>` | `dcr` mode, at least one client, and the chart cache lacks the platform's oidc-dcr chart (`existing` mode does not need it) | pull it into the chart cache ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
