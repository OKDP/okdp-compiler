# Usage

How to use okdp-compiler, a Go library: it reads input documents, the
platform's values, projects and instances, and a catalog of packages,
binds the claims of the instances to their providers, and gives the
values of each instance's charts. The file formats it reads and what it
gives are specified in
[service definition](specs/02_service_definition.md) and
[inputs and outputs](specs/01_inputs_outputs.md); how it works inside is
in [architecture](00_architecture.md); how to write a package is in
[writing a package](02_writing_package.md).

The module holds a thin command over the library, `okdp-compile` ([command
line](#command-line)), no writer for a deployer and no chart fetching: it
reads no upstream chart. The rest belongs to a host, a program that
embeds the compiler ([hosts](#hosts)): it may lay out a deployment's files
in a tree of its own and turn them into input documents, write the files
of a deployer engine (Flux, Argo CD) and their order, deploy each
instance as a single release, fetch the upstream charts into a chart
cache and check the final values against them.
The rules of a platform's hosts, OAuth clients and hardened workloads,
and the policy on an instance's upstream values, are the security rule
set's, which a compile is given as any rule set: see
okdp-compiler-security's
[usage](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/01_usage.md).
Helm is not needed to compile, only to install the result.

## Input documents

A compile reads two things: input documents, the platform's values, the
projects and the instances (`compiler.LoadInputs`, `okdp-compile -f`),
and a catalog, the packages and the contracts they name
(`compiler.LoadCatalog`, `okdp-compile --catalog`). The documents are
YAML, any number per file, separated by `---`, in files laid out in any
way, a directory read whole or one file: each says what it is by its
`kind` ([input documents](specs/01_inputs_outputs.md#input-documents)):

```yaml
# examples/04_claims/input/config/inputs.yaml, trimmed
kind: Settings                    # the platform's values, every instance's first
valuesFiles: [platform.yaml]
---
kind: Instance                    # the release data-postgresql, namespace data
project: data
name: postgresql
service: postgresql               # the package catalog/postgresql/1.0.0/
version: 1.0.0
claimers: [demo]                  # demo's instances may claim of it
---
kind: Project                     # the namespace demo
name: demo
valuesFiles: [demo/project.yaml]
---
kind: Instance                    # the release demo-dashboards, namespace demo
project: demo
name: dashboards
service: app
version: 1.0.0
valuesFiles: [demo/dashboards/config.yaml]
```

| Kind | Keys | What a compile receives |
|---|---|---|
| `Settings` | `values`, `valuesFiles` | the first values layers of every instance; one document at most |
| `Project` | `name`, `values`, `valuesFiles` | the values layers of the instances of the project `name`, a namespace, after the `Settings` document's, its groups among them (`global.okdp.access.groups`); optional |
| `Instance` | `project`, `name`, `service`, `version`, `claimers`, `connections`, `values`, `valuesFiles` | the release `<project>-<name>` of the namespace `<project>`, of the package `catalog/<service>/<version>/`; `connections`, a layer `{connections: ...}` after its project's; `values` and `valuesFiles`, its own layers, last |

- `valuesFiles` lists values files, each a path relative to the
  document's file, inside the same input files, read as Helm reads one
  (`compiler.ParseValues`), in order, then `values`, inline, last. A file
  a document names so is a values file, not read for documents; every
  other `.yaml` and `.yml` file of a directory given is, each non-empty
  document of a kind, of its keys only.
- An instance's values layers are, in order, the `Settings` document's,
  the platform values (`global.okdp`, the classes of the claims and
  their default among it, the platform's secret store, the connections
  every instance may reference); the `Project` document's, what a
  project adds for its instances (its settings under `global`, its
  classes, its connections, its named claims, `claims: {<name>:
  {contract: <contract>}}`, which its instances reference by name); its
  `connections`, its own (`{<name>: {...}}`); then its own values. They
  merge in order, as `helm -f` merges files
  ([layers](specs/01_inputs_outputs.md#layers)): any layer may set any
  key, and a later one changes what an earlier one sets. The compiler
  does not know where a layer comes from. What each may set (the
  platform's settings to the platform, the keys of `global` a project
  sets, no template code from a layer no one reviews) is a host's to
  decide, before it gives the layers: `okdp-compile` decides none of it,
  and trusts whoever writes the input documents with every key. A
  connection's credentials are in a Secret of the instance's namespace,
  `secretRef: {name: <secret>}`, or in the platform's secret store,
  `secretRef: {remoteKey: <key>}`, which the compiler reads into one;
  which keys a namespace's connections may read is a host's to decide,
  and a key under `platform/` is refused with `secrets.provider`, where
  no namespace reads it
  ([credentials in the platform's secret
  store](specs/01_inputs_outputs.md#credentials-in-the-platforms-secret-store)).
- `claimers`, optional, lists the namespaces whose instances may claim of
  the instance, a provider, from another namespace, by naming it or
  through a class, whichever class routes them (`claimers: [demo]`,
  `["*"]` for any; absent, its package's, `provisions`, none by default)
  ([providers](specs/01_inputs_outputs.md#providers)).
- A `Project` document's values may give the identity provider's groups
  their roles in the namespace, `global.okdp.access.groups`,
  `{<group>: viewer|editor|admin}`, a setting of the security set,
  which checks it and gives a mapping the groups of each role in
  `platform.access`, with the platform's administrators
  (`global.okdp.access.admins`): absent, no group; the compiler gives no
  default group, and a compile without the security set takes the
  setting unread. A host that writes Flux or Argo CD files writes them
  as the Argo CD roles of the project's AppProject and the RoleBindings
  of its namespace to Kubernetes' `view`, `edit` and `admin`, with the
  prefix the API server gives the groups, a setting of its own rule set
  ([access](specs/01_inputs_outputs.md#access)). Who may set it is a
  host's to decide; to `okdp-compile`, whoever writes the documents.
- The instance values are its parameters and upstream values; like any
  layer they may set any key, which a host that does not review them
  refuses.
- Every connection of an instance's layers, whichever gives it, is
  checked at its declaration, referenced or not
  ([connections](specs/01_inputs_outputs.md#connections)): one that
  does not hold refuses the instances whose layers give it, at
  `connections.<name>`.
- The release is `<project>-<name>`, the project and the instance's name
  joined by `-`, the namespace the project: no key overrides them. The
  instance `webapp` of the project `demo` is the release `demo-webapp` of
  the namespace `demo`: an output it publishes is named `webapp` by
  default, the instance's name, as its claims' IDs are
  (`demo.webapp.database`), and a class names it as a provider by its
  name or its release, `demo/webapp` or `demo/demo-webapp`
  ([providers](specs/01_inputs_outputs.md#providers)). A project, an
  instance, is given once, and a release is unique in its namespace
  (`CompileAll`); across namespaces, names that hold a `-` may make one
  release twice (`a-b/c` and `a/b-c` are both `a-b-c`), which a host that
  writes the files of a deployer engine may refuse.

Every problem of the documents is reported at once, each naming its file,
the document's position and line and its kind
([input errors](specs/01_inputs_outputs.md#input-errors)). Where the
documents are kept is the caller's: a host that lays out a deployment's
files in a tree of its own (a platform's file, a directory per project
and per instance) gives the compiler the documents of it, and checks its
own layout.

The catalog is one or more directories, each read for two directories
and nothing else ([the catalog](specs/01_inputs_outputs.md#the-catalog)):

```text
charts/
  catalog/<service>/<version>/           a package (Definition)
  contracts/<contract>.schema.json       the contracts (Inputs.Contracts)
```

A service is in one directory, every version of it; a contract may be in
several, the same bytes in each: a catalog ships the contracts of its
packages, and the order of the directories changes nothing. An
instance's `catalog/<service>/<version>/` holds a `Chart.yaml` of that
`name` and `version`, which `LoadInputs` checks; a caller of a compile
treats a released version directory as immutable: a change is a new
version. The compiler reads directories: checking a repository out at a
revision, and which repositories a deployment trusts for what, are its
caller's.

The examples of this module, `examples/<name>/`, are laid out so:
`input/catalog/<service>/<version>/` and `input/contracts/`, the catalog
(`--catalog input`), and `input/config/`, the input documents,
`inputs.yaml`, beside the values files it names (`platform.yaml`,
`demo/project.yaml`, `demo/webapp/config.yaml`...).
`exampleInstances` (`examples_test.go`) reads them as `okdp-compile`
does, `LoadCatalog` then `LoadInputs`; `TestExample` compiles them
(`CompileAll`) and compares what `Write` gives with its `output/`. The
example instances of the project `demo`, `hello`, `webapp`, `db`,
`dashboards`..., are the releases `demo-hello`, `demo-webapp`,
`demo-db`, `demo-dashboards`; the claims example's provider,
`postgresql` of the project `data`, is `data-postgresql`.

## Installing an instance

A compile gives values, no chart: the package of the catalog is installed
as it is. `compiler.Write(res)` gives, by file name:

| File | When | What |
|---|---|---|
| `values.yaml` (`compiler.ValuesFile`) | the instance has charts | the final values of the package's charts, by key; a dependency the mapping leaves out switched off (`<key>: {enabled: false}`) |
| `okdp-objects.values.yaml` (`compiler.ObjectsValuesFile`) | the instance has objects | the values of the [okdp-objects chart](specs/01_inputs_outputs.md#the-objects-chart): `manifests`, the objects as one YAML stream |
| `okdp.info.yaml` (`compiler.InfoFile`) | the instance publishes something | [what it publishes](specs/01_inputs_outputs.md#the-info-file), for the instances that reference it |

Each instance is two Helm releases, in the namespace of its project, the
objects first, as they hold the Secrets the package's charts read:

```bash
helm install demo-webapp-okdp ../okdp-objects-chart -n demo -f okdp-objects.values.yaml
helm dependency build catalog/webapp/1.0.0
helm install demo-webapp catalog/webapp/1.0.0 -n demo -f values.yaml
```

The chart of the objects is the one `Result.ObjectsSource` names,
`global.okdp.charts.objects` of the platform or the rule set's default.
That chart is OKDP/okdp-objects-chart 0.1.0, installed from a checkout:
it is not published to a registry yet.
`okdp-objects.values.yaml` absent, there is no first release. A dependency
the mapping may leave out needs `condition: <key>.enabled` in the
package's `Chart.yaml`, without which `Write` fails. Deploying each
instance as one release, an umbrella chart over both, is a host's: a
host that writes the files of a deployer engine may write that.

## Command line

`okdp-compile`, from a release ([install](#install)), compiles the input
documents of local files against local catalogs, `compiler.LoadCatalog`,
`LoadInputs`, `CompileAll` then `Write`, and writes the files above under
`<output>/projects/<namespace>/instances/<release>/`:

```bash
okdp-compile -f examples/02_advanced/input/config --catalog examples/02_advanced/input \
  --rules examples/02_advanced/rules -o out
okdp-compile --check -f examples/04_claims/input/config --catalog examples/04_claims/input
i=out/projects/demo/instances/demo-webapp
helm install demo-webapp-okdp ../okdp-objects-chart -n demo \
  -f $i/okdp-objects.values.yaml
helm dependency build examples/02_advanced/input/catalog/webapp/1.0.0
helm install demo-webapp examples/02_advanced/input/catalog/webapp/1.0.0 -n demo \
  -f $i/values.yaml
```

| Flag | What |
|---|---|
| `-f`, `--file <file\|dir>` | input documents, repeatable, one required: a YAML file, read in the directory that holds it (the one it points to, a symbolic link), or a directory whose `.yaml` and `.yml` files are read, at any depth ([input documents](#input-documents)) |
| `--catalog <dir>` | a catalog, `catalog/` and `contracts/` only, repeatable, one required |
| `--rules <dir>` | a platform's rule set, `rules.yaml` at its root, with a `name`, added to the mandatory one, of which it replaces no rule; none, and the mandatory rules alone apply, their hooks among them; a platform whose `global.okdp.ruleSets` requires a set not given is refused |
| `--output`, `-o <dir>` | where to write; the command owns its `projects/` |
| `--check` | compile and check, write nothing, no `-o`; no upstream chart read |
| `--cost-limit`, `--instance-timeout`, `--timeout` | the bounds of a compile: 1000000 (`compiler.DefaultCostLimit`), `1m` and `10m` by default |
| `--force` | write into a `projects/` the command did not write (no `projects/.okdp-compile`), removing what it does not write; never into a `projects/` that is a symbolic link |
| `--allow-plain-secrets` | `Inputs.AllowPlainSecrets` of every instance |
| `--version` | the version |

It reads its directories with the files bounded and no symbolic link
under them followed (a path given on the command line may be one), the
output apart from every input; it writes no file through a link, and
removes those under `projects/`. It prints on stderr the bindings of the
claims, the refusals and the warnings, then, once the files are written,
the dependencies, `<namespace>/<release>: depends on <instance>
(<contract> <output>, <field>)`. It writes nothing when an instance is
refused, nor when `-f` holds no `Instance` document (`no instance: -f
holds no document of kind Instance`). It compiles with Helm's merge
alone: it sets no
`Inputs.Finalize` and checks no layer's `global`, so no host's policy on
the upstream values runs, whatever `--rules` gives, and it decides nothing
of what a layer may set. It orders no release: the dependencies are
printed, not checked as a deployment graph (a release that exists, no
cycle), which is a host's. A host's command line may add the rest: the
layout of a deployment's files, the files of a deployer engine (Flux, Argo
CD, a single release per instance) and their order, the writing of the
projects that compile when others do not, a report of the compile, and the
checks of the final values against the upstream charts from a chart cache
([chart cache](specs/01_inputs_outputs.md#chart-cache)).

### Install

A [release](https://github.com/OKDP/okdp-compiler/releases), built by
goreleaser from its tag, attaches for `linux`, `darwin` and `windows`,
`amd64` and `arm64`:

| File | What |
|---|---|
| `okdp-compile_<version>_<os>_<arch>.tar.gz`, `.zip` for Windows | `okdp-compile`, `LICENSE` and `README.md` |
| `<archive>.sbom.json` | the SPDX SBOM of the archive, by syft |
| `okdp-compile_<version>_checksums.txt` | the SHA-256 sums of the archives and the SBOMs |
| `okdp-compile_<version>_checksums.txt.sigstore.json` | the Sigstore bundle of the sums, signed keyless with cosign by the release workflow: the signature, the certificate of the workflow's identity, the transparency log entry |

The build provenance of each archive is attested too (GitHub's
attestations). The latest release, downloaded, verified and installed:

```bash
v=$(gh release view -R OKDP/okdp-compiler --json tagName -q .tagName) os=linux arch=amd64
a=okdp-compile_${v#v}_${os}_${arch}.tar.gz s=okdp-compile_${v#v}_checksums.txt
gh release download $v -R OKDP/okdp-compiler -p $a -p "$s*"
cosign verify-blob $s --bundle $s.sigstore.json \
  --certificate-identity https://github.com/OKDP/okdp-compiler/.github/workflows/release-please.yml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum -c --ignore-missing $s
gh attestation verify $a --repo OKDP/okdp-compiler
tar xzf $a okdp-compile && install okdp-compile ~/.local/bin/
```

`cosign` checks that the release workflow signed the sums, `sha256sum`
the archive against them; `gh attestation verify` checks the archive's
provenance on its own, without `cosign`.

`go install github.com/okdp/okdp-compiler/cmd/okdp-compile@<tag>`, or
`@latest`, builds a release from the Go module proxy; its `--version`
prints `dev`, as no `-X main.version` is set. From a checkout, `make
build` gives `bin/okdp-compile`, its version that of `git describe`.

## Go library

`go get github.com/okdp/okdp-compiler@vX.Y.Z`, a version of
[the releases](https://github.com/OKDP/okdp-compiler/releases), `@latest`
the latest. To build against a checkout next to the module, while changing
both, the `replace` dropped before the commit:

```bash
go mod edit -replace github.com/okdp/okdp-compiler=../okdp-compiler
go mod tidy
```

From input documents and a catalog, as `okdp-compile` reads them, with
`os.DirFS` (run from the repository root): `LoadCatalog`, `LoadInputs`,
then `CompileAll` of every instance together, each claim bound to its
provider, and `Write` of each result. The mandatory rules alone apply
(nil rules); the caller adds the bounds and the hooks to the instances'
`Inputs`:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"maps"
	"path"
	"slices"

	compiler "github.com/okdp/okdp-compiler"
)

func main() {
	ctx := context.Background()
	// The catalog: catalog/<service>/<version>/ and contracts/ of each
	// directory, nothing else of it.
	cat, err := compiler.LoadCatalog([]compiler.CatalogFiles{
		{Name: "charts", FS: os.DirFS("examples/04_claims/input")},
	})
	if err != nil {
		log.Fatal(err)
	}
	// The input documents: every .yaml file of the directory, the values
	// files they name aside. nil rules: the mandatory set alone.
	instances, err := compiler.LoadInputs(ctx, []compiler.InputFiles{
		{Name: "config", FS: os.DirFS("examples/04_claims/input/config")},
	}, cat, nil)
	if err != nil {
		log.Fatal(err) // every problem of the documents at once
	}
	for i := range instances {
		instances[i].Inputs.CostLimit = compiler.DefaultCostLimit
	}
	files := map[string][]byte{}
	for i, out := range compiler.CompileAll(ctx, instances) {
		if out.Err != nil {
			log.Fatalf("%s/%s: %v", instances[i].Inputs.Namespace, instances[i].Inputs.Release, out.Err)
		}
		for _, b := range out.Claims {
			fmt.Printf("%s: claim %s bound to %s/%s\n", out.Result.Release, b.ID, b.Namespace, b.Release)
		}
		written, err := compiler.Write(out.Result)
		if err != nil {
			log.Fatal(err)
		}
		for name, b := range written {
			files[path.Join(out.Result.Namespace, out.Result.Release, name)] = b
		}
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		fmt.Println(name)
	}
}
```

```text
demo-dashboards: claim demo.analytics bound to data/data-postgresql
demo-metrics: claim demo.metrics.database bound to data/data-postgresql
demo-reports: claim demo.analytics bound to data/data-postgresql
data/data-postgresql/okdp-objects.values.yaml
data/data-postgresql/okdp.info.yaml
data/data-postgresql/values.yaml
demo/demo-dashboards/okdp-objects.values.yaml
demo/demo-dashboards/values.yaml
...
```

One instance, its inputs given in Go, with `LoadDefinition` and
`Compile`: the claim bound by hand, as `CompileAll` binds it. The example
gives the mandatory set with the example's own added, which its platform
requires and whose function its package calls: the compile applies both,
their hooks among them ([rule sets](#rule-sets)):

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
	// The mandatory set with the example's added: the platform requires it
	// (global.okdp.ruleSets), and the package calls its function.
	rules, err := compiler.LoadRules(os.DirFS("examples/02_advanced/rules"))
	if err != nil {
		log.Fatal(err)
	}
	def, err := compiler.LoadDefinition(os.DirFS("examples/02_advanced/input/catalog/webapp/1.0.0"), nil) // the package
	if err != nil {
		log.Fatal(err)
	}
	in := compiler.Inputs{
		Rules:     rules, // nil: the mandatory set alone
		Contracts: os.DirFS("examples/02_advanced/input/contracts"),
		Release:   "demo-webapp",
		Namespace: "demo",
		// The claims of the instance, by parameter, each bound to its provider:
		// database's to demo-db, with the output demo-db publishes for it (its
		// okdp.info.yaml).
		Bindings: map[string]compiler.Binding{"database": {
			Namespace: "demo", Release: "demo-db", Contract: "database", ID: "demo.webapp.database",
			Output: &compiler.Output{
				Name: "demo.webapp.database", Contract: "database",
				Values:    map[string]any{"host": "demo-db-db.demo.svc", "dbName": "demo_webapp_database"},
				SecretRef: &compiler.SecretRef{Name: "demo.webapp.database-credentials"},
			},
		}},
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
	in.Values = []map[string]any{
		read("examples/02_advanced/input/config/platform.yaml"),
		read("examples/02_advanced/input/config/demo/webapp/config.yaml"),
	}
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
charts: 1 outputs: 1
replicaCount: 2
app: https://charts.example.org app 1.8.0
dependency: database -> demo-db (database)
```

From memory, with `fstest.MapFS`, the mandatory rules alone and no
platform: no file is read, and the result is given in memory.

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
	}, nil)
	if err != nil {
		log.Fatal(err)
	}
	res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
		Contracts: fstest.MapFS{},                    // no connection, no provides: no contract read
		Values:    []map[string]any{{"replicas": 3}}, // no settings: the package requires none
		Release:   "demo-app",
		Namespace: "demo",
	})
	if err != nil {
		log.Fatal(err)
	}
	files, err := compiler.Write(res) // values.yaml, nothing else here
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(files[compiler.ValuesFile]))
}
```

```text
# Code generated by okdp-compile. DO NOT EDIT.
app:
  image:
    tag: "4.5"
  replicaCount: 3
```

Any `fs.FS` works: `embed.FS` (`fs.Sub` for a subdirectory), a git tree.

`compiler.LoadDefinition(fsys, rules)` reads the package at the root of
`fsys` for the compiles of a rule set, whose sections `okdp.yaml` may
then hold (nil rules: the mandatory set alone):
`Chart.yaml`, `okdp.yaml` and the values file, `values.cel.yaml` or
`values.yaml`, then keeps `fsys` (`Definition.Files`) for the files each
compile reads, `config.schema.json` and the defaults files. A `Definition`
can also be built in Go: the `Chart.yaml` metadata (`Chart`), the package
keys of `okdp.yaml` (`Package`; `Package.Require` is a list of keys),
`okdp.yaml` and the values file as bytes (`Okdp`, `Values`, nil when
absent), and the values file's name (`ValuesFile`, `values.cel.yaml` when
empty), which its errors start with.

`compiler.Inputs` takes what the input documents give an instance
(`LoadInputs`, [what a compile
receives](specs/01_inputs_outputs.md#what-a-compile-receives)):
`Contracts` (those of the catalog's `contracts/`; it may be empty when
nothing references a contract), `Values` (its values layers, parsed,
the `Settings` document's, its project's, its `connections`, then its
own), `Release`, `Namespace`, `Claimers`; `Settings`, which the
documents never give: a host's own settings, the content of
`global.okdp`, which join the layers' settings and reach no chart ([host
settings](specs/01_inputs_outputs.md#host-settings)); and `Published` (what the
other instances of the namespace
publish, by release, each a `compiler.Info`;
nil, none is known; `CompileAll` sets it), `Claims` (the claims bound to
the instance of a provider, `compiler.Claim`), `Bindings` (the providers
the instance's claims are bound to, by parameter path,
`compiler.Binding`): `CompileAll` sets both
([claims](specs/01_inputs_outputs.md#claims)); and the host's
parts, `Rules` and `Finalize` ([rule sets](#rule-sets),
[hosts](#hosts)). `Release`, `Namespace` and
`Contracts` are required
([inputs and outputs](specs/01_inputs_outputs.md)).

`Result.Values` is the final values by chart key, `Result.Charts` the upstream
chart of each key, `Result.Info` what the instance publishes,
`Result.Objects` OKDP's objects, and `Result.Dependencies` what a deployer
orders the releases with, in no file
([outputs](specs/01_inputs_outputs.md#outputs)).

`compiler.Write(res)` gives the files of [an
install](#installing-an-instance), name -> content, for a caller to write
where it keeps them. `compiler.ChartValues(res)` is the values of the
package's charts alone, and `compiler.ObjectsChartValues(res)` the values
of the okdp-objects chart, nil without objects or clients: `manifests` a
string, so that Helm changes neither the `{{` of an ExternalSecret's
template, nor a null, nor a large integer. `compiler.Info` is what
`okdp.info.yaml` holds, to read it back.

### Rule sets

What a compile applies of a platform is data, in rule sets. The compiler
embeds the mandatory one, `core`, which every compile applies: the
functions it gives, the schema of the platform's settings, what a
mapping sees as `platform`, the labels, the names, the documents of
the secret store, the document of its hook `namespace`, the schema of
the mapping and the platform's charts. `Inputs.Rules` nil compiles with
it alone, which refuses only the namespaces OKDP keeps (`platform`,
`kube-*`): no object, no Secret in clear, no namespace a platform
reserves, which are the security set's to refuse, nor a host's engines'
namespace or a release named like an object a host writes, which are
the host's own set's.

`compiler.LoadRules(sets...)` reads a platform's own sets, each
`rules.yaml` at the root of its `fs.FS`, and adds them to the mandatory
one, in order, in a `*compiler.RuleSet`: their tables, documents, hooks
and their inputs, functions, templates, sections, the keys they add to
the schemas and their scope documents. All apply, a set adding to those
before it. It fails on a set without a `name` or of a name another has,
on a rule a set read before gives, a table, a key of a schema, a chart,
a document or a document listed again at a hook (a rule set replaces
none), and on values added to a table that is not extensible: never a
rule turned off or replaced, the error naming the set ([how the sets
add up](specs/05_rules.md#how-the-sets-add-up)). `okdp-compile --rules`
gives one set.

The usual added set is the security set, okdp-compiler-security: the
platform's policy (the objects of the projects that are not privileged,
credentials in clear, the namespaces the platform reserves), the groups
of each role, the hosts a project serves, its OAuth clients, the
hardened workloads and their functions. The command line takes its
directory, Go its module, which reads the same files:

```bash
okdp-compile --rules ../okdp-compiler-security/rules -f config --catalog charts -o out
```

```go
// compiler.LoadRules(security.FS()), read once; LoadRules(security.FS(), own) adds a set after it
rules, err := security.Rules()
if err != nil {
	log.Fatal(err)
}
in.Rules = rules
def, err := compiler.LoadDefinition(fsys, rules) // okdp.yaml may hold the set's sections
```

A platform lists the sets it requires in `global.okdp.ruleSets` of its
values, and a compile not given one of them fails
([the rule set](specs/05_rules.md#platform-settings)): a platform that
relies on the security set requires it, so that a compile without it
does not pass what the set would refuse
([okdp-compiler-security's
usage](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/01_usage.md#requiring-the-set-in-a-tree)).

The helpers that read rules take the set first, nil being the mandatory
one: `compiler.CheckNamespace(ctx, rules, name, values)` and
`CheckConnection(rules, contracts, namespace, c, values)`.

### Hosts

A hook lets a host add what the library does not hold, run inside the
compile of the instance, its error refusing it:

- `Inputs.Finalize` makes the final values of
  the charts from what the compile computed. Nil, it is Helm's merge, with
  no policy over the upstream values and no check against the upstream
  charts, which the compiler does not read. A host that adds one wraps
  `compiler.FinalValues(in, check)`, `check` what it refuses of the
  upstream values: its `FinalValuesOf` holds the final values by key
  (`Values`), the chart of each (`Charts`), and the paths of the
  instance's lists appended to the package's (`Appended`, by key, each
  once), with which the host warns when a final list lacks items of the
  chart's default list. The host fetches the charts itself, checks the
  final values against them, may render each chart key with
  `FinalInput`'s `Release`, `Namespace` and `Specs[key].Source`, and
  checks the manifests with `compiler.CheckObjects(ctx, rules, svc,
  namespace, settings, allowPlainSecrets, objects)`, which runs the rule
  sets' `objects` hook; a chart's own Secrets, rendered in the cluster
  and never written to the deployments repository, are checked with
  `allowPlainSecrets` true. The compile runs the
  `finalValues` hook on the values Finalize returns, after the host's
  own checks. `compiler.CheckFinalValues` runs the hook on final values
  a host reads outside a compile.

A host sets it on the `Inputs` of the instances `LoadInputs` gives,
before `CompileAll`; it may fetch the upstream charts of every
instance's `Definition.Chart` there, before the compiles, so that no
fetch counts against `Inputs.Timeout`.

A host may wire the security set's package `policy` as its policy on
the upstream values
([the upstream policy](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/05_upstream_policy.md)),
and its own checks against the upstream charts beside it
([checks of the final values](specs/01_inputs_outputs.md#checks-of-the-final-values)).

What a layer may set is a host's too: the compiler takes the layers of
`Inputs.Values` as given, whoever wrote them. A host that gives layers
of several origins, the platform's, a project's, an instance's, checks
each before the compile: that a layer only adds to the platform's, the
keys of `global` and `global.okdp` a project may set, no template code
from a layer it does not review, the keys of the secret store a
namespace's connections may read, who gives a project's groups
(`global.okdp.access.groups`), which a host better gives in its own
settings (`Inputs.Settings`): no layer may give them then. The
security set's `policy.CheckGlobals` checks the `global` of the layers
a host does not review against the upstream policy: nothing calls it
but a host.

### The rest of the API

- `Result.Warnings` lists what the compile warns of without failing: those
  of the package, found by `LoadDefinition` (`Definition.Warnings`), a
  `values.yaml` that holds expressions; and those of a host's Finalize,
  such as one for an instance list appended to that leaves out items of
  the chart's default list. Each
  compile gives them with its result, so that a server compiling many
  instances returns them with each.
- `Result.Dependencies` lists the instances of the namespace the
  connection references point to, by parameter path, each a
  `compiler.Dependency`, `{Instance, Namespace, Contract, Field, Output}`,
  `Namespace` the instance's when it is another namespace's (a claim's
  provider), `Output` the output the reference resolved to, by name, or
  the claim's ID
  ([dependencies](specs/01_inputs_outputs.md#dependencies)): what a
  deployer orders the releases with. `Write` writes them in no file.
- `compiler.LoadInputs(ctx, files, cat, rules)` reads the input
  documents of `files`, each a `compiler.InputFiles` (its name in
  messages, its `fs.FS`, and `Path`, one file of it, or `""` for every
  `.yaml` and `.yml` file under it), as the instances `CompileAll`
  compiles, sorted by project then name, each named after its document
  (`Instance.Name`), the name of its outputs that name none and of its
  claims' IDs ([input documents](#input-documents)); every problem at
  once, each naming its file and document. It reads the packages of `cat`
  for the rule set `rules`, as `LoadDefinition` does; nil rules stand for
  the mandatory set. `compiler.KindSettings`, `KindProject` and
  `KindInstance` are the kinds of the documents.
- `compiler.LoadCatalog(dirs)` is the catalog of one or more directories,
  each a `compiler.CatalogFiles` (`Name`, `FS`), read for `catalog/` and
  `contracts/` alone (`compiler.CatalogDir`, `ContractsDir`), for
  `LoadInputs` and for a caller that lists the packages (a console's
  menu): `Services()`, each a `compiler.CatalogService` (`Name`,
  `Repository`, the directory's name, `Versions`: the directories of
  `catalog/<service>/`, highest first, the SemVer ones by precedence),
  `Service(name)`, `Package(service, version)`, the files of
  `catalog/<service>/<version>/`, `PackagePath(service, version)`, where
  it is, and `Contracts()`, every `contracts/<contract>.schema.json`. A
  service or a version no directory has is an error wrapping
  `compiler.ErrNotInCatalog`; a service in two directories, or a contract
  that differs between two, fails `LoadCatalog`, every problem at once
  ([the catalog](specs/01_inputs_outputs.md#the-catalog)).
- `compiler.ParseConnection(name, v)` and `compiler.ParseClaim(name, v)`
  read an entry of `connections` and of `claims` as written, for a caller
  that lists or edits them (a control plane): a `compiler.Connection`
  (`Name`, `Contract`, `Description`, `Values`, the contract's fields,
  `SecretRef`, the Secret its secret fields are in, or `RemoteKey`, their
  key in the platform's secret store), which marshals to YAML as it is
  written, `contract`, `description` and `secretRef` first, then its
  fields, sorted; a `compiler.ClaimSpec` (`Name`, `Contract`, `Class`,
  `Provider`, `Request`), its tags the keys it is written with, and why
  the compile refuses it, if it does, the claim holding what the entry
  does all the same. A connection is checked by the compile of the
  instances that reference it.
- `compiler.IsName(name)` is whether a name may be a project's, an
  instance's, a connection's or a named claim's, a DNS-1123 label;
  `compiler.CheckNamespace(ctx, rules, name, values)`, `values` a layer
  whose `global.okdp` gives the settings, why the compile would refuse
  every instance of a namespace of that name, in its words (`namespace
  "kube-foo": the namespace is reserved: ...`): not a name, or one the
  rule sets' `namespace` hook refuses, with the settings as written: the
  mandatory set's (`platform`, `kube-*`), the security set's
  (`global.okdp.policy.reserved`), a host's set's (its engines'), for a
  program that creates a namespace before any compile. The release
  of an instance is `<project>-<name>`; how a host names the files of a
  deployment is its own.
- The settings of an instance, for a program that reads them (a
  control plane): `compiler.SettingsOf(ctx, in)`, the settings of the
  layers of `in` as a compile checks them, their defaults applied, a
  `*compiler.NamespaceError` when the namespace of `in` is refused (the
  `namespace` hook). The groups of a namespace are among them,
  `access.groups`, a setting of the security set, whose package `access`
  reads and writes them for a host
  ([access](specs/01_inputs_outputs.md#access)).
- `compiler.CompileAll(ctx, instances)` compiles instances together, each
  a `compiler.Instance`, its `Definition` and its `Inputs` without
  `Name`, `Published`, `Claims` and `Bindings`, and its `Name`, which a
  provider names it by, as by its release, and which names its outputs
  and its claims (`LoadInputs`: its document's `name`; empty: its release),
  unique in its namespace: each claim bound to its provider, each
  instance after the instances of its namespace whose outputs it
  references and the providers of its claims, with what they publish. One
  `compiler.Outcome` per instance, in their order: its `Result`, or, nil,
  why it is refused (`Err`), and its claims, bound (`Claims`); the order
  changes none ([compiling instances
  together](specs/01_inputs_outputs.md#compiling-instances-together)).
  A provider, a claim's or a class's, is `<namespace>/<name>` or `<name>`
  of the claimer's namespace, the name an instance's or its release; one
  that names two instances, the release of one and the name of the other,
  is refused, and so is one of another namespace, named or a class's,
  any class, whose claimers (`Inputs.Claimers`, else its package's,
  `Instance.Claimers(contract)`) do not list the claimer's namespace
  ([providers](specs/01_inputs_outputs.md#providers)).
- `CompileAll` builds no deployment graph: `Result.Dependencies` of
  each instance is what a host orders the releases with, checks (each
  release exists and provides what is referenced, no cycle) and turns
  into Flux's `dependsOn` or Argo CD's sync waves
  ([the deployment graph](specs/01_inputs_outputs.md#the-deployment-graph)).
  Which projects a host writes when some of their instances are refused,
  and what changed since its last compile, are its own too.
- The compile's rules, for a program that answers before it compiles
  (a control plane), none of them written again:
  - `compiler.FieldErrors(err)` reads the error of a compile, an
    instance's (`Outcome.Err`) for one, as the problems of its
    fields, each a `compiler.FieldError`: its
    `Path` as the messages write it (`catalogs[1].storage`,
    `claims.analytics`), its `Pointer` (RFC 6901, `/catalogs/1/storage`)
    and the compile's `Message`. A violation of the parameter schema is
    at its place (`at '/replicas': got string, want integer`), a
    reference that does not resolve or a claim that does not bind at
    its parameter path, a named claim at `claims.<name>`; any other
    problem is the instance's, `Path` `""`.
  - `compiler.ConnectionRefs(schema)` lists the connection references
    of a parameter schema as the compile finds them, through
    `properties` and `items` (`compiler.ConnectionRef`: `Path`,
    `catalogs[].storage` for every item of a list, `Schema`, its place,
    `Contract`, `Provision`, `Request`, `Require`, `Required`, its
    `Property` whole); the compile's error when one cannot be read or
    is out of its reach ([declaring a
    reference](specs/01_inputs_outputs.md#declaring-a-reference)).
  - `compiler.ReadContracts(contracts)` reads the contracts as a
    compile reads them, its error when one cannot be: for each, a
    `compiler.ContractDescriptor`, its `Label`
    (`x-okdp-contract`), `Title`, `Description`, its `Fields` in the
    order of the file (`ContractField`: `Secret`, `Required`, `Default`,
    `Derived`, `Condition`, its `Property` as written), `Required`,
    its claim schema (`Claim`), the document (`Schema`) for the
    keywords the compile does not read (`x-okdp-icon`,
    `x-ui-widget`...).
  - `compiler.CheckConnection(rules, contracts, namespace, c, values)`
    checks a connection of `namespace` at its declaration, as
    `CompileAll` checks every connection of an instance's layers: its
    contract, its fields, none secret, its required ones, its types,
    its `secretRef` against the store `values` give
    (`global.okdp.secrets`) and its provider; the compile's error, at
    `connections.<name>`.
  - The names of the claims: `compiler.NamedClaimID(project, name)`,
    `<project>.<name>`, the output its provider publishes for it;
    `compiler.IsClaimID(name)`, a name of that form, DNS-1123 labels
    joined by dots (which outputs of a provider are its claims' is its
    compile's, `Result.Claims`, the claims bound to it);
    `compiler.CheckProvider(where, provider)`, the form a claim or a
    class names its provider by; `compiler.CheckClaimers(name,
    claimers)`, an instance's claimers, as `LoadInputs` checks them.
- `compiler.ParseValues(b)` reads one values file as Helm 3 does (YAML 1.1,
  the first document of the file), for `Inputs.Values`. A layer built
  in Go may hold other types than `map[string]any` and `[]any`
  (`map[string]string`, `[]string`, `int32`, a struct by its `json`
  tags): it is read as the values it would be once written to a file
  and read back
  ([values files](specs/01_inputs_outputs.md#values-files)); what a file
  cannot hold fails, its layer and path named.
- `compiler.ChartRef` is an upstream chart, `{Repository, Chart, Version}`:
  a dependency of `Chart.yaml` (its `repository`, `name`, `version`), an
  entry of `Result.Charts`. `compiler.ChartMetadata` is a `Chart.yaml`, the
  package's in `Result.Chart`.
- The objects a rule set's section adds (`ownObjects`) are
  `Result.Objects`, with the mapping's own.
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
  (a rule set's sections, generated Secrets; 0: no limit): once
  the total passes it, the compile fails with
  `the expressions of the compile exceed its CEL cost limit (n)`. The
  total is checked after each expression, which may itself cost up to the
  limit: a compile can pass the limit by one expression's cost. A host
  function counts what it reads and what it builds: a call costs 1, a
  unit a byte of its argument, about its serialisation (a string's
  bytes, a map's keys and values, a list's elements), and the bytes of
  the string it returns (`sha256`, `toJson`, `toPrettyJson`, `toYaml`,
  `quote`, `str`, `first` and OKDP's own; `join` is CEL's): it converts
  its whole argument, which a map's entry count does not measure. A
  server compiling packages it does not review sets one, as an
  instance's values can make an expression iterate over them;
  `okdp-compile` sets 1000000 (`compiler.DefaultCostLimit`), about
  1 MB that host functions read or write; one expression costs what is
  left of the limit, not the limit again. `Inputs.Timeout` bounds one
  compile in time, beside the context's deadline: past it the instance
  is refused with its own message (`okdp-compile --instance-timeout`
  sets it on every instance). A `$for` costs 1 an element
  and the size of what its body builds, its context checked at each.
  Whatever the limit, a document's values
  are 32 MiB at most (about their bytes in memory, a value a list holds
  ten times counted ten times): past it, the compile fails with
  `the values the document builds exceed 32 MiB`.
- `Inputs.AllowPlainSecrets` accepts credentials in clear: a `provides`
  `secret` without `generate`, a plain Secret of its `stringData`, and,
  where the security set refuses them through the `objects` hook, which
  the compiler gives the flag, a Secret of the `objects` section with
  `data` or `stringData`, an ExternalSecret whose template holds a
  literal at a sensitive key, a Fake generator with values. Unset, they
  fail the compile, and a
  literal of the upstream values at a sensitive key is a warning: the
  objects are written to the deployments repository, so credentials
  are generated in the cluster or read from the platform's secret store
  ([credentials Secrets](specs/01_inputs_outputs.md#credentials-secrets)).
- Errors are messages for the user, returned as they are; the package
  defines no error types, but `ErrNotInCatalog`, which a missing package
  wraps. A message carries its context: the file, the document, the
  parameter path, the chart key, the mapping section (see
  [troubleshooting](#troubleshooting)).

## Troubleshooting

`<field>` is the parameter path (`database`, `backends[1].database`, a key
that holds a dot in brackets: `databases["eu.main"]`),
`<key>` a chart key (of a `Chart.yaml` dependency), `<chart>-<version>`
the upstream chart that renders it, `<service>` the `name` of
`Chart.yaml`. `error: <namespace>/<release>: `
starts a message as `okdp-compile` prints it; the library's is what
follows. The messages about the namespaces and releases OKDP keeps and
the platform's schema are the mandatory rule set's; those about the
objects of a project, plain Secrets of the objects, the namespaces the
platform reserves, hosts, OAuth clients and upstream values the security
set's
([its troubleshooting](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/01_usage.md#troubleshooting));
those about the upstream charts (the chart cache, their schemas, their
default lists) a host's checks', in its Finalize: a compile without
them gives none of them.

### Inputs and instance values

| Message | Cause | Fix |
|---|---|---|
| `Inputs.Release "demo-Demo_App": a release name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 53 characters at most (Helm's limit)`, `Inputs.Namespace "<name>": a namespace name, ...`, `Inputs.Namespace "platform": the namespace is reserved: ...` | an instance's `project` and `name`, both DNS labels, make a release, `<project>-<name>`, longer than 53 characters, or a library caller's `Inputs` hold names Kubernetes and Helm do not accept; a project named `platform`, the segment of the platform's keys of the secret store | rename it ([instance](specs/01_inputs_outputs.md#instance), [inputs](specs/01_inputs_outputs.md)) |
| `config/inputs.yaml: document 2 (line 7, Instance): the package charts/catalog/web/1.0.0: open Chart.yaml: no such file or directory` | a version directory of the catalog without its `Chart.yaml` | add it ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: apiVersion "v1": v2, the only one the compiler reads`, `Chart.yaml: name is required`, `Chart.yaml: version is required`, `Chart.yaml: type "library": a package is an application chart` | the package metadata is incomplete, or not that of an application chart of `apiVersion: v2` | set them ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: version "1.0": SemVer (1.2.3), the version of the umbrella chart` | a package version Helm does not accept as a chart's | a SemVer `version` in `Chart.yaml` |
| `Chart.yaml: yaml: unmarshal errors:` then `line 9: field condtion not found in type chart.Dependency`; `Chart.yaml: one YAML document` | a field Helm's `Chart.yaml` does not define, here a misspelt `condition` in a dependency, one line per unknown field; a second YAML document | fix the name; one document ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: dependencies[1] (api): condition: the values decide what is rendered ($if on api in values.cel.yaml)` | a dependency with `tags`, `enabled`, `import-values` or a `condition` other than `<key>.enabled` | remove it; leave the dependency out with `$if` on its key in the values file ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0] (my_app): key "my_app": a DNS label (lowercase letters, digits and -, a letter or digit at each end, 63 characters at most): the key names the umbrella's dependency alias` | a dependency key (its `alias`, else its `name`) that is not a DNS label: `../app`, an upper case letter, `_` or `.` | give it an `alias` that is one, and rename the key in `okdp.yaml` and the values file ([chart keys](specs/02_service_definition.md#chart-keys)) |
| `Chart.yaml: dependencies[2] (api): the key of dependencies[1] too: give one of them an alias` | two dependencies of one key: a chart rendered twice, one of them without its alias | an alias each ([recipe](02_writing_package.md#render-a-chart-twice)) |
| `Chart.yaml: dependencies[0].name is required` | a dependency without its chart's name | name the published chart that renders it ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0].repository "charts.example.org": the URL of a chart repository (https://... or oci://...)`, `Chart.yaml: dependencies[0].name "a/b": the name of a chart of the repository` | a repository that is not a URL (`https://`, `oci://`) or whose host and path cannot be laid out as directories of a chart cache (`https://`, `https://x/../y`), a name that is not a chart's name | fix the dependency ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0].repository "http://charts.example.org": http://, the chart fetched in clear: the URL of a chart repository (https://... or oci://...)`, `global.okdp.charts.objects.repository "http://...": ...` | a dependency, or a platform chart, of an `http://` repository: the chart would be fetched in clear, by a host's chart cache and by the deployer | an `https://` or `oci://` repository: a mirror, with TLS ([dependencies](specs/02_service_definition.md#dependencies), [platform charts](specs/01_inputs_outputs.md#platform-charts)) |
| `Chart.yaml: dependencies[0].version "^1.8": a pinned chart version (1.2.3), not a range` | a version range | pin the version |
| `okdp.yaml: yaml: unmarshal errors:` then `line 3: field requires not found in type okdp.Package` | a key `okdp.yaml` does not define, here a misspelt `require`; one line per unknown key | fix the name ([okdp.yaml](specs/02_service_definition.md#okdpyaml)) |
| `okdp.yaml: output: the charts' values are in values.cel.yaml (or values.yaml)`, `okdp.yaml: name: the name, version and charts are Chart.yaml's` | a key of the former package format | the values in `values.cel.yaml`, without the `output:` wrapper; the name, version and charts in `Chart.yaml` ([service definition](specs/02_service_definition.md)) |
| `values.yaml and values.cel.yaml: a package has one of them` | the package has both values files | keep one: `values.cel.yaml` when the values hold expressions ([values.yaml](specs/02_service_definition.md#valuesyaml)) |
| `okdp.yaml: defaults.web: not a dependency of Chart.yaml`, `okdp.yaml: upstream.web: not a dependency of Chart.yaml` | a key of `defaults` or `upstream` that is no dependency's key | fix the key, or add the dependency ([okdp.yaml](specs/02_service_definition.md#okdpyaml)) |
| `okdp.yaml: upstream: true, false or a map by dependency`, `okdp.yaml: upstream.app: true, false or {protect, append}` | `upstream`, or a dependency's value in it, of another form: a string, a number, a list; a map with another key than `protect` and `append` (a misspelt `protect`) | `true`, `false`, or a map by key, each `true`, `false` or `{protect, append}` ([upstream](specs/02_service_definition.md#upstream)) |
| `okdp.yaml: upstream.app.protect[0] "a[": not a path: ...` | a `protect` or `append` path out of the grammar | fix the path ([protect and append](specs/02_service_definition.md#protect-and-append)) |
| `okdp.yaml: yaml: unmarshal errors:` then `line 9: cannot unmarshal !!map into string` | a `require` entry of another form than a key (`{key, $if}` included) | a key; check a value of one mode in the mapping's `errors` ([require](specs/02_service_definition.md#require)) |
| `app: chart app-1.8.0 is not in the chart cache: helm pull app --repo https://charts.example.org --version 1.8.0 --untar -d <chart cache>/charts.example.org/app-1.8.0` | a host's check against a chart cache: a chart the compile renders (a dependency the values file leaves out, by `$if` or null, is not read) is not unpacked in its directory (`charts.example.org/app-1.8.0/app/`), even when the chart cache holds its archive (`helm pull -d` without `--untar`) or the chart elsewhere; for an `oci://` repository the command is `helm pull oci://<repository>/<chart> --version <version> --untar -d <chart cache>/<repository>/<chart>-<version>` | run the command, `<chart cache>` the chart cache ([chart cache](specs/01_inputs_outputs.md#chart-cache)) |
| `global.okdp.charts.objects.version "1.x": a pinned chart version (1.2.3), not a range` | a platform chart checked as a source is (its repository, chart and version, the same messages); its form is checked by the platform schema (below) | fix the platform values ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |
| `database: the database contract cannot be read: database.schema.json: file does not exist` | a connection reference or a provided connection names a contract no catalog directory's `contracts/` has | add it to a catalog ([contracts](specs/01_inputs_outputs.md#location)) |
| `database: the database contract cannot be read: database.schema.json: unexpected EOF` | the contract file is not JSON, not an object or not a valid JSON Schema; also `provides "<name>" (<contract>): the <contract> contract cannot be read: ...` | fix the contract ([contracts](specs/01_inputs_outputs.md#location)) |
| `<file>: cannot unmarshal yaml document: error converting YAML to JSON: yaml: line 1: did not find expected node content` | a values file is not valid YAML | fix the file |
| `global.okdp.tls.clusterIssuer is required. It is a setting, of the values (Inputs.Values)` | `require` in `okdp.yaml` lists it, and no values layer sets it (or a later one sets it to `null`) | set the key in the platform's values, its `Settings` document ([values layers](specs/01_inputs_outputs.md#values-layers)) |
| `config.schema.json: global.okdp is the platform's settings: a package schema declares nothing under it`, `... a package schema gives it no default (properties.global.default)` | the package's parameter schema declares `properties.global.properties.okdp`, or its `properties.global.default` has the key `okdp`: a default of the platform's settings | remove it: the settings are `global.okdp` of the values layers, the platform's values ([defaults](specs/01_inputs_outputs.md#defaults)) |
| `values layer 2: json: cannot marshal from Go func() within "/hooks/0"`, `values layer 1: json: cannot marshal from Go float64 within "/global/proxy/no.proxy": unsupported value: NaN` | a values layer built in Go, the second or the first of `Inputs.Values`, holds what a values file cannot: a func, a chan, a complex, a NaN or an infinity, text that is not UTF-8, a key that is not text or a number, two keys that read as the same, a value that contains itself | build the layer of values a file can hold ([values files](specs/01_inputs_outputs.md#values-files)) |
| `values don't meet the specifications of the schema(s) in the following chart(s):` then `<service>:` and `- at '/replicas': minimum: got 0, want 1` | the parameters violate the package's `config.schema.json`; every violation is listed | fix the instance values ([parameters](specs/01_inputs_outputs.md#parameters)) |
| `compiled values don't meet the specifications of the schema(s) in the following chart(s):` then `<key> (<chart>-<version>):` (`app (app-1.8.0):`) and `- at '/replicaCount': maximum: got 3, want 2` | a host's check of the final values against the upstream charts, in its Finalize: the final values, over the chart's defaults, violate the `values.schema.json` of the upstream chart | fix the instance's upstream values; if they are not involved, OKDP's defaults or the mapping |
| `warning: upstream.app.env: appended to the package's list, not to the default list of app-1.8.0, which Helm replaces: the final list lacks 1 of its 2 items; ...` | a host's check of the final values against the upstream charts, in its Finalize: the instance appends to a list (`append`) the chart's defaults hold items of that the package's list leaves out: Helm replaces the chart's list with the final one | restate the items to keep in the package's values of the key, its defaults or values file ([append](specs/01_inputs_outputs.md#append)) |
| `web: objects: ClusterRole/demo-web-reader: a cluster-scoped kind (rbac.authorization.k8s.io/v1): the platform's projects install cluster-scoped objects (global.okdp.policy.privileged), demo is not one of them` | the package writes an object of a cluster-scoped kind (its `objects` section, `<kind>/<name>`), and the instance's project is not one of the platform's own (the security set's `objects` hook) | a namespaced object; for the platform's own projects (cert-manager, an operator...), list the namespace in `global.okdp.policy.privileged` of the platform's values ([objects section](specs/01_inputs_outputs.md#objects-section)) |
| `web: objects: Secret/token: namespace kube-system, not the instance's (demo): the platform's projects write other namespaces (global.okdp.policy.privileged), demo is not one of them` | an object of the package, or a generated Secret (`okdpGeneratedSecret`'s `namespace`), names another namespace than the instance's (the security set's `objects` hook) | the instance's namespace (`release.namespace`), or leave `namespace` out; the platform's own projects write other namespaces ([objects section](specs/01_inputs_outputs.md#objects-section)) |
| `web: output "store": secret: a plain value (stringData): credentials are generated (generate) or read from the platform's secret store; Inputs.AllowPlainSecrets allows it` | an output of `provides` has a `secret` of `stringData` alone: its credentials would be written in clear to the deployments repository | `generate` the secret keys, `stringData` for the others; or point `secretRef` at a Secret an operator, the chart or an ExternalSecret writes; a caller whose repository is private may set `Inputs.AllowPlainSecrets` ([credentials Secrets](specs/01_inputs_outputs.md#credentials-secrets)) |
| `web: objects: Secret/demo-web-token: a plain value (stringData): credentials are generated (okdpGeneratedSecret) or read from the platform's secret store (an ExternalSecret); Inputs.AllowPlainSecrets allows it` | a Secret of the package's `objects` holds values, `data` or `stringData` (the security set's `objects` hook) | `okdpGeneratedSecret`, or an ExternalSecret of the platform's secret store; a caller whose repository is private may set `Inputs.AllowPlainSecrets` ([objects section](specs/01_inputs_outputs.md#objects-section)) |
| `web: objects: ExternalSecret/demo-web: a plain value (spec.target.template.data.password): a literal at a sensitive key, ...`, `web: objects: Fake/demo-web: a plain value (spec.data): a Fake generator gives the values it holds; ...` | an ExternalSecret's template holds a literal at a sensitive key (an `okdpGeneratedSecret` `stringData`'s too), a Fake generator values (the security set's `objects` hook) | a template of what the store or a generator gives (`'{{ .password }}'`), a generated key; the static keys of `stringData` are not credentials ([objects section](specs/01_inputs_outputs.md#objects-section)) |
| `warning: upstream.app.auth.password: a literal at a sensitive key, a credential in clear in the values and the deployments repository: ...` | an instance's upstream value at a key named as a credential is a literal | generate it or read it from the store, the chart given the Secret's name (its `existingSecret`); a key that only names one is a false positive ([objects section](specs/01_inputs_outputs.md#objects-section)) |
| `database: connection "store" has contract "s3", "database" is expected` | the parameter references a `connections` entry of another contract | reference a connection of the expected contract |
| `database: connection "shared-db": field "user" is not part of the database contract` | an external connection has a field its contract does not declare | remove it ([connections](specs/01_inputs_outputs.md#connections)) |
| `database: connection "shared-db": "password" is a secret field, put it in the credentials Secret` | a secret field written in the connection | put it in a Secret named by `secretRef` |
| `database: connection "shared-db": secretRef.remoteKey: its credentials are in the platform's secret store, global.okdp.secrets.store, which is not set` | a connection whose credentials are in the platform's secret store (`secretRef: {remoteKey: <key>}`), and the platform names no store | set `global.okdp.secrets.store` in the platform's values, or give the Secret of the instance's namespace, `secretRef: {name: <secret>}` ([credentials in the platform's secret store](specs/01_inputs_outputs.md#credentials-in-the-platforms-secret-store)) |
| `database: connection "shared-db": secretRef: name or remoteKey, not both`, `... secretRef.remoteKey: the key of the credentials in the platform's secret store, a non-empty string, got 5`, `... secretRef.remoteKey: the connection's name is not a DNS-1123 label: ...`, `... secretRef.remoteKey: the queue contract has no secret field (x-okdp-secret): ...`, `... secretRef.remoteKey "demo//shared-db": a path of the platform's secret store, segments separated by "/", none empty, "." or ".."` | a `secretRef` with both a Secret and a key of the store; a key that is not a string, or empty; a connection named so that its Secret, `<release>.<name>-credentials`, cannot be; a contract without credentials; a key that is no path of the store | one of them; the key, a string; rename the connection; drop `secretRef`; a path, `<segment>/<segment>` |
| `storage: connection "datalake": secretRef.remoteKey "platform/datalake": no namespace reads platform/ when the platform gives a secret store provider (...)` | a connection whose key is under `platform/` while `global.okdp.secrets.provider` is set (a SecretStore per namespace, whose policy reads no `platform/`) | copy the platform's credentials under the key of each namespace that uses them, `<namespace>/<key>`, and name it in a connection of the namespace's layers ([credentials in the platform's secret store](specs/01_inputs_outputs.md#credentials-in-the-platforms-secret-store)) |
| `database (connection "shared-db"): field "dbName" is required by the database contract` | a required contract field is missing; one line per missing field | set it |
| `database (connection "shared-db"): the fields violate the database contract:` then `- at '/engine': value must be one of 'postgresql', 'mysql'`, `- at '/port': got string, want integer` | a field of an external connection the contract's schema refuses; one line per violation | give a value of the contract's type (`port: 5432`, not `"5432"`) and among its values ([checks](specs/01_inputs_outputs.md#checks)) |
| `<field>: "<name>" is not a connection, nor an output of an instance of the namespace` | the name is not in `connections`, nor an output of that contract another instance of the namespace publishes (`(demo-pg publishes it with contract "database", "s3" is expected)`: one of another contract) | add the connection under `connections` of a values layer (its `Instance` document's `connections` for the instance's own), or the instance that publishes the output to the input documents ([resolution](specs/01_inputs_outputs.md#resolution)) |
| `<field>: "<name>" names s3 outputs of several instances of the namespace: demo-a, demo-b` | two instances of the namespace publish an output of that name and contract | rename one of the outputs, or reference a connection |
| `<field>: "<name>" is not a connection, nor a release name: lowercase letters, digits and "-", a letter or digit at each end, 53 characters at most` | the name is not in `connections`, and cannot name an instance of the namespace (`Shared_DB`, a name longer than 53 characters) | fix the name, or add the connection under `connections` of a values layer |
| `database: a connection reference must be a string, got map[string]interface {}{"host":"db.example.org"}` | a connection reference given as a map, a number or a list (the parameter schema does not say `"type": "string"`) | give the name of a connection or an instance; put the connection itself under `connections` |
| `database (connection "shared-db"): field "host": the value contains "{{"` | template code in a connection, whichever values layer gives it, or in a contract default | fix the layer or the contract |
| `database (claim "demo.app.database" of data/data-postgresql): the connection does not meet the package's requirement (x-okdp-connection-ref.require); data/data-postgresql is the provider of the default class standard of database-server connections (global.okdp.provisioning.database-server.default): name a class whose provider meets it (class), or change the default:` then `- at '/engine': value must be 'postgresql'`; `database (connection "shared-db"): the connection does not meet ...:` then `- at '': missing property 'secretRef'` | the connection, external, an output or what a claim's provider publishes, does not meet what the package requires of it (`require` of its `x-okdp-connection-ref`); one line per violation | reference another connection, or fix it (add its `secretRef`); for a claim, name a class whose provider meets it (`{class: <name>}`), a project's own if need be, or change the default in the platform's values ([what a reference requires](specs/01_inputs_outputs.md#what-a-reference-requires)) |
| `database: x-okdp-connection-ref: require: field "major" is not part of the database contract`, `... require: "password" is a secret field, in the credentials Secret, never in the connection: require secretRef` | the package's requirement names a field the connection cannot have | fix the package's `config.schema.json`: require `secretRef` for credentials |
| `config.schema.json: properties.database: x-okdp-connection-ref: require: a JSON Schema, an object, got true`, `... require: "file:///require.schema.json#" is not valid against metaschema: ...` | the package's requirement is no JSON Schema, whether the instance sets the reference or not | fix the package's `config.schema.json` |
| several lines of the connection messages above, one `<field>` each | several references do not resolve: every one is reported, in parameter path order (keys sorted at each level), with its first error (the fields of a connection in alphabetical order), or all its missing required fields, or all its schema violations | fix each, then compile again: a fixed field may reveal the next one ([connection errors](specs/01_inputs_outputs.md#connection-errors)) |
| `app: upstream.app.service.port: service.port is set by the platform and cannot be changed` | the instance's upstream values set a path `okdp.yaml` protects (`upstream.<key>.protect`, and always `global`) | remove the value; set it through the parameters when the package offers one |
| `app: upstream.app.podAnnotations.x: the value contains "{{"`, `... the key contains "{{"` | template code in upstream values, of any values layer or of the package's defaults (the parameters are not checked: refusing template code of a layer it does not review is a host's) | remove it |
| `app: upstream.app.env must be a list` | a path of `append` given as a non-list | give a list |
| `the values (Inputs.Values) don't meet the specifications of the schema of global.okdp:` then `- at '/global/okdp/proxy': got string, want object`, `- at '/global/okdp/secrets/refreshInterval': got integer, want string` | a platform setting the compiler reads, or a map on the way to it, of another type (`proxy: <url>`, a number or a list for a string), or not among its values; it would otherwise read as not set. Every violation is listed | fix the platform values against `platform.schema.json`; `null` is not set ([fields the compiler reads](specs/01_inputs_outputs.md#fields-the-compiler-reads)) |
| `the values (Inputs.Values) don't meet ...` then `- at '/global/okdp/certificateIssuers': false schema` | the platform values set the former `certificateIssuers` | rename `certificateIssuers.selfSigned.name` to `tls.clusterIssuer`, the ClusterIssuer of the services' certificates; set `tls.caBundleSecret` to the Secret of the platform's CA bundle, if it has one ([fields the compiler reads](specs/01_inputs_outputs.md#fields-the-compiler-reads)) |
| `the values (Inputs.Values) don't meet ...` then `- at '/global/okdp/serviceCatalog': false schema` | the platform values hold the former console menu | remove it: the console's menu is a host's, no value of the platform's |
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
file](specs/01_inputs_outputs.md#the-info-file).

### Input documents, the catalog and claims

Each problem of the input documents and of the catalog, then each
refusal of `CompileAll` ([input
errors](specs/01_inputs_outputs.md#input-errors), [compiling instances
together](specs/01_inputs_outputs.md#compiling-instances-together)).
`<where>` is `<-f>/<path>: document <n> (line <l>, <kind>)`:

| Message | Cause | Fix |
|---|---|---|
| `<where>: line 9: replicas: not a key of the document, which holds kind, project, name, service, version, claimers, connections, values, valuesFiles` (`<where>: line 9: name is given twice, first at line 4`) | a key the document's kind does not hold (here values written beside the instance's keys, which belong under `values` or in a values file), or a key twice | write the values under `values`, or in a file of `valuesFiles` ([instance](specs/01_inputs_outputs.md#instance)) |
| `config/inputs.yaml: document 3 (line 12): no kind: Settings, Project or Instance, or a values file that a document names (valuesFiles)`, `... kind "Deployment": Settings, Project or Instance` | a document of no kind, or of another kind, in a file of `-f` that no `valuesFiles` names: a values file left among the documents, a manifest | name the values file in the `valuesFiles` of its document, or move the file out of `-f` ([input files](specs/01_inputs_outputs.md#input-files)) |
| `<where>: valuesFiles: "../shared.yaml": a path relative to the document's file, inside config`, `<where>: valuesFiles: config/demo/web.yaml: no such file or directory` | a values file named by an absolute path, or out of the input files, or that does not exist | keep it among the input files, its path relative to the document's file ([valuesFiles](specs/01_inputs_outputs.md#valuesfiles)) |
| `<where>: a second Settings document, after <where>: give the values of every instance once`, `<where>: project "demo" is given twice, first in <where>`, `<where>: instance demo/web is given twice, first in <where>` | the platform's values, a project or an instance given in two documents, of one `-f` or of two | keep one; several layers of one document are several `valuesFiles` |
| `<where>: project is required` (`name`, `service`, `version`), `<where>: line 3: project: a DNS label expected (lowercase letters, digits and -, 63 characters at most)` | an instance or a project without its name, or of a name that is no DNS label | name it ([instance](specs/01_inputs_outputs.md#instance)) |
| `<where>: claimers[0]: "Demo" is not a namespace name, nor "*"` (`claimers: demo is listed twice`), `<where>: line 8: claimers: a list of strings expected` | an instance's claimer that is not a project's name nor `*`, or listed twice; `claimers` not a list | list each project once, by its name, or `"*"` |
| `<where>: line 9: connections: a mapping expected` | an instance's `connections` that is not a map of connections | `connections: {<name>: {contract: ..., ...}}` ([connections](specs/01_inputs_outputs.md#connections)) |
| `<where>: service "web" is in the catalog of no repository`, `<where>: version "2.0.0" of service "web" is not in charts/catalog/web` | the instance names a package no catalog directory has | add it to a catalog, or fix the instance's `service` and `version` ([the catalog](specs/01_inputs_outputs.md#the-catalog)) |
| `<where>: the package charts/catalog/web/1.0.1: its Chart.yaml is web 1.0.0` | a version directory whose `Chart.yaml` has another name or version | rename the directory, or fix `Chart.yaml`: a released version is never changed, a change is a new version |
| `service "web" is in the repositories a and b` | a service of two catalog directories, whatever its versions | keep it in one ([the catalog](specs/01_inputs_outputs.md#the-catalog)) |
| `contract s3 differs between the repositories a and b` | two catalog directories ship different contracts of one name | make them the same, or rename one |
| `error: demo/demo-web: database: "demo-db" is not a connection, nor an output of an instance of the namespace, ... (demo-db does not compile either: what it publishes is unknown)` | the output is published by an instance that is refused, or by none; instances that reference each other's outputs are both refused so | fix that instance; make one of two references a connection's of the values layers: it waits for nothing |
| `error: demo/demo-p: provides "x" (server): demo-q publishes an output of that contract and name too, ...` | two instances of the namespace publish one output | name one of them otherwise: a reference must find one |
| `error: demo/demo-app: Secret "demo-app-x.yy-credentials" of namespace demo is written by ExternalSecret demo-app-x.yy-credentials of demo-app and ExternalSecret demo-app-x.yy-credentials of demo-app-x: two objects may not write one Secret` | two objects of the namespace, of one instance or two, write one Secret: here a claimer's copy of a provider's credentials, `<release>-<secretRef.name>`, and another instance's connection, `<release>.<connection>-credentials` | rename one: the provider's `secretRef.name`, an instance, a connection or an object |
| `error: demo/demo-app: db: claim (database-server): it names no class, and the settings set no default one, global.okdp.provisioning.database-server.default: the classes are fast, standard` | a claim that names neither a class nor a provider, and no values layer sets a default class; `no class of database-server connections is defined (...)` when none is; `claims.<name>: ...` for a named claim, every instance whose values declare it refused | set one, `global: {okdp: {provisioning: {database-server: {default: standard, classes: {standard: {provider: data/pg}}}}}}`, or name a class: `db: {class: fast}` ([classes](specs/01_inputs_outputs.md#classes)) |
| `error: demo/demo-app: db: claim (database-server): class "nvme" is not a class of database-server connections (global.okdp.provisioning.database-server.classes): fast, standard` (`the default class "nvme" (...default) is not a class ...`) | the claim, or the default, names a class the settings do not define | name one of those, or define it |
| `error: demo/demo-app: db: claim (database-server): the default class "standard": data/pg is no instance that provisions database-server connections` (`class "fast": ...`, `data/pg is no instance ...` for a provider the claim names) | the class's provider names no instance of its namespace, by name or release, or one whose package does not provision the contract | fix the class's provider, `<project>/<instance>` or `<project>/<release>`, or add the instance |
| `error: demo/demo-app: db: claim (database-server): demo/demo-a names more than one instance, demo-a (its release) and demo-demo-a (its name): the claim would not know which` (`class "fast": ...`) | the provider, the claim's or its class's, is the release of the instance `a` and the name of the instance `demo-a`, both of the project `demo` | name the one meant otherwise, `demo/a` or `demo/demo-demo-a`, or rename one of them ([providers](specs/01_inputs_outputs.md#providers)) |
| `error: demo/demo-app: db: claim: request.size: set by the class large (global.okdp.provisioning.database-server.classes.large): remove it, or name another class` | the claim requests a key its class fixes, in its `request` or its package's | remove it, or name a class that does not fix it |
| `error: demo/demo-app: db: claim: class or provider, not both` | the claim names both | name one |
| `error: demo/demo-app: db: claim "demo.app.db" (database-server): of data/data-pg, the provider of class "large", another namespace: demo is not one of its claimers (claimers of instance data/data-pg: none, its package's): ...` (`of data/data-pg, another namespace` for a provider named, `claims.<name>: ...` for a named claim) | the provider, of another project, is named by the claim or by a class, whichever layer defines it, and does not consent: its `Instance` document's `claimers`, else its package's (`provisions`), do not list the project | list the project in the provider's `claimers`, `claimers: [demo]` in its `Instance` document ([providers](specs/01_inputs_outputs.md#providers)) |
| `error: demo/demo-b: db: claim "reports" has contract "s3", "database-server" is expected` | the reference names a named claim of the namespace of another contract | name a claim of its contract, or declare one ([claims](specs/01_inputs_outputs.md#claims)) |
| `error: demo/demo-b: db: "reports" is a claim: the reference takes none (x-okdp-connection-ref's provision)` | the reference names a named claim, and its package does not let it take claims | name a connection or an output |
| `error: demo/demo-app: database: claim (database-server): the request does not meet the contract's claim schema (x-okdp-claim):` then `- at '/size': '5G' does not match pattern '^[0-9]+(Mi|Gi)$'` | what the claim requests (its `request`, merged over the package's, or a named claim's declaration's), its claim schema's defaults applied, is not what the contract's claim schema (`x-okdp-claim`) allows; one line per violation | request what it allows ([requests](specs/01_inputs_outputs.md#requests)) |
| `error: demo/demo-app: database: claim (s3): request: the contract declares no claim schema (x-okdp-claim): a claim requests nothing of it` | a claim requests something of a contract that declares no claim schema | remove the request, or add the claim schema to the contract |
| `error: demo/demo-b: claims.reports: claim: unknown key "share": contract, class, provider and request only` (`contract is required, ...`, `a connection of that name too (connections.reports)`) | a named claim not of the form, every instance whose values declare it refused | fix the declaration ([claims](specs/01_inputs_outputs.md#claims)) |
| `error: data/data-pg: claim "demo.app.db" (database-server): claimed of namespace demo: its credentials go through the platform's secret store, global.okdp.secrets.store, which is not set` | a claim across namespaces, its credentials in the provider's namespace | set the platform's secret store, `global.okdp.secrets.store`, with its `provider` for a SecretStore of each namespace ([credentials across namespaces](specs/01_inputs_outputs.md#credentials-across-namespaces), [what the store must enforce](specs/01_inputs_outputs.md#what-the-store-must-enforce)) |
| `error: platform/platform-x: Inputs.Namespace "platform": the namespace is reserved: platform/ holds the platform's keys of the secret store, which no project writes` | a project named `platform`: its namespace would own the platform's keys of the secret store | rename the project |
| `error: kube-system/kube-system-x: Inputs.Namespace "kube-system": the namespace is reserved: kube-* is Kubernetes' own (kube-system, kube-public, kube-node-lease)` | a project of a namespace the compiler reserves, Kubernetes' own; a host's set refuses its deployer engines' namespaces in its own words | rename the project; a component of the platform that is not one of its projects is installed outside the platform's projects |
| `error: openbao/openbao-x: Inputs.Namespace "openbao": the namespace is reserved by the platform (global.okdp.policy.reserved): no project has it` (`, privileged or not: ...`) | a project of a namespace the platform's values reserve, `global.okdp.policy.reserved`, even one `policy.privileged` lists (the security set's `namespace` hook) | rename the project, or remove the namespace from `policy.reserved` |
