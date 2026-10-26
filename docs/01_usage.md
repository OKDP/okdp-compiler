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
the tag without its `v`. From
[the releases](https://github.com/OKDP/okdp-compiler/releases):

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
command finds the module by its path, `github.com/okdp/okdp-compiler`
(`go.mod`), through the Go module proxy by default; `@latest` is the latest
release.

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
okdp-compile --repository <dir> [--repository <dir>]... -o <dir>
             [--target helm|flux|argocd] [--path-prefix <path>]
             [--flux-source <namespace>/<name>]
             [--repo-url <url>] [--revision <revision>]
okdp-compile --check [--charts <dir>] --repository <dir> [--repository <dir>]...
okdp-compile --version
```

| Flag | Default | |
|---|---|---|
| `--repository` | required | a repository of the tree, its directory, repeatable: any part of the [tree](#repository-layout), each part in one repository; every instance of the tree compiled together ([`--repository`](#--repository)) |
| `--output`, `-o` | required, refused with `--check` | the output directory: under `projects/<namespace>/instances/<release>/`, each instance's umbrella chart, its values, OKDP's own objects and what it publishes, and the files of `--target` ([`--output`](#--output)) |
| `--check` | | compile and check, write nothing: the errors and warnings, no `--output` ([`--check`](#--check)) |
| `--charts` | `$OKDP_CHARTS`, with `--check` | chart cache directory, read by `--check` only: each upstream chart unpacked under its repository's directory, the URL without its scheme, as `helm pull --untar -d <dir>/<repository>/<chart>-<version>` writes it, `<repository>/<chart>-<version>/<chart>/` (`quay.io/adaltas/oidc-dcr-0.5.0/oidc-dcr/`) ([chart cache](specs/01_inputs_outputs.md#chart-cache)); the final values checked against each upstream chart. Without `--check`, refused, and `$OKDP_CHARTS` not read |
| `--target` | `helm` | what is written: `helm`, each instance's umbrella chart; `flux`, with its HelmRelease and the Kustomizations; `argocd`, with its Application ([targets](specs/01_inputs_outputs.md#targets)) |
| `--path-prefix` | the root | `--target flux` and `argocd`: the path of the output directory in the repository the deployer reads |
| `--flux-source` | `flux-system/okdp-gitops` | `--target flux`: the GitRepository of that repository, `<namespace>/<name>` |
| `--repo-url` | required by `--target argocd` | `--target argocd`: the repository Argo CD reads the output directory from |
| `--revision` | `HEAD` | `--target argocd`: its revision |
| `--version` | | print the version and exit |

Flags take one or two dashes, and every input is a flag: an argument that
is not one is refused, where it would end the flags and those after it be
ignored (a directory without its `--repository`). The directories of
`--repository` and `--charts` must exist. Every output is built before any
is written: anything that fails writes nothing; an error while writing may
leave some of the files written.

### `--repository`

Every instance of a tree compiled together, the inputs of each read from
the tree ([repository layout](#repository-layout)): `--repository <dir>`,
repeatable, each directory any part of it. In order:

1. the tree: every instance, by project then instance
   (`compiler.LoadRepository`), every problem of the tree at once;
2. the instances compiled together, each instance with what the others of
   its namespace publish, after those whose outputs it references
   ([compiling instances
   together](specs/01_inputs_outputs.md#compiling-instances-together)),
   checked against the chart cache with `--check --charts`: each refusal
   on stderr, `error: <namespace>/<release>: <message>`, each warning,
   `warning: <namespace>/<release>: <message>`, in the order of the
   instances;
3. their [deployment graph](specs/01_inputs_outputs.md#the-deployment-graph):
   each problem on stderr, `error: <namespace>/<release>: <message>`;
4. the files of the target ([targets](specs/01_inputs_outputs.md#targets)),
   written under `-o` ([`--output`](#--output)); then each dependency,
   `<namespace>/<release>: depends on <instance> (<contract>, <field>)`.

Any problem exits with status 1, nothing written; `--check` stops before
writing, and prints no dependency. The options are checked before the
tree is read.

```console
$ bin/okdp-compile --repository examples/03_full/input/db -o out/tree
demo/demo-workspace: depends on demo-db (database, catalogs[1].database)
demo/demo-workspace: depends on demo-db (database, database)
demo/demo-workspace: depends on demo-alerts (http, notifications.webhook)
```

`out/tree` then holds `examples/03_full/output/`, byte for byte.

| `--target` | Writes, besides each instance's umbrella chart | Options |
|---|---|---|
| `helm` (the default) | nothing | none |
| `flux` | each instance's `helmrelease.yaml` and `kustomization.yaml`, each namespace's `kustomization.yaml` | `--path-prefix`; `--flux-source` |
| `argocd` | each instance's `application.yaml` | `--repo-url`, required; `--revision`; `--path-prefix` |

An option of another target is refused. What the deployer sets up once,
and what sync waves order, is in [targets](specs/01_inputs_outputs.md#targets).

### `--output`

Each instance as one chart, for one Helm release per instance
([GitOps](#gitops)), in `projects/<namespace>/instances/<release>/` of
the output directory:

| File | Content |
|---|---|
| `Chart.yaml` | an umbrella chart: the package's `Chart.yaml`, its metadata kept (`name`, `version`...), with one dependency per key of the [chart index](specs/01_inputs_outputs.md#chart-index): the rendered dependencies of the package, in `Chart.yaml` order, as written; then the OAuth client charts, by key, each its upstream chart (repository, name, version), aliased to the key when the key is not the chart's name ([umbrella chart](specs/01_inputs_outputs.md#umbrella-chart)) |
| `instance.values.yaml` | `Result.Values`: the final values of each key, under the key, after the line that marks it generated, `# Code generated by okdp-compile. DO NOT EDIT.` |
| `okdp.objects.yaml` | when the instance has objects, `Result.Objects`, OKDP's own objects: a YAML stream, one document per object, after the same line ([the objects file](specs/01_inputs_outputs.md#the-objects-file)) |
| `templates/okdp.yaml` | with `okdp.objects.yaml`, the umbrella's template that renders it as written, `{{ .Files.Get "okdp.objects.yaml" }}`: Helm does not template the objects |
| `okdp.info.yaml` | when the instance publishes a URL, a usage or a connection, `Result.Info`: `url`, `usage` and `provides`, the connections it provides, with their `secretRef` ([the info file](specs/01_inputs_outputs.md#the-info-file)) |

The keys are the dependencies of the package and, in `dcr` mode, one per
[OAuth client chart](specs/01_inputs_outputs.md#oauth-client-charts).
A dependency the values file leaves out (its key left out by `$if`, or
null) is no dependency of the umbrella; one it does not write is, with its
defaults
([rendered dependencies](specs/02_service_definition.md#rendered-dependencies)).

The chart has no `values.yaml`: its values are given to Helm as user
values, with `-f`. As the chart's `values.yaml` they would be its defaults,
and Helm drops the nulls of defaults, where a null of the final values
deletes an upstream default
([layers and nulls](specs/01_inputs_outputs.md#layers-and-nulls)).

The advanced example's platform is in `dcr` mode
(`global.okdp.oidc.clientProvisioning: dcr`): the webapp's OAuth client
adds the key `oidc-dcr`, which `Chart.yaml` does not list: a dependency
after `app`, the platform's oidc-dcr chart. It has no objects, and so no
`okdp.objects.yaml`; `okdp.info.yaml` holds its URL, its usage and the
connection it provides; the instance it depends on, `demo-db`, which its
`database` parameter references, is printed on stderr:

```console
$ bin/okdp-compile --repository examples/02_advanced/input/db -o out
demo/demo-webapp: depends on demo-db (database, database)
$ ls out/projects/demo/instances out/projects/demo/instances/demo-webapp
out/projects/demo/instances:
demo-db
demo-webapp

out/projects/demo/instances/demo-webapp:
Chart.yaml
instance.values.yaml
okdp.info.yaml
$ cat out/projects/demo/instances/demo-webapp/Chart.yaml
apiVersion: v2
name: webapp
version: 1.0.0
dependencies:
  - name: app
    repository: https://charts.example.org
    version: 1.8.0
  - name: oidc-dcr
    repository: oci://quay.io/adaltas
    version: 0.5.0
$ cat out/projects/demo/instances/demo-webapp/okdp.info.yaml
url: https://demo-webapp.okdp.example.org
usage: |
  A web application. Open https://demo-webapp.okdp.example.org
provides:
  - name: demo-webapp
    contract: http
    values:
      internalUrl: http://demo-webapp-app.demo.svc:8080
      url: https://demo-webapp.okdp.example.org
```

In the full example, `project.yaml`, the project's file at level 1,
between the platform's file and the instance's, adds the project's
settings and connections, and may change none of the platform's; the
instance's `connections.yaml`, at level 2, adds its own connection, and
may change none of theirs ([levels](specs/01_inputs_outputs.md#levels)).
Its workspace's files list as dependencies those of the package the values
file renders, in `Chart.yaml` order (`api`, `scheduler`, `gateway`,
`metrics`: `backup`, left out, is not among them), then the chart of each
OAuth client in `dcr` mode, by key; its objects, the credentials Secrets
of its outputs and its `objects` section, are in `okdp.objects.yaml`; it
depends on `demo-db`, by two parameters, and on `demo-alerts`
([`--repository`](#--repository)).

The directory is created if needed, and the files written into it,
overwriting those of the same name. The command owns
`<output>/projects/`: a file there it does not write, that of an instance
removed from the tree or one an earlier compile wrote and this one does
not (`okdp.objects.yaml` without objects, which Helm would render,
`okdp.info.yaml` publishing nothing, which a control plane would read), is
removed, and the directories left empty. So `-o` and each `--repository`
must not contain one another, nor a `--repository` another. Nothing else
of the directory is read, removed or checked.

Once the files are written, the command prints on stderr, after the
warnings, the `Result.Dependencies` of each instance: one line per
connection reference to another instance, by parameter path,
`<namespace>/<release>: depends on <instance> (<contract>, <field>)`, the
instance's release name, the contract it must provide and the parameter that
references it ([dependencies](specs/01_inputs_outputs.md#dependencies));
for an output of another name than the instance, the output after the
contract, `depends on demo-pg (database-server demo-pg-hive, db)`. No file
holds them: the graph orders the releases with them
([the deployment graph](specs/01_inputs_outputs.md#the-deployment-graph)).
`--check` prints none.

### `--check`

`--check` compiles as `-o` does and writes nothing; it takes no `-o`.
What fails a compile fails it, with status 1, and its warnings are
printed: a pre-commit hook or a CI job checks a tree without writing its
outputs.

With `--check`, `--charts <dir>` (`$OKDP_CHARTS` by default) names a chart
cache: each upstream chart the instances render unpacked under its
repository's directory, as `helm pull --untar` writes it
([chart cache](specs/01_inputs_outputs.md#chart-cache)). The check reads
each chart's `values.yaml` and `values.schema.json`, nothing else of it:

- the final values of each key, coalesced over the chart's defaults as
  Helm coalesces them when it renders, must meet its `values.schema.json`:
  a violation is an error, those of every key reported together;
- an instance list appended to (`append` of `okdp.yaml`'s `upstream`)
  where the chart's defaults hold a list some item of which the final
  list lacks is a warning: `append` extends the package's list, OKDP's
  defaults and the computed values, and Helm replaces the chart's
  ([append](specs/01_inputs_outputs.md#append)). The package restates the
  items it keeps in its values of that key, its defaults file or its
  values file.

The final values are the same with or without the chart cache: a compile
reads no chart, and `--charts` without `--check` fails, `$OKDP_CHARTS`
not read. The full example's chart cache, `examples/03_full/.charts/`,
holds the charts of its instances, the workspace chart's
`values.schema.json` among them; an instance value its schema refuses
compiles, and fails the check. In `bad`, a copy of the full example's tree
whose workspace's `config.yaml` adds `{name: bad-name, value: x}` to
`upstream.api.env`:

```console
$ bin/okdp-compile --check --charts examples/03_full/.charts --repository bad
error: demo/demo-workspace: compiled values don't meet the specifications of the schema(s) in the following chart(s):
api (workspace-3.2.0):
- at '/env/26/name': 'bad-name' does not match pattern '^[A-Za-z_][A-Za-z0-9_]*$'
```

### Exit status and errors

| Status | When |
|---|---|
| 0 | compiled, or checked (`--check`), each binding and warning on stderr, then, the files written, each dependency, as [`--repository`](#--repository) prints them; `--version`, `--help` |
| 1 | any error: `error: <message>` on stderr, one per refused instance and per problem of the graph, `error: <namespace>/<release>: <message>` |
| 2 | unknown flag, a flag without its argument, an argument that is not a flag: the message and the usage on stderr |

A message is a whole report and may span several lines: every schema
violation, every message of the mapping's `errors` section. An instance
whose `config.yaml` sets `replicas: 0`, its schema's minimum 1:

```console
$ bin/okdp-compile --repository tree -o out
error: demo/demo-web: values don't meet the specifications of the schema(s) in the following chart(s):
web:
- at '/replicas': minimum: got 0, want 1
```

A warning (`Result.Warnings`) does not fail the compile: it is printed
before the files are written, and the command exits with status 0. A
`values.yaml` that holds expressions gives one
([values.yaml](specs/02_service_definition.md#valuesyaml)), and so does,
with `--check --charts`, an instance list appended to that leaves out
items of the chart's default list ([`--check`](#--check)):

```text
warning: demo/demo-hello: values.yaml holds expressions or directives (${{ }}, $-keys): name it values.cel.yaml
warning: demo/demo-webapp: upstream.app.env: appended to the package's list, not to the default list of app-1.8.0, which Helm replaces: the final list lacks 1 of its 2 items; restate them in the package's values of app (its defaults or values file) to keep them
```

A dependency (`Result.Dependencies`) is no warning: it is printed after
the files are written, one line each, by parameter path, and never with
`--check` ([`--output`](#--output)):

```text
demo/demo-webapp: depends on demo-db (database, database)
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
already merge OKDP's defaults, the computed values and the instance's
upstream values, and Helm applies the chart's own defaults ([merge
precedence](specs/01_inputs_outputs.md#merge-precedence)). The example's
charts are fictional: its umbrella compiles, it does not render.
The umbrella's own template, `templates/okdp.yaml`, writes each document
of `okdp.objects.yaml` as a manifest, as written
([the objects file](specs/01_inputs_outputs.md#the-objects-file)); the
example has none.

Installed by hand, as one release, its dependencies built in a copy of
the directory; the OAuth client charts need no order, their Job being a
pre-install hook:

```bash
chart=$(mktemp -d) && cp -R <dir>/. "$chart" && helm dependency build "$chart"
helm upgrade --install <release> "$chart" --namespace <namespace> -f <dir>/instance.values.yaml
```

Each upstream chart renders as a subchart, with the final values of its
key, as it renders alone, but for what Helm does to every subchart
(checked with Helm 4.3.0 and the versions the deployers embed,
[GitOps](#gitops), [umbrella chart](specs/01_inputs_outputs.md#umbrella-chart)):

- an aliased subchart is named after its alias: its `.Chart.Name` is the
  key, not the chart's name. A name the chart derives from `.Chart.Name`
  changes, unless the values set it (`fullnameOverride`, `nameOverride`);
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

## Repository layout

The tree of an OKDP deployment: the platform, the catalog of packages,
the projects and their instances. `--repository` reads it
(`compiler.LoadRepository`), each file the input of the instances it is
for (`db/` is an example's `input/db/`, beside its `output/`, and its
`.charts/` for the full example):

```text
db/
  platform.yaml                          optional: the context, level 0 (Inputs.Context)
  contracts/<contract>.schema.json       the contracts (Inputs.Contracts)
  catalog/<service>/<version>/           a package (Definition)
  projects/<project>/                    a namespace (Inputs.Namespace)
    project.yaml                         optional: the context, level 1
    instances/<instance>/                a release (Inputs.Release)
      instance.yaml                      {service: <name>, version: <version>}: which package
      connections.yaml                   optional: the context, level 2
      config.yaml                        optional: the instance values (Inputs.Values)
.charts/                                 --check --charts <dir>
output/projects/<project>/instances/<instance>/   -o <dir>
```

- `instance.yaml` holds `service` and `version`, nothing else: the
  instance's package is `catalog/<service>/<version>/`.
- The context has a file per level
  ([levels](specs/01_inputs_outputs.md#levels)): 0 `platform.yaml`, the
  platform values (`global.okdp`, the connections every instance may
  reference); 1 `project.yaml`, what a project adds for its instances (its
  settings under `global`, its connections); 2 `connections.yaml`, the
  instance's own connections (`connections: {<name>: {...}}`), written as
  the lower levels write theirs, and nothing else: `global` is the
  platform's or the project's. A level adds values and changes none of a
  lower level's.
- `config.yaml` is the instance values: its parameters and upstream
  values, no `connections`.
- The release is the instance's directory name, the namespace the
  project's: no field overrides them.
- `.charts/`, the chart cache of the checks (`helm pull --untar`), is
  outside `db/`: it is not source. Only the full example has one; the
  compiles read none.

`--repository` checks that `catalog/<service>/<version>/` holds a
`Chart.yaml` of that `name` and `version`; a caller of a compile does,
and treats a released version directory as immutable: a change is a new
version.

The tree may be in several repositories, `--repository` given for each (a
`compiler.Repository` each): a platform repository with `platform.yaml`,
catalog repositories with `catalog/` and `contracts/` (OKDP's
platform-charts is one, as it is), a repository per project with
`projects/<project>/`, or all in one. They make one tree, part by part,
with no precedence, so that the order of the flags changes nothing:

| Part | Across repositories |
|---|---|
| `platform.yaml` | in one at most |
| `catalog/<service>/` | in one: every version of a service from it |
| `contracts/<contract>.schema.json` | in several, the same bytes in each: a catalog ships the contracts of its packages |
| `projects/<project>/` | in one: the project's file and its instances |

An instance's package and contracts come from any repository. The
compiler reads directories: checking a repository out at a revision is
its caller's.

`TestExample` (`examples_test.go`) compiles each example's tree this way
(`LoadRepository`, then `CompileAll`), and compares what the helm target
writes with its `output/`. The example instances are named
after the release names of their outputs, `demo-hello`, `demo-webapp`,
`demo-db`, `demo-workspace`, `demo-alerts`; a real instance would be named
`hello`, the namespace already saying `demo`. The full example's command
([`--output`](#--output)) gives every file of the tree.

## Go library

`go get github.com/okdp/okdp-compiler@vX.Y.Z`, a version of
[the releases](https://github.com/OKDP/okdp-compiler/releases), `@latest`
the latest. To build against a checkout next to the module, while changing
both, the `replace` dropped before the commit:

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
	def, err := compiler.LoadDefinition(os.DirFS("examples/02_advanced/input/db/catalog/webapp/1.0.0")) // the package
	if err != nil {
		log.Fatal(err)
	}
	in := compiler.Inputs{
		Contracts: os.DirFS("examples/02_advanced/input/db/contracts"),
		Release:   "demo-webapp",
		Namespace: "demo",
		// What the other instances of the namespace publish (their okdp.info.yaml),
		// by release: demo-db, the database the instance references.
		Published: map[string]compiler.Info{"demo-db": {Provides: []compiler.Output{{
			Name: "demo-db", Contract: "database",
			Values:    map[string]any{"host": "demo-db-db.demo.svc", "dbName": "demo-db"},
			SecretRef: &compiler.SecretRef{Name: "demo-db-database-credentials"},
		}}}},
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
	in.Context = []compiler.ContextLayer{{Values: read("examples/02_advanced/input/db/platform.yaml")}}
	in.Values = []map[string]any{read("examples/02_advanced/input/db/projects/demo/instances/demo-webapp/config.yaml")}
	res, err := compiler.Compile(context.Background(), def, in)
	if err != nil {
		log.Fatal(err) // the whole report, e.g. every schema violation
	}
	fmt.Println("charts:", len(res.Values), "outputs:", len(res.Info.Provides))
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
charts: 2 outputs: 1
replicaCount: 2
app: https://charts.example.org app 1.8.0
oidc-dcr: oci://quay.io/adaltas oidc-dcr 0.5.0
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
	res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
		Contracts: fstest.MapFS{},                    // no connection, no provides: no contract read
		Values:    []map[string]any{{"replicas": 3}}, // no context: the package requires no platform value
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
```

Both programs are examples of `example_test.go` (`ExampleCompile`,
`ExampleWrite`, as README's is `Example`): `go test` runs them, and checks
that they are the programs above and print what they show
(`TestDocPrograms`).

Any `fs.FS` works: `embed.FS` (`fs.Sub` for a subdirectory), a git tree.

`compiler.LoadDefinition(fsys)` reads the package at the root of `fsys`:
`Chart.yaml`, `okdp.yaml` and the values file, `values.cel.yaml` or
`values.yaml`, then keeps `fsys` (`Definition.Files`) for the files each
compile reads, `config.schema.json` and the defaults files. A `Definition`
can also be built in Go: the `Chart.yaml` metadata (`Chart`), the package
keys of `okdp.yaml` (`Package`; `Package.Require` is a list of keys),
`okdp.yaml` and the values file as bytes (`Okdp`, `Values`, nil when
absent), and the values file's name (`ValuesFile`, `values.cel.yaml` when
empty), which its errors start with.

`compiler.Inputs` takes what the tree gives an instance
(`LoadRepository`): `Charts` (`--charts`, nil without `--check`: with a
chart cache, the compile checks its final values against the upstream
charts, [`--check`](#--check)), `Contracts` (those of `contracts/`; it may
be empty when nothing references a contract), `Context` (the context
files, parsed, each a `ContextLayer` of its level), `Values` (its
`config.yaml`, parsed), `Release`, `Namespace` and `Published` (what the
other instances of the namespace publish, by release, each a
`compiler.Info`; nil, none is known; `CompileAll` sets it); all but
`Charts`, `Context`, `Values` and `Published` are required
([inputs and outputs](specs/01_inputs_outputs.md)). `Result.Values` is what
`-o` writes in `instance.values.yaml`, `Result.Charts` the index the
umbrella's `Chart.yaml` names the charts from, `Result.Info` what it
writes in `okdp.info.yaml`, `Result.Objects` OKDP's objects, what it
writes in `okdp.objects.yaml`, and `Result.Dependencies` what it prints on
stderr, in no file ([outputs](specs/01_inputs_outputs.md#outputs)).
`Result.Values` renders as in [rendering with Helm](#rendering-with-helm),
`Result.Charts` being the index.

`compiler.Write(res)` gives the files `okdp-compile -o` writes (path ->
content, `templates/okdp.yaml` in a subdirectory), for a caller to write
where it keeps them: the umbrella chart (the package's `Chart.yaml`,
`Result.Chart`, its dependencies the compiled charts), its values
(`compiler.UmbrellaValues`), OKDP's objects and their template when there
are objects (`compiler.ObjectsFile`, `compiler.ObjectsTemplate`), and what
the instance publishes when it publishes something
(`compiler.InfoFile`). `compiler.Info` is what `okdp.info.yaml`
holds, to read it back.

- `Result.Warnings` lists what the compile warns of without failing: those
  of the package, found by `LoadDefinition` (`Definition.Warnings`), a
  `values.yaml` that holds expressions; with `Charts`, an instance list
  appended to that leaves out items of the chart's default list. Each
  compile gives them with its result, so that a server compiling many
  instances returns them with each; the binary prints each as
  `warning: <message>`.
- `Result.Dependencies` lists the instances of the namespace the
  connection references point to, by parameter path, each a
  `compiler.Dependency`, `{Instance, Contract, Field, Output}`, `Output`
  the output the reference resolved to, by name
  ([dependencies](specs/01_inputs_outputs.md#dependencies)): what a
  deployer orders the releases with ([GitOps](#gitops)). `Write` writes
  them in no file; the binary prints each, once the files are written, as
  `depends on <instance> (<contract>, <field>)`, the output after the
  contract when it is not named after the instance.
- `compiler.LoadRepository(repos)` reads a tree, in one or more
  repositories, each a `compiler.Repository` (its name in messages, its
  `fs.FS`), as the instances `CompileAll` compiles, by project then
  instance ([repository layout](#repository-layout)); every problem at
  once, each naming its repository and path.
- `compiler.CompileAll(ctx, instances)` compiles instances together, each
  a `compiler.Instance`, its `Definition` and its `Inputs` without
  `Published`: each instance of a namespace after the instances whose
  outputs it references, with what they publish. One `compiler.Outcome`
  per instance, in their order: its `Result`, or, nil, why it is refused
  (`Err`); the order changes none ([compiling instances
  together](specs/01_inputs_outputs.md#compiling-instances-together)).
- `compiler.NewGraph(results)` is the deployment graph of the results of
  `CompileAll`: each release waits for the releases of its namespace its
  references point to. It checks every dependency, the release exists and
  provides what is referenced, and refuses a cycle, every problem at once,
  each a `*compiler.GraphError` at its release; then `DependsOn` gives
  what a release waits for, `Wave` its Argo CD sync wave
  ([the deployment graph](specs/01_inputs_outputs.md#the-deployment-graph)).
- A target writes the results for a deployer, `Files(results, graph)`,
  path -> content, under `projects/<namespace>/`: `compiler.Helm{}`, the
  umbrella chart of each instance, in `instances/<release>/`;
  `compiler.Flux{Source, PathPrefix}`, with each instance's HelmRelease,
  its `dependsOn` from the graph, and the Kustomizations;
  `compiler.ArgoCD{RepoURL, Revision, PathPrefix}`, with each instance's
  Application, its sync wave from the graph
  ([targets](specs/01_inputs_outputs.md#targets)).
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
- `oidc-dcr` is the chart key of a service's main
  [OAuth client](specs/01_inputs_outputs.md#oauth-client-charts), and the
  suffix of the others; oidc-dcr 0.5.0 of `oci://quay.io/adaltas` is the
  chart they are rendered by unless the platform names another
  ([platform charts](specs/01_inputs_outputs.md#platform-charts)): the
  chart the chart cache of a check needs for a `dcr` platform.
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
  (OAuth clients, generated Secrets; 0: no limit): once
  the total passes it, the compile fails with
  `the expressions of the compile exceed its CEL cost limit (n)`. The
  total is checked after each expression, which may itself cost up to the
  limit: a compile can pass the limit by one expression's cost. A
  server compiling packages it does not review sets one, as an instance's
  values can make an expression iterate over them. The example costs under
  1000, the OAuth client's template most of it (`ExampleCompile_server`).
- Errors are messages for the user, returned as they are; the package
  defines no error types. A message carries its context: the file, the
  parameter path, the chart key, the mapping section (see
  [troubleshooting](#troubleshooting)).

## Integration

### CI

Compile every instance of the tree, the repositories of its parts
checked out side by side; any error fails the job:

```bash
bin/okdp-compile --repository platform --repository catalog --repository projects \
  -o compiled --target flux || exit 1
test -z "$(git status --porcelain compiled/)" || { git status --short compiled/; exit 1; }
```

The instances are compiled together: one that references an output of
another ([resolution](specs/01_inputs_outputs.md#resolution)) gets what
that one publishes, and their dependencies are checked
([`--repository`](#--repository)).

A job that pulls the upstream charts into a chart cache checks the final
values against them too: the same flags, `--check --charts charts` for
`-o` ([`--check`](#--check)).

The emitted files can be committed and checked against the commit, as
`go test .` checks the example's outputs: `git status
--porcelain` lists the changed, removed and new files, where
`git diff --exit-code` misses a new, untracked file (those of a new
instance).

### GitOps

A compile writes the instance as one Helm chart: the umbrella deploys
alone as one Helm release, `helm upgrade --install`
([rendering with Helm](#rendering-with-helm)). OKDP publishes no chart of
the service: the upstream charts come from their own repositories, the
umbrella's dependencies. The targets write compiled instances for a
deployer, `okdp-compile --repository <dir> --target helm|flux|argocd`:
the umbrella chart of each (`Helm`), with its Flux HelmRelease,
`dependsOn` the releases it waits for (`Flux`), or its Argo CD
Application and sync wave (`ArgoCD`), from the
[deployment graph](specs/01_inputs_outputs.md#the-deployment-graph), in
okdp-gitops' format ([targets](specs/01_inputs_outputs.md#targets)).

[okdp-gitops](https://github.com/OKDP/okdp-gitops) compiles a whole
deployments repository with this library, every instance of it, and writes
the Flux and Argo CD files of each, a Flux HelmRelease and its
kustomization or an Argo CD Application, their `dependsOn` and sync waves
from `Result.Dependencies`: the instances of the namespace the connection
references point to, one item per parameter
([dependencies](specs/01_inputs_outputs.md#dependencies)). Each compile
is given what the other instances of its namespace publish
(`Inputs.Published`): a reference names one of their outputs.

A deployer that renders the umbrella, okdp-gitops or another, respects
what it is compiled for:

**The release.** The values of every key are compiled for the instance's
release name and namespace: the umbrella renders under these, one release
per instance, one Flux HelmRelease or one Argo CD Application.

**The values.** `instance.values.yaml` is given to Helm as user values:
Flux `valuesFrom` a ConfigMap of the file (a kustomize
`configMapGenerator`, `valuesKey: instance.values.yaml`), Argo CD
`helm.valueFiles`, `helm -f`. Never as the chart's defaults, Flux
`chart.spec.valuesFiles` or a `values.yaml` of the chart: Helm drops the
nulls of defaults, where a null of the final values deletes an upstream
default ([layers and nulls](specs/01_inputs_outputs.md#layers-and-nulls)).

**The chart.** The umbrella's directory is committed with the instance's
compiled files, and the chart built from the deployments repository. Flux
needs `reconcileStrategy: Revision`, which rebuilds the chart at every
revision of the repository: its version, the package's, does not change
when the platform changes the version of a dependency. Both fetch the
dependencies from their repositories: a private repository needs its
credentials there. Each renders with the Helm it embeds (latest patch
releases, October 2026): Flux 2.9 Helm 4.2.4 (helm-controller 1.6), Flux
2.8 Helm 4.2.0 (helm-controller 1.5), Flux 2.7 Helm 3.19.2
(helm-controller 1.4); Argo CD 3.5 Helm 4.2.1, Argo CD 3.4 and 3.3 Helm
3.19.4. The umbrella, the final values and the schema reports are checked
with each ([rendering with Helm](#rendering-with-helm),
[Helm fixtures](00_architecture.md#helm-fixtures)).

**OAuth clients.** In `dcr` mode the index lists one chart per OAuth
client (`oidc-dcr`, `<name>-oidc-dcr`, rendered by the platform's oidc-dcr
chart; [OAuth client
charts](specs/01_inputs_outputs.md#oauth-client-charts)), dependencies of
the umbrella like the others. Their Job, a Helm pre-install/pre-upgrade
hook (an Argo CD `PreSync` hook), writes the client's Secret; the pods
that read it do not start before it exists.

**OKDP's own objects.** `Result.Objects`, the credentials Secrets and the
mapping's objects, are `okdp.objects.yaml`, a
multi-document stream the umbrella renders as written, in the instance's
release ([the objects file](specs/01_inputs_outputs.md#the-objects-file),
[why](03_okdp_objects.md#how-it-reaches-the-cluster)): deployed with the
upstream charts, pruned with them. A deployer that does not render the
umbrella applies the file as it is.

**What the instance publishes.** `okdp.info.yaml`
([the info file](specs/01_inputs_outputs.md#the-info-file)), in
the instance's directory, beside its chart: its `url`, its `usage`, and
`provides`, the connections it provides with their `secretRef`. Nothing of
it is deployed: a control plane or a console reads it for each instance
from the deployments repository, to list the instances of a namespace
(their URL, usage and version, that of the `Chart.yaml` beside it) and
offer, in a deployment form, the connections they provide, which another
instance of the namespace references by name; an
instance that publishes nothing has no file, neither URL, usage nor
connection. The repository says what is to run; what runs is the GitOps
engine's status.

## Troubleshooting

`<field>` is the parameter path (`database`, `backends[1].database`, a key
that holds a dot in brackets: `databases["eu.main"]`),
`<key>` a chart key (of a `Chart.yaml` dependency, or an OAuth client
chart), `<chart>-<version>` the upstream chart that renders it,
`<service>` the `name` of `Chart.yaml`. Every error exits with status 1, a
usage error with status 2.

### Inputs and instance values

| Message | Cause | Fix |
|---|---|---|
| `--repository is required` | no tree | give its repositories |
| `--check writes nothing: no --output (-o)`, `--charts is read by --check only: a compile reads no chart` | `-o` given with `--check`, `--charts` without it | drop one of them ([`--check`](#--check)) |
| `Inputs.Release "Demo_App": a release name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 53 characters at most (Helm's limit)`, `Inputs.Namespace "<name>": a namespace name, ...` | an instance's or a project's directory is not a name Kubernetes and Helm accept | rename it ([inputs](specs/01_inputs_outputs.md)) |
| `app: stat charts.example.org/app-1.8.0/app: not a directory` | `--charts`, with `--check`, names a file: it fails at the first chart read from it | fix the path |
| `unexpected argument "b": every input is a flag (--repository before each directory)` | an argument that is not a flag, here a second directory after one `--repository`; exit status 2, nothing compiled | one `--repository` per directory |
| `--output (-o) is required` | no output directory | give one: the command writes the tree there |
| `db/projects/demo/instances/demo-web/instance.yaml: the package db/catalog/web/1.0.0: open Chart.yaml: no such file or directory` | a version directory of the catalog without its `Chart.yaml` | add it ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: apiVersion "v1": v2, the only one the compiler reads`, `Chart.yaml: name is required`, `Chart.yaml: version is required`, `Chart.yaml: type "library": a package is an application chart` | the package metadata is incomplete, or not that of an application chart of `apiVersion: v2` | set them ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: version "1.0": SemVer (1.2.3), the version of the umbrella chart` | a package version Helm does not accept as a chart's | a SemVer `version` in `Chart.yaml` |
| `Chart.yaml: yaml: unmarshal errors:` then `line 9: field condtion not found in type chart.Dependency`; `Chart.yaml: one YAML document` | a field Helm's `Chart.yaml` does not define, here a misspelt `condition` in a dependency, one line per unknown field; a second YAML document | fix the name; one document ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: dependencies[1] (api): condition: the values decide what is rendered ($if on api in values.cel.yaml)` | a dependency with `condition`, `tags`, `enabled` or `import-values` | remove it; leave the dependency out with `$if` on its key in the values file ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0] (my_app): key "my_app": a DNS label (lowercase letters, digits and -, a letter or digit at each end, 63 characters at most): the key names the umbrella's dependency alias` | a dependency key (its `alias`, else its `name`) that is not a DNS label: `../app`, an upper case letter, `_` or `.` | give it an `alias` that is one, and rename the key in `okdp.yaml` and the values file ([chart keys](specs/02_service_definition.md#chart-keys)) |
| `Chart.yaml: dependencies[2] (api): the key of dependencies[1] too: give one of them an alias` | two dependencies of one key: a chart rendered twice, one of them without its alias | an alias each ([recipe](02_writing_package.md#render-a-chart-twice)) |
| `Chart.yaml: dependencies[0].name is required` | a dependency without its chart's name | name the published chart that renders it ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0].repository "charts.example.org": the URL of a chart repository (https://... or oci://...)`, `Chart.yaml: dependencies[0].name "a/b": the name of a chart of the repository` | a repository that is not a URL (`https://`, `http://`, `oci://`) or whose host and path are no directory of the chart cache (`https://`, `https://x/../y`), a name that is not a chart's name | fix the dependency ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0].version "^1.8": a pinned chart version (1.2.3), not a range` | a version range | pin the version |
| `okdp.yaml: yaml: unmarshal errors:` then `line 3: field requires not found in type okdp.Package` | a key `okdp.yaml` does not define, here a misspelt `require`; one line per unknown key | fix the name ([okdp.yaml](specs/02_service_definition.md#okdpyaml)) |
| `okdp.yaml: output: the charts' values are in values.cel.yaml (or values.yaml)`, `okdp.yaml: name: the name, version and charts are Chart.yaml's` | a key of the former package format | the values in `values.cel.yaml`, without the `output:` wrapper; the name, version and charts in `Chart.yaml` ([service definition](specs/02_service_definition.md)) |
| `values.yaml and values.cel.yaml: a package has one of them` | the package has both values files | keep one: `values.cel.yaml` when the values hold expressions ([values.yaml](specs/02_service_definition.md#valuesyaml)) |
| `okdp.yaml: defaults.web: not a dependency of Chart.yaml`, `okdp.yaml: upstream.web: not a dependency of Chart.yaml` | a key of `defaults` or `upstream` that is no dependency's key | fix the key, or add the dependency ([okdp.yaml](specs/02_service_definition.md#okdpyaml)) |
| `okdp.yaml: upstream: true, false or a map by dependency`, `okdp.yaml: upstream.app: true, false or {protect, append}` | `upstream`, or a dependency's value in it, of another form: a string, a number, a list; a map with another key than `protect` and `append` (a misspelt `protect`) | `true`, `false`, or a map by key, each `true`, `false` or `{protect, append}` ([upstream](specs/02_service_definition.md#upstream)) |
| `okdp.yaml: upstream.app.protect[0] "a[": not a path: ...` | a `protect` or `append` path out of the grammar | fix the path ([protect and append](specs/02_service_definition.md#protect-and-append)) |
| `okdp.yaml: yaml: unmarshal errors:` then `line 9: cannot unmarshal !!map into string` | a `require` entry of another form than a key (`{key, $if}` included) | a key; check a value of one mode in the mapping's `errors` ([require](specs/02_service_definition.md#require)) |
| `app: chart app-1.8.0 is not in the chart cache: helm pull app --repo https://charts.example.org --version 1.8.0 --untar -d <chart cache>/charts.example.org/app-1.8.0` | with `--check --charts`, a chart the compile renders (a dependency the values file leaves out, by `$if` or null, is not read) is not unpacked in its directory (`charts.example.org/app-1.8.0/app/`), even when the chart cache holds its archive (`helm pull -d` without `--untar`) or the chart elsewhere; for an `oci://` repository the command is `helm pull oci://<repository>/<chart> --version <version> --untar -d <chart cache>/<repository>/<chart>-<version>` | run the command, `<chart cache>` the chart cache, or check `--charts` / `$OKDP_CHARTS` ([chart cache](specs/01_inputs_outputs.md#chart-cache)) |
| `global.okdp.charts.oidcDcr.version "1.x": a pinned chart version (1.2.3), not a range` | a platform chart checked as a source is (its repository, chart and version, the same messages); its form is checked by the platform schema (below) | fix the platform values ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
| `database: the database contract cannot be read: database.schema.json: file does not exist` | a connection reference or a provided connection names a contract no repository's `contracts/` has | add it to a repository ([contracts](specs/01_inputs_outputs.md#location)) |
| `database: the database contract cannot be read: database.schema.json: unexpected EOF` | the contract file is not JSON, not an object or not a valid JSON Schema; also `provides "<name>" (<contract>): the <contract> contract cannot be read: ...` | fix the contract ([contracts](specs/01_inputs_outputs.md#location)) |
| `<file>: cannot unmarshal yaml document: error converting YAML to JSON: yaml: line 1: did not find expected node content` | a values file is not valid YAML | fix the file |
| `global.okdp.ingress.className is required. It is a context value: the platform's or a project's (Inputs.Context)` | `require` in `okdp.yaml` lists it, and no context layer sets it (or a later one of its level sets it to `null`) | set the key in `platform.yaml` ([context values](specs/01_inputs_outputs.md#context-values)) |
| `context layer 2 (level 1): global.okdp.ingress.suffix is set at level 0 (Inputs.Context): a level adds values, it does not change a lower level's`, `context layer 2 (level 1): connections.platform-db is set at level 0 ...` | a context file, the second (`Inputs.Context[1]`), `project.yaml` at level 1, sets a value a file of a lower level sets (the same value and `null` included, or a parent of it to a non-map), or gives a connection of a lower level's connection's name, even by a field it leaves out | remove it from the file: a level only adds keys and connections; change the value in the lower level's files ([levels](specs/01_inputs_outputs.md#levels)) |
| `values layer 1: global.okdp is set by the context (Inputs.Context)`, `values layer 2: global is set by the context ...` | an instance file, the first, second... values layer (`Inputs.Values[0]`, `[1]`...), its `config.yaml` first, sets `global`: a platform value, a key of `global` the parameter schema does not declare, or `global` itself to a non-map | give platform values in `platform.yaml`; a package lets instances set a key of `global` by declaring it in `config.schema.json` ([context values](specs/01_inputs_outputs.md#context-values)) |
| `values layer 1: connections are given by the context (Inputs.Context): an instance's own in a context layer of its level` | an instance's `config.yaml` sets `connections`, to any value | give the instance's own connections in its `connections.yaml`, a context file of its level; a connection of a lower level is not changed ([context values](specs/01_inputs_outputs.md#context-values)) |
| `values layer 1: database: the value contains "{{"`, `values layer 2: upstream.app.podAnnotations.{{ x }}: the key contains "{{"` | template code in an instance's `config.yaml`, in a key or a value under any key: parameters, upstream values; `context layer <n> (level <l>): ...` in a context file above level 0 (`project.yaml`, `connections.yaml`); the platform's files, level 0, are not checked | remove it ([layers](specs/01_inputs_outputs.md#layers)) |
| `values layer 2: json: cannot marshal from Go func() within "/hooks/0"`, `context layer 1 (level 0): json: cannot marshal from Go float64 within "/global/proxy/no.proxy": unsupported value: NaN` | a values layer built in Go, the second of `Inputs.Values`, the first of `Inputs.Context`, holds what a values file cannot: a func, a chan, a complex, a NaN or an infinity, text that is not UTF-8, a key that is not text or a number, two keys that read as the same, a value that contains itself | build the layer of values a file can hold ([values files](specs/01_inputs_outputs.md#values-files)) |
| `values don't meet the specifications of the schema(s) in the following chart(s):` then `<service>:` and `- at '/replicas': minimum: got 0, want 1` | the parameters violate the package's `config.schema.json`; every violation is listed | fix the instance values ([parameters](specs/01_inputs_outputs.md#parameters)) |
| `compiled values don't meet the specifications of the schema(s) in the following chart(s):` then `<key> (<chart>-<version>):` (`app (app-1.8.0):`) and `- at '/replicaCount': maximum: got 3, want 2` | with `--check --charts`, the final values, over the chart's defaults, violate the `values.schema.json` of the upstream chart | fix the instance's upstream values; if they are not involved, OKDP's defaults or the mapping |
| `warning: upstream.app.env: appended to the package's list, not to the default list of app-1.8.0, which Helm replaces: the final list lacks 1 of its 2 items; ...` | with `--check --charts`, the instance appends to a list (`append`) the chart's defaults hold items of that the package's list leaves out: Helm replaces the chart's list with the final one | restate the items to keep in the package's values of the key, its defaults or values file ([append](specs/01_inputs_outputs.md#append)) |
| `database: connection "store" has contract "s3", "database" is expected` | the parameter references a `connections` entry of another contract | reference a connection of the expected contract |
| `database: connection "shared-db": field "user" is not part of the database contract` | an external connection has a field its contract does not declare | remove it ([connections](specs/01_inputs_outputs.md#connections)) |
| `database: connection "shared-db": "password" is a secret field, put it in the credentials Secret` | a secret field written in the connection | put it in a Secret named by `secretRef` |
| `database (connection "shared-db"): field "dbName" is required by the database contract` | a required contract field is missing; one line per missing field | set it |
| `database (connection "shared-db"): the fields violate the database contract:` then `- at '/engine': value must be one of 'postgresql', 'mysql'`, `- at '/port': got string, want integer` | a field of an external connection the contract's schema refuses; one line per violation | give a value of the contract's type (`port: 5432`, not `"5432"`) and among its values ([checks](specs/01_inputs_outputs.md#checks)) |
| `<field>: "<name>" is not a connection, nor an output of an instance of the namespace` | the name is not in `connections`, nor an output of that contract another instance of the namespace publishes (`(demo-pg publishes it with contract "database", "s3" is expected)`: one of another contract) | add the connection under `connections` of a context file (`connections.yaml` for the instance's own), or the instance that publishes the output to the tree ([resolution](specs/01_inputs_outputs.md#resolution)) |
| `<field>: "<name>" names s3 outputs of several instances of the namespace: demo-a, demo-b` | two instances of the namespace publish an output of that name and contract | rename one of the outputs, or reference a connection |
| `<field>: "<name>" is not a connection, nor a release name: lowercase letters, digits and "-", a letter or digit at each end, 53 characters at most` | the name is not in `connections`, and cannot name an instance of the namespace (`Shared_DB`, a name longer than 53 characters) | fix the name, or add the connection under `connections` of a context file |
| `database: a connection reference must be a string, got map[string]interface {}{"host":"db.example.org"}` | a connection reference given as a map, a number or a list (the parameter schema does not say `"type": "string"`) | give the name of a connection or an instance; put the connection itself under `connections` |
| `database (connection "shared-db"): field "host": the value contains "{{"` | template code in a connection given by the platform's values, or in a contract default (a context file above level 0 fails earlier, `context layer <n> (level <l>): ...`) | fix the platform values or the contract |
| several lines of the connection messages above, one `<field>` each | several references do not resolve: every one is reported, in parameter path order (keys sorted at each level), with its first error (the fields of a connection in alphabetical order), or all its missing required fields, or all its schema violations | fix each, then compile again: a fixed field may reveal the next one ([connection errors](specs/01_inputs_outputs.md#connection-errors)) |
| `webapp: database connection "shared-db" has no secretRef, the password cannot be read` | not a compiler check: a message of the example mapping's `errors` section | add `secretRef: {name: ...}` to the connection |
| `app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed` | the instance's upstream values set a path `okdp.yaml` protects (`upstream.<key>.protect`, and always `global`) | remove the value; set it through the parameters when the package offers one |
| `app: upstream.app.podAnnotations.x: the value contains "{{"`, `... the key contains "{{"` | template code in upstream values the platform's values or the package's defaults give (in an instance's, its values file fails before: `values layer <n>: ...`) | remove it |
| `app: upstream.app.env must be a list` | a path of `append` given as a non-list | give a list |
| `context values (Inputs.Context) don't meet the specifications of the schema of global.okdp:` then `- at '/global/okdp/oidc': got boolean, want object`, `- at '/global/okdp/oidc/clientProvisioning': value must be one of 'existing', 'dcr'` | a platform setting the compiler reads, or a map on the way to it, of another type (`oidc: false`, `proxy: <url>`, a list for a string), or not among its values; it would otherwise read as not set. Every violation is listed | fix the platform values against `platform.schema.json`; `null` is not set ([fields the compiler reads](specs/01_inputs_outputs.md#fields-the-compiler-reads)) |
| `failing loading "<url>": no URLLoader registered for "<url>"` | a `config.schema.json`, or an upstream chart's `values.schema.json`, references a file, a URL or a `urn:` | inline the referenced schema |

### Mapping

The errors of the mapping, the sections of `okdp.yaml` and the values
file, mostly for package authors, are listed with their causes in [service
definition](specs/02_service_definition.md#errors): the messages of its
`errors` section, one per line, as written; CEL errors prefixed with the
file, then the section, key and list position path (`values.cel.yaml: app:
env[2]: value: ${{ ... }}: no such key: host`, `okdp.yaml: vars db: ...`);
a key of the values file that is no dependency's (`values.cel.yaml: web:
not a dependency of Chart.yaml`); sections of the wrong shape, against
`mapping.schema.json`, every violation at once (`the mapping's sections
don't meet the specifications of mapping.schema.json:` then `- at
'/output/app': got string, want null or object`); arguments of
`okdpGeneratedSecret` unknown or of the wrong type (`- at
'/okdpGeneratedSecret/keys/0': additional properties 'lenght' not
allowed`); YAML errors (`values.cel.yaml: yaml: line <n>: ...`,
`okdp.yaml: yaml: line <n>: ...`). The `provides` checks are in [the info
file](specs/01_inputs_outputs.md#the-info-file), the OAuth client errors
[below](#oauth-clients).

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
| `context values (Inputs.Context) don't meet ...` then `- at '/global/okdp/oidc/dcr/ttlSeconds': got string, want integer` | a platform setting of the wrong type, here a quoted number; the types of the settings are checked in every mode, with or without a client | fix the platform values; `null` takes the default |
| `- at '/oidcClients/0': additional properties 'redirectUri' not allowed` | a typo, or a registration field written as a client key | use a client key (`mapping.schema.json`); a registration field goes under `request` |
| `global.okdp.oidc.dcr.authMethod "" is not supported: OKDP registers its OAuth clients anonymously` | `dcr` mode, at least one client, and `authMethod` unset (`""`, as when the platform sets no `dcr` settings) or not `anonymous` | `authMethod: anonymous` in the platform values |
| `global.okdp.oidc.dcr.registrationUrl is required (clientProvisioning: dcr): the endpoint the oidc-dcr Jobs register the OAuth clients at` | `dcr` mode, at least one client, and no `registrationUrl` (`testdata/context/0/dcr-without-registration-url.yaml`) | set it in the platform values |
| `Chart.yaml: dependencies[1] (oidc-dcr): the chart of oidcClients[0] is added by the compiler: remove it` | `Chart.yaml` lists the chart key of a client | remove it: the compiler adds the client charts; give the dependency another key (`alias`), or rename the client, when it is another chart |
| `values.cel.yaml: oidc-dcr: the chart of oidcClients[0] is compiled from oidcClients: remove it` | the values file computes values for the chart key of a client | remove it; set registration fields with the client's `request` |
| `oidc-dcr: chart oidc-dcr-0.5.0 is not in the chart cache: helm pull oci://quay.io/adaltas/oidc-dcr --version 0.5.0 --untar -d <chart cache>/quay.io/adaltas/oidc-dcr-0.5.0` | with `--check --charts`, `dcr` mode, at least one client, and the chart cache lacks the platform's oidc-dcr chart (`existing` mode does not need it) | pull it into the chart cache ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |

### Repositories and the graph

`--repository` prints each refusal and each problem of the graph after
`error: <namespace>/<release>: ` ([compiling instances
together](specs/01_inputs_outputs.md#compiling-instances-together), [the
deployment graph](specs/01_inputs_outputs.md#the-deployment-graph)); those
of the targets' options are in
[targets](specs/01_inputs_outputs.md#targets).

| Message | Cause | Fix |
|---|---|---|
| `--flux-source is read by --target flux only` (`--path-prefix`, `--repo-url`, `--revision`) | a target's option with another target | drop it, or give its target |
| `--repo-url is required by --target argocd` | no repository for the Applications | give the one Argo CD reads the output from |
| `--repository db/projects is inside --repository db`, `--output (-o) out and --repository out/db must not contain one another: ...` | nested directories: a part read twice, or an output that would remove the sources | give disjoint directories |
| `project "demo" is in the repositories a and b` (`platform.yaml`, `service "<name>"`) | a part of the tree in two repositories | keep it in one ([repository layout](#repository-layout)) |
| `db/projects/demo/instances/web/connections.yaml: global: the instance's own connections only (connections): ...` | an instance's `connections.yaml` sets more than `connections` | set it in `project.yaml`, or `platform.yaml` ([repository layout](#repository-layout)) |
| `contract s3 differs between the repositories a and b` | two repositories ship different contracts of one name | make them the same, or rename one |
| `db/projects/demo/instances/web/instance.yaml: service "web" is in the catalog of no repository`, `version "2.0.0" of service "web" is not in db/catalog/web` | the instance names a package no repository has | add it to a catalog, or fix `instance.yaml` |
| `error: demo/demo-web: database: "demo-db" is not a connection, nor an output of an instance of the namespace, ... (demo-db does not compile either: what it publishes is unknown)` | the output is published by an instance that is refused, or by none; instances that reference each other's outputs are both refused so | fix that instance; make one of two references a connection file's: it waits for nothing |
| `error: demo/demo-p: provides "x" (server): demo-q publishes an output of that contract and name too, ...` | two instances of the namespace publish one output | name one of them otherwise: a reference must find one |
