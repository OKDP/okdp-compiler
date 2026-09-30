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
okdp-compile --chart <dir> --service <dir> [--contracts <dir>] [--instance <dir>]
             [-f <values.yaml>]... [--release <name>] [--namespace <name>]
             [--out <file>] [--emit-dir <dir>] [--expect <file>]
okdp-compile --version
```

| Flag | Default | |
|---|---|---|
| `--chart` | required | wrapper chart directory: `Chart.yaml`, `values.yaml`, `values.schema.json`, `vendor/`, `vendor-values/` ([wrapper chart](specs/01_inputs_outputs.md#wrapper-chart)) |
| `--service` | required | service definition directory: `service.yaml` and `mapping.cel.yaml` at its root |
| `--contracts` | `$OKDP_CONTRACTS` | contract schemas directory: `<contract>.schema.json` ([contracts](specs/01_inputs_outputs.md#contracts)); required, by flag or variable |
| `--instance` | none | instance directory: the upstream values files `service.yaml` names (`upstream.<key>.yaml`, by chart key; [instance values](specs/01_inputs_outputs.md#instance-values)) |
| `-f` | none | a values file; repeatable, merged in order as `helm -f` files. The platform values come first ([platform values](specs/01_inputs_outputs.md#platform-values), [merge precedence](specs/01_inputs_outputs.md#merge-precedence)) |
| `--release` | `demo-<base name of --chart>` | release name; give it explicitly when `--chart` is `.` (the default would be `demo-.`) |
| `--namespace` | `demo` | namespace |
| `--out` | none | write the computed values, the chart index, the objects and the dependencies to one YAML file |
| `--emit-dir` | none | write the final values of each chart, the chart index, the objects and the dependencies to a directory, one file each |
| `--expect` | none | compare the result with a `helm template` output of the wrapper chart |
| `--version` | | print the version and exit |

Flags take one or two dashes. Without `--out`, `--emit-dir` or `--expect`,
the command only compiles: exit status 0 means the instance compiles.
When several are given, `--emit-dir` is written first, then `--out`, then
the comparison runs.

### `--out`

One YAML document, keys sorted: `charts` (`Result.Charts`, the
[chart index](specs/01_inputs_outputs.md#chart-index): chart key ->
the vendored chart that renders it), `dependencies` (`Result.Dependencies`),
`objects` (`Result.Objects`) and `values` (`Result.Computed`: chart key ->
what the mapping computed, before the merge with the chart defaults, the
base values and the instance's upstream values; in `dcr` mode, also the
values of the [OAuth client charts](specs/01_inputs_outputs.md#oauth-client-charts)).
The final values are written by `--emit-dir`, not by `--out`.

```console
$ export OKDP_CONTRACTS=example/contracts
$ bin/okdp-compile --chart example/webapp --service example \
    -f example/platform-values.yaml -f example/values/internal-db.yaml --out /dev/stdout
