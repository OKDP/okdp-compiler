# Threat model

Who writes what in an OKDP deployment, what each author may reach, and
what is refused of each, as it is enforced now. The [security
boundaries](00_architecture.md#security-boundaries) list the checks by
mechanism; this document lists them by author. Each refusal names the
code that enforces it, `<file>: <function>`, and the test of this module
that proves it, a case that must fail; a rule proved only by compiling a
whole deployment is a host's to prove, its cell empty. [What cannot be
enforced](#what-cannot-be-enforced) closes it.

These rules are data. The walkers of `internal/okdp` (`policy.go`,
`release.go`) run the checks of the compiler's mandatory rule set, which
every compile applies; a rule set a compile is given (`Inputs.Rules`)
adds to it and replaces none of its rules. A platform that requires a
set (`global.okdp.ruleSets`) is refused by every compile not given it, or
given a version its constraint refuses, so that the settings only that
set reads are never left unchecked.

The hosts a project serves, the OAuth clients it registers, the hardened
pod of the packages and the policy on the upstream values of an instance
are rules of the security set, okdp-compiler-security, not of the
mandatory set: [the security set](#the-security-set) says what it covers
and what a compile without it does not check.

This module's own `cmd/okdp-compile` reads its input documents and its
catalogs bounded (`internal/dirfs`), and writes nothing out of its
output's `projects/` that it did not write (`internal/outdir`); it follows
symbolic links, which are the user's. It compiles with the mandatory rules
and the sets of `--rules`, the mandatory rules alone without them, their
checks and hooks among them, and with no policy on upstream values. It
reads no upstream chart: it checks neither the final values against the
upstream charts nor the manifests they render, and it writes no file of a
deployer engine. A compile to deploy from goes through a host that adds
them.

A host, a program that embeds the compiler, holds part of what is named
here: the files of the deployer engines and the manifests that confine
each project, the chart cache and the checks against the upstream
charts, the layout of a deployment's files and who may write each, the
commits to the deployments repository and a control plane's edits,
what each values layer it gives may set ([what a host checks of the
layers](#what-a-host-checks-of-the-layers)), and the wiring of the
security set and of its package `policy` (through `Inputs.Rules`,
`Inputs.Finalize` and its calls of `policy.CheckGlobals`). A rule
enforced by a host names no code of this module: `okdp-compile` alone
does not enforce it.

## What is at stake

- The cluster: an object of a cluster-scoped kind (a ClusterRole, a
  webhook, a CustomResourceDefinition), an object of another namespace,
  a privileged pod, a pod of another identity.
- The credentials: the Secrets of a namespace; the keys of the
  platform's secret store, the platform's under `platform/` and those of
  the claims that cross namespaces, under `<provider namespace>/<claim
  ID>`.
- The platform's settings, `global.okdp`: the objects' chart, the TLS
  settings, the proxy, the secret store, the privileged projects, the
  rule sets the platform requires and the settings those sets read.
- The deployments repository: what is committed under its output,
  `compiled/`, the engines apply as it is.
- The runner of a compile: what it reads, how long it runs, what it
  writes and prints.

## Authority comes from the file

The compiler does not know who wrote a file: it trusts a catalog for its
packages and contracts, and takes every values layer of a compile alike
(`Inputs.Values`, merged in order as `helm -f` merges files): any layer
sets parameters, upstream values, `global`, `global.okdp` (the
settings), `connections` and `claims`. Of a catalog's directory
(`compiler.LoadCatalog`, `--catalog`) it reads `catalog/` and
`contracts/`, nothing else; of the input documents
(`compiler.LoadInputs`, `-f`), the layers of an instance are the
`Settings` document's values, its `Project` document's, its `Instance`
document's `connections` and its values, in this order, and its groups
the `access` of its `Project` document (`access.groups` of
`Inputs.Settings`, the host's settings, which no layer may set too)
([what a compile
receives](specs/01_inputs_outputs.md#what-a-compile-receives)). Which
files hold which documents, and who may write each, is a host's: its
layout and the hosting of its repositories, their write access and their
reviews (a platform repository its administrators write, a project's its
team, a catalog its maintainers), so that a catalog's maintainers add no
project nor platform values. What each of them may set is a host's too
([what a host checks of the
layers](#what-a-host-checks-of-the-layers)): `okdp-compile` checks none
of it. Through the library, the caller gives the layers and the groups.

| Author | Writes | Reviewed | The compiler takes it as |
|---|---|---|---|
| [The platform](#the-platform-author) | the `Settings` document | with the platform | a values layer |
| [A project](#a-project-author) | its `Project` document, its `access`, and its instances' | by the project's owners | a values layer, and the groups |
| [An instance](#an-instance-author) | its `Instance` document: its package, its `claimers`, its `connections`, its values | a console user or a pull request | values layers |
| [A catalog](#a-catalog-author) | `catalog/<service>/<version>/`, `contracts/` | in the catalog's repository | reviewed, but for the rules below |
| [The engines](#the-engines) | apply `compiled/` | | the identity the targets give each project |
| [The secret store](#the-secret-store) | keeps the keys | | the keys the compiler writes |
| [A host](#the-host-and-the-runner) | runs the compile, commits `compiled/` and a control plane's edits | | a caller |

## What a host checks of the layers

The compiler refuses of a layer what it refuses of any, whoever wrote
it. A host that gives layers of several origins, the platform's
reviewed, a project's or an instance's not, decides what each may set,
before the compile, as its layout and its reviews decide who writes
each file. The model assumes these rows of such a host. `okdp-compile`
holds none of them: a `Project` document that sets
`global.okdp.policy.privileged` makes its project privileged.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may not | a project's or an instance's layer: change a value, a connection, a class or a default class the platform's layers set | a host | |
| may not | a project's or an instance's layer: template code (`{{`, which some charts pass to `tpl`) in parameters; the platform's layers and the package's defaults are reviewed | a host | |
| may not | a project's or an instance's layer: a key of `global.okdp` but `provisioning`, the classes a project adds: the objects' chart, TLS, the proxy, the store, the policy (making itself privileged), the platform's administrators (`access`), a setting of a rule set | a host | |
| may not | a project's or an instance's layer: a key of `global` beside `okdp` the platform does not list (`global.okdp.policy.projectGlobals`, which the compiler does not read): every chart receives `global`, past `protect`; what a listed key holds checked as upstream values, the security set's `policy.CheckGlobals` | a host | |
| may not | an instance's values: `connections` (its own are a layer of their own), `claims` (its project's), a key of `global` its package's schema does not declare | a host | |
| may not | a project's or an instance's connection: a store key outside `<namespace>/`, the platform's, another namespace's | a host | |
| may not | groups (`global.okdp.access.groups`) given by another than the project's owners: a host gives them in `Inputs.Settings`, which a layer that gives them too fails | a host; `layers.go: hostSettings` | `TestHostSettings` |

## The platform author

The platform's values, the `Settings` document, are a layer as any
other to the compiler. A host that reviews them with the platform lets
them hold what it refuses of the other layers: template code (`{{`) but
in a connection and in upstream values, which are checked whatever
their layer; `global.okdp`: TLS, the objects' chart (`charts`), the
proxy, the secret store (`secrets`), the policy (`policy`: the
privileged projects, the reserved namespaces), the rule sets it
requires (`ruleSets`) and the settings of those sets. Its connections
name any key of the store, visible to every namespace that receives
them, and its classes route a namespace to a provider of another one
when that provider consents.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may | set any key of `global.okdp`, template code in its values | not checked | |
| may | reserve namespaces no project may have, its own components' (`policy.reserved`), a reservation winning over `policy.privileged` | `policy.go: checkPlatformNamespace`, `pipeline.go: Compile`; before a namespace is created, `compiler.CheckNamespace` | `TestCompileErrors`, `TestCheckNamespace` |
| may | name the platform's own projects, `policy.privileged` | `policy.go: privileged` | `TestPrivileged` |
| may | require rule sets (`ruleSets`): every compile not given one, or given a version its constraint refuses, is refused | `context.go: requiredRuleSets`, `RuleSet.Require` | `TestRequiredRuleSets` |
| may | name its administrators' groups and the API server's group prefix (`access`): a mapping reads the administrators in `platform.access`; the bootstrap binds them, the compiler writes nothing of them | `platform.schema.json`, `scope.cel.yaml` | `TestPlatformAccess` |
| may | name the control plane's identity (`controlPlane.serviceAccount`), bound in each project's namespace to what it does there, its cluster-wide role reduced to `namespaces: get, create` | a host, with the project's manifests | |
| may | isolate the projects' namespaces (`network.policy`): ingress to a project's pods from its namespace, the platform's ingress namespaces (`network.ingressNamespaces`) and its claimers' only; off by default, egress open | a host, with the project's manifests | |
| may | give a connection of any key of the store, `platform/<key>` of a ClusterSecretStore; with `secrets.provider`, a key a namespace reads, `<namespace>/<key>`, read through the project's connection | `connections.go: storeSecret` | `TestResolveFromStore` |
| may not | give a connection a key under `platform/` when the platform gives `secrets.provider`: no namespace reads it, the platform's credentials are copied under the keys of the namespaces that use them | `connections.go: storeSecret` | `TestResolveFromStoreErrors` |
| may | route any namespace to a provider (a class), of another namespace when the provider's claimers list it: a class consents for no provider | `namespace.go: consent` | `TestCompileAllClaims`, `TestCompileAllConsent` |
| may not | a platform chart (`charts`) over `http://`, or of a version range | `chart/ref.go: Ref.Check` | |
| may not | a former setting of `global.okdp`, `certificateIssuers` or `serviceCatalog` (a console's menu is a host's), which no compile reads | `platform.schema.json` (`false`) | `TestPlatformSchema` |
| may not | `{{` in a connection or in upstream values | `connections.go: resolver.resolve`, `merge.go: applyUpstream` (`chart.NoTemplate`) | `TestResolveTemplate`, `TestApplyUpstreamErrors` |

## A project author

A project owns its namespace: its `Project` document adds a layer to
the platform's for its instances and gives its groups (`access`), and
its instances' documents are its own, wherever a host keeps them. What
its layer may set is a host's to check ([what a host checks of the
layers](#what-a-host-checks-of-the-layers)).

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may | parameters, upstream values, connections, named claims and classes for its instances, under an instance's rules | `chart.Parameters`, `merge.go: applyUpstream`, `connections.go: declaredConnections` | `examples/04_claims` (`demo/project.yaml`) |
| may | consent to serve another project, a provider: `claimers` of its instance's `Instance` document | `namespace.go: consent`, `inputs.go: readInstance` | `TestCompileAllConsent` |
| may | give the identity provider's groups a role within the project, `access` (`viewer`, `editor`, `admin`; no group by default): a project team gets exactly its roles, those of its AppProject and of its namespace, nothing of another project's | `access.go: ParseAccess`, `platform.schema.json` (`access.groups`); a host writes the roles | `TestLoad` (`internal/inputs`), `TestPlatformAccess` |
| may not | an `access` of another form: a group's path (`/`), a space, a role that is none of the three | `access.go: ParseAccess`, `platform.schema.json` (`access.groups`) | `TestParseAccess`, `TestLoadErrors` (`internal/inputs`) |
| may not | a provider of another project without its consent, through any class, the platform's too, or by naming it | `namespace.go: consent` | `TestCompileAllConsent` |
| may not | keep a declaration unchecked by having no instance: its named claims (class, request, name) and its connections checked as an instance's compile checks them; of a project without instance, where a host checks it | `namespace.go: CompileAll`; `compiler.CheckDeclarations`, a host's call | `TestCompileAllDeclaredConnections` |
| may not | the name `platform`, the segment of the platform's keys | `policy.go: checkNamespace`, `pipeline.go: Compile`; before a namespace is created, `compiler.CheckNamespace` | `TestCompileErrors`, `TestCheckNamespace` |
| may not | a reserved namespace: `kube-*`, Kubernetes' own; `flux-system`, `argocd`, `okdp-releases`, the engines' and those a host writes the engines' objects in (its releases, with every verb of the namespace, would rewrite the engine's sources and the other projects' identities); another namespace a host writes in (a Flux source's); those of `policy.reserved`, privileged or not | `policy.go: checkNamespace`, `checkPlatformNamespace` (the mandatory set's `engineNamespaces`, `compiler.EngineNamespaces`); a host, for another namespace it writes in; before a namespace is created, `compiler.CheckNamespace`, the compile's two | `TestCompileErrors`, `TestCheckNamespace` |

## An instance author

An instance is input: a console user's form, a pull request. Its
`Instance` document names its package, `service` and `version`, of the
catalog, and, a provider, its `claimers`; its `connections`, its own
connections, a layer of their own; its values (`values`,
`valuesFiles`), the last layers. What these may set is a host's to
check ([what a host checks of the
layers](#what-a-host-checks-of-the-layers)).

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may | any package of the catalog, `service` and `version` | `inputs.go: definition` | `examples/02_advanced`, `TestLoadErrors` (`internal/inputs`) |
| may | parameters the package's schema accepts | `chart.Parameters` | `examples/02_advanced` |
| may | upstream values of the keys its package opens (`upstream`) | `merge.go: applyUpstream` | `examples/02_advanced` |
| may | connections of its own, their credentials a key of the store | `connections.go: storeSecret` | `TestResolveFromStore` |
| may | claims of a class, or of a provider of its namespace or with its consent; a request the contract's claim schema accepts | `namespace.go: compilation.provider`, `consent`, `claimRequest` | `TestCompileAllClaims`, `TestCompileAllConsent` |
| may not | a key its `Instance` document does not hold, a values file out of its input files (an absolute path, `..`) | `inputs.go: readInstance`, `document.values` | `TestLoadErrors` (`internal/inputs`) |
| may not | template code in its upstream values or a connection, in a key or a string | `merge.go: applyUpstream`, `connections.go: resolver.resolve` (`chart.NoTemplate`) | `TestApplyUpstreamErrors`, `TestResolveTemplate` |
| may not | a parameter the schema refuses | `chart.Parameters` | `TestParameters` |
| may not | `global` or a path its package protects in upstream values | `merge.go: applyUpstream` | `TestApplyUpstreamErrors` |
| may not | a connection of another contract, a field the contract does not declare, a secret field | `connections.go: resolver.resolve`, `contract.fields` | `TestResolveErrors` |
| may not | a connection that does not hold at its declaration, referenced or not, of any of its layers (a name, a contract, a field, a required field, a type, a `secretRef`) | `connections.go: declaredConnections`, `namespace.go: CompileAll` | `TestCompileAllDeclaredConnections` |

## A catalog author

A package is reviewed in its repository, with the upstream charts it pins:
its mapping, its defaults and values, its `upstream` (the keys an instance
may set, `protect`, `append`), its `objects`, `provides`, `provisions` and
the sections a rule set adds. Its own values may hold template code and
are not held to the upstream policy; its objects reach the cluster as
written (the objects' chart does not template them), an ExternalSecret or
a PushSecret of it names any key of the store. Its `provisions` give the
consent of its instances by default (`claimers`), which an instance's list
replaces.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may | its own values, template code included, not held to the upstream policy | not checked | |
| may | objects of any namespaced kind in the instance's namespace, an ExternalSecret or a PushSecret of any key of the store among them | not checked: the store's policy | |
| may | consent for its instances to serve other namespaces (`provisions[].claimers`), unless an instance lists its own | `namespace.go: Instance.Claimers` | `TestProvisions`, `TestInputsClaimers` |
| may not | add a project, privileged or in a reserved namespace, or the platform's values, from a catalog: of a catalog's directory (`--catalog`) the compiler reads `catalog/` and `contracts/` only; that no input document comes from a catalog's files is a host's | `catalog.go: Load`; a host, for the files it gives as documents | `TestCatalog` (`internal/catalog`) |
| may not | a package of one service in two catalogs, a contract that differs between two | `catalog.go: Load` | `TestCatalogErrors` |
| may not | declare `global.okdp` in its `config.schema.json` or give it a default (`properties.global.default`): its defaults would set the platform's settings (the privileged projects, the store, the charts); the compiler reads them of the values layers alone, whatever the parameters hold | `chart/parameters.go: ReadParameterSchema`, `layers.go: valuesLayers`, `context.go: parameters` | `TestPlatformFromValues` |
| may not | an object of another namespace, for a project that is not privileged, whatever its source | `policy.go: CheckObjects` | `TestObjectsPolicy` |
| may not | an object of a cluster-scoped kind, for a project that is not privileged | `policy.go: CheckObjects` | `TestObjectsPolicy` |
| may not | an object of the deployer engines' API groups (Flux's toolkit, the Flux Operator's `fluxcd.controlplane.io`, `source.extensions.fluxcd.io`, Argo CD's `argoproj.io`), for a project that is not privileged: it would drive the engine with the engine's identity | `policy.go: CheckObjects`, the table `deployerApiGroups` | `TestObjectsPolicy` |
| may not | a dependency of key `global`, its values Helm's global values, which every chart of the umbrella reads | `chart/metadata.go: Metadata.Check` | `TestMetadataCheck` |
| may not | two objects of one API group, kind, namespace and name in an instance's objects, the second would replace the first: two claimers' objects whose name a provider derived from the claim's ID by a lossy mapping (`a-b.c`, `a.b-c`); the names are `okdpSqlName(id)`, `okdpObjectName(id)`, injective over the IDs | `policy.go: checkDuplicateObjects`, `names.go: sqlName`, `objectName` | `TestDuplicateObjects`, `TestNamesInjective` |
| may not | a Secret of the objects with `data` or `stringData`, unless the caller allows it | `policy.go: checkPlainSecrets` | `TestPlainSecrets` |
| may not | an ExternalSecret (or ClusterExternalSecret) whose `target.template.data` holds a literal at a sensitive key, an `okdpGeneratedSecret` `stringData`'s included, nor a Fake generator (or ClusterGenerator of kind Fake) with values, unless the caller allows it | `policy.go: checkPlainSecrets`, `credentialLiteral`, `credentialReference` | `TestPlainSecrets` |
| may not | a `provides` secret without `generate`, unless the caller allows it | `provides.go: credentialsSecret` | `TestProvidesErrors` |
| may not | a dependency over `http://`, a version range | `chart/ref.go: Ref.Check` | `TestMetadataCheck` |
| may not | values for a chart `Chart.yaml` does not declare, the key of the objects' chart | `objects.go: checkObjectsChart`, `merge.FinalValues` | `TestObjects` |
| may not | a release named like an object the platform writes in every namespace (`okdp-project`, `okdp-control-plane`, `okdp-access-*`, the secret store, its ServiceAccount, the names a rule set adds), or the beginning of one up to a `-`: the project `okdp`'s instance `project` would write the Role `okdp-project` that confines every release of the namespace | `release.go: checkRelease`, `PlatformObjectNames` | `TestReleaseNamedLikeAPlatformObject` |
| may not | a file outside the package, a schema `$ref` fetched | `fs.FS`, `chart/schema.go: compileSchema` | `TestCompileSchemas` |
| may not | a connection reference out of the compile's reach (under `allOf`, `$defs`, `additionalProperties`...), which a reader of the schema, a console, would take for one the compile resolves and checks | `connections.go: requirements` (`schemaRefs`) | `TestRequirementsOutOfReach` |
| may not | a Secret's name for its credentials (`provides[].secretRef.name`) that is not a DNS-1123 subdomain | `mapping.schema.json`, `provides.go: provided` | `TestProvidesErrors` |
| may not | a Secret of a claimer's namespace another object writes, through its claimers' copies, `<release>-<secretRef.name>`, or two objects of one namespace that write one Secret or one key of the store, an item of a `List` among them | `namespace.go: compilation.refuseSecretTargets`, `policy.go: eachObject` | `TestCompileAllSecretTargets` |
| may not | expressions past the cost limit, a host function's call counted by the bytes it reads and writes, or that read the cluster, the environment or the time | `cel2yaml.Session`, `hostFunctions`, `hostCost` (github.com/okdp/cel2yaml) | `TestHostCostBytes` (cel2yaml) |
| may not | amplify its input past the cost limit or the size of a document: a `$for`'s elements and what its body builds counted in the cost, a document's values 32 MiB at most whatever the limit, each copy of a value counted | `cel2yaml.Session`: `charge`, `grow`, `memSize` (github.com/okdp/cel2yaml) | `TestLoopCost` (cel2yaml) |

## The engines

The engine renders each instance's chart, with every object it holds,
the upstream charts' included, which the compiler does not read. A host
that writes the files of Flux or Argo CD confines each project to its
namespace with manifests of the project's own, which the compiler does
not write: it gives what they are made of (the groups,
`access.groups` of `Inputs.Settings`, the privileged projects, the
network settings, the names of the platform's objects,
`compiler.PlatformObjectNames`). The confinement this model assumes:

| | A project | A privileged project (`policy.privileged`) |
|---|---|---|
| Namespace | Pod Security Admission `enforce: baseline`, `audit` and `warn: restricted` | `enforce: privileged` |
| Flux | helm-controller impersonates `project-<namespace>` of `okdp-releases`, bound to a Role of every verb on every resource of the namespace only | helm-controller's own identity |
| Argo CD | the AppProject `project-<namespace>`: the deployments repository, the namespace, no cluster-scoped resource | every namespace, every cluster-scoped resource |
| People | the roles of its groups (`access`): Argo CD roles of its AppProject, its Applications only, no `exec`; RoleBindings of its namespace to `view`, `edit`, `admin` | the same |
| Control plane | when the platform names its identity (`controlPlane.serviceAccount`): the Role `okdp-control-plane` of the namespace, what the server does there, Secrets by name and never listed | the same |
| Network | with `network.policy`: the NetworkPolicy `okdp-project`, ingress from the namespace, the platform's ingress namespaces and the namespaces of the claims it provides, egress open; written for a project kept too, of today's settings and claims | none: the platform's own |
| Kept | the Namespace when the project goes (Flux's prune label, Argo CD's `Delete=false`); the objects Helm keeps, Argo CD's `Prune=false,Delete=false` (`write.go: keepSynced`) | the same |

A chart that writes another namespace or a ClusterRole is refused by
the API server (Flux) or by Argo CD, not by the compiler; Pod Security
Admission refuses a privileged pod. A privileged project escapes all of
it: the objects policy, the upstream policy, the confined identity and
the baseline.

A host's objects in the engines' namespaces are named after the
releases and the projects (an Application or a HelmRelease
`<project>-<instance>`, its ConfigMap `values-<release>`, an AppProject
or a ServiceAccount `project-<namespace>`): a project and an instance
choose those names. The platform's own objects there must take a name
none of them can, with a dot (`okdp.instances`, not `okdp-instances`,
which the instance `instances` of a project `okdp` would replace,
pruning every Application its app of apps applies): a rule of the
platform's manifests, which the compiler does not see.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may not | a project of a namespace a host writes in (`okdp-releases`, `argocd`, a Flux source's): the engine's objects there, rewritten with the project's every verb | `policy.go: checkNamespace` (`compiler.EngineNamespaces`); a host, for another namespace it writes in | `TestCompileErrors` |
| may not | an object of the platform's of the same name as a release's in the engines' namespaces, when the platform names its own with a dot | the platform's naming rule, not the compiler | |

## The secret store

The compiler writes the keys and the objects that read and push them;
the store's authorisation keeps the namespaces apart ([what the store
must enforce](specs/01_inputs_outputs.md#what-the-store-must-enforce)):

| Key | Written by | Read by |
|---|---|---|
| `<provider namespace>/<claim ID>` | the provider's PushSecret (`transfer.go: pushSecrets`) | the claimers' ExternalSecrets (`pullSecret`), the claim ID starting with their namespace |
| `<namespace>/<key>` | the namespace's PushSecrets, an administrator | the connections of the namespace's instances (`storeSecret`) |
| `platform/<key>` | the platform, as the store's operator | the platform's connections, through a ClusterSecretStore only |

With `secrets.provider`, the store is a SecretStore of each namespace,
authenticating with its ServiceAccount (`secrets.serviceAccount`,
`okdp-secrets`): one templated policy (OpenBao, Vault) gives each
namespace write on `<namespace>/*` and read on `+/<namespace>.*`, and
nothing on `platform/`: the platform's credentials are copied under the
keys of the namespaces that use them (`<namespace>/<key>`, the sandbox's
`<namespace>/s3/platform`), and a connection naming a `platform/` key is
refused. A connection of the platform's layers is visible to every
project: its key is read by each project's instances as that project,
so one that is not a `platform/` key serves the namespace whose segment
it is under, and no other. Without a provider, the store is one
ClusterSecretStore, one identity for every namespace it admits: the
platform is one trust domain, whatever the keys. Which keys a
namespace's own connections name, under its segment, is a host's to
check ([what a host checks of the
layers](#what-a-host-checks-of-the-layers)); the compiler refuses a
project named `platform`, and sees no store policy.

## The host and the runner

Everything in this section is a host's to enforce, not this module's,
but where a row names this module's code: `okdp-compile` alone enforces
none of the rest. A host compiles a deployment with this library and
commits the files of a deployer engine to the output repository, which
the engines reconcile. This model assumes that its CI job runs each
compile in a new container, with nothing of the runner's (no file, no
credentials, no Docker socket) and one volume, the chart cache, on a
runner registered for the repositories of the sources only; that the
token that writes the deployments repository is the step's environment
only, masked in the logs, and sent to the host of the deployments
repositories only; and that the host reads its sources without following
a symbolic link, bounded, and bounds each compile's CEL cost.

The chart cache a host fills, a control plane's too, is read for the
checks of the final values against the upstream charts and of the
manifests they render, which the compiler does not run, and for the
console's defaults, never deployed. What it pulls must be bounded and
must be the chart the instance names:

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may not | have the cache keep an archive other than the layer an OCI manifest names (a registry, a mirror or a proxy serving another one) | a host's chart cache: the layer read whole and verified against its size and digest before it is unpacked | |
| may not | have a chart pulled over plain http, from a repository, an archive the index names or a redirect, but on a host the platform declares insecure, nor redirected from https to http, by an OCI registry too | a host's chart cache: its checks of each URL and redirect | |
| may not | make the puller reach the platform's network (a blind request from the CI or the control plane to an RFC 1918, loopback or link-local address, the cloud metadata endpoint): a registry, a token realm, a redirect, an index or an archive at one refused as dialed, but for a host the platform declares private or insecure; behind a proxy (`$HTTPS_PROXY`), which would connect to any address, the destination is resolved by the puller and refused at such an address before the proxy is dialed, a name it cannot resolve refused (the proxy resolves it again: one that answers differently to it is the proxy's to refuse) | a host's chart cache: its dialer | |
| may not | have the puller accept a certificate no trusted CA signed: the system's, or the pool the platform gives (a platform's CA, for a registry of the cluster), never a check turned off | a host's chart cache, net/http's TLS verification | |
| may not | have the cache keep an archive of another chart or version than the one named (an index entry, a tag) | a host's chart cache: the chart checked against the name | |
| may not | read a file of the cache other than a chart's files, or through a symbolic link, from the console or a compile | a host's chart cache | |

The cache directory is the platform's: where a chart is pulled once, by
version, with no digest pinned nor expiry, on a volume the jobs share,
whoever writes the volume chooses what the checks and the console read.

A host's command, run in a CI job of its own, holds these lines against
the files it reads; this module's `okdp-compile` holds those whose row
names its code:

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may not | block it (a FIFO) or exhaust it (a file over 4 MiB, a directory's files over 64 MiB) | `cmd/okdp-compile/main.go: openInputs`, `internal/dirfs: FS.Open` | `TestDirFS`, `TestDirFSBounds` |
| may not | exhaust the chart cache's reads with the charts its packages name, refusing another project's instances: each chart's files bounded together, the cache unbounded but by its charts | a host's reader of the chart cache | |
| may not | make it run without end, or hold the runner for long: the CEL cost of an instance's expressions is bounded (the default limit about ten times the dearest case of the catalogs), one expression may cost what is left of the limit, not the limit again, one instance's compile is bounded in time and refused alone past it, the whole compile too | `--cost-limit`, `--instance-timeout`, `--timeout` (`cel2yaml.Session`: `program`, `okdp.Compile`: `Inputs.Timeout`) | `TestSession` (cel2yaml), `TestCompileTimeout` |
| may not | make it delete an output it did not write | `outdir.Check` | `TestCheck` (`outdir`) |
| may not | have a credential printed in a message, under a key that names one | `value.Short`, `value.ShortAt`, `chart.Schema.Validate` | `TestRedact`, `TestValidateRedacts`, `TestResolveRedacted` |
| may not | have one project's failure keep the others from deploying, where a host deploys the projects that compile and keeps the output of the others: a refused instance, a release name it shares with another project, a file of its own that does not load, each refusing that project and keeping its output | a host: `CompileAll` refuses instances one by one (`Outcome.Err`), what is written and kept of a project is the host's | |
| may not | make such a host read a file out of the output through a project kept (a symbolic link), or write a release a kept project's output holds in another namespace | a host's reader of its output | |
| may not | keep a privilege revoked, a role taken back or a policy tightened by keeping one instance of a project refused: the kept project's Namespace, identity, AppProject, RoleBindings and Flux HelmReleases are of today's settings, its instances kept applied with them | a host, which derives a kept project's manifests again | |
| may not | keep a project in a namespace reserved after it was written, by the platform (`policy.reserved`) or by the compiler (a release that reserves a name): a project kept whose namespace is none a project may have today is dropped from the output, nothing of its Role, Namespace, access or releases kept nor derived again, said so; the compile does not fail for it | `access.go: SettingsOf` (one view with `Compile`); a host drops the project | `TestSettingsOf` |

A control plane edits the inputs through a host, which commits each
edit:
whatever the API in front of it, an edit holds these lines.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may not | address a directory other than its own through a name, a dot segment joined into a path (an instance `..`, the project, removed with every instance) | a host's edits of its files | |
| may not | write a change after which an instance it writes, or one that compiled before, does not compile, nor remove what an instance uses | a host's edits, comparing the outcomes of `CompileAll` before and after | |
| may not | create an instance whose release another project's instance has, nor a project of a name the compile refuses | a host's edits, with `compiler.CheckNamespace` | `TestCheckNamespace` |

## The security set

The security set,
[okdp-compiler-security](https://github.com/OKDP/okdp-compiler-security),
is a rule set added to the mandatory set (`okdp-compile --rules`,
`security.Rules()`), and a host gives it to every compile. Its
[threat
model](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/03_threat_model.md)
lists its rules by author, as this page does. It covers:

| Threat | What the set does |
|---|---|
| a project takes another's traffic, or a certificate for its name, the identity provider's among them | refuses a host the project does not serve, on its objects (Ingress, Certificate, Gateway API routes and listeners, host annotations) and in the `ingress` keys of its charts' final values |
| a project receives another's authorization codes, or registers a client the provider trusts beyond it | refuses an OAuth client's redirect URI on a host the project does not serve, a trusted URL in a client's `request`, a client's Secret that is not the release's |
| a package's pod runs with privileges | its functions write a hardened pod (non-root, no capability, no privilege escalation, no host namespace or `hostPath`), for the packages that call them |
| an instance's upstream values grant a pod privileges, another identity or containers, or images of other registries | refuses denied keys at any depth, root, images of registries the platform does not list, in upstream values and in what the keys of `global` of a layer a host does not review hold; `okdp-compile` runs none of it, only a host that wires the set's package `policy` |

A compile given the mandatory set alone checks none of this. It reads
no host and no redirect URI; a package that declares OAuth clients or
calls the set's functions fails to compile (an unknown key of
`okdp.yaml`, an unknown function), and the keys the set adds to the
closed map `policy` (`ingressHosts`, `upstream`) are refused by the
mandatory schema. The other settings the set reads, the suffix of the
hosts and the identity provider's, are keys `global.okdp` does not
describe, and pass unread. A platform that relies on the set therefore
requires it, and every compile not given it, or given a version its
constraint refuses, is refused:

```yaml
global:
  okdp:
    ruleSets:
      - {name: security, version: "^1.0.0"}
```

The requirement is in the platform's values, in git, and holds with the
compiler alone, without a control plane.

## What cannot be enforced

- **A compile without the security set, or without the host's
  hooks.** Every compile applies the mandatory rule set, its checks
  among them. A compile not given the security set checks no host, no
  OAuth client and no upstream value ([the security
  set](#the-security-set)), unless the platform requires it. A caller that
  gives no `Inputs.Finalize` applies no policy on upstream values, and
  one that calls no `policy.CheckGlobals` no check of what the keys of
  `global` of a project's layer hold, whatever its rule sets. It is a
  host's to give them; `okdp-compile` gives neither.
- **What a layer sets.** The compiler takes every values layer alike: a
  project's or an instance's sets `global.okdp`, its namespace
  privileged among it, template code in parameters, a connection of
  another namespace's key of the store, as the platform's does. Only a
  host that knows where each layer comes from refuses it ([what a host
  checks of the layers](#what-a-host-checks-of-the-layers));
  `okdp-compile` refuses none of it.
- **Who writes a file.** The compiler trusts a document for its kind,
  wherever it is, and reads a catalog's `catalog/` and `contracts/`
  only: which files are given as documents (`-f`), and who writes them,
  is the caller's. A catalog's files given as documents add projects all
  the same; in one repository, whoever may write a project's documents
  may write the `Settings` document but for the repository's reviews;
  whoever writes the output repository deploys anything, a privileged
  project's objects included, and so does the token that writes it.
- **A kind the compiler does not know.** An object of a cluster-scoped
  kind of no known operator is taken as namespaced: the engine's
  identity refuses it.
- **The chart's own defaults.** A chart version is a tag,
  not a digest: a repository that publishes a version again changes what
  deploys; the transport is pinned (`https://`, `oci://`), not the
  content.
- **The package's own values and objects**, reviewed with it, are not
  held to the upstream policy; its ExternalSecrets and PushSecrets name
  any key of the store, which only the store's policy limits.
- **A privileged project** escapes every check: the objects policy, the
  upstream policy, the confined identity, the baseline. Any layer that
  sets `policy.privileged` names one: a host lets the platform's only.
- **The namespace is the boundary, not the instance.** The instances of
  a project share its namespace and its identity: a release reads the
  Secrets of the others; a provider's namespace holds the credentials of
  every claim it serves. Two of OKDP's objects that write one Secret of
  a namespace are refused, not a Secret an upstream chart renders. The
  names of a release's objects start with `<release>-` (`okdpFullname`),
  a separator a release holds too: the instances `a` and `a-b` of a
  project share the prefix of `<namespace>-a-b-...`, and the first may
  name an object of the second's; a separator no release holds would
  rename every package's objects. What the compiler decides is limited
  to what it writes itself: a release is none of the platform's objects'
  names.
- **The store's policy.** The compiler writes the keys; it cannot check
  that the store scopes them. A ClusterSecretStore, without
  `secrets.provider`, is one trust domain. A Kubernetes namespace named
  `platform` made outside the platform's projects (by an administrator,
  by a privileged project) writes `platform/*` under the templated
  policy, which reads none: the compiler refuses only a project of
  that name. Likewise a namespace of the platform's components that
  `policy.reserved` does not list: the compiler knows Kubernetes', the
  engines' and the store's only.
- **Who changes a project's access.** `access` is in the project's
  `Project` document (`access.groups` of `Inputs.Settings`): whoever
  writes it gives the roles, its repository's writers (the split
  topology, the project's administrators), every writer of a shared
  repository. The compiler
  names the groups; the identity provider decides who is in them, the
  API server's authentication settings and Argo CD's how the groups
  claim reaches them, and the bootstrap binds the platform's
  administrators.
- **Consent is in the provider's own document.** The `claimers` of a
  provider's `Instance` document are written by whoever writes that
  document, in a shared repository any of its writers.
- **The network.** Without `global.okdp.network.policy` (off by
  default: the compiler knows neither where traffic enters nor whether
  the network plugin enforces a policy) no NetworkPolicy is written, and
  every pod reaches every project's. With it, a project's ingress is
  its namespace's, the platform's ingress namespaces' and its claimers';
  its egress, and the platform's own namespaces (`policy.privileged`),
  stay open: the namespace reached guards itself, and a project's
  services reach the API server, the identity provider and networks the
  compiler does not know. A network plugin that does not enforce
  policies (kind's default) leaves them inert.
- **Credentials under a name that does not say so.** A message redacts
  the values of keys named as credentials (`password`, `token`,
  `...Key`); another key's value, and a store key, are printed.
- **Credentials in clear allowed.** `Inputs.AllowPlainSecrets`
  (okdp-compile's `--allow-plain-secrets`) accepts them, for a caller
  that knows its deployments repository private.
- **The scope of the plain-secrets rule.** Refused: a `v1` Secret's `data`
  or `stringData`; an ExternalSecret's (a ClusterExternalSecret's)
  `target.template.data` literal (a non-empty string without `{{` nor
  `${`, a number) at a key `value.Sensitive` names, a reference to a
  credential (`existing...`, `...Ref`, `passwordKey`) excepted; a Fake
  generator's values. Warned of, not refused: such a literal at a
  sensitive key of an instance's upstream values, where a host runs the
  security set's `policy.PlainUpstream`. Not seen: a credential under
  another key name, in a template's literal or a template of a constant,
  in a ConfigMap, an annotation, an environment variable's `value`, any
  other kind or field, or in OKDP's defaults and the values file of a
  package; the key names are a guess, the review of a package and of the
  values its instances give is the rest.
- **Bounds the caller sets.** The CEL cost is bounded when the caller
  passes a limit (`Inputs.CostLimit`: `okdp-compile` does), one
  instance's time when it passes `Inputs.Timeout` (`okdp-compile` does);
  the files read are bounded by `okdp-compile`'s reader and by a host's,
  a library caller's `fs.FS` being its own.
- **The upstream schemas** are not checked by the compiler, which reads
  no upstream chart, nor by `okdp-compile` alone: a host may check them,
  in its Finalize, with the charts it fetched, and the manifests they
  render; else Helm does when it renders. A chart a host cannot fetch
  leaves its keys unchecked, and a host may refuse a project that is not
  privileged giving it upstream values when it renders.
- **The token of the CI.** Whoever can push a workflow to a repository
  of the sources reads the token of a host's job, which writes the
  output repository: the repositories of the sources are the platform's,
  their writers deploy already.
- **What is kept.** An object Helm keeps (a claim's database) and the
  Namespace of a project that goes outlive their release, with their
  data, until an administrator deletes them.
