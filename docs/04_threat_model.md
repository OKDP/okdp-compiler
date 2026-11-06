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
place in the tree ([repository layout](01_usage.md#repository-layout)) and
for its repository's role: a repository of the tree holds any part of it,
a catalog `catalog/` and `contracts/` only (`--catalog`,
`compiler.RoleCatalog`). `platform.yaml` is level 0, the platform's;
`project.yaml`, level 1, a project's; an instance's `connections.yaml`,
level 2, and its `config.yaml`, the instance values. Who writes each is
decided by the hosting of the repositories: their write access and their
reviews (a platform repository its administrators write, a project's its
team, a catalog its maintainers). Through the library, the caller decides
which layers are level 0 (`Inputs.Context`).

| Author | Writes | Reviewed | The compiler takes it as |
|---|---|---|---|
| [The platform](#the-platform-author-level-0) | `platform.yaml`, level 0 | with the platform | the platform's: every value, template code included |
| [A project](#a-project-author-level-1) | `projects/<project>/project.yaml`, level 1, and its instances' directories | by the project's owners | additions to the platform's, under rules |
| [An instance](#an-instance-author-level-2-and-configyaml) | `config.yaml`, `connections.yaml` (level 2), `instance.yaml` | a console user or a pull request | input |
| [A catalog](#a-catalog-author) | `catalog/<service>/<version>/`, `contracts/` | in the catalog's repository | reviewed, but for the rules below |
| [The engines](#the-engines) | apply `compiled/` | | the identity the targets give each project |
| [The secret store](#the-secret-store) | keeps the keys | | the keys the compiler writes |
| [okdp-gitops](#okdp-gitops-and-the-runner) | runs the compile, commits `compiled/` and a control plane's edits (`treeedit`) | | a caller |

## The platform author (level 0)

`platform.yaml` is the only layer the compiler takes as reviewed. It is
the only level whose values may hold template code (`{{`, which some
charts pass to `tpl`), but in a connection and in upstream values, which
are checked whatever their layer; the only one that sets `global.okdp`
but `provisioning`: the ingress, TLS, the OAuth clients and their chart
(`charts`), the proxy, the secret store (`secrets`) and the policy
(`policy`: the privileged projects, the upstream deny-list and
registries). Its connections name any key of the store, visible to every
project (a connection of level 0 is every project's), and its classes
route every namespace to their providers, consenting for them.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may | set any key of `global.okdp`, template code in its values | not checked | |
| may | reserve namespaces no project may have, its own components' (`policy.reserved`), a reservation winning over `policy.privileged` | `policy.go: checkPlatformNamespace`, `pipeline.go: Compile`; before a project is created, `compiler.CheckProjectName` | `testdata/context/0/reserved-namespace.yaml`, `reserved-and-privileged.yaml`, `testdata/cli/policy.txtar`, `TestCheckProjectName` |
| may | name the platform's own projects, `policy.privileged` | `policy.go: privileged` | `testdata/cli/policy.txtar` (a privileged project's ClusterRole written) |
| may | name its administrators' groups and the API server's group prefix (`access`): the bootstrap binds the administrators, the compiler writes nothing of them | `platform.schema.json`, `access.go: PlatformAdmins` | `TestPlatformAdmins`, `TestAccessCompile` |
| may | name the control plane's identity (`controlPlane.serviceAccount`), bound in each project's namespace to what it does there, its cluster-wide role reduced to `namespaces: get, create` | `target.ControlPlane` | `TestControlPlane`, `TestControlPlaneSetting` |
| may | isolate the projects' namespaces (`network.policy`): ingress to a project's pods from its namespace, the platform's ingress namespaces (`network.ingressNamespaces`) and its claimers' only; off by default, egress open | `target.NetworkPolicy`, `okdp.network` | `testdata/cli/network.txtar`, `TestNetworkPolicy`, `TestClaimsNetwork` |
| may | give a connection of any key of the store, `platform/<key>` of a ClusterSecretStore; with `secrets.provider`, a key a namespace reads, `<namespace>/<key>`, read through the project's connection | `connections.go: storeSecret` | `testdata/cli/secrets.txtar` (`platform/datalake`) |
| may not | give a connection a key under `platform/` when the platform gives `secrets.provider`: no namespace reads it, the platform's credentials are copied under the keys of the namespaces that use them | `connections.go: storeSecret` | `testdata/cli/secrets.txtar` (`provider`) |
| may | route any namespace to a provider of any namespace (a class) | `namespace.go: tree.consent` | `testdata/cli/claims.txtar` |
| may not | a platform chart (`charts`) over `http://`, or of a version range | `chart/ref.go: Ref.Check` | `testdata/context/0/http-chart-repository.yaml` |
| may not | a former setting of `global.okdp`, `certificateIssuers` or `serviceCatalog` (the menu is `catalogs.yaml`'s), which no compile reads | `platform.schema.json` (`false`) | `TestPlatformSchema` |
| may not | a `catalogs.yaml` the compile does not read: an unknown key, at any level, a value of another type, a catalog or a category named twice, a service listed twice, of no catalog, or of a default version its catalog lacks | `repository/catalogs.go: ParseCatalogs`, `checkCatalogs`; `treeedit.ReadCatalogRepositories`, `UpdateCatalog`, `CheckCatalogs` | `TestParseCatalogs`, `TestLoadCatalogsFile`, `TestCheckCatalogs` |
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
| may | give the identity provider's groups a role within the project, `access` (`viewer`, `editor`, `admin`; `okdp-project-<namespace>` an editor by default): a project team gets exactly its roles, those of its AppProject and of its namespace, nothing of another project's | `access.go: ParseAccess`, `target.Access`, `argocd.Policies` | `TestAccessCompile`, `TestAccess`, `testdata/targets/<target>/projects/demo/project/` |
| may not | an `access` of another form: a group's path (`/`), a space, a role that is none of the three; nor set it from another level than its file's (the platform's, an instance's `connections.yaml` or `config.yaml`) | `access.go: ParseAccess`, `layers.go: contextLayers`, `valuesLayers` | `TestParseAccess`, `TestAccessCompile` |
| may not | the platform's administrators or the API server's group prefix (`global.okdp.access`) | `layers.go: platformOnly` | `TestAccessCompile` |
| may not | any other key of `global.okdp`, known to the compiler or not, set by the platform or not: the chart of the OAuth clients, where they register, the ingress, TLS, the proxy, the store, the policy (making itself privileged) | `layers.go: platformOnly` | `testdata/context/1/sets-platform-charts.yaml`, `sets-platform-oidc.yaml`, `sets-platform-ingress.yaml`, `sets-platform-tls.yaml`, `sets-platform-proxy.yaml`, `sets-platform-secrets.yaml`, `sets-platform-policy.yaml`, `sets-unknown-platform-key.yaml` |
| may not | change a value, a connection, a class or a default class of a lower level | `layers.go: contextLayers` (`chart.Overlap`) | `testdata/context/1/redefines-platform-connection.yaml`, `redefines-platform-class.yaml` |
| may not | template code | `layers.go: contextLayers` (`chart.NoTemplate`) | `testdata/context/1/template-code.yaml` |
| may not | a connection of a store key outside `<project>/`: the platform's, another namespace's | `connections.go: storeSecret` | `testdata/context/1/remote-key-of-the-platform.yaml` |
| may not | a provider of another project without its consent, through a class of its own or by naming it | `namespace.go: tree.consent` | `testdata/cli/claims.txtar` |
| may not | keep a declaration unchecked by having no instance: its named claims (class, request, name) and its connections checked as an instance's compile checks them, the tree refused | `repository.Load`, `okdp.CheckDeclarations` | `testdata/cli/layout.txtar`, `TestValidateClaim` |
| may not | the name `platform`, the segment of the platform's keys | `policy.go: checkNamespace`, `pipeline.go: Compile`; before a project is created, `compiler.CheckProjectName` | `testdata/cli/secrets.txtar`, `TestCheckProjectName` |
| may not | a reserved namespace: `kube-*`, Kubernetes' own; `flux-system`, `argocd`, `okdp-releases`, the engines' and the targets' (its releases, with every verb of the namespace, would rewrite the engine's sources and the other projects' identities); another namespace a target writes in (the flux target's `Source`); those of `policy.reserved`, privileged or not | `policy.go: checkNamespace`, `checkPlatformNamespace`, `target.OwnNamespaces`; before a project is created, `compiler.CheckProjectName`, the compile's two | `testdata/cli/policy.txtar` (`reserved`), `TestCompileErrors`, `TestCheckProjectName`, `TestOwnNamespaces` |

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
| may not | a file in its directory but `instance.yaml`, `config.yaml` and `connections.yaml` (a `values.yaml` no compile reads) | `repository.go: instanceFiles` | `TestInstanceFiles` (`instance_test.go`), `testdata/cli/layout.txtar` |
| may not | template code, in a key or a string | `layers.go: valuesLayers` (`chart.NoTemplate`) | `testdata/template-code.yaml` |
| may not | a parameter the schema refuses | `chart.Parameters` | `testdata/schema-violation.yaml` |
| may not | `global` or a path its package protects in upstream values | `chart/merge.go: applyUpstream` | `testdata/protected-upstream.yaml` |
| may not | a key of a pod's privileges, security context, identity, containers or objects in upstream values, in a string read as YAML too | `policy.go: upstreamCheck` | `testdata/denied-upstream.yaml`, `denied-upstream-text.yaml` |
| may not | an image of none of the platform's registries, set as an `image` string, a `registry` and a `repository`, or by a key that names one (`initImage`, `imageRegistry`, `defaultImageRegistry`, `imageName`, `...ImageRepository`) | `policy.go: upstreamCheck`, `namesImage` | `testdata/context/0/unlisted-registry.yaml`, `unlisted-image-registry.yaml`, `unlisted-default-image-registry.yaml`, `unlisted-image-name.yaml`, `unlisted-init-image.yaml`, `unlisted-sidecar-image-repository.yaml` |
| may not | a connection of another contract, a field the contract does not declare, a secret field | `connections.go: resolver.resolve`, `contract.fields` | `testdata/context/2/wrong-contract.yaml`, `unknown-field.yaml`, `secret-in-connection.yaml` |
| may not | a connection that does not hold at its declaration, referenced or not, its own or its project's (a name, a contract, a field, a required field, a type, a `secretRef`) | `connections.go: declaredConnections`, `namespace.go: CompileAll` | `TestCompileAllDeclaredConnections`, `testdata/cli/layout.txtar` |
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
| may not | hold `platform.yaml` or `projects/` in its repository, a catalog's (`--catalog`, `RoleCatalog`): a project, privileged or in a reserved namespace, or the platform's values, added from a catalog; nor a repository given without a role | `repository.go: index` | `testdata/cli/repository.txtar` (`--catalog`), `TestLoadRoles` |
| may not | declare `global.okdp` in its `config.schema.json` or give it a default (`properties.global.default`): its defaults would set the platform's settings (the privileged projects, the store, the charts); the compiler reads them of the context's layers alone, whatever the parameters hold | `chart/parameters.go: ReadParameterSchema`, `layers.go: contextLayers`, `context.go: parameters` | `testdata/cli/policy.txtar` (`schema-okdp`, `schema-default`), `TestPlatformFromContext` |
| may not | an object of another namespace, for a project that is not privileged, whatever its source | `policy.go: checkObjects` | `testdata/cli/policy.txtar` (an ExternalSecret of another namespace) |
| may not | an object of a cluster-scoped kind, for a project that is not privileged | `policy.go: checkObjects` | `testdata/cli/policy.txtar` (a ClusterRole) |
| may not | an object of the deployer engines' API groups (Flux's toolkit, the Flux Operator's `fluxcd.controlplane.io`, `source.extensions.fluxcd.io`, Argo CD's `argoproj.io`), for a project that is not privileged: it would drive the engine with the engine's identity | `policy.go: checkObjects`, `engineGroups` | `testdata/cli/policy.txtar` (a HelmRelease), `TestObjectsPolicy` |
| may not | a dependency of key `global`, its values Helm's global values, which every chart of the umbrella reads | `chart/metadata.go: Metadata.Check` | `TestMetadataCheck` |
| may not | a Secret of the objects with `data` or `stringData`, unless the caller allows it | `policy.go: checkPlainSecrets` | `testdata/cli/plain-secrets.txtar` (an objects' Secret) |
| may not | an ExternalSecret (or ClusterExternalSecret) whose `target.template.data` holds a literal at a sensitive key, an `okdpGeneratedSecret` `stringData`'s included, nor a Fake generator (or ClusterGenerator of kind Fake) with values, unless the caller allows it | `policy.go: checkPlainSecrets`, `credentialLiteral`, `credentialReference` | `TestPlainSecrets` |
| may not | a `provides` secret without `generate`, unless the caller allows it | `provides.go: credentialsSecret` | `testdata/cli/plain-secrets.txtar` (a `provides` secret) |
| may not | a dependency over `http://`, a version range | `chart/ref.go: Ref.Check` | `testdata/cli/chart-sources.txtar` |
| may not | values for a chart `Chart.yaml` does not declare, an OAuth client chart of its own | `objects.go: addOwnCharts`, `chart.FinalValues` | `TestDcrCompile` |
| may not | a release named like an object the platform writes in every namespace (`okdp-project`, `okdp-control-plane`, `okdp-access-*`, the secret store, its ServiceAccount, the DCR token's Secret), or the beginning of one up to a `-`: the project `okdp`'s instance `project` would write the Role `okdp-project` that confines every release of the namespace | `release.go: checkRelease`, `PlatformObjectNames` | `TestReleaseNamedLikeAPlatformObject`, `TestPlatformObjectNames` |
| may not | an OAuth client's Secret that is not the release's, `<release>-<client name>` exactly (`<release>-oidc` for the main client), not any name that starts with `<release>-`: that prefix is the instances' `web-x` as much as `web`'s | `oidc.go: oidcClients` | `testdata/cli/oidc.txtar` |
| may not | a file outside the package, a schema `$ref` fetched | `fs.FS`, `chart.CompileSchema` | `TestCompileSchemas` |
| may not | a connection reference out of the compile's reach (under `allOf`, `$defs`, `additionalProperties`...), which a reader of the schema, a console, would take for one the compile resolves and checks | `connections.go: requirements` (`schemaRefs`) | `TestRequirementsOutOfReach` |
| may not | a Secret's name for its credentials (`provides[].secretRef.name`) that is not a DNS-1123 subdomain | `mapping.schema.json`, `provides.go: provided` | `TestProvidesErrors` |
| may not | a Secret of a claimer's namespace another object writes, through its claimers' copies, `<release>-<secretRef.name>`, or two objects of one namespace that write one Secret or one key of the store, an item of a `List` among them | `namespace.go: tree.refuseSecretTargets`, `policy.go: eachObject` | `testdata/cli/secrets.txtar`, `TestCompileAllSecretTargets` |
| may not | expressions past the cost limit, a host function's call counted by the bytes it reads and writes, or that read the cluster, the environment or the time | `compile.Session`, `hostFunctions`, `hostCost` | `testdata/cli/limits.txtar`, `TestHostCostBytes` |
| may not | amplify its input past the cost limit or the size of a document: a `$for`'s elements and what its body builds counted in the cost, a document's values 32 MiB at most whatever the limit, each copy of a value counted | `compile.Session`: `charge`, `grow`, `memSize` | `TestLoopCost` |

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
| People | the roles of its groups (`access`): Argo CD roles of its AppProject, its Applications only, no `exec`; RoleBindings of its namespace to `view`, `edit`, `admin` | the same |
| Control plane | when the platform names its identity (`controlPlane.serviceAccount`): the Role `okdp-control-plane` of the namespace, what the server does there, Secrets by name and never listed | the same |
| Network | with `network.policy`: the NetworkPolicy `okdp-project`, ingress from the namespace, the platform's ingress namespaces and the namespaces of the claims it provides, egress open; written for a project kept too, of today's settings and claims | none: the platform's own |
| Kept | the Namespace when the project goes (Flux's prune label, Argo CD's `Delete=false`); the objects Helm keeps, Argo CD's `Prune=false,Delete=false` (`keep.go: keepSynced`) | the same |

A chart that writes another namespace or a ClusterRole is refused by
the API server (Flux) or by Argo CD, not by the compiler; Pod Security
Admission refuses a privileged pod. A privileged project escapes all of
it: the objects policy, the upstream policy, the confined identity and
the baseline. The files are compared with `testdata/targets/<target>/`
(`TestTargets`): `demo` confined, `data` privileged.

The targets' objects in the engines' namespaces are named after the
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
| may not | a project of a namespace a target writes in (`okdp-releases`, `argocd`, Flux's `Source`'s): the engine's objects there, rewritten with the project's every verb | `policy.go: reservedNamespace`, `target.OwnNamespaces` | `TestOwnNamespaces`, `testdata/cli/policy.txtar` |
| may not | an object of the platform's of the same name as a release's in the engines' namespaces, when the platform names its own with a dot | the platform's naming rule, not the compiler | |

## The secret store

The compiler writes the keys and the objects that read and push them;
the store's authorisation keeps the namespaces apart ([what the store
must enforce](specs/01_inputs_outputs.md#what-the-store-must-enforce)):

| Key | Written by | Read by |
|---|---|---|
| `<provider namespace>/<claim ID>` | the provider's PushSecret (`transfer.go: pushSecrets`) | the claimers' ExternalSecrets (`pullSecret`), the claim ID starting with their namespace |
| `<namespace>/<key>` | the namespace's PushSecrets, an administrator | the connections of the namespace's project and instances (`storeSecret`) |
| `platform/<key>` | the platform, as the store's operator | the platform's connections (level 0), through a ClusterSecretStore only |

With `secrets.provider`, the store is a SecretStore of each namespace,
authenticating with its ServiceAccount (`secrets.serviceAccount`,
`okdp-secrets`): one templated policy (OpenBao, Vault) gives each
namespace write on `<namespace>/*` and read on `+/<namespace>.*`, and
nothing on `platform/`: the platform's credentials are copied under the
keys of the namespaces that use them (`<namespace>/<key>`, the sandbox's
`<namespace>/s3/platform`), and a connection naming a `platform/` key is
refused. A connection of level 0 is visible to every project: its key is
read by each project's instances as that project, so one that is not a
`platform/` key serves the namespace whose segment it is under, and no
other. Without a provider, the store is one ClusterSecretStore, one
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
| may not | have a chart pulled over plain http, from a repository, an archive the index names or a redirect, but on a host of `InsecureHosts`, nor redirected from https to http, by an OCI registry too | `RepositoryPuller.check`, `checkRedirect` | `TestIndexIsHTTPS`, `TestPullerDialsPublicAddresses` |
| may not | make the puller reach the platform's network (a blind request from the CI or the control plane to an RFC 1918, loopback or link-local address, the cloud metadata endpoint): a registry, a token realm, a redirect, an index or an archive at one refused as dialed, but for a host of `PrivateHosts` or `InsecureHosts`; behind a proxy (`$HTTPS_PROXY`), which would connect to any address, the destination is resolved here and refused at such an address before the proxy is dialed, a name this host cannot resolve refused (the proxy resolves it again: one that answers differently to it is the proxy's to refuse) | `RepositoryPuller.dial`, `proxyFor`, `publicAddress`, oras's `BlockedRealmPrefixes` | `TestPullerDialsPublicAddresses`, `TestPullerChecksTheDestinationBehindAProxy` |
| may not | have the puller accept a certificate no trusted CA signed: the system's, or the pool given to `SetRootCAs` (a platform's CA, for a registry of the cluster), never a check turned off | `RepositoryPuller.SetRootCAs`, net/http's TLS verification | `TestPullerRootCAs` |
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
| may not | exhaust the chart cache's reads with the charts its packages name, refusing another project's instances: each chart's files 64 MiB together, the cache unbounded but by its charts | `internal/dirfs: NewCharts`, `chartcache.Cache.FS`, `--charts` | `TestChartsBounds`, `TestEachChartHasItsBudget` |
| may not | make it run without end, or hold the tree for long: the CEL cost of an instance's expressions is bounded (the default limit about ten times the dearest case of the catalogs), one expression may cost what is left of the limit, not the limit again, one instance's compile is bounded in time and refused alone past it, the whole compile too | `--cost-limit`, `--instance-timeout`, `--timeout` (`compile.Session`: `program`, `okdp.Compile`: `Inputs.Timeout`) | `testdata/cli/limits.txtar`, `TestSession`, `TestCompileTimeout` |
| may not | make it delete an output it did not write, or write through a symbolic link of the output, at any depth under `projects/`, in the output or out of it (every link there removed before a file is written) | `main.go: checkOutput`, `writeTree`, `removeLinks` | `testdata/cli/output.txtar`, `TestWriteTree` |
| may not | have a credential printed in a message, under a key that names one | `value.Short`, `value.ShortAt`, `chart.Validate` | `testdata/cli/errors.txtar` |
| may not | have one project's failure keep the others from deploying (`--partial`): a refused instance, a release name it shares with another project, a file of its own that does not load (a stray file, an unknown key, a `project.yaml` that does not parse), each refusing that project and keeping its output | `repository.go: LoadProjects`, `compiler.Partial` (`internal/tree/partial.go`), `compiler.TreeFiles` (`internal/output`) | `testdata/cli/partial.txtar`, `testdata/cli/kept.txtar`, `testdata/cli/load.txtar`, `TestLoadProjects`, `TestAReleaseCollisionKeepsItsProjects` |
| may not | make `--partial` read a file out of the output through a project kept (a symbolic link), or write a release a kept project's output holds in another namespace | `openDir` of `--output`, `compiler.TreeFiles` | `testdata/cli/kept.txtar` |
| may not | keep a privilege revoked, a role taken back or a policy tightened by keeping one instance of a project refused (`--partial`): the kept project's Namespace, identity, AppProject, RoleBindings and Flux HelmReleases are of today's settings, its instances kept applied with them | `output.go: rederive`, `target.Keeper` (`flux.Target.Kept`, `argocd.Target.Kept`), `okdp.ProjectOf` | `testdata/cli/partial.txtar` (`ops`), `TestPartialRederives`, `TestFilesRederive`, `TestKept` |
| may not | keep a project in a namespace reserved after it was written, by the platform (`policy.reserved`) or by the compiler (a release that reserves a name): a project kept by `--partial` whose namespace is none a project may have today is dropped from the output, nothing of its Role, Namespace, access or releases kept nor re-derived, said so; the tree does not fail for it | `access.go: CheckProject` (`ProjectOf`, one view with `Compile`), `output.go: rederive`, `Kept.Dropped` | `testdata/cli/reserved.txtar`, `TestProjectOfNamespace`, `TestFilesDropped` |

A control plane edits the tree with package `treeedit`, okdp-gitops
committing each edit: whatever the API in front of it, an edit holds
these lines.

| | Rule | Enforced by | Fixture |
|---|---|---|---|
| may not | address a directory other than its own through a name, a dot segment joined into a path (an instance `..`, the project, removed with every instance) | `treeedit: checkRef`, `ValidateName` | `TestDotSegmentNamesAreNotFound` |
| may not | write a change after which an instance it writes, or one that compiled before, does not compile, nor remove what an instance uses | `treeedit: Check.Verify` (`compiler.AddedProblems`, `compiler.Regressions`) | `TestAnEditThatDoesNotCompileIsRefused`, `TestARemovalRefusesWhatIsInUse` |
| may not | create an instance whose release another project's instance has, nor a project of a name the compile refuses | `Check.Verify` (`compiler.ReleaseTaken`), `treeedit.CheckProjectName` | `TestCreateRefusesACollidingReleaseName`, `TestAProjectOfAReservedNameIsRefused` |

## What the compiler cannot enforce

- **Who writes a file.** The compiler trusts a file for its place in the
  tree and its repository's role, which the caller gives: a catalog given
  as a repository of the tree (`--repository`, `RoleTree`) adds projects
  all the same. In one repository, whoever may write a project's directory
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
  a namespace are refused, not a Secret an upstream chart renders. The
  names of a release's objects start with `<release>-` (`okdpFullname`),
  a separator a release holds too: the instances `a` and `a-b` of a
  project share the prefix of `<namespace>-a-b-...`, and the first may
  name an object of the second's; a separator no release holds would
  rename every package's objects. What the compiler decides is limited
  to what it writes itself: an OAuth client's Secret is the release's
  exactly, and a release is none of the platform's objects' names.
- **The OAuth clients a namespace may register.** With
  `oidc.dcr.authMethod: initialAccessToken`, the registration Jobs read
  the initial access token of their namespace (`dcr.tokenSecret`, key
  `token`, `okdp-dcr-token` by default): whoever reads that Secret, a
  project's editor with Kubernetes' `edit` among them, registers OAuth
  clients at the identity provider with it. The compiler cannot bound
  what a client asks for (its redirect URIs, scopes, grants); the
  identity provider's client registration policies, and the token's
  expiry and use count, are the boundary.
- **The store's policy.** The compiler writes the keys; it cannot check
  that the store scopes them. A ClusterSecretStore, without
  `secrets.provider`, is one trust domain. A Kubernetes namespace named
  `platform` made outside the tree (by an administrator, by a privileged
  project) writes `platform/*` under the templated policy, which reads
  none: the compiler refuses only a project of that name. Likewise a
  namespace of the platform's components that `policy.reserved` does not
  list: the compiler knows Kubernetes', the engines' and the store's
  only.
- **Who changes a project's access.** `access` is in `project.yaml`:
  whoever writes it gives the roles, its repository's writers (the
  split topology, the project's administrators), every writer of a
  shared repository. The compiler names the groups; the identity
  provider decides who is in them, the API server's OIDC settings and
  Argo CD's how the groups claim reaches them, and the bootstrap binds
  the platform's administrators.
- **Consent is in the provider's own file.** The `claimers` of a
  provider's `instance.yaml` are written by whoever writes that
  instance's directory, in a shared repository any of its writers.
- **The network.** Without `global.okdp.network.policy` (off by
  default: the compiler knows neither the ingress controller nor whether
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
- **Credentials in clear allowed.** `--allow-plain-secrets`
  (`Inputs.AllowPlainSecrets`) accepts them, for a caller that knows its
  deployments repository private.
- **The scope of the plain-secrets rule.** Refused: a `v1` Secret's
  `data` or `stringData`; an ExternalSecret's (a ClusterExternalSecret's)
  `target.template.data` literal (a non-empty string without `{{` nor
  `${`, a number) at a key `value.Sensitive` names, a reference to a
  credential (`existing...`, `...Ref`, `passwordKey`) excepted; a Fake
  generator's values. Warned of, not refused: such a literal at a
  sensitive key of an instance's upstream values (`plainUpstream`,
  `TestPlainUpstream`). Not seen: a credential under another key name,
  in a template's literal or a template of a constant, in a ConfigMap,
  an annotation, an environment variable's `value`, any other kind or
  field, or in OKDP's defaults and the values file of a package; the
  key names are a guess, the review of a package and of the values its
  instances give is the rest.
- **Bounds the caller sets.** The CEL cost is bounded when the caller
  passes a limit (`Inputs.CostLimit`: the CLI and okdp-gitops do), one
  instance's time when it passes `Inputs.Timeout` (the CLI does); the
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
