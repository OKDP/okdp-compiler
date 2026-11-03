# Threat model

Who writes what in an OKDP tree, what each author may reach, and what
the compiler refuses of each, as it enforces it now. The [security
boundaries](00_architecture.md#security-boundaries) list the checks by
mechanism; this document lists them by author. Each refusal names the
code that enforces it, `<file>: <function>`, and the fixture that proves
it, a case that must fail: `testdata/<case>.yaml` and
`testdata/context/<level>/<case>.yaml`, which `TestExampleErrors`
compiles on the example `02_advanced`, and the scripts of
`testdata/cli/`, which `TestScript` runs
([failure cases](00_architecture.md#failure-cases)); a unit test where
no fixture is. [What the compiler cannot
enforce](#what-the-compiler-cannot-enforce) closes it.

## What is at stake

- The cluster: an object of a cluster-scoped kind (a ClusterRole, a
  webhook, a CustomResourceDefinition), an object of another namespace,
  a privileged pod, a pod of another identity.
- The credentials: the Secrets of a namespace; the keys of the
  platform's secret store, the platform's under `platform/` and those of
  the claims that cross namespaces, under `<provider namespace>/<claim
  ID>`.
- The platform's settings, `global.okdp`: where the OAuth clients
  register and the chart that registers them, the ingress and TLS
  settings, the proxy, the secret store, the privileged projects and the
  upstream policy.
- The deployments repository: what is committed under its output,
  `compiled/`, the engines apply as it is.
- The runner of a compile: what it reads, how long it runs, what it
  writes and prints.

## Authority comes from the file

The compiler does not know who wrote a file: it trusts a file for its
place in the tree ([repository layout](01_usage.md#repository-layout)).
`platform.yaml` is level 0, the platform's; `project.yaml`, level 1, a
project's; an instance's `connections.yaml`, level 2, and its
`config.yaml`, the instance values. Who writes each is decided by the
hosting of the repositories: their write access and their reviews (a
platform repository its administrators write, a project's its team, a
catalog its maintainers). Through the library, the caller decides which
layers are level 0 (`Inputs.Context`).

| Author | Writes | Reviewed | The compiler takes it as |
|---|---|---|---|
| [The platform](#the-platform-author-level-0) | `platform.yaml`, level 0 | with the platform | the platform's: every value, template code included |
| [A project](#a-project-author-level-1) | `projects/<project>/project.yaml`, level 1, and its instances' directories | by the project's owners | additions to the platform's, under rules |
| [An instance](#an-instance-author-level-2-and-configyaml) | `config.yaml`, `connections.yaml` (level 2), `instance.yaml` | a console user or a pull request | input |
| [A catalog](#a-catalog-author) | `catalog/<service>/<version>/`, `contracts/` | in the catalog's repository | reviewed, but for the rules below |
| [The engines](#the-engines) | apply `compiled/` | | the identity the targets give each project |
| [The secret store](#the-secret-store) | keeps the keys | | the keys the compiler writes |
| [okdp-gitops](#okdp-gitops-and-the-runner) | runs the compile, commits `compiled/` | | a caller |

## The platform author (level 0)

`platform.yaml` is the only layer the compiler takes as reviewed. It is
the only level whose values may hold template code (`{{`, which some
charts pass to `tpl`), but in a connection and in upstream values, which
are checked whatever their layer; the only one that sets `global.okdp`
but `provisioning`: the ingress, TLS, the OAuth clients and their chart
(`charts`), the proxy, the secret store (`secrets`) and the policy
(`policy`: the privileged projects, the upstream deny-list and
registries). Its connections name any key of the store, the platform's
own under `platform/`, and its classes route every namespace to their
providers, consenting for them.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may | set any key of `global.okdp`, template code in its values | not checked | |
| may | name the platform's own projects, `policy.privileged` | `policy.go: privileged` | `testdata/cli/policy.txtar` (a privileged project's ClusterRole written) |
| may | give a connection of any key of the store, `platform/<key>` | `connections.go: storeSecret` | `testdata/cli/secrets.txtar` (`platform/datalake`) |
| may | route any namespace to a provider of any namespace (a class) | `namespace.go: tree.consent` | `testdata/cli/claims.txtar` |
| may not | a platform chart (`charts`) over `http://`, or of a version range | `chart/ref.go: Ref.Check` | `testdata/context/0/http-chart-repository.yaml` |
| may not | `{{` in a connection or in upstream values | `connections.go: resolver.resolve`, `chart/merge.go: applyUpstream` (`chart.NoTemplate`) | `TestResolveTemplate` |
| may not | upstream values the policy denies, for a project that is not privileged | `policy.go: upstreamCheck` | `testdata/context/0/unlisted-registry.yaml` |

## A project author (level 1)

A project owns its namespace: `project.yaml` adds to the platform's
values for its instances, and its instances' directories are its own.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may | settings under `global` beside `okdp` (`global.project`, `storageClass`), parameters and upstream values for its instances, under an instance's rules | `layers.go: contextLayers` | `examples/03_full` (`project.yaml`) |
| may | classes of its own, and the default class of a contract the platform leaves without one (`global.okdp.provisioning`) | `layers.go: platformOnly` | `examples/04_claims` (`project.yaml`) |
| may | connections and named claims of its own | `layers.go: contextLayers` | `examples/03_full`, `examples/04_claims` |
| may | consent to serve another project, a provider: `claimers` of its instance's `instance.yaml` | `namespace.go: tree.consent` | `testdata/cli/claims.txtar` |
| may not | any other key of `global.okdp`, known to the compiler or not, set by the platform or not: the chart of the OAuth clients, where they register, the ingress, TLS, the proxy, the store, the policy (making itself privileged) | `layers.go: platformOnly` | `testdata/context/1/sets-platform-charts.yaml`, `sets-platform-oidc.yaml`, `sets-platform-ingress.yaml`, `sets-platform-tls.yaml`, `sets-platform-proxy.yaml`, `sets-platform-secrets.yaml`, `sets-platform-policy.yaml`, `sets-unknown-platform-key.yaml` |
| may not | change a value, a connection, a class or a default class of a lower level | `layers.go: contextLayers` (`chart.Overlap`) | `testdata/context/1/redefines-platform-connection.yaml`, `redefines-platform-class.yaml` |
| may not | template code | `layers.go: contextLayers` (`chart.NoTemplate`) | `testdata/context/1/template-code.yaml` |
| may not | a connection of a store key outside `<project>/`: the platform's, another namespace's | `connections.go: storeSecret` | `testdata/context/1/remote-key-of-the-platform.yaml` |
| may not | a provider of another project without its consent, through a class of its own or by naming it | `namespace.go: tree.consent` | `testdata/cli/claims.txtar` |
| may not | the name `platform`, the segment of the platform's keys | `pipeline.go: Compile` | `testdata/cli/secrets.txtar` |

## An instance author (level 2 and `config.yaml`)

An instance is input: a console user's form, a pull request. Its
`config.yaml` sets its parameters and upstream values, its
`connections.yaml` its own connections and nothing else, its
`instance.yaml` its package, `service` and `version`, of the catalog,
and, a provider, its `claimers`.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may | any package of the catalog, `service` and `version` | `repository.go: tree.definition` | `examples/02_advanced` |
| may | parameters the package's schema accepts, the keys of `global` it declares | `layers.go: valuesLayers`, `chart.Parameters` | `examples/02_advanced` |
| may | upstream values of the keys its package opens (`upstream`) | `chart/merge.go: applyUpstream` | `examples/02_advanced` |
| may | connections of its own, their credentials under `<project>/` of the store | `connections.go: storeSecret` | `testdata/cli/secrets.txtar` (`demo/own`) |
| may | claims of a class, or of a provider of its namespace or with its consent; a request the contract's claim schema accepts | `namespace.go: tree.provider`, `tree.consent`, `tree.request` | `testdata/cli/claims.txtar` |
| may not | `global` but the keys the parameter schema declares, never `okdp` | `layers.go: valuesLayers` | `testdata/instance-sets-global.yaml` |
| may not | `connections` or `claims` in `config.yaml` | `layers.go: valuesLayers` | `testdata/instance-sets-connections.yaml`, `instance-sets-claims.yaml` |
| may not | anything but `connections` in `connections.yaml` | `repository.go: tree.instance` | `TestLoadErrors` |
| may not | template code, in a key or a string | `layers.go: valuesLayers` (`chart.NoTemplate`) | `testdata/template-code.yaml` |
| may not | a parameter the schema refuses | `chart.Parameters` | `testdata/schema-violation.yaml` |
| may not | `global` or a path its package protects in upstream values | `chart/merge.go: applyUpstream` | `testdata/protected-upstream.yaml` |
| may not | a key of a pod's privileges, security context, identity, containers or objects in upstream values, in a string read as YAML too | `policy.go: upstreamCheck` | `testdata/denied-upstream.yaml`, `denied-upstream-text.yaml` |
| may not | an image of none of the platform's registries | `policy.go: upstreamCheck` | `testdata/context/0/unlisted-registry.yaml` |
| may not | a connection of another contract, a field the contract does not declare, a secret field | `connections.go: resolver.resolve`, `contract.fields` | `testdata/context/2/wrong-contract.yaml`, `unknown-field.yaml`, `secret-in-connection.yaml` |
| may not | a store key outside `<project>/` | `connections.go: storeSecret` | `testdata/context/2/remote-key-of-another-namespace.yaml` |

## A catalog author

A package is reviewed in its repository, with the upstream charts it
pins: its mapping, its defaults and values, its `upstream` (the keys an
instance may set, `protect`, `append`), its `objects`, `provides`,
`provisions` and `oidcClients`. Its own values may hold template code
and are not held to the upstream policy; its objects reach the cluster
as written (the objects file is not templated by Helm), an
ExternalSecret or a PushSecret of it names any key of the store. Its
`provisions` give the consent of its instances by default (`claimers`),
which an instance's list replaces.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may | its own values, template code included, not held to the upstream policy | not checked | |
| may | objects of any namespaced kind in the instance's namespace, an ExternalSecret or a PushSecret of any key of the store among them | not checked: the store's policy | |
| may | consent for its instances to serve other namespaces (`provisions[].claimers`), unless an instance lists its own | `namespace.go: Instance.Claimers` | `testdata/cli/claims.txtar` |
| may not | declare `global.okdp` in its `config.schema.json` or give it a default (`properties.global.default`): its defaults would set the platform's settings (the privileged projects, the store, the charts); the compiler reads them of the context's layers alone, whatever the parameters hold | `chart/parameters.go: ReadParameterSchema`, `layers.go: contextLayers`, `context.go: parameters` | `testdata/cli/policy.txtar` (`schema-okdp`, `schema-default`), `TestPlatformFromContext` |
| may not | an object of another namespace, for a project that is not privileged, whatever its source | `policy.go: checkObjects` | `testdata/cli/policy.txtar` (an ExternalSecret of another namespace) |
| may not | an object of a cluster-scoped kind, for a project that is not privileged | `policy.go: checkObjects` | `testdata/cli/policy.txtar` (a ClusterRole) |
| may not | a Secret of the objects with `data` or `stringData`, unless the caller allows it | `policy.go: checkPlainSecrets` | `testdata/cli/plain-secrets.txtar` (an objects' Secret) |
| may not | a `provides` secret without `generate`, unless the caller allows it | `provides.go: credentialsSecret` | `testdata/cli/plain-secrets.txtar` (a `provides` secret) |
| may not | a dependency over `http://`, a version range | `chart/ref.go: Ref.Check` | `testdata/cli/chart-sources.txtar` |
| may not | values for a chart `Chart.yaml` does not declare, an OAuth client chart of its own | `objects.go: addOwnCharts`, `chart.FinalValues` | `TestDcrCompile` |
| may not | an OAuth client's Secret that is not the release's, `<release>-...` | `oidc.go: oidcClients` | `testdata/cli/oidc.txtar` |
| may not | a file outside the package, a schema `$ref` fetched | `fs.FS`, `chart.CompileSchema` | `TestCompileSchemas` |
| may not | a Secret's name for its credentials (`provides[].secretRef.name`) that is not a DNS-1123 subdomain | `mapping.schema.json`, `provides.go: provided` | `TestProvidesErrors` |
| may not | a Secret of a claimer's namespace another object writes, through its claimers' copies, `<release>-<secretRef.name>`, or two objects of one namespace that write one Secret or one key of the store | `namespace.go: tree.refuseSecretTargets` | `testdata/cli/secrets.txtar`, `TestCompileAllSecretTargets` |
| may not | expressions past the cost limit, a host function's call counted by the bytes it reads and writes, or that read the cluster, the environment or the time | `compile.Session`, `hostFunctions`, `hostCost` | `testdata/cli/limits.txtar`, `TestHostCostBytes` |

## The engines

The engine renders each instance's chart, with every object it holds,
the upstream charts' included, which the compiler does not read. The
Flux and Argo CD targets confine each project to its namespace, with the
manifests of `projects/<namespace>/project/`
([the project's manifests](specs/01_inputs_outputs.md#the-projects-manifests)):

| | A project | A privileged project (`policy.privileged`) |
|---|---|---|
| Namespace | Pod Security Admission `enforce: baseline`, `audit` and `warn: restricted` | `enforce: privileged` |
| Flux | helm-controller impersonates `project-<namespace>` of `okdp-releases`, bound to a Role of every verb on every resource of the namespace only | helm-controller's own identity |
| Argo CD | the AppProject `project-<namespace>`: the tree's repository, the namespace, no cluster-scoped resource | every namespace, every cluster-scoped resource |
| Kept | the Namespace when the project goes (Flux's prune label, Argo CD's `Delete=false`); the objects Helm keeps, Argo CD's `Prune=false,Delete=false` (`keep.go: keepSynced`) | the same |

A chart that writes another namespace or a ClusterRole is refused by
the API server (Flux) or by Argo CD, not by the compiler; Pod Security
Admission refuses a privileged pod. A privileged project escapes all of
it: the objects policy, the upstream policy, the confined identity and
the baseline. The files are compared with `testdata/targets/<target>/`
(`TestTargets`): `demo` confined, `data` privileged.

## The secret store

The compiler writes the keys and the objects that read and push them;
the store's authorisation keeps the namespaces apart ([what the store
must enforce](specs/01_inputs_outputs.md#what-the-store-must-enforce)):

| Key | Written by | Read by |
|---|---|---|
| `<provider namespace>/<claim ID>` | the provider's PushSecret (`transfer.go: pushSecrets`) | the claimers' ExternalSecrets (`pullSecret`), the claim ID starting with their namespace |
| `<namespace>/<key>` | the namespace's PushSecrets, an administrator | the connections of the namespace's project and instances (`storeSecret`) |
| `platform/<key>` | the platform, as the store's operator | the platform's connections (level 0) |

With `secrets.provider`, the store is a SecretStore of each namespace,
authenticating with its ServiceAccount (`secrets.serviceAccount`,
`okdp-secrets`): one templated policy (OpenBao, Vault) gives each
namespace write on `<namespace>/*`, read on `+/<namespace>.*` and on
`platform/*`. Without it, the store is one ClusterSecretStore, one
identity for every namespace it admits: the tree is one trust domain,
whatever the keys. The compiler keeps the connections it resolves under
their namespace's segment, and refuses a project named `platform`; it
sees no store policy.

## okdp-gitops and the runner

[okdp-gitops](https://github.com/OKDP/okdp-gitops) compiles the tree
with this library and commits the files of a target to the output
repository, which the engines reconcile. Its CI job runs each compile
in a new container of its image, with nothing of the runner's (no file,
no credentials, no Docker socket) and one volume, the chart cache, on a
runner registered for the repositories of the sources only; the token,
the repository's secret `OKDP_GITOPS_TOKEN` of scope
`write:repository`, is the step's environment only, masked in the logs,
and sent to the host of the deployments repositories only. It reads the
tree without following a symbolic link, bounded, and bounds each
compile's CEL cost.

The chart cache it fills, package `chartcache`, the control plane's too,
is read for the checks and the console's defaults, never deployed; what
it pulls is bounded and must be the chart the instance names:

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may not | have the cache keep an archive other than the layer an OCI manifest names (a registry, a mirror or a proxy serving another one) | `chartcache/oci.go: pullOCI`, the layer read whole and verified against its size and digest before it is unpacked | `TestPullOCIVerifiesTheLayer` |
| may not | have a chart pulled over plain http, from a repository, an archive the index names or a redirect, but on a host of `InsecureHosts`, nor redirected from https to http | `RepositoryPuller.check`, `checkRedirect` | `TestIndexIsHTTPS` |
| may not | have the cache keep an archive of another chart or version than the one named (an index entry, a tag) | `chartcache/cache.go: checkChart` | `TestTheChartIsTheOneNamed` |
| may not | read a file of the cache other than a chart's `Files`, or through a symbolic link, from the console (`File`) or a compile (`FS`) | `Cache.File`, `Cache.FS` (`internal/dirfs`) | `TestFileIsOneOfFiles`, `TestFSFollowsNoLink` |

The cache directory is the platform's: a chart is pulled once, by
version, with no digest pinned nor expiry, each directory readable by
every user (0755) of a volume the jobs share; whoever writes the volume
chooses what the checks and the console read.

`okdp-compile`, run in a CI job of its own, holds the same lines against
the tree it reads:

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may not | make it read a file out of a repository (a symbolic link), block it (a FIFO) or exhaust it (a file over 4 MiB, a directory's files over 64 MiB) | `cmd/okdp-compile/main.go: openDir`, `internal/dirfs: FS.Open` | `testdata/cli/symlink.txtar`, `TestDirFSBounds` |
| may not | make it run without end | `--cost-limit`, `--timeout` (`compile.Session`) | `testdata/cli/limits.txtar` |
| may not | make it delete an output it did not write, or write through a symbolic link of the output, at any depth under `projects/`, in the output or out of it (every link there removed before a file is written) | `main.go: checkOutput`, `writeTree`, `removeLinks` | `testdata/cli/output.txtar`, `TestWriteTree` |
| may not | have a credential printed in a message, under a key that names one | `value.Short`, `value.ShortAt`, `chart.Validate` | `testdata/cli/errors.txtar` |
| may not | have one project's failure keep the others from deploying (`--partial`), a release name it shares with another project among them | `compiler.Partial` (`internal/tree/partial.go`), `compiler.TreeFiles` (`internal/output`) | `testdata/cli/partial.txtar`, `testdata/cli/kept.txtar`, `TestAReleaseCollisionKeepsItsProjects` |
| may not | make `--partial` read a file out of the output through a project kept (a symbolic link), or write a release a kept project's output holds in another namespace | `openDir` of `--output`, `compiler.TreeFiles` | `testdata/cli/kept.txtar` |

## What the compiler cannot enforce

- **Who writes a file.** The compiler trusts a file for its place in
  the tree: in one repository, whoever may write a project's directory
  may write `platform.yaml` but for the repository's reviews; whoever
  writes the output repository deploys anything, a privileged project's
  objects included, and so does the token that writes it.
- **What a chart does with its values.** The upstream policy names keys,
  not their effect: a privilege a chart grants from a key of another
  name (`rbac.create`) is the platform's to deny by name
  (`policy.upstream.deny`); a string a chart renders as YAML is checked
  when it reads as YAML whole, not a fragment its template completes.
  The engine's identity and Pod Security Admission are the second line.
- **A kind the compiler does not know.** An object of a cluster-scoped
  kind of no known operator is taken as namespaced: the engine's
  identity refuses it.
- **The chart's own defaults.** Its default images are not read: the
  registries hold the images the values set. A chart version is a tag,
  not a digest: a repository that publishes a version again changes what
  deploys; the transport is pinned (`https://`, `oci://`), not the
  content.
- **The package's own values and objects**, reviewed with it, are not
  held to the upstream policy; its ExternalSecrets and PushSecrets name
  any key of the store, which only the store's policy limits.
- **A privileged project** escapes every check: the objects policy, the
  upstream policy, the confined identity, the baseline. Only level 0
  names one.
- **The namespace is the boundary, not the instance.** The instances of
  a project share its namespace and its identity: a release reads the
  Secrets of the others; a provider's namespace holds the credentials of
  every claim it serves. Two of OKDP's objects that write one Secret of
  a namespace are refused, not a Secret an upstream chart renders.
- **The store's policy.** The compiler writes the keys; it cannot check
  that the store scopes them. A ClusterSecretStore, without
  `secrets.provider`, is one trust domain. A Kubernetes namespace named
  `platform` made outside the tree (by an administrator, by a privileged
  project) writes `platform/*` under the templated policy: the compiler
  refuses only a project of that name.
- **Consent is in the provider's own file.** The `claimers` of a
  provider's `instance.yaml` are written by whoever writes that
  instance's directory, in a shared repository any of its writers.
- **The network.** No NetworkPolicy is written: network isolation is the
  platform's.
- **Credentials under a name that does not say so.** A message redacts
  the values of keys named as credentials (`password`, `token`,
  `...Key`); another key's value, and a store key, are printed.
- **Credentials in clear allowed.** `--allow-plain-secrets`
  (`Inputs.AllowPlainSecrets`) accepts them, for a caller that knows its
  deployments repository private.
- **Bounds the caller sets.** The CEL cost is bounded when the caller
  passes a limit (`Inputs.CostLimit`: the CLI and okdp-gitops do); the
  files read are bounded by the CLI's reader and okdp-gitops's, a
  library caller's `fs.FS` being its own.
- **The upstream schemas** are checked with a chart cache only
  (`--check --charts`), else by Helm when it renders.
- **The token of the CI.** Whoever can push a workflow to a repository
  of the sources reads `OKDP_GITOPS_TOKEN`, which writes the output
  repository: the repositories of the sources are the platform's, their
  writers deploy already.
- **What is kept.** An object Helm keeps (a claim's database) and the
  Namespace of a project that goes outlive their release, with their
  data, until an administrator deletes them.