charts:
  app: app
  metrics: metrics
  okdp: okdp
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
$ bin/okdp-compile --chart example/webapp --service example \
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
| `<key>.values.yaml` | `Result.Values[key]`: the final values of the chart key, one file per chart the mapping outputs, and per [OAuth client chart](specs/01_inputs_outputs.md#oauth-client-charts) in `dcr` mode |
| `okdp.charts.yaml` | `Result.Charts`, the [chart index](specs/01_inputs_outputs.md#chart-index): `<key>: <vendored chart>`, one entry per values file; `{}` when there is no chart |
| `okdp.objects.yaml` | `Result.Objects`: a YAML list of whole manifests |
| `okdp.dependencies.yaml` | `Result.Dependencies`: a YAML list, `[]` when there is none |

With `example/values/dcr.yaml` (the platform registers the OAuth clients by
DCR), the webapp's client adds the chart `oidc-dcr`, which `service.yaml`
does not list:

```console
$ bin/okdp-compile --chart example/webapp --service example \
    -f example/platform-values.yaml -f example/values/dcr.yaml --emit-dir dcr
$ ls dcr
app.values.yaml
oidc-dcr.values.yaml
okdp.charts.yaml
okdp.dependencies.yaml
okdp.objects.yaml
okdp.values.yaml
$ cat dcr/okdp.charts.yaml
app: app
oidc-dcr: oidc-dcr
okdp: okdp
```

A chart the mapping leaves out has no file and no index entry: metrics are
off in `example/values/dcr.yaml`, so there is no `metrics.values.yaml`; in
`existing` mode (`example/values/internal-db.yaml`) there is no
`oidc-dcr.values.yaml`. Files of a previous run are not removed: emit into
an empty directory.

### `--expect`

Compares the result with a `helm template` output of the wrapper chart,
rendered with the same values files, release and namespace (see
[compare](specs/01_inputs_outputs.md#compare)). The report goes to
stdout; any difference fails the command with `error: <n> difference(s)`.
The example wrapper chart has no templates: the report below was produced
against a stream assembled from the emitted files, with one value changed.

```console
$ bin/okdp-compile --chart example/webapp --service example \
    -f example/platform-values.yaml -f example/values/internal-db.yaml --expect rendered.yaml
  objects        identical to ConfigMap/demo-webapp-okdp
  objects        identical to ConfigMap/demo-webapp-settings
  objects        identical to Password/demo-webapp-session-session-key
  objects        identical to ExternalSecret/demo-webapp-session
  app            DIFF vs demo-webapp-app-values:
      changed .replicaCount: got 3, want 2
  metrics        identical to demo-webapp-metrics-values
  okdp           identical to demo-webapp-okdp-values
error: 1 difference(s)
```

### Exit status and errors

| Status | When |
|---|---|
| 0 | compiled (and identical, with `--expect`); `--version`, `--help` |
| 1 | any error: `error: <message>` on stderr |
| 2 | unknown flag, or a flag without its argument: the message and the usage on stderr |

A message is a whole report and may span several lines: every schema
violation, every message of the mapping's `errors` section.

```console
$ bin/okdp-compile --chart example/webapp --service example \
    -f example/platform-values.yaml -f testdata/schema-violation.yaml
error: values don't meet the specifications of the schema(s) in the following chart(s):
webapp:
- at '/metrics/enabled': got string, want boolean
- at '/replicas': minimum: got 0, want 1
```

### Rendering with Helm

Render each key of the index with its `<key>.values.yaml` alone, with the
vendored chart the index names for it, `index[key]`, and the release and
namespace of the compile:

```bash
helm template <release> <wrapper chart>/vendor/<index[key]> --namespace <namespace> -f <dir>/<key>.values.yaml
```

`index[key]` is the key's entry in `okdp.charts.yaml`: the key itself, its
[alias](specs/02_service_definition.md#aliases), or `oidc-dcr` for an
OAuth client chart, so one vendored chart may render several keys, each with
its own values file. Do not add the base
values (`vendor-values/`), the instance files or the values files of the
compile: the final values already merge the chart's own defaults, the base
values, the computed values and the instance's upstream values
([merge precedence](specs/01_inputs_outputs.md#merge-precedence)),
and give what Helm gives with them. A chart the index does not list is not
rendered.

The example's `vendor/<chart>/` hold only the charts' `values.yaml`, enough to
compile but not to render. With the charts unpacked there (here in a copy of
the wrapper, `webapp/`, with the okdp-objects chart under `vendor/okdp/` and
oidc-dcr 0.5.0 under `vendor/oidc-dcr/`), from the `dcr` directory above:

```bash
yq 'to_entries[] | .key + " " + .value' dcr/okdp.charts.yaml |
while read -r key chart; do
  helm template demo-webapp "webapp/vendor/$chart" --namespace demo -f "dcr/$key.values.yaml"
done > manifests.yaml
```

```console
$ helm template demo-webapp webapp/vendor/oidc-dcr --namespace demo -f dcr/oidc-dcr.values.yaml | yq -N '.kind + " " + .metadata.name'
ServiceAccount demo-webapp-oidc-dcr
ConfigMap demo-webapp-oidc-dcr
Role demo-webapp-oidc-dcr
RoleBinding demo-webapp-oidc-dcr
Service demo-webapp-oidc-dcr-headless
Job demo-webapp-oidc-dcr
$ helm template demo-webapp webapp/vendor/okdp --namespace demo -f dcr/okdp.values.yaml | yq -N '.kind + " " + .metadata.name'
ConfigMap demo-webapp-okdp
ConfigMap demo-webapp-settings
ExternalSecret demo-webapp-session
Password demo-webapp-session-session-key
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
	"fmt"
	"log"
	"maps"
	"os"
	"slices"

	"github.com/okdp/okdp-compiler/compiler"
)

func main() {
	def, err := compiler.LoadDefinition(os.DirFS("example")) // service.yaml, mapping.cel.yaml
	if err != nil {
		log.Fatal(err)
	}
	in := compiler.Inputs{
		Chart:     os.DirFS("example/webapp"),
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
	// Each chart key is rendered by the vendored chart res.Charts[key].
	for _, key := range slices.Sorted(maps.Keys(res.Charts)) {
		fmt.Printf("%s: vendor/%s\n", key, res.Charts[key])
	}
	for _, d := range res.Dependencies {
		fmt.Printf("dependency: %s -> %s (%s)\n", d.Field, d.Instance, d.Contract)
	}
}
```

```text
charts: 4 objects: 4
replicaCount: 3
app: vendor/app
metrics: vendor/metrics
oidc-dcr: vendor/oidc-dcr
okdp: vendor/okdp
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
		"service.yaml":     {Data: []byte("charts:\n  app: {base: vendor-values/app.yaml}\n")},
		"mapping.cel.yaml": {Data: []byte("output:\n  app:\n    replicaCount: ${{ values.replicas }}\n")},
	})
	if err != nil {
		log.Fatal(err)
	}
	chart := fstest.MapFS{
		"Chart.yaml":             {Data: []byte("name: demo\nversion: 1.2.3\nappVersion: \"4.5\"\n")},
		"values.yaml":            {Data: []byte("replicas: 1\n")},
		"values.schema.json":     {Data: []byte(`{"properties": {"replicas": {"type": "integer"}}}`)},
		"vendor/app/values.yaml": {Data: []byte("replicaCount: 1\nimage: {tag: latest}\n")},
		"vendor-values/app.yaml": {Data: []byte("image: {tag: \"4.5\"}\n")},
	}
	res, err := compiler.Compile(def, compiler.Inputs{
		Chart:     chart,
		Contracts: fstest.MapFS{}, // no connection, no provides: no contract read
		Values:    []map[string]any{{"replicas": 3}},
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
global: {}
image:
    tag: "4.5"
replicaCount: 3
```

Any `fs.FS` works: `embed.FS` (`fs.Sub` for a subdirectory), a git tree.

`LoadDefinition(fsys)` reads `service.yaml` and `mapping.cel.yaml` at the
root of `fsys`; a `Definition{Service, Mapping}` can also be built in Go.

`Inputs` takes what the flags give: `Chart` (`--chart`), `Instance`
(`--instance`, `nil` when there is none), `Contracts` (`--contracts`; it may
be empty when nothing references a contract), `Values` (the `-f` files,
parsed, in order), `Release` and `Namespace`; all but `Instance` and
`Values` are required ([inputs and outputs](specs/01_inputs_outputs.md)).
`Result.Values` is what `--emit-dir` writes, `Result.Computed` what `--out`
writes under `values`, `Charts` (the index), `Objects` and `Dependencies`
what both write ([outputs](specs/01_inputs_outputs.md#outputs)).
`Result.Values` renders as in [rendering with Helm](#rendering-with-helm),
`Result.Charts` being the index.

- `compiler.ParseValues(b)` reads one values file as Helm does (YAML 1.1,
  every document of the file merged in order), for `Inputs.Values`.
- `compiler.DcrChart` (`oidc-dcr`) is the vendored chart of the
  [OAuth client charts](specs/01_inputs_outputs.md#oauth-client-charts):
  `Result.Charts` maps each to it.
- `compiler.Compare(def, in, res, rendered, w)` compares `res` with a
  `helm template` output of the wrapper chart, writes the report to `w`
  and returns `<n> difference(s)` on any difference.
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
  bin/okdp-compile --chart example/webapp --service example \
    -f example/platform-values.yaml -f "$f" --emit-dir "ci-out/$name" || exit 1
done
```

The emitted files can be committed and checked with `git diff --exit-code`,
as `go test ./compiler` does with `example/compiled/`.

While a wrapper chart still renders its upstream charts with Helm templates
(okdp-lib-chart's `okdp.vendor.render`, which writes the
`okdp.io/vendor-values` ConfigMaps), compare the mapping with it on every
`ci/*-values.yaml`, with the same layers, release and namespace on both sides
(not run here: the example wrapper has no templates):

```bash
for f in "$chart"/ci/*-values.yaml; do
  helm template "$release" "$chart" --namespace demo -f platform-values.yaml -f "$f" > rendered.yaml
  okdp-compile --chart "$chart" --service "$service" --release "$release" --namespace demo \
    -f platform-values.yaml -f "$f" --expect rendered.yaml || exit 1
done
```

### GitOps

The compiler writes values and objects, not HelmRelease or Application
objects. A deployment renders each key of the index under the instance's
release name and namespace, as in [rendering with Helm](#rendering-with-helm):
the vendored chart comes from `okdp.charts.yaml`, not from the key.

**OAuth clients.** In `dcr` mode the index lists one chart per OAuth client
(`oidc-dcr`, `<name>-oidc-dcr`, rendered with `vendor/oidc-dcr`;
[OAuth client charts](specs/01_inputs_outputs.md#oauth-client-charts)):
deploy them with the others. Their Job, a Helm pre-install/pre-upgrade hook
(an Argo CD `PreSync` hook), writes the client's Secret; the pods that read
it do not start before it exists.

**OKDP's own objects.** When the service declares the `okdp` chart,
`Result.Objects` are in its values
([the okdp chart](specs/01_inputs_outputs.md#the-okdp-chart)):
deploying `vendor/okdp` with `okdp.values.yaml`, like the other charts,
brings the descriptor ConfigMap, the credentials Secrets and the mapping's
objects to the cluster. Without it, they are only in `Result.Objects`
(`okdp.objects.yaml`, a YAML list): `yq '.[] | split_doc'
out/okdp.objects.yaml` turns the list into a multi-document stream to apply.

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
`<key>` a chart key (of `service.yaml`, or an OAuth client chart), `<chart>`
a vendored chart (`vendor/<chart>`: the key, or its alias), `<wrapper>` the
name in `Chart.yaml`. Every error exits with status 1.

### Inputs and instance values

| Message | Cause | Fix |
|---|---|---|
| `--chart is required`, `--service is required`, `--contracts (or $OKDP_CONTRACTS) is required` | a required flag is missing | give it |
| `open service.yaml: no such file or directory`, `open values.yaml: ...` | `--service` or `--chart` is not the right directory | point `--service` at `service.yaml` and `mapping.cel.yaml`, `--chart` at the wrapper chart root |
| `open values.schema.json: no such file or directory` | the wrapper chart has no parameter schema; it is always read (connection references are declared there) | add `values.schema.json` ([wrapper chart](specs/01_inputs_outputs.md#wrapper-chart)) |
| `metrics: vendor/metrics/values.yaml not found: the chart metrics is not vendored (vendor.yaml, then scripts/vendor-charts.sh)` | a chart the mapping outputs has no vendored `values.yaml` (a chart left out by `$if` is not read) | vendor the chart under `vendor/<chart>/` |
| `metrics: vendor/prometheus-exporter/values.yaml not found: the chart prometheus-exporter is not vendored (vendor.yaml, then scripts/vendor-charts.sh)` | the key `metrics` has the alias `chart: prometheus-exporter`, and no `vendor/prometheus-exporter/` | vendor the chart, or fix the alias ([aliases](specs/02_service_definition.md#aliases)) |
| `service.yaml: charts.metrics.chart "../metrics": not the name of a vendored chart (vendor/<chart>)` | an alias that is a path, `.`, `..`, or has leading or trailing spaces | the name of a directory of `vendor/` |
| `database: unknown contract "database"` | no `<contract>.schema.json` in the contracts directory | check `--contracts` / `$OKDP_CONTRACTS` |
| `<file>: cannot unmarshal yaml document: error converting YAML to JSON: yaml: line 1: did not find expected node content` | a values file is not valid YAML | fix the file |
| `global.okdp.ingress.className is required. It is a platform value: set it in platform/platform-values.yaml` | `require` in `service.yaml` lists it, and no layer sets it (or a later one sets it to `null`) | pass the platform values as the first `-f`; set the key ([platform values](specs/01_inputs_outputs.md#platform-values)) |
| `values don't meet the specifications of the schema(s) in the following chart(s):` then `<wrapper>:` and `- at '/replicas': minimum: got 0, want 1` | the parameters violate the wrapper's `values.schema.json`; every violation is listed | fix the instance values ([parameters](specs/01_inputs_outputs.md#parameters)) |
| `compiled values don't meet the specifications of the schema(s) in the following chart(s):` then `vendor/<key>:`, or `<key> (vendor/<chart>):` for a key with an alias (`metrics (vendor/exporter):`) | the final values violate `vendor/<chart>/values.schema.json` | fix the instance's upstream values; if they are not involved, the base values or the mapping |
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
| `<n> difference(s)` | `--expect`: the result differs from the wrapper's render | read the report on stdout |

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
| `service.yaml: charts.oidc-dcr: the chart of oidcClients[0] is added by the compiler (from vendor/oidc-dcr): remove it from service.yaml` | `service.yaml` lists the chart key of a client, often in a port of a chart that rendered oidc-dcr itself | remove it: the compiler adds the client charts; rename the chart key or the client when it is another chart |
| `mapping output "oidc-dcr": the chart of oidcClients[0] is compiled from oidcClients: remove it from output` | `output` computes values for the chart key of a client | remove it; set registration fields with the client's `request` |
| `oidcClients: vendor/oidc-dcr/values.yaml not found: the OAuth clients are registered by the chart oidc-dcr (repository oci://quay.io/adaltas, version >= 0.5.0): list it in vendor.yaml, then run scripts/vendor-charts.sh` | the mapping declares a client and the wrapper chart does not vendor oidc-dcr, in `existing` mode too | vendor it once, under `vendor/oidc-dcr/` ([wrapper chart](specs/01_inputs_outputs.md#wrapper-chart)) |
