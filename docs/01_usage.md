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
Windows), `LICENSE` and `README.md`; an SPDX SBOM of each archive,
`<archive>.sbom.json` (syft); and
`okdp-compile_<version>_checksums.txt`, the SHA-256 sums of them all,
signed keyless with cosign by the release workflow,
`okdp-compile_<version>_checksums.txt.sigstore.json`, the Sigstore bundle
of its signature, the workflow's certificate and its transparency log
entry. The build provenance of each archive is attested too (SLSA,
GitHub's artifact attestations). `<version>` is the tag without its `v`.
From [the releases](https://github.com/OKDP/okdp-compiler/releases):

```bash
version=X.Y.Z os=linux arch=amd64
url=https://github.com/OKDP/okdp-compiler/releases/download/v$version
curl -fsSLO "$url/okdp-compile_${version}_${os}_${arch}.tar.gz"
curl -fsSLO "$url/okdp-compile_${version}_checksums.txt"
curl -fsSLO "$url/okdp-compile_${version}_checksums.txt.sigstore.json"
# The checksums, signed by the release workflow of OKDP/okdp-compiler on main
cosign verify-blob \
  --bundle "okdp-compile_${version}_checksums.txt.sigstore.json" \
  --certificate-identity "https://github.com/OKDP/okdp-compiler/.github/workflows/release-please.yml@refs/heads/main" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "okdp-compile_${version}_checksums.txt"
# The archive, by its checksum
sha256sum --ignore-missing -c "okdp-compile_${version}_checksums.txt"
# Its provenance: built by that workflow from that repository
gh attestation verify "okdp-compile_${version}_${os}_${arch}.tar.gz" --repo OKDP/okdp-compiler
tar -xzf "okdp-compile_${version}_${os}_${arch}.tar.gz" okdp-compile
./okdp-compile --version    # X.Y.Z
```

`cosign verify-blob` needs cosign 2.4 or later (the Sigstore bundle),
`gh attestation verify` the GitHub CLI; either alone is a verification:
the signature proves the checksums are the release workflow's, the
attestation that the archive was built by it. The releases before the
signature was added have their checksums only.

### `go install`

```bash
go install github.com/okdp/okdp-compiler/cmd/okdp-compile@vX.Y.Z
```

builds a release with Go 1.27.3 or later and installs it in `$GOBIN`
(`$(go env GOPATH)/bin` by default); `--version` prints `dev`, the version
being set by the `-ldflags` of the release archives and `make build`. The go
command finds the module by its path, `github.com/okdp/okdp-compiler`
(`go.mod`), through the Go module proxy by default; `@latest` is the latest
release.

### From a checkout

Go 1.27.3 or later (`go 1.27.3` in `go.mod`):

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
okdp-compile --repository <dir> [--repository <dir>]...
             [--catalog <dir>]... -o <dir>
             [--target helm|flux|argocd] [--path-prefix <path>]
             [--flux-source <namespace>/<name>]
             [--repo-url <url>] [--revision <revision>]
             [--cost-limit <n>] [--instance-timeout <duration>]
             [--timeout <duration>] [--force]
             [--report <file>|-] [--partial] [--allow-plain-secrets]
okdp-compile --check [--charts <dir>] [--report <file>|-]
             [--allow-plain-secrets]
             --repository <dir> [--repository <dir>]...
             [--catalog <dir>]...
okdp-compile --version
```

| Flag | Default | |
|---|---|---|
| `--repository` | required | a repository of the tree, its directory, repeatable: any part of the [tree](#repository-layout), each part in one repository; every instance of the tree compiled together ([`--repository`](#--repository)) |
| `--catalog` | | a catalog of packages, its directory, repeatable: `catalog/` and `contracts/` only, as a `--repository`'s, each service in one repository; one that holds `platform.yaml` or `projects/` is refused ([repository layout](#repository-layout)) |
| `--output`, `-o` | required, refused with `--check` | the output directory: under `projects/<namespace>/instances/<release>/`, each instance's umbrella chart, its values, OKDP's own objects and what it publishes, and the files of `--target`; every symbolic link under its `projects/` removed first, nothing written through one ([`--output`](#--output)) |
| `--check` | | compile and check, write nothing: the errors and warnings, no `--output` ([`--check`](#--check)) |
| `--charts` | `$OKDP_CHARTS`, with `--check` | chart cache directory, read by `--check` only: each upstream chart unpacked under its repository's directory, the URL without its scheme, as `helm pull --untar -d <dir>/<repository>/<chart>-<version>` writes it, `<repository>/<chart>-<version>/<chart>/` (`quay.io/adaltas/oidc-dcr-0.6.0/oidc-dcr/`) ([chart cache](specs/01_inputs_outputs.md#chart-cache)); the final values checked against each upstream chart. Without `--check`, refused, and `$OKDP_CHARTS` not read |
| `--target` | `helm` | what is written: `helm`, each instance's umbrella chart; `flux`, with its HelmRelease, the Kustomizations and each project's manifests; `argocd`, with its Application and each project's manifests ([targets](specs/01_inputs_outputs.md#targets)) |
| `--path-prefix` | the root | `--target flux` and `argocd`: the path of the output directory in the repository the deployer reads |
| `--flux-source` | `flux-system/okdp-gitops` | `--target flux`: the GitRepository of that repository, `<namespace>/<name>` |
| `--repo-url` | required by `--target argocd` | `--target argocd`: the repository Argo CD reads the output directory from |
| `--revision` | `HEAD` | `--target argocd`: its revision |
| `--cost-limit` | `1000000` | the CEL cost each instance's expressions may use together, its mapping's and the compiler's templates', what a host function reads and builds counted (`Inputs.CostLimit`): past it, the instance is refused, `the expressions of the compile exceed its CEL cost limit (n)`; an expression costs what is left of it, not the limit again; `0`, no limit. The default is about ten times the dearest case of the three OKDP catalogs (88000) |
| `--instance-timeout` | `1m` | how long one instance's compile may take (`Inputs.Timeout`), a Go duration: past it that instance is refused, `the compile takes longer than the instance timeout (<duration>)`, the others compiled, and `--partial` keeps its project; `0`, no limit |
| `--report` | | write a JSON report of the compile to the file, or to stdout for `-`, even with `--check` and when the compile fails: each instance, its status, error, warnings, dependencies, claims and charts, and the tree's errors ([`--report`](#--report)); refused inside a `--repository` or `<output>/projects/` |
| `--partial` | | write the projects whose instances all compile and that load, and keep what the output holds of the others' instances, their manifests and identity of today's settings, status 1 all the same ([`--partial`](#--partial)); refused with `--check` |
| `--force` | | write into an `<output>/projects/` that holds files but no `projects/.okdp-compile`, which the command did not write, removing from it every file it does not write ([`--output`](#--output)); refused with `--check` |
| `--allow-plain-secrets` | | accept credentials written in clear (`Inputs.AllowPlainSecrets` of every instance): a `provides` `secret` without `generate`, a Secret of the `objects` section with `data` or `stringData`; without it they fail the compile, the output being committed to the deployments repository |
| `--timeout` | `10m` | how long the compile may take, every instance together, a Go duration (`90s`, `1h`): past it, `the compile takes longer than --timeout <duration>: nothing written`; `0`, no limit |
| `--version` | | print the version and exit |

Flags take one or two dashes, and every input is a flag: an argument that
is not one is refused, where it would end the flags and those after it be
ignored (a directory without its `--repository`). The directories of
`--repository`, `--catalog` and `--charts` must exist. Every output is
built before any is written: anything that fails writes nothing; an error
while writing may leave some of the files written.

What the command reads of those directories is bounded. A symbolic link
under one is not followed: the file it is, a file whose path goes
through it, a package's version directory (not reported missing), or a
directory listed that holds it fails, named,
`<repository>/<path>: a symbolic link, not followed`, wherever it points
(the directory itself may be a link). A file that is neither regular nor
a directory (a FIFO, a device) fails, `not a regular file nor a
directory: not read`; so does a file over 4 MiB, `<n> bytes, over the 4
MiB a file read may hold`, and a new one once the files read of one
directory pass 64 MiB together, `the files read of --repository <dir>
pass 64 MiB together`. The chart cache (`--charts`, okdp-gitops's) is
bounded by chart: the files of each chart's directory 64 MiB together,
`the files read of --charts <dir>, <chart directory>, pass 64 MiB
together`, so that the charts one project names do not exhaust the
cache's reads and refuse another project's instances.

### `--repository`

Every instance of a tree compiled together, the inputs of each read from
the tree ([repository layout](#repository-layout)): `--repository <dir>`,
repeatable, each directory any part of it, and `--catalog <dir>`,
repeatable, each a catalog of packages, `catalog/` and `contracts/`
only. In order:

1. the tree: every instance, by project then instance, the release
   `<project>-<instance>` ([repository layout](#repository-layout),
   `compiler.LoadRepository`), every problem of the tree at once; a
   project's own (its `project.yaml`, an instance's files, the version
   it names) refuses that project, not the tree ([`--partial`](#--partial));
2. the instances compiled together, each claim bound to its provider
   ([claims](specs/01_inputs_outputs.md#claims)), each instance with what
   the others of its namespace publish, after those whose outputs it
   references and the providers of its claims ([compiling instances
   together](specs/01_inputs_outputs.md#compiling-instances-together)),
   checked against the chart cache with `--check --charts`: each binding
   on stderr, `<namespace>/<release>: <field>: claim <id> (<contract>)
   bound to <namespace>/<release>`, then `, class <class>` for the class
   it names, `, the default class <class>` when it names neither a class
   nor a provider, nothing when it names its provider, then each refusal,
   `error: <namespace>/<release>: <message>`, each warning,
   `warning: <namespace>/<release>: <message>`, in the order of the
   instances;
3. their [deployment graph](specs/01_inputs_outputs.md#the-deployment-graph):
   each problem on stderr, `error: <namespace>/<release>: <message>`;
4. the files of the target ([targets](specs/01_inputs_outputs.md#targets)),
   written under `-o` ([`--output`](#--output)); then each dependency,
   `<namespace>/<release>: depends on <instance> (<contract> <output>,
   <field>)`.

Any problem exits with status 1, nothing written; `--check` stops before
writing, and prints no dependency. The options are checked before the
tree is read.

```console
$ bin/okdp-compile --repository examples/03_full/input/db -o out/tree
demo/demo-workspace: catalogs[1].database: claim demo.marketing (database) bound to demo/demo-db, the default class standard
demo/demo-workspace: database: claim demo.workspace.database (database) bound to demo/demo-db, the default class standard
demo/demo-workspace: depends on demo-db (database demo.marketing, catalogs[1].database)
demo/demo-workspace: depends on demo-db (database demo.workspace.database, database)
demo/demo-workspace: depends on demo-alerts (http alerts, notifications.webhook)
```

`out/tree` then holds `examples/03_full/output/`, byte for byte.

| `--target` | Writes, besides each instance's umbrella chart | Options |
|---|---|---|
| `helm` (the default) | nothing | none |
| `flux` | each instance's `helmrelease.yaml` and `kustomization.yaml`, each namespace's `project/` and `kustomization.yaml`, and `projects/kustomization.yaml`, the namespaces' directories, what a Flux Kustomization of `projects/` syncs | `--path-prefix`; `--flux-source` |
| `argocd` | each instance's `application.yaml`, each namespace's `project/` | `--repo-url`, required; `--revision`; `--path-prefix` |

Each namespace's `project/` holds the project's own manifests: its
Namespace, with its Pod Security Admission labels, kept by both engines
when the project goes, the platform's secret store in it when the
platform names a provider
(`global.okdp.secrets.provider`), and the identity its releases are
installed with, confined to its namespace: `flux`, a ServiceAccount,
`project-<namespace>`, bound to a Role of the namespace, which each
HelmRelease names; `argocd`, the AppProject `project-<namespace>` its
Applications are in, destined to the namespace alone. A project of
`global.okdp.policy.privileged`, one of the platform's own, keeps the
engine's identity (the HelmRelease names no ServiceAccount, the
AppProject admits every namespace and cluster-scoped resource). The
platform's entry point applies them before the instances ([the project's
manifests](specs/01_inputs_outputs.md#the-projects-manifests)). The
files both targets write are the same, so that one tree holds both:
`namespace.yaml` and `secret-store.yaml` of `project/` among them.

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
connection it provides. Its `database` parameter claims a database,
`{}`, bound to `demo-db`, the provider of the platform's default class of
database connections, `standard` (`global.okdp.provisioning` in
`platform.yaml`): the binding and the dependency are printed on stderr:

```console
$ bin/okdp-compile --repository examples/02_advanced/input/db -o out
demo/demo-webapp: database: claim demo.webapp.database (database) bound to demo/demo-db, the default class standard
demo/demo-webapp: depends on demo-db (database demo.webapp.database, database)
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
    version: 0.6.0
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
of its outputs, its `objects` section and the ExternalSecret that reads
the credentials of `datalake`, a connection of the platform, from the
platform's secret store, are in `okdp.objects.yaml`; it depends on
`demo-db`, by its two claims, and on `demo-alerts`
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

The command marks the directory it owns, `projects/.okdp-compile`, and
refuses one it did not write: a `projects/` that holds files but not the
mark (a directory of another tool, a wrong `-o`) fails before anything
is compiled, nothing removed, `<output>/projects/ exists and was not
written by okdp-compile (no projects/.okdp-compile): choose an empty
output, or remove it yourself`. `--force` writes over it, the mark then
written; so does an output written by a version without the mark, once.
A `projects/` that is a symbolic link fails, even with `--force`. Every
symbolic link under `projects/`, at any depth, a file's or a
directory's, wherever it points, is removed before a file is written:
the link, not what it points to; a project's files then land in a
directory of their own, never through a link, even one that stays in
the output. Every file is written through an `os.Root` of the directory
besides: nothing leads a file out of it.

Once the files are written, the command prints on stderr, after the
warnings, the `Result.Dependencies` of each instance: one line per
connection reference to another instance, by parameter path,
`<namespace>/<release>: depends on <instance> (<contract> <output>,
<field>)`: the instance's release name, `<namespace>/<instance>` for a
claim's provider of another namespace, the contract it must provide, the
output the reference resolved to and the parameter that references it
([dependencies](specs/01_inputs_outputs.md#dependencies)), `depends on
demo-pg (database-server pg-hive, db)`. No file holds them: the graph
orders the releases with them
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

### `--report`

`--report <file>` writes what the compile gives as JSON, for a CI job, a
bot that comments a pull request or a dashboard: `-` writes it to
stdout, which the command prints nothing else on. It is written with
`--check` too, and when the compile fails (the exit status is the
same), but not when `--report` itself is refused; the messages on stderr
are unchanged.

| Key | |
|---|---|
| `instances` | every instance of the tree, in the order of its repositories' projects and instances |
| `instances[].namespace`, `instance`, `release` | the project, the instance's name, its release |
| `instances[].service`, `version` | its package |
| `instances[].privileged` | its project is one of the platform's own (`global.okdp.policy.privileged`); `false` when refused |
| `instances[].status` | `compiled`; `refused`, its compile failed, the deployment graph has a problem at its release, or an instance of another project has its release name (`--target flux`, `argocd`); `skipped`, not compiled, the compile stopped (`--timeout`), or, with `--partial`, compiled but not written, another instance of its project refused |
| `instances[].error` | why it is refused, the message stderr prints after `error: <namespace>/<release>: `; `""` when it is not |
| `instances[].warnings` | its warnings (`Result.Warnings`) |
| `instances[].dependencies` | the instances it depends on, `{instance, namespace, contract, field, output}` (`Result.Dependencies`, `namespace` when another) |
| `instances[].claims` | each claim of its references bound to its provider, `{field, id, contract, provider: {namespace, release}, class}`, `class` `""` when the claim names its provider |
| `instances[].charts` | the upstream charts it renders, by key, `{key, repository, chart, version}` (`Result.Charts`) |
| `errors` | what fails that is no instance's: an option, a repository, a problem of a project that does not load, the target, the output, `--timeout` |
| `written` | the files were written under `-o`, those of some projects only with `--partial` |

The lists are empty, never `null`. An instance whose compile succeeded
but which is not written, another being refused, is `compiled`: whether
the tree was written is `written`.

```console
$ bin/okdp-compile --check --report - --repository examples/02_advanced/input/db | jq -c '.instances[] | [.release, .status, .claims[].id]'
["demo-db","compiled"]
["demo-webapp","compiled","demo.webapp.database"]
```

### `--partial`

Without it, one refused instance writes nothing: every project waits for
it. With `--partial`, a refusal fails its project, not the tree: the
projects whose instances all compile, and at whose releases the
deployment graph has no problem, are written; the others are not, what
the output holds of their instances kept as it is (their last good
output, still deployed), each printed after the refusals. What the
platform decides of a project kept is written all the same, of today's
settings: its manifests (`project/`: its Namespace and its Pod Security
labels, its identity, the RoleBindings of its groups and of the control
plane, its AppProject), and, with `--target flux`, each kept instance's
HelmRelease (its ServiceAccount, its `dependsOn` as it was), so that a
privilege revoked, a role taken back or a policy tightened is not kept
by a project one of whose instances is refused: its instances kept are
applied with today's identity, which refuses what it no longer allows (a
ClusterRole of a project no longer privileged). A manifest of before
that today's settings do not write is removed.

```text
error: demo/demo-web: values don't meet the specifications of the schema(s) in the following chart(s):
web:
- at '/replicas': minimum: got 0, want 1
error: demo: not written, an instance of it refused (--partial): out/projects/demo/ kept as it is, its namespace, identity and roles of today's settings
```

A project whose namespace is reserved since the output was written, by
`global.okdp.policy.reserved` or by a release of the compiler that
reserves a name, is not kept: its instances are refused like any
project's of that namespace, and its output is dropped, nothing of its
Role, Namespace or releases kept nor re-derived (they would sit in a
namespace no project has), and not in the root `kustomization.yaml`; said
so, and the other projects are written.

```text
error: openbao: dropped from the output (--partial): Inputs.Namespace "openbao": the namespace is reserved by the platform (global.okdp.policy.reserved): no project has it: nothing of out/projects/openbao/ kept
```

A project that does not load fails alike: a file of its own that does not
parse (`project.yaml`, an `instance.yaml`, a `config.yaml`), a key
`instance.yaml` does not know, a stray file in an instance's directory, a
package version its catalog lacks, a connection or a claim it declares
that does not hold. The project is refused as a whole, none of its
instances compiled, each problem printed, and its output kept as it is,
its manifests of today's settings when its `project.yaml` reads (its
network policy then has no claimers: the claims bound to it do not
bind); the other projects are written. Without `--partial` the problems
fail the command, nothing written. Only the platform's parts fail the
tree, with or without `--partial`: `platform.yaml`, `catalogs.yaml`, a
repository and its role, a package that does not load, a contract that
differs between repositories.

```text
error: db/projects/demo/instances/web/values.yaml: not a file of an instance, which holds instance.yaml, config.yaml and connections.yaml only: no compile reads it
error: demo: not written, it does not load (--partial): out/projects/demo/ kept as it is, its namespace, identity and roles of today's settings
```

An instance of another project that depends on a release of a project
not written, a connection reference or a claim, fails its own project in
turn, round after round, said so, its report status `skipped`: it
compiled, with what that release publishes now, which is not deployed:

```text
error: demo/demo-app: it depends on data/data-pg, of the project data, whose output is kept
```

A problem of the graph of its own (a cycle, a release that does not
provide what is referenced) fails its project with that problem, its
status `refused`. So does a release name two projects share, `demo/app-x`
and `demo-app/x` both `demo-app-x`, with `--target flux` and `argocd`,
whose objects share a namespace: both projects fail, the others are
written; one that the output does not hold yet writes nothing.

```text
error: demo/demo-app-x: release "demo-app-x" is in namespaces "demo" and "demo-app": the HelmReleases of okdp-releases need distinct names
error: demo-app/demo-app-x: release "demo-app-x" is in namespaces "demo" and "demo-app": the HelmReleases of okdp-releases need distinct names
error: demo: not written, an instance of it refused (--partial): out/projects/demo/ kept as it is, its namespace, identity and roles of today's settings
error: demo-app: not written, an instance of it refused (--partial): out/projects/demo-app/ kept as it is, its namespace, identity and roles of today's settings
```

Without `--partial`, the same release name fails the command, each
instance of it printed, nothing written. The rule is the library's,
`compiler.Partial`, the files and the release names `compiler.TreeFiles`
([Go library](#go-library)). With `--target flux`, the root
`kustomization.yaml` still lists a project kept, when the output holds
its `kustomization.yaml`, which Flux would prune otherwise
(`Flux.Keep`); with `argocd`, the Applications of a project kept stay
where they are. The output of the projects kept is read as the
repositories are, bounded, no symbolic link followed: a link in it fails
the command, nothing written. The command exits with status 1 all the
same: what was refused is to be fixed. A failure of the tree writes
nothing, as without `--partial`: an option, a repository, the target's
files, a release the output of a project kept holds and a project
written has too in another namespace, or the output of two projects kept
(`--target flux` and `argocd`), `--timeout`.

### Exit status and errors

| Status | When |
|---|---|
| 0 | compiled, or checked (`--check`), each binding and warning on stderr, then, the files written, each dependency, as [`--repository`](#--repository) prints them; `--version`, `--help` |
| 1 | any error: `error: <message>` on stderr, one per refused instance, per problem of the graph and per instance of a release name two projects share, `error: <namespace>/<release>: <message>`; one per problem of a project that does not load, `error: <repository>/<path>: <message>`; with `--partial`, one per project not written too, `error: <namespace>: not written, ...`, the others written |
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

A message does not print the value of a sensitive key: a schema violation
of a password, a token, a client secret, an API key, a key whose name
ends with `Password`, `Secret`, `Token` or `Key`, or of an item of a list
of one, prints `<redacted>` in its place, and so does a value a message
quotes for those of its keys, or whole when it is one's: a connection
reference or a named claim of such a name that is not of its type
(`value.Sensitive`,
[security boundaries](00_architecture.md#security-boundaries)):

```text
error: demo/demo-web: values don't meet the specifications of the schema(s) in the following chart(s):
web:
- at '/auth/password': <redacted> does not match pattern '^[^ ]{12,}$'
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
demo/demo-webapp: depends on demo-db (database demo.webapp.database, database)
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
  catalogs.yaml                          optional: the catalogs a caller fetches, the console's menu
  contracts/<contract>.schema.json       the contracts (Inputs.Contracts)
  catalog/<service>/<version>/           a package (Definition)
  projects/<project>/                    a namespace (Inputs.Namespace)
    project.yaml                         optional: the context, level 1; its description, its access
    instances/<instance>/                the release <project>-<instance> (Inputs.Release)
      instance.yaml                      {service: <name>, version: <version>}: which package; claimers
      connections.yaml                   optional: the context, level 2
      config.yaml                        optional: the instance values (Inputs.Values)
.charts/                                 --check --charts <dir>
output/projects/<project>/instances/<project>-<instance>/   -o <dir>
```

- `instance.yaml` holds `service` and `version`, the instance's package,
  `catalog/<service>/<version>/`, and, optional, `claimers`, nothing
  else: the namespaces whose instances may claim of it, a provider, from
  another namespace, by naming it or through a class of their project's
  (`claimers: [demo]`, `["*"]` for any; absent, its package's,
  `provisions`, none by default), a class of `platform.yaml` needing none
  ([providers](specs/01_inputs_outputs.md#providers)).
- The context has a file per level
  ([levels](specs/01_inputs_outputs.md#levels)): 0 `platform.yaml`, the
  platform values (`global.okdp`, the classes of the claims and their
  default among it, the platform's secret store, the connections every
  instance may reference); 1 `project.yaml`, what a project adds for its
  instances (its settings under `global`, its classes beside the
  platform's, a default class of a contract the platform sets none of, its
  connections, its named claims, `claims: {<name>: {contract:
  <contract>}}`, which its instances reference by name); 2
  `connections.yaml`, the instance's own connections (`connections:
  {<name>: {...}}`), written as the lower levels write theirs, and nothing
  else: `global` is the platform's or the project's. A level adds values
  and changes none of a lower level's. The named claims are a project's
  only: `platform.yaml` declares none. A connection's credentials are in a
  Secret of the instance's namespace, `secretRef: {name: <secret>}`, or in
  the platform's secret store, `secretRef: {remoteKey: <key>}`, which the
  compiler reads into one, a key under `<project>/` for a project's or
  an instance's connection, the platform's under `platform/` with a
  ClusterSecretStore, refused with `secrets.provider`, where no namespace
  reads it
  ([credentials in the platform's secret
  store](specs/01_inputs_outputs.md#credentials-in-the-platforms-secret-store)).
- `catalogs.yaml`, beside `platform.yaml`, is the program's that fetches
  the catalogs (okdp-gitops) and the console's: `repositories`, a list
  of `{name, url, ref}`, each catalog's repository at a ref, `main` by
  default (`treeedit.DefaultCatalogRef`), the names unique;
  `categories`, the console's menu, in order, each `{title, icon,
  services}`, the titles unique, each service `{name, description,
  default, label, exposesUI}` of a catalog, listed once, `default` a
  version of its catalog (none: the highest). No other key, at any
  level: a former `serviceCatalog` wrapper, a service's `versions` (its
  catalog's directories) or `repository`, are refused. The compile
  checks it when a repository of the tree holds it, its services against
  the catalogs (`treeedit.ParseCatalogs`, `treeedit.CheckCatalogs`).
- `project.yaml` may describe its project, `description: <text>`, a
  string: the project's own, which is no value of the context, and which
  no instance receives (`compiler.ProjectDescription`).
- `project.yaml` may give its people their roles, `access: {<group>:
  viewer|editor|admin}`, the identity provider's groups
  (`compiler.ProjectAccessKey`): absent, `okdp-project-<project>` is an
  editor; `{}`, no group. No instance receives it; the flux and argocd
  targets write it as the Argo CD roles of the project's AppProject and
  the RoleBindings of its namespace to Kubernetes' `view`, `edit` and
  `admin`, `platform.yaml` naming the platform's administrators
  (`global.okdp.access.admins`) and the prefix the API server gives the
  groups (`global.okdp.access.groupPrefix`)
  ([access](specs/01_inputs_outputs.md#access)). Whoever writes
  `project.yaml` sets it: in a repository of its own (the split
  topology), its writers, the project's administrators.
- `config.yaml` is the instance values: its parameters and upstream
  values, no `connections`, no `claims`. An instance's directory holds
  `instance.yaml`, `config.yaml` and `connections.yaml` only: another
  entry, a `values.yaml`, a directory, a hidden file, would be read by
  no compile, and is refused, each named.
- Every connection of the context, the platform's, a project's, an
  instance's own, is checked at its declaration, referenced or not
  ([connections](specs/01_inputs_outputs.md#connections)): one that
  does not hold refuses the instances of its context, at
  `connections.<name>`.
- The release is `<project>-<instance>`, the project's directory name
  and the instance's joined by `-`, the namespace the project's: no field
  overrides them. `projects/demo/instances/webapp/` is the release
  `demo-webapp` of the namespace `demo`: its files of `-o` are in
  `projects/demo/instances/demo-webapp/`, an output it publishes is named
  `webapp` by default, the instance's name, as its claims' IDs are
  (`demo.webapp.database`), and a class names it as a provider by its
  name or its release, `demo/webapp` or `demo/demo-webapp`
  ([providers](specs/01_inputs_outputs.md#providers)). The releases of a
  tree are unique across its projects, but for names that hold a `-`
  (`a-b/c` and `a/b-c` are both `a-b-c`), which the Flux and Argo CD
  targets refuse ([targets](specs/01_inputs_outputs.md#targets)).
- `.charts/`, the chart cache of the checks (`helm pull --untar`), is
  outside `db/`: it is not source. Only the full example has one; the
  compiles read none.

`--repository` checks that `catalog/<service>/<version>/` holds a
`Chart.yaml` of that `name` and `version`; a caller of a compile does,
and treats a released version directory as immutable: a change is a new
version.

The tree may be in several repositories, `--repository` given for each (a
`compiler.Repository` each, of the role `compiler.RoleTree`): a platform
repository with `platform.yaml`, a repository per project with
`projects/<project>/`, or all in one; and catalogs, `--catalog` for each
(`compiler.RoleCatalog`), with `catalog/` and `contracts/` only (OKDP's
platform-charts is one, as it is). A catalog is trusted for its packages,
not for the deployment: one that holds `platform.yaml` or `projects/`, a
file or a directory, even empty, is refused, each path named, rather than
read without them, so that its maintainers add no project, of a
privileged name or not, nor the platform's values. The library requires
the role of each `compiler.Repository`: the zero value is refused. They
make one tree, part by part, with no precedence, so that the order of
the flags changes nothing:

| Part | Across repositories |
|---|---|
| `platform.yaml`, `catalogs.yaml` | in one at most, each |
| `catalog/<service>/` | in one: every version of a service from it |
| `contracts/<contract>.schema.json` | in several, the same bytes in each: a catalog ships the contracts of its packages |
| `projects/<project>/` | in one: the project's file and its instances |

An instance's package and contracts come from any repository. The
compiler reads directories: checking a repository out at a revision is
its caller's.

`TestExample` (`examples_test.go`) compiles each example's tree this way
(`LoadRepository`, then `CompileAll`), and compares what the helm target
writes with its `output/`. The example instances of the project `demo`,
`hello`, `webapp`, `db`, `workspace`, `alerts`, `app`..., are the
releases `demo-hello`, `demo-webapp`, `demo-db`, `demo-workspace`,
`demo-alerts`, `demo-app`; the claims example's provider, `postgresql`
of the project `data`, is `data-postgresql`. The full example's command
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
	in.Context = []compiler.ContextLayer{{Values: read("examples/02_advanced/input/db/platform.yaml")}}
	in.Values = []map[string]any{read("examples/02_advanced/input/db/projects/demo/instances/webapp/config.yaml")}
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
oidc-dcr: oci://quay.io/adaltas oidc-dcr 0.6.0
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
`compiler.Info`; nil, none is known; `CompileAll` sets it), `Claims` (the
claims bound to the instance of a provider, `compiler.Claim`), `Bindings`
(the providers the instance's claims are bound to, by parameter path,
`compiler.Binding`): `CompileAll` sets both
([claims](specs/01_inputs_outputs.md#claims)); all but `Charts`,
`Context`, `Values`, `Published`, `Claims` and `Bindings` are required
([inputs and outputs](specs/01_inputs_outputs.md)). `Result.Values` is
what `-o` writes in `instance.values.yaml`, `Result.Charts` the index the
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
  `compiler.Dependency`, `{Instance, Namespace, Contract, Field, Output}`,
  `Namespace` the instance's when it is another namespace's (a claim's
  provider), `Output` the output the reference resolved to, by name, or
  the claim's ID
  ([dependencies](specs/01_inputs_outputs.md#dependencies)): what a
  deployer orders the releases with ([GitOps](#gitops)). `Write` writes
  them in no file; the binary prints each, once the files are written, as
  `depends on <instance> (<contract> <output>, <field>)`.
- `compiler.LoadRepository(repos)` reads a tree, in one or more
  repositories, each a `compiler.Repository` (its name in messages, its
  `fs.FS`), as the instances `CompileAll` compiles, by project then
  instance, each named after its directory (`Instance.Name`), the name of
  its outputs that name none and of its claims' IDs
  ([repository layout](#repository-layout)); every problem at once, each
  naming its repository and path.
- `compiler.LoadCatalog(repos)` is the catalog of the same repositories,
  as `LoadRepository` reads it, for a caller that lists the packages
  (a console's menu): `Services()`, each a `compiler.CatalogService`
  (`Name`, `Repository`, `Versions`: the directories of
  `catalog/<service>/`, highest first, the SemVer ones by precedence),
  `Service(name)`, `Package(service, version)`, the files of
  `catalog/<service>/<version>/`, and `Contracts()`, every
  `contracts/<contract>.schema.json`. A service or a version no
  repository has is an error wrapping `compiler.ErrNotInCatalog`; a
  service in two repositories, or a contract that differs between two,
  fails `LoadCatalog` as it fails `LoadRepository`.
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
- The names of the tree are constants, those of the [repository
  layout](#repository-layout): `compiler.PlatformFile`, `ContractsDir`,
  `CatalogDir`, `ProjectsDir`, `ProjectFile`, `InstancesDir`,
  `InstanceFile`, `ConnectionsFile` and `ConfigFile`, and the key of a
  project's description, `ProjectDescription`;
  `compiler.ReleaseName(project, instance)` is the release of an instance
  of the tree, `<project>-<instance>`; `compiler.IsName(name)` whether a
  name may be a project's, an instance's, a connection's or a named
  claim's, a DNS-1123 label; `compiler.CheckProjectName(name,
  platform)`, `platform` the values of `platform.yaml`, why the compile
  would refuse every instance of a project of that name, in its words
  (`project "kube-foo": the namespace is reserved: ...`): not a name,
  one the compiler reserves (`platform`, `kube-*`, `argocd`,
  `flux-system`, `okdp-releases`) or one of `global.okdp.policy.reserved`,
  for a program that creates a project's namespace or directory before
  any compile (the flux target's `Source`'s namespace is refused by the
  target, when it writes the tree); `compiler.ProjectDir(namespace)` and
  `compiler.InstanceDir(namespace, release)` are where a target writes the
  files of a namespace, `projects/<namespace>`, and of an instance,
  `projects/<namespace>/instances/<release>`. A caller that writes a tree
  (a control plane's git transactions) or reads what a target wrote names
  them so, rather than spelling the layout again.
- The authority over projects, for a program that mirrors it (a control
  plane): `compiler.ProjectAccess(file, project)`, the roles of the
  groups of a project, `file` its `project.yaml` as written (nil: none),
  a `map[string]compiler.AccessRole` (`AccessViewer`, `AccessEditor`,
  `AccessAdmin`), the default `compiler.DefaultProjectGroup(project)`,
  `okdp-project-<project>`, an editor, when it gives none, the compile's
  errors; `compiler.PlatformAdmins(platform)`, the groups of the
  platform's administrators of the values of `platform.yaml`, checked as
  the compile checks them; `compiler.IsGroupName(name)`, whether a name
  may be a group's ([access](specs/01_inputs_outputs.md#access)).
- `compiler.CompileAll(ctx, instances)` compiles instances together, each
  a `compiler.Instance`, its `Definition` and its `Inputs` without
  `Name`, `Published`, `Claims` and `Bindings`, and its `Name`, which a
  provider names it by, as by its release, and which names its outputs
  and its claims (`LoadRepository`: its directory's; empty: its release),
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
  is refused, and so is one of another namespace, named or a project's
  class's, whose claimers (`Inputs.Claimers`, else its package's,
  `Instance.Claimers(contract)`) do not list the claimer's namespace
  ([providers](specs/01_inputs_outputs.md#providers)).
- `compiler.NewGraph(results)` is the deployment graph of the results of
  `CompileAll`: each release waits for the releases of its namespace its
  references point to. It checks every dependency, the release exists and
  provides what is referenced, and refuses a cycle, every problem at once,
  each a `*compiler.GraphError` at its release; then `DependsOn` gives
  what a release waits for, `Wave` its Argo CD sync wave
  ([the deployment graph](specs/01_inputs_outputs.md#the-deployment-graph)).
- A target writes the results for a deployer, `Files(results, graph)`,
  path -> content, under `projects/`: `compiler.Helm{}`, the umbrella
  chart of each instance, in `<namespace>/instances/<release>/`;
  `compiler.Flux{Source, PathPrefix}`, with each instance's HelmRelease,
  its `dependsOn` from the graph, and the Kustomizations, each namespace's
  and the root's, `projects/kustomization.yaml`; `compiler.ArgoCD{RepoURL,
  Revision, PathPrefix}`, with each instance's Application, its sync wave
  from the graph ([targets](specs/01_inputs_outputs.md#targets)).
- `compiler.CompileTree(ctx, instances, charts)` is `CompileAll`, then
  `NewGraph` of the instances that compile, for a caller that reads a
  compiled tree (a control plane). With `charts`, a
  `compiler.ChartSource`, a chart cache that fetches what it lacks
  (`Ensure(ctx, refs)`), the upstream charts the compile names
  (`Result.Charts`, the OAuth clients' among them) are fetched, then the
  instances compiled again with them (`Inputs.Charts`), their final
  values checked; a chart that cannot be fetched leaves them unchecked,
  `Tree.ChartsErr` saying why. It is a `compiler.Tree`, its `Instances`
  in their order, each a `compiler.TreeInstance`, the `Instance` with its `Outcome`, the
  problem of the graph at its release (`GraphErr`), why it does not
  deploy (`Err`), the claims bound to it as a provider, by ID
  (`Provided`, each a `compiler.ProvidedClaim`, the `Bound` claim and
  its `Claimer`), what it granted a claim (`Granted(id)`, the output it
  publishes for it), the contracts its package provisions
  (`Provisions`) and the namespaces that may claim of it
  (`Claimers(contract)`); the tree's `Graph`, nil when it has a problem,
  `Instance(namespace, name)`, `Release(namespace, release)`, and
  `Problems()`, `<namespace>/<name>: <why>` per instance that does not
  deploy.
- `compiler.Build(ctx, repos, compiler.BuildOptions{CostLimit,
  AllowPlainSecrets, Charts})` is the compile of a tree as `okdp-compile`
  and okdp-gitops run it: the load, the options on each instance,
  `CompileTree`. A problem of the platform's parts (a repository,
  `platform.yaml`, `catalogs.yaml`, a package, a contract) is a
  `*compiler.LoadError`, `Problems` one per problem (a report of several
  lines one of them), the projects' among them; `LoadRepository`
  returns one for any problem, a project's too. A project that does not
  load is refused as a whole, not the tree: no instance of it, a
  `compiler.RefusedProject` in `Tree.Refused` (`Namespace`, `Problems`,
  `Errs`), kept by `Partial` like a project of a refused instance;
  `compiler.LoadErrorOf(tree)` is their problems as one `*LoadError`,
  nil when every project loads, and `compiler.AddedProblems(before,
  after)` the problems a change added.
  `compiler.StaticCharts(fsys)` is a chart cache filled already, which
  fetches nothing: the instances compiled once with it (`okdp-compile
  --check --charts`); package `chartcache` keeps one that pulls what it
  lacks from the charts' repositories, OCI or https (`chartcache.NewCache`,
  `NewRepositoryPuller`), the control plane's and okdp-gitops': an OCI
  chart layer verified against its manifest's digest before it is
  unpacked, a pull that fails leaving no directory of the chart; an
  https chart repository's archive and redirects https too, plain http
  only on the hosts given to `NewRepositoryPuller` (`InsecureHosts`),
  never from https, an OCI registry's redirects (a blob's) alike; every
  connection it opens (a registry, a token realm, a redirect, an index,
  an archive) refused at a private, loopback or link-local address, the
  address checked as dialed, but for the hosts of
  `RepositoryPuller.PrivateHosts` (host, `host:port` or `*.<domain>`: a
  registry of the cluster, `*.svc.cluster.local`) and of
  `InsecureHosts`, a proxy of the environment (`$HTTPS_PROXY`) dialed
  whatever its address, the destination of a request it serves resolved
  here first and refused at such an address likewise (a name this host
  cannot resolve is refused: the proxy resolves it again); its TLS
  connections trust the system's
  certificates, or the pool `RepositoryPuller.SetRootCAs` gives (a
  platform's CA beside the system's, before the first pull: a registry
  of the cluster signed by it, its certificate checked all the same);
  the knobs are the program's (the control plane's
  `INSECURE_OCI_REGISTRIES` reaches `InsecureHosts`, okdp-gitops'
  `GITOPS_PRIVATE_CHART_HOSTS` `PrivateHosts`), not the tree's, as one
  cache serves every tree; the archive's `Chart.yaml` the chart
  and version named; `Cache.File` reads one of `chartcache.Files` only, and
  `Cache.FS` follows no symbolic link and bounds what it reads, as
  `okdp-compile` reads `--charts`.
- `compiler.Partial(tree)` is what `--partial` writes: the results of
  the projects written and their graph, and the projects kept, each a
  `compiler.KeptProject` (`Namespace`; `Dropped`, why a project whose
  namespace is reserved now is dropped from the output, nothing of it
  among the files; `Problems`, `<namespace>/<instance>:
  <why>`; `Instances`, each a `compiler.KeptInstance`, its `Err`, and
  `Depends` for one that compiled but depends on a release of a project
  kept; `Unloaded`, a project that does not load, its `Problems` its load
  problems and no instance). `compiler.TreeFiles(tree, targets,
  previous)` is the output of a
  tree for one or more targets, path -> content: the files of the
  projects written, merged (a path two targets write differently fails
  it), the files of the projects kept as `previous`, the output written
  before (`fs.FS`, `nil` for none), holds them under
  `projects/<namespace>/`, but their platform's part, which the Flux and
  Argo CD targets write of today's settings (their manifests, a Flux
  HelmRelease's identity: `KeptProject.Rederived` lists them), the Flux
  target's `Keep` set for those whose `kustomization.yaml` it holds; and
  the projects kept, those of
  `compiler.Partial` and, for the Flux and Argo CD targets, those of a
  release name two projects of the tree share, its instances each a
  `KeptInstance` whose `Err` wraps a `compiler.ReleaseError`, and those
  that depend on them; a release of the kept output that a project
  written, or another kept output, has too fails it. Bytes only, the
  caller writes them, removing what it wrote before that they do not
  hold, in the kept projects' directories too. A caller that records the
  revision of the sources it compiled (okdp-gitops' `sources.yaml`)
  names a kept project's instances as of the last one it wrote whole:
  they are kept since then.
- `compiler.Regressions(before, after)` are the instances that deployed
  in a tree before a change and do not after;
  `compiler.ReleaseTaken(tree, namespace, name)` the instance of another
  namespace whose release an instance has too: what `treeedit`'s
  `Check.Verify` checks an edit with.
- The readers of an output: `compiler.ReadArgoRoot(raw)`, the
  `compiler.ArgoSource` (`RepoURL`, `Revision`, `HEAD` by default) of an
  Argo CD entry point, the first `Application` with a `spec.source`;
  `compiler.ReadPublished(output, namespace)`, what each compiled
  instance of a namespace publishes (`compiler.Published`: its release,
  its package, its `okdp.info.yaml`); `compiler.ReadCompiled(output,
  namespace, release)`, an instance's umbrella chart
  (`compiler.Compiled`: its `Chart.yaml` as written, its values),
  `fs.ErrNotExist` before it is compiled.
- Package `treeedit` reads and edits a tree for a caller that edits it
  (a control plane), which keeps the files where it likes (Git): every
  rule of an edit is the package's, the caller holds the files and
  commits them. A tree is a `treeedit.Reader` (`ReadFile`, an error
  wrapping `treeedit.ErrNotFound` for a missing file, `ReadDir`,
  `Exists`), a transaction a `treeedit.Writer` (`WriteFile`, `Remove`
  too); the paths are `ProjectDir`, `ProjectFilePath`, `InstancesDir`,
  `InstanceDir`, beside `platform.yaml` `CatalogsFile` and
  `ProjectsFile`, and `ValidateName` refuses a name `compiler.IsName`
  refuses. A name that is a dot segment is not found
  (`treeedit.ErrNotFound`), never joined into a path.
  - The readers: `ReadInstance`, `ListInstances` (a
    `treeedit.Instance`: `Project`, `Name`, `Service`, `Version`,
    `Claimers` as written, `Values`, its `Release`); `ListConnections`,
    `ReadConnection` (a `compiler.Connection`, of a project or of the
    platform for `""`, `ConnectionsFile`); `ListClaims` (each a
    `compiler.ClaimSpec`, listed even when the compile refuses it);
    `ReadProject`, `ListProjects(r, split)` (a `treeedit.Project`, its
    description; `split`, the projects each in a repository of their
    own, `ProjectsFile` lists them, and a project whose `project.yaml`
    cannot be read is listed all the same, `ReadErr`);
    `CheckProjectName(r, name)` (`compiler.CheckProjectName` with the
    platform's values of `r`, a `*treeedit.CompileError`),
    `CheckNewProject`; `ReadCatalogRepositories` (the `repositories` of
    `catalogs.yaml`, `CatalogRepositories`, each a `CatalogRepository`
    `{Name, URL, Ref}`, its ref `DefaultCatalogRef`, `main`, by default,
    the whole file read strictly, `ParseCatalogs`, a `Catalogs` of
    `Repositories` and `Categories`), `CheckCatalogs(doc, catalog)` (its
    menu's services those of a `compiler.Catalog`, as the compile checks
    it), `ReadCatalog` (the file, the console's menu),
    `ReadPlatformValues`; `DecodeProjects`
    (`projects.yaml`: `ProjectsList`, each a `ProjectEntry`, its url,
    branch or path of a repository). Of an output a target wrote, an
    `fs.FS`: `ListPublished(r, output, project)` (a
    `treeedit.Published`, the instances of the tree only) and
    `ReadCompiled(output, project, name)` (`treeedit.CompiledFiles`, an
    error wrapping `ErrNotFound` and `fs.ErrNotExist` before the compile).
  - The edits: `CreateInstance`, `UpdateInstance(project, name, change)`,
    `DeleteInstance`; `PutConnection(project, c, create)`,
    `DeleteConnection`; `PutClaim`, `DeleteClaim`; `CreateProject(p,
    split)`, `UpdateProject`, `DeleteProject(name, split)` (`split`:
    `projects.yaml` edited, the project's repository the caller's);
    `UpdateCatalog(target, change)` (a file `ParseCatalogs` refuses not
    written). Each refuses a name or an instance it cannot write at once
    and is a `treeedit.Edit`: its `Action` and
    `Target` name it (`deploy` `demo/web`, `create connection`
    `platform/pg`), and `Change(r)`, pure, is its change of the tree `r`
    holds, or an error wrapping `ErrNotFound` or `ErrExists`: a
    `treeedit.Change`, the `Files` to write or remove in order, the
    instance written (`Instance`) and its `Check`, nil when none is
    needed. An instance's files are written when their content changes
    only, a file's head, comments and other keys kept.
  - The check: `Check.Verify(before, after)`, the tree compiled before
    the change and after it (each a `treeedit.State`, `StateOf(tree,
    err)`, its tree or its `*compiler.LoadError`), is a
    `*treeedit.CompileError` when the change adds a problem to the
    tree: an instance it writes (`Subjects`) does not compile, one that
    compiled does not any more, the tree does not load and did, or with
    a problem it did not have (whatever the problems for an edit of an
    instance); a `*treeedit.ErrInUse` (`What`, `Users`) when it removes
    what an instance that compiled uses; a `*treeedit.ErrReleaseTaken`
    (`Release`, `Project`, `Instance`) when a new instance's release is
    another project's instance's. It gives the warnings of the
    instances written.
  - `treeedit.Apply(w, edit, compile)` runs an edit on a transaction:
    its change, the tree compiled before it is written and after it
    with `compile` (the caller's, of the tree a `Reader` holds; nil:
    unchecked), the verdict. On an error the caller drops the
    transaction.
  - The verdict without the write, for a form checked before it is
    sent: `treeedit.Validate(r, edit, compile)` is `Apply` of the edit
    on the tree in memory, `r` left as it is; a `*CompileError` its
    field errors (`[]compiler.FieldError`, nil when the edit passes),
    with the warnings, any other refusal its error (`ErrNotFound`,
    `*ErrInUse`, `*ErrReleaseTaken`). `ValidateInstance(r, compile,
    inst)` validates the write of an instance, its package and values,
    an update when its directory is there, else a creation: its
    parameters against its package's schema, its references resolved,
    its claims bound, each problem at its parameter path, the compile's
    rules only. `ValidateClaim(r, compile, project, claim)` validates a
    named claim as the project's instances bind it, at `claims.<name>`;
    a project without instance binds none of its claims, which the tree
    checks at their declaration all the same (class, request, name),
    their field errors at `claims.<name>` too.
- The files as written, `treeedit`'s too, for a caller that edits them
  itself: `ParseInstance` and `EncodeInstance` (`instance.yaml`, its
  `claimers` absent, none or listed, each kept apart), `EncodeValues`,
  `DecodeValues` (as the compile reads a values file, every number a
  `float64`), `MarshalYAML`, `RoundTrip`; `EditEntry` (an entry of
  `connections` or `claims`), `EditKey`, `WriteDescription` and
  `ReadDescription` (the `description` of `project.yaml`),
  `MergeDocument` (a document merged key by key): each in place, the
  file's head, comments and other keys kept.
- The compile's rules, for a program that answers before it compiles
  (a control plane), none of them written again:
  - `compiler.FieldErrors(err)` reads the error of a compile, an
    instance's (`TreeInstance.Err`) or a `*treeedit.CompileError`, as
    the problems of its fields, each a `compiler.FieldError`: its
    `Path` as the messages write it (`catalogs[1].storage`,
    `claims.analytics`), its `Pointer` (RFC 6901, `/catalogs/1/storage`)
    and the compile's `Message`. A violation of the parameter schema is
    at its place (`at '/replicas': got string, want integer`), a
    reference that does not resolve or a claim that does not bind at
    its parameter path, a named claim at `claims.<name>`; any other
    problem, and each of a tree that does not load, is the instance's,
    `Path` `""`.
  - `compiler.ConnectionRefs(schema)` lists the connection references
    of a parameter schema as the compile finds them, through
    `properties` and `items` (`compiler.ConnectionRef`: `Path`,
    `catalogs[].storage` for every item of a list, `Schema`, its place,
    `Contract`, `Provision`, `Request`, `Require`, `Required`, its
    `Property` whole); the compile's error when one cannot be read or
    is out of its reach ([declaring a
    reference](specs/01_inputs_outputs.md#declaring-a-reference)).
  - `compiler.ReadContract(contracts, name)` and `ReadContracts` read
    the contracts as a compile reads them, its error when one cannot
    be: a `compiler.ContractDescriptor`, its `Label`
    (`x-okdp-contract`), `Title`, `Description`, its `Fields` in the
    order of the file (`ContractField`: `Secret`, `Required`, `Default`,
    `Derived`, `Condition`, its `Property` as written), `Required`,
    its claim schema (`Claim`), the document (`Schema`) for the
    keywords the compile does not read (`x-okdp-icon`,
    `x-ui-widget`...).
  - `compiler.MergeValues(layers...)` merges values layers as the
    compile merges an instance's, Helm's `-f` (maps key by key, a list
    replaced, a null kept); `compiler.CoalesceValues(values, defaults)`
    coalesces values over a chart's defaults as Helm gives them to its
    templates (a null deletes a default).
  - `compiler.CheckConnection(contracts, project, c, platform)` checks
    a connection of a project (`""`, the platform's) at its
    declaration, as the compile of the tree checks every connection of
    a context: its contract, its fields, none secret, its required
    ones, its types, its `secretRef` against the store of the
    platform's values; the compile's error, at `connections.<name>`.
  - The names of the claims: `compiler.NamedClaimID(project, name)`,
    `<project>.<name>`, the output its provider publishes for it;
    `compiler.IsClaimID(name)`, a name of that form, DNS-1123 labels
    joined by dots (which outputs of a provider are its claims' is its
    compile's, `TreeInstance.Provided`); `compiler.CheckProvider(where,
    provider)`, the form a claim or a class names its provider by. An
    instance's release is `compiler.ReleaseName(project, instance)`.
- `compiler.CheckReleases(results)` is what the Flux and Argo CD targets
  refuse of the release names, their objects sharing one namespace: a
  `*compiler.ReleaseError` (`Release`, `Namespaces`) per release in two
  namespaces or more, which a control plane checks before it adds an
  instance (`a-b/c` and `a/b-c` are both `a-b-c`); their `Files` wrap
  it.
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
  suffix of the others; oidc-dcr 0.6.0 of `oci://quay.io/adaltas` is the
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
  limit: a compile can pass the limit by one expression's cost. A host
  function counts what it reads and what it builds: a call costs 1, a
  unit a byte of its argument, about its serialisation (a string's
  bytes, a map's keys and values, a list's elements), and the bytes of
  the string it returns (`sha256`, `toJson`, `toPrettyJson`, `toYaml`,
  `quote`, `str`, `first` and OKDP's own; `join` is CEL's): it converts
  its whole argument, which a map's entry count does not measure. A
  server compiling packages it does not review sets one, as an
  instance's values can make an expression iterate over them;
  okdp-gitops and `okdp-compile` set 1000000 (`--cost-limit`), about
  1 MB that host functions read or write; one expression costs what is
  left of the limit, not the limit again. `Inputs.Timeout` bounds one
  compile in time, beside the context's deadline: past it the instance
  is refused with its own message (`BuildOptions.InstanceTimeout` sets
  it on every instance of a tree, `--instance-timeout`), `Result.Cost`
  is the cost it used (counted under a limit only). A `$for` costs 1 an element
  and the size of what its body builds, its context checked at each.
  The example costs under 1000, the OAuth client's template most of it
  (`ExampleCompile_server`). Whatever the limit, a document's values
  are 32 MiB at most (about their bytes in memory, a value a list holds
  ten times counted ten times): past it, the compile fails with
  `the values the document builds exceed 32 MiB`.
- `Inputs.AllowPlainSecrets` accepts credentials in clear: a `provides`
  `secret` without `generate`, a plain Secret of its `stringData`, a
  Secret of the `objects` section with `data` or `stringData`, an
  ExternalSecret whose template holds a literal at a sensitive key, a
  Fake generator with values. Unset, they fail the compile, and a
  literal of the upstream values at a sensitive key is a warning: the
  objects file is written to the deployments repository, so credentials
  are generated in the cluster or read from the platform's secret store
  ([credentials Secrets](specs/01_inputs_outputs.md#credentials-secrets)).
- Errors are messages for the user, returned as they are; the package
  defines no error types, but `ErrNotInCatalog`, which a missing package
  wraps, `*LoadError`, the problems of a tree that does not load, and
  `*ReleaseError`, which the Flux and Argo CD targets wrap. A message carries its context: the file, the
  parameter path, the chart key, the mapping section (see
  [troubleshooting](#troubleshooting)).

## Integration

### CI

Compile every instance of the tree, the repositories of its parts
checked out side by side; any error fails the job:

```bash
bin/okdp-compile --repository platform --catalog catalog --repository projects \
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
instance). The mark of the output, `compiled/projects/.okdp-compile`, is
committed with them; a `compiled/` written by a version without it takes
`--force` once ([`--output`](#--output)).

### GitOps

A compile writes the instance as one Helm chart: the umbrella deploys
alone as one Helm release, `helm upgrade --install` ([rendering with
Helm](#rendering-with-helm)). OKDP publishes no chart of the service: the
upstream charts come from their own repositories, the umbrella's
dependencies. The targets write compiled instances for a deployer,
`okdp-compile --repository <dir> --target helm|flux|argocd`: the umbrella
chart of each (`Helm`), with its Flux HelmRelease, `dependsOn` the
releases it waits for, and the Kustomizations, the root's of the
namespaces among them (`Flux`), or its Argo CD Application and sync wave
(`ArgoCD`), from the [deployment
graph](specs/01_inputs_outputs.md#the-deployment-graph), in okdp-gitops'
format ([targets](specs/01_inputs_outputs.md#targets)); and, for each
project, its own manifests, `projects/<namespace>/project/`: its
Namespace, the platform's secret store in it, and the identity its
releases are installed with, confined to its namespace unless the project
is one of the platform's own ([the project's
manifests](specs/01_inputs_outputs.md#the-projects-manifests)). The files
of a target are all an engine needs of the compiled tree, a Flux
Kustomization of `projects/`, or an Argo CD Application that applies the
Applications of `projects/` and an ApplicationSet over the projects'
manifests.

[okdp-gitops](https://github.com/OKDP/okdp-gitops) compiles a whole
deployments repository with this library, every instance of it
(`LoadRepository`, `CompileAll`, `NewGraph`), and commits the files of
the Flux or Argo CD target as they are, under its output directory,
`compiled/`, writing none of its own: their `dependsOn` and sync waves
come from `Result.Dependencies`, the instances the connection references
point to, one item per parameter
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

**OKDP's own objects.** `Result.Objects`, the credentials Secrets, the
mapping's objects and the ExternalSecrets of the credentials read from
the platform's secret store, are `okdp.objects.yaml`, a
multi-document stream the umbrella renders as written, in the instance's
release ([the objects file](specs/01_inputs_outputs.md#the-objects-file),
[why](03_okdp_objects.md#how-it-reaches-the-cluster)): deployed with the
upstream charts, pruned with them, but those Helm keeps
(`helm.sh/resource-policy: keep`, a claim's database), which carry Argo
CD's equivalents too, `argocd.argoproj.io/sync-options:
Prune=false,Delete=false`: Argo CD renders with `helm template` and
reads no Helm annotation. A deployer that does not render the umbrella
applies the file as it is.

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
| `Inputs.Release "demo-Demo_App": a release name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 53 characters at most (Helm's limit)`, `Inputs.Namespace "<name>": a namespace name, ...`, `Inputs.Namespace "platform": the namespace is reserved: ...` | an instance's or a project's directory is not a name Kubernetes and Helm accept, or the release they make, `<project>-<instance>`, is longer than 53 characters; a project named `platform`, the segment of the platform's keys of the secret store | rename it ([repository layout](#repository-layout), [inputs](specs/01_inputs_outputs.md)) |
| `app: stat charts.example.org/app-1.8.0/app: not a directory` | `--charts`, with `--check`, names a file: it fails at the first chart read from it | fix the path |
| `unexpected argument "b": every input is a flag (--repository before each directory)` | an argument that is not a flag, here a second directory after one `--repository`; exit status 2, nothing compiled | one `--repository` per directory |
| `--output (-o) is required` | no output directory | give one: the command writes the tree there |
| `db/projects/demo/instances/web/instance.yaml: the package db/catalog/web/1.0.0: open Chart.yaml: no such file or directory` | a version directory of the catalog without its `Chart.yaml` | add it ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: apiVersion "v1": v2, the only one the compiler reads`, `Chart.yaml: name is required`, `Chart.yaml: version is required`, `Chart.yaml: type "library": a package is an application chart` | the package metadata is incomplete, or not that of an application chart of `apiVersion: v2` | set them ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: version "1.0": SemVer (1.2.3), the version of the umbrella chart` | a package version Helm does not accept as a chart's | a SemVer `version` in `Chart.yaml` |
| `Chart.yaml: yaml: unmarshal errors:` then `line 9: field condtion not found in type chart.Dependency`; `Chart.yaml: one YAML document` | a field Helm's `Chart.yaml` does not define, here a misspelt `condition` in a dependency, one line per unknown field; a second YAML document | fix the name; one document ([Chart.yaml](specs/02_service_definition.md#chartyaml)) |
| `Chart.yaml: dependencies[1] (api): condition: the values decide what is rendered ($if on api in values.cel.yaml)` | a dependency with `condition`, `tags`, `enabled` or `import-values` | remove it; leave the dependency out with `$if` on its key in the values file ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0] (my_app): key "my_app": a DNS label (lowercase letters, digits and -, a letter or digit at each end, 63 characters at most): the key names the umbrella's dependency alias` | a dependency key (its `alias`, else its `name`) that is not a DNS label: `../app`, an upper case letter, `_` or `.` | give it an `alias` that is one, and rename the key in `okdp.yaml` and the values file ([chart keys](specs/02_service_definition.md#chart-keys)) |
| `Chart.yaml: dependencies[2] (api): the key of dependencies[1] too: give one of them an alias` | two dependencies of one key: a chart rendered twice, one of them without its alias | an alias each ([recipe](02_writing_package.md#render-a-chart-twice)) |
| `Chart.yaml: dependencies[0].name is required` | a dependency without its chart's name | name the published chart that renders it ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0].repository "charts.example.org": the URL of a chart repository (https://... or oci://...)`, `Chart.yaml: dependencies[0].name "a/b": the name of a chart of the repository` | a repository that is not a URL (`https://`, `oci://`) or whose host and path are no directory of the chart cache (`https://`, `https://x/../y`), a name that is not a chart's name | fix the dependency ([dependencies](specs/02_service_definition.md#dependencies)) |
| `Chart.yaml: dependencies[0].repository "http://charts.example.org": http://, the chart fetched in clear: the URL of a chart repository (https://... or oci://...)`, `global.okdp.charts.oidcDcr.repository "http://...": ...` | a dependency, or a platform chart, of an `http://` repository: the chart would be fetched in clear, by a check and by the deployer | an `https://` or `oci://` repository: a mirror, with TLS ([dependencies](specs/02_service_definition.md#dependencies), [platform charts](specs/01_inputs_outputs.md#platform-charts)) |
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
| `context layer 2 (level 1): global.okdp.charts: a platform setting, level 0 only (Inputs.Context): a project sets global.okdp.provisioning, nothing else of global.okdp` | a context file above level 0, `project.yaml`, sets a key of `global.okdp` other than `provisioning`, one the compiler reads or not, one the platform sets or leaves unset (a key it sets fails before, `is set at level 0`), or `global.okdp` to a non-map | set it in `platform.yaml`, the platform's; a project's own settings, which the mappings read, go beside `okdp`: `global.project` ([levels](specs/01_inputs_outputs.md#levels)) |
| `values layer 1: global.okdp is set by the context (Inputs.Context)`, `values layer 2: global is set by the context ...` | an instance file, the first, second... values layer (`Inputs.Values[0]`, `[1]`...), its `config.yaml` first, sets `global`: a platform value, a key of `global` the parameter schema does not declare, or `global` itself to a non-map | give platform values in `platform.yaml`; a package lets instances set a key of `global` by declaring it in `config.schema.json` ([context values](specs/01_inputs_outputs.md#context-values)) |
| `config.schema.json: global.okdp is the platform's settings: a package schema declares nothing under it`, `... a package schema gives it no default (properties.global.default)` | the package's parameter schema declares `properties.global.properties.okdp`, or its `properties.global.default` has the key `okdp`: a default of the platform's settings | remove it: the platform's settings are the context's, `platform.yaml` ([defaults](specs/01_inputs_outputs.md#defaults)) |
| `values layer 1: connections are given by the context (Inputs.Context): an instance's own in a context layer of its level` | an instance's `config.yaml` sets `connections`, to any value | give the instance's own connections in its `connections.yaml`, a context file of its level; a connection of a lower level is not changed ([context values](specs/01_inputs_outputs.md#context-values)) |
| `values layer 1: database: the value contains "{{"`, `values layer 2: upstream.app.podAnnotations.{{ x }}: the key contains "{{"` | template code in an instance's `config.yaml`, in a key or a value under any key: parameters, upstream values; `context layer <n> (level <l>): ...` in a context file above level 0 (`project.yaml`, `connections.yaml`); the platform's files, level 0, are not checked | remove it ([layers](specs/01_inputs_outputs.md#layers)) |
| `values layer 2: json: cannot marshal from Go func() within "/hooks/0"`, `context layer 1 (level 0): json: cannot marshal from Go float64 within "/global/proxy/no.proxy": unsupported value: NaN` | a values layer built in Go, the second of `Inputs.Values`, the first of `Inputs.Context`, holds what a values file cannot: a func, a chan, a complex, a NaN or an infinity, text that is not UTF-8, a key that is not text or a number, two keys that read as the same, a value that contains itself | build the layer of values a file can hold ([values files](specs/01_inputs_outputs.md#values-files)) |
| `values don't meet the specifications of the schema(s) in the following chart(s):` then `<service>:` and `- at '/replicas': minimum: got 0, want 1` | the parameters violate the package's `config.schema.json`; every violation is listed | fix the instance values ([parameters](specs/01_inputs_outputs.md#parameters)) |
| `compiled values don't meet the specifications of the schema(s) in the following chart(s):` then `<key> (<chart>-<version>):` (`app (app-1.8.0):`) and `- at '/replicaCount': maximum: got 3, want 2` | with `--check --charts`, the final values, over the chart's defaults, violate the `values.schema.json` of the upstream chart | fix the instance's upstream values; if they are not involved, OKDP's defaults or the mapping |
| `warning: upstream.app.env: appended to the package's list, not to the default list of app-1.8.0, which Helm replaces: the final list lacks 1 of its 2 items; ...` | with `--check --charts`, the instance appends to a list (`append`) the chart's defaults hold items of that the package's list leaves out: Helm replaces the chart's list with the final one | restate the items to keep in the package's values of the key, its defaults or values file ([append](specs/01_inputs_outputs.md#append)) |
| `web: objects: ClusterRole/demo-web-reader: a cluster-scoped kind (rbac.authorization.k8s.io/v1): the platform's projects install cluster-scoped objects (global.okdp.policy.privileged), demo is not one of them` | the package writes an object of a cluster-scoped kind (its `objects` section, `<kind>/<name>`), and the instance's project is not one of the platform's own | a namespaced object; for the platform's own projects (cert-manager, an operator...), list the namespace in `global.okdp.policy.privileged` of `platform.yaml` ([objects section](specs/01_inputs_outputs.md#objects-section)) |
| `web: objects: Secret/token: namespace kube-system, not the instance's (demo): the platform's projects write other namespaces (global.okdp.policy.privileged), demo is not one of them` | an object of the package, or a generated Secret (`okdpGeneratedSecret`'s `namespace`), names another namespace than the instance's | the instance's namespace (`release.namespace`), or leave `namespace` out; the platform's own projects write other namespaces ([objects section](specs/01_inputs_outputs.md#objects-section)) |
| `web: output "store": secret: a plain value (stringData): credentials are generated (generate) or read from the platform's secret store; Inputs.AllowPlainSecrets allows it` | an output of `provides` has a `secret` of `stringData` alone: its credentials would be written in clear to the deployments repository | `generate` the secret keys, `stringData` for the others; or point `secretRef` at a Secret an operator, the chart or an ExternalSecret writes; a caller whose repository is private may set `Inputs.AllowPlainSecrets` ([credentials Secrets](specs/01_inputs_outputs.md#credentials-secrets)) |
| `web: objects: Secret/demo-web-token: a plain value (stringData): credentials are generated (okdpGeneratedSecret) or read from the platform's secret store (an ExternalSecret); Inputs.AllowPlainSecrets allows it` | a Secret of the package's `objects` holds values, `data` or `stringData` | `okdpGeneratedSecret`, or an ExternalSecret of the platform's secret store; a caller whose repository is private may set `Inputs.AllowPlainSecrets` ([objects section](specs/01_inputs_outputs.md#objects-section)) |
| `web: objects: ExternalSecret/demo-web: a plain value (spec.target.template.data.password): a literal at a sensitive key, ...`, `web: objects: Fake/demo-web: a plain value (spec.data): a Fake generator gives the values it holds; ...` | an ExternalSecret's template holds a literal at a sensitive key (an `okdpGeneratedSecret` `stringData`'s too), a Fake generator values | a template of what the store or a generator gives (`'{{ .password }}'`), a generated key; the static keys of `stringData` are not credentials ([objects section](specs/01_inputs_outputs.md#objects-section)) |
| `warning: upstream.app.auth.password: a literal at a sensitive key, a credential in clear in the values and the deployments repository: ...` | an instance's upstream value at a key named as a credential is a literal | generate it or read it from the store, the chart given the Secret's name (its `existingSecret`); a key that only names one is a false positive ([objects section](specs/01_inputs_outputs.md#objects-section)) |
| `database: connection "store" has contract "s3", "database" is expected` | the parameter references a `connections` entry of another contract | reference a connection of the expected contract |
| `database: connection "shared-db": field "user" is not part of the database contract` | an external connection has a field its contract does not declare | remove it ([connections](specs/01_inputs_outputs.md#connections)) |
| `database: connection "shared-db": "password" is a secret field, put it in the credentials Secret` | a secret field written in the connection | put it in a Secret named by `secretRef` |
| `database: connection "shared-db": secretRef.remoteKey: its credentials are in the platform's secret store, global.okdp.secrets.store, which is not set` | a connection whose credentials are in the platform's secret store (`secretRef: {remoteKey: <key>}`), and the platform names no store | set `global.okdp.secrets.store` in `platform.yaml`, or give the Secret of the instance's namespace, `secretRef: {name: <secret>}` ([credentials in the platform's secret store](specs/01_inputs_outputs.md#credentials-in-the-platforms-secret-store)) |
| `database: connection "shared-db": secretRef: name or remoteKey, not both`, `... secretRef.remoteKey: the key of the credentials in the platform's secret store, a non-empty string, got 5`, `... secretRef.remoteKey: the connection's name is not a DNS-1123 label: ...`, `... secretRef.remoteKey: the queue contract has no secret field (x-okdp-secret): ...`, `... secretRef.remoteKey "demo//shared-db": a path of the platform's secret store, segments separated by "/", none empty, "." or ".."` | a `secretRef` with both a Secret and a key of the store; a key that is not a string, or empty; a connection named so that its Secret, `<release>.<name>-credentials`, cannot be; a contract without credentials; a key that is no path of the store | one of them; the key, a string; rename the connection; drop `secretRef`; a path, `<segment>/<segment>` |
| `storage: connection "datalake": secretRef.remoteKey "platform/datalake": no namespace reads platform/ when the platform gives a secret store provider (...)` | a connection whose key is under `platform/` while `global.okdp.secrets.provider` is set (a SecretStore per namespace, whose policy reads no `platform/`) (`testdata/cli/secrets.txtar`) | copy the platform's credentials under the key of each namespace that uses them, `<namespace>/<key>`, and name it in a connection of the project (`project.yaml`, level 1) ([credentials in the platform's secret store](specs/01_inputs_outputs.md#credentials-in-the-platforms-secret-store)) |
| `storage: connection "stolen": secretRef.remoteKey "finance/finance.ledger.database": a key of the platform's secret store a project may not read: a connection above level 0 (level 2) names a key under "demo/", its namespace; the platform's keys are read through the platform's connections (level 0)` | a connection of `project.yaml` (level 1) or `connections.yaml` (level 2) keeps its credentials in the platform's secret store under a key outside its namespace's segment: another namespace's claim, the platform's keys (`testdata/cli/secrets.txtar`) | a key under `<project>/`, which the namespace's PushSecrets write and an administrator of the store fills; reference the platform's connection for the platform's keys ([credentials in the platform's secret store](specs/01_inputs_outputs.md#credentials-in-the-platforms-secret-store)) |
| `database (connection "shared-db"): field "dbName" is required by the database contract` | a required contract field is missing; one line per missing field | set it |
| `database (connection "shared-db"): the fields violate the database contract:` then `- at '/engine': value must be one of 'postgresql', 'mysql'`, `- at '/port': got string, want integer` | a field of an external connection the contract's schema refuses; one line per violation | give a value of the contract's type (`port: 5432`, not `"5432"`) and among its values ([checks](specs/01_inputs_outputs.md#checks)) |
| `<field>: "<name>" is not a connection, nor an output of an instance of the namespace` | the name is not in `connections`, nor an output of that contract another instance of the namespace publishes (`(demo-pg publishes it with contract "database", "s3" is expected)`: one of another contract) | add the connection under `connections` of a context file (`connections.yaml` for the instance's own), or the instance that publishes the output to the tree ([resolution](specs/01_inputs_outputs.md#resolution)) |
| `<field>: "<name>" names s3 outputs of several instances of the namespace: demo-a, demo-b` | two instances of the namespace publish an output of that name and contract | rename one of the outputs, or reference a connection |
| `<field>: "<name>" is not a connection, nor a release name: lowercase letters, digits and "-", a letter or digit at each end, 53 characters at most` | the name is not in `connections`, and cannot name an instance of the namespace (`Shared_DB`, a name longer than 53 characters) | fix the name, or add the connection under `connections` of a context file |
| `database: a connection reference must be a string, got map[string]interface {}{"host":"db.example.org"}` | a connection reference given as a map, a number or a list (the parameter schema does not say `"type": "string"`) | give the name of a connection or an instance; put the connection itself under `connections` |
| `database (connection "shared-db"): field "host": the value contains "{{"` | template code in a connection given by the platform's values, or in a contract default (a context file above level 0 fails earlier, `context layer <n> (level <l>): ...`) | fix the platform values or the contract |
| `database (claim "demo.app.database" of data/data-postgresql): the connection does not meet the package's requirement (x-okdp-connection-ref.require); data/data-postgresql is the provider of the default class standard of database-server connections (global.okdp.provisioning.database-server.default): name a class whose provider meets it (class), or change the default:` then `- at '/engine': value must be 'postgresql'`; `database (connection "shared-db"): the connection does not meet ...:` then `- at '': missing property 'secretRef'` | the connection, external, an output or what a claim's provider publishes, does not meet what the package requires of it (`require` of its `x-okdp-connection-ref`); one line per violation | reference another connection, or fix it (add its `secretRef`); for a claim, name a class whose provider meets it (`{class: <name>}`), a project's own if need be, or change the default in `platform.yaml` ([what a reference requires](specs/01_inputs_outputs.md#what-a-reference-requires)) |
| `database: x-okdp-connection-ref: require: field "major" is not part of the database contract`, `... require: "password" is a secret field, in the credentials Secret, never in the connection: require secretRef` | the package's requirement names a field the connection cannot have | fix the package's `config.schema.json`: require `secretRef` for credentials |
| `config.schema.json: properties.database: x-okdp-connection-ref: require: a JSON Schema, an object, got true`, `... require: "file:///require.schema.json#" is not valid against metaschema: ...` | the package's requirement is no JSON Schema, whether the instance sets the reference or not | fix the package's `config.schema.json` |
| several lines of the connection messages above, one `<field>` each | several references do not resolve: every one is reported, in parameter path order (keys sorted at each level), with its first error (the fields of a connection in alphabetical order), or all its missing required fields, or all its schema violations | fix each, then compile again: a fixed field may reveal the next one ([connection errors](specs/01_inputs_outputs.md#connection-errors)) |
| `app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed` | the instance's upstream values set a path `okdp.yaml` protects (`upstream.<key>.protect`, and always `global`) | remove the value; set it through the parameters when the package offers one |
| `app: upstream.app.podSecurityContext: podSecurityContext is denied to the instance's upstream values (a pod's privileges, identity, containers or objects): the platform's projects set it (global.okdp.policy.privileged), demo is not one of them`; `... runAsUser 0, root, is denied ...`; `app: upstream.app.extraVolumes, read as YAML: [0].hostPath: ...` | the upstream values of an instance of a project that is not privileged set, at any depth, a key of a pod's privileges, identity, containers or objects, or one of `global.okdp.policy.upstream.deny` (`(global.okdp.policy.upstream.deny)`), whatever the package's `protect`; in a string that reads as YAML too | remove the value; set it through the parameters when the package offers one; for the platform's own projects, list the namespace in `global.okdp.policy.privileged` of `platform.yaml` ([upstream policy](specs/01_inputs_outputs.md#upstream-policy)) |
| `app: upstream.app.image.repository: image docker.io/evil/app is not from a registry of global.okdp.policy.upstream.registries (registry.example.org): the platform's projects pull from any (global.okdp.policy.privileged), demo is not one of them` | with the platform's registries, an image of the upstream values (an `image` string, a `registry` and a `repository` with the computed ones, a `null`) comes from none of them | pull the image through one of them (the platform's mirror), or add the registry to `global.okdp.policy.upstream.registries` ([upstream policy](specs/01_inputs_outputs.md#upstream-policy)) |
| `app: upstream.app.podAnnotations.x: the value contains "{{"`, `... the key contains "{{"` | template code in upstream values the platform's values or the package's defaults give (in an instance's, its values file fails before: `values layer <n>: ...`) | remove it |
| `app: upstream.app.env must be a list` | a path of `append` given as a non-list | give a list |
| `context values (Inputs.Context) don't meet the specifications of the schema of global.okdp:` then `- at '/global/okdp/oidc': got boolean, want object`, `- at '/global/okdp/oidc/clientProvisioning': value must be one of 'existing', 'dcr'` | a platform setting the compiler reads, or a map on the way to it, of another type (`oidc: false`, `proxy: <url>`, a list for a string), or not among its values; it would otherwise read as not set. Every violation is listed | fix the platform values against `platform.schema.json`; `null` is not set ([fields the compiler reads](specs/01_inputs_outputs.md#fields-the-compiler-reads)) |
| `context values (Inputs.Context) don't meet ...` then `- at '/global/okdp/certificateIssuers': false schema` | the platform values set the former `certificateIssuers` | rename `certificateIssuers.selfSigned.name` to `tls.clusterIssuer`, the ClusterIssuer of the services' certificates; set `tls.caBundleSecret` to the Secret of the platform's CA bundle, if it has one ([fields the compiler reads](specs/01_inputs_outputs.md#fields-the-compiler-reads)) |
| `context values (Inputs.Context) don't meet ...` then `- at '/global/okdp/serviceCatalog': false schema` | the platform values hold the former console menu | move its `categories` to `catalogs.yaml`, beside `platform.yaml` ([repository layout](#repository-layout)) |
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
| `oidcClients[0] (console): secret "console-creds": the Secret the client is written to is the release's, "demo-webapp-console" (<release>-<client name>, <release>-oidc for the main client)` | a client's `secret` names a Secret of the namespace that is not the release's, exactly: its Job's Role would write another instance's Secret | name it after the release and the client, `${{ release.name }}-console`, or drop `secret` for the default, `oidcClientSecret(<name>)` |
| `oidc-dcr: chart oidc-dcr-0.6.0 is not in the chart cache: helm pull oci://quay.io/adaltas/oidc-dcr --version 0.6.0 --untar -d <chart cache>/quay.io/adaltas/oidc-dcr-0.6.0` | with `--check --charts`, `dcr` mode, at least one client, and the chart cache lacks the platform's oidc-dcr chart (`existing` mode does not need it) | pull it into the chart cache ([platform charts](specs/01_inputs_outputs.md#platform-charts)) |

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
| `charts/projects: a catalog repository holds catalog/ and contracts/ only: projects is the deployment's (a repository of role tree)` (`charts/platform.yaml`) | a `--catalog` (`compiler.RoleCatalog`) holds a part of the deployment, `projects/` or `platform.yaml` | move it to a repository of the tree (`--repository`), or give the directory as one if it is |
| `repository db: no role (Repository.Role): tree, a part of the deployment, or catalog, packages and contracts only` | a library caller's `compiler.Repository` without its `Role` | set `compiler.RoleTree` or `compiler.RoleCatalog` |
| `db/projects/demo/instances/web/connections.yaml: global: the instance's own connections only (connections): ...` | an instance's `connections.yaml` sets more than `connections` | set it in `project.yaml`, or `platform.yaml` ([repository layout](#repository-layout)) |
| `db/projects/demo/project.yaml: description: a string, the project's description` | a project's description that is a map, a list or a number | write it as text ([repository layout](#repository-layout)) |
| `contract s3 differs between the repositories a and b` | two repositories ship different contracts of one name | make them the same, or rename one |
| `db/projects/demo/instances/web/instance.yaml: service "web" is in the catalog of no repository`, `version "2.0.0" of service "web" is not in db/catalog/web` | the instance names a package no repository has | add it to a catalog, or fix `instance.yaml` |
| `error: demo/demo-web: database: "demo-db" is not a connection, nor an output of an instance of the namespace, ... (demo-db does not compile either: what it publishes is unknown)` | the output is published by an instance that is refused, or by none; instances that reference each other's outputs are both refused so | fix that instance; make one of two references a connection file's: it waits for nothing |
| `error: demo/demo-p: provides "x" (server): demo-q publishes an output of that contract and name too, ...` | two instances of the namespace publish one output | name one of them otherwise: a reference must find one |
| `error: demo/demo-app: Secret "demo-app-x.yy-credentials" of namespace demo is written by ExternalSecret demo-app-x.yy-credentials of demo-app and ExternalSecret demo-app-x.yy-credentials of demo-app-x: two objects may not write one Secret` | two objects of the namespace, of one instance or two, write one Secret: here a claimer's copy of a provider's credentials, `<release>-<secretRef.name>`, and another instance's connection, `<release>.<connection>-credentials` (`testdata/cli/secrets.txtar`) | rename one: the provider's `secretRef.name`, an instance, a connection or an object |
| `error: demo/demo-app: db: claim (database-server): it names no class, and the context sets no default one, global.okdp.provisioning.database-server.default (Inputs.Context): the classes are fast, standard` | a claim that names neither a class nor a provider, and neither `platform.yaml` nor the project's `project.yaml` sets a default class; `no class of database-server connections is defined (...)` when none is; `claims.<name>: ...` for a named claim, every instance of its project refused | set one, `global: {okdp: {provisioning: {database-server: {default: standard, classes: {standard: {provider: data/pg}}}}}}`, or name a class: `db: {class: fast}` ([classes](specs/01_inputs_outputs.md#classes)) |
| `error: demo/demo-app: db: claim (database-server): class "nvme" is not a class of database-server connections (global.okdp.provisioning.database-server.classes): fast, standard` (`the default class "nvme" (...default) is not a class ...`) | the claim, or the default, names a class the context does not define | name one of those, or define it |
| `error: demo/demo-app: db: claim (database-server): the default class "standard": data/pg is no instance that provisions database-server connections` (`class "fast": ...`, `data/pg is no instance ...` for a provider the claim names) | the class's provider names no instance of its namespace, by name or release, or one whose package does not provision the contract | fix the class's provider, `<project>/<instance>` or `<project>/<release>`, or add the instance |
| `error: demo/demo-app: db: claim (database-server): demo/demo-a names more than one instance, demo-a (its release) and demo-demo-a (its name): the claim would not know which` (`class "fast": ...`) | the provider, the claim's or its class's, is the release of the instance `a` and the name of the instance `demo-a`, both of the project `demo` | name the one meant otherwise, `demo/a` or `demo/demo-demo-a`, or rename one of them ([providers](specs/01_inputs_outputs.md#providers)) |
| `error: demo/demo-app: db: claim: request.size: set by the class large (global.okdp.provisioning.database-server.classes.large): remove it, or name another class` | the claim requests a key its class fixes, in its `request` or its package's | remove it, or name a class that does not fix it |
| `error: demo/demo-app: db: claim: class or provider, not both` | the claim names both | name one |
| `error: demo/demo-app: db: claim "demo.app.db" (database-server): of data/data-pg, the provider of class "large", another namespace: demo is not one of its claimers (claimers of instance data/data-pg: none, its package's): ...` (`of data/data-pg, another namespace` for a provider named, `claims.<name>: ...` for a named claim) | the provider, of another project, is named by the claim or by a class of the project's, and does not consent: its `instance.yaml`'s `claimers`, else its package's (`provisions`), do not list the project | list the project in the provider's `claimers`, `claimers: [demo]` in its `instance.yaml`, or have the platform add a class of its own in `platform.yaml` and name it ([providers](specs/01_inputs_outputs.md#providers)) |
| `db/projects/demo/instances/web/instance.yaml: yaml: unmarshal errors:` then `line 3: field replicas not found: service, version and claimers only` | an `instance.yaml` key it does not define (here values written in it, which belong in `config.yaml`), a key twice, a `service` or `version` that is no string, `claimers` no list of names; one line per problem, no Go type | `instance.yaml` holds `service`, `version` and `claimers` only; the values are `config.yaml`'s |
| `db/projects/data/instances/pg/instance.yaml: claimers[0]: "Demo" is not a namespace name, nor "*"` (`claimers: demo is listed twice`) | an instance's claimer that is not a project's name nor `*`, or listed twice | list each project once, by its name, or `"*"` |
| `context layer 2 (level 1): global.okdp.provisioning.database-server.default is set at level 0 (Inputs.Context): ...` (`...classes.standard is set at level 0`) | a project changes the platform's default class, or one of its classes | add a class of the project's own and name it ([levels](specs/01_inputs_outputs.md#levels)) |
| `error: demo/demo-b: db: claim "reports" has contract "s3", "database-server" is expected` | the reference names a named claim of the project of another contract | name a claim of its contract, or declare one ([claims](specs/01_inputs_outputs.md#claims)) |
| `error: demo/demo-b: db: "reports" is a claim: the reference takes none (x-okdp-connection-ref's provision)` | the reference names a named claim, and its package does not let it take claims | name a connection or an output |
| `error: demo/demo-app: database: claim (database-server): the request does not meet the contract's claim schema (x-okdp-claim):` then `- at '/size': '5G' does not match pattern '^[0-9]+(Mi|Gi)$'` | what the claim requests (its `request`, merged over the package's, or a named claim's declaration's), its claim schema's defaults applied, is not what the contract's claim schema (`x-okdp-claim`) allows; one line per violation | request what it allows ([requests](specs/01_inputs_outputs.md#requests)) |
| `error: demo/demo-app: database: claim (s3): request: the contract declares no claim schema (x-okdp-claim): a claim requests nothing of it` | a claim requests something of a contract that declares no claim schema | remove the request, or add the claim schema to the contract |
| `db/platform.yaml: claims: a project's (project.yaml)` | the platform's values declare named claims | declare them in the `project.yaml` of the project whose instances reference them |
| `error: demo/demo-b: claims.reports: claim: unknown key "share": contract, class, provider and request only` (`contract is required, ...`, `a connection of that name too (connections.reports)`) | a named claim of `project.yaml` not of the form, every instance of the project refused | fix the declaration ([claims](specs/01_inputs_outputs.md#claims)) |
| `error: data/data-pg: claim "demo.app.db" (database-server): claimed of namespace demo: its credentials go through the platform's secret store, global.okdp.secrets.store, which is not set` | a claim across namespaces, its credentials in the provider's namespace | set the platform's secret store, `global.okdp.secrets.store`, with its `provider` for a SecretStore of each namespace ([credentials across namespaces](specs/01_inputs_outputs.md#credentials-across-namespaces), [what the store must enforce](specs/01_inputs_outputs.md#what-the-store-must-enforce)) |
| `error: platform/platform-x: Inputs.Namespace "platform": the namespace is reserved: platform/ holds the platform's keys of the secret store, which no project writes` | a project named `platform`: its namespace would own the platform's keys of the secret store | rename the project |
| `error: kube-system/kube-system-x: Inputs.Namespace "kube-system": the namespace is reserved: kube-* is Kubernetes' own ...`, `error: argocd/argocd-x: Inputs.Namespace "argocd": the namespace is reserved: a deployer engine's, or the namespace its target writes in (argocd, flux-system, okdp-releases): ...` | a project of a namespace the compiler reserves: Kubernetes' own, or a deployer engine's, whose objects its releases would rewrite with every verb of the namespace | rename the project; a component of the platform that is not one of its projects is installed outside the tree |
| `error: openbao/openbao-x: Inputs.Namespace "openbao": the namespace is reserved by the platform (global.okdp.policy.reserved): no project has it` (`, privileged or not: ...`) | a project of a namespace `platform.yaml` reserves, `global.okdp.policy.reserved`, even one `policy.privileged` lists | rename the project, or remove the namespace from `policy.reserved` |
| `project "gitops": the namespace is reserved: it holds the GitRepository of the tree (Flux.Source), no project's` | `--target flux` with `--flux-source` of the namespace of a project | rename the project, or keep the GitRepository in a namespace of no project |
