# The rule set

The compiler's rule set says what a package's mapping may call, how OKDP
names its objects and what a compile refuses of a platform. It is data,
one set, the mandatory set, embedded in this module
([`internal/rules/mandatory`](../../internal/rules/mandatory)) and read
once (`compiler.Mandatory()`): every compile applies it.
`compiler.LoadRules` adds a platform's own set to it, and replaces none
of its rules. This page is the reference of the set: every table, check,
schema, function and document, where each is applied, and how a set
added to it extends it. The hosts a project serves, the OAuth clients,
the hardened pod and the policy on upstream values are rules of the
security set, added this way: [its
reference](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/04_rules.md).

- [How the sets add up](#how-the-sets-add-up)
- [Who applies it](#who-applies-it)
- [What is not in the rule set](#what-is-not-in-the-rule-set)
- [When each rule applies](#when-each-rule-applies)
- [Tables](#tables)
- [Checks](#checks)
- [Hooks](#hooks)
- [What a host adds](#what-a-host-adds)
- [Platform settings](#platform-settings)
- [What a mapping sees as platform](#what-a-mapping-sees-as-platform)
- [What a mapping writes](#what-a-mapping-writes)
- [Functions](#functions)
- [The secret store](#the-secret-store)
- [Names, labels and charts](#names-labels-and-charts)
- [What a platform can change](#what-a-platform-can-change)
- [Changing a rule](#changing-a-rule)

```text
internal/rules/mandatory/     what every compile applies
  rules.yaml                  its tables, and every file below by name
  scope.cel.yaml              what a mapping sees as platform
  schemas/
    platform.schema.json      global.okdp, the settings
    mapping.schema.json       what a mapping writes, and the functions' arguments
  checks/                     one file per check
  fragments/
    generated-secret.cel.yaml okdpGeneratedSecret
  objects/
    secret-store.cel.yaml     the platform's secret store
    push-secret.cel.yaml      the PushSecret of a claim of another namespace
    pull-secret.cel.yaml      the ExternalSecret that reads it
    deployer-role.cel.yaml    the Role of a project's releases
```

Every rule file this page names is of this tree, at its relative path;
`rules.schema.json` is beside it, in `internal/rules`.

`rules.yaml` is `apiVersion: okdp-compiler/rules.v1`, `name: core`,
`version: 1.0.0`. `compiler.LoadRules` (`okdp-compile --rules`) reads a
platform's set, `rules.yaml` at its root, adds it to the mandatory set
and fails closed. It refuses a file that does not meet its
`rules.schema.json`, another major `apiVersion`, a set without a `name`
or of a name a set read before has, a `version` that is not
`MAJOR.MINOR.PATCH` with an optional `-prerelease`, a rule the mandatory
set gives (`a rule set adds rules, it replaces none`): a table, a check,
a key of a schema, a chart, a document, a scope document, a function, a
template, a section, `names`, `labels` or `keepSync`; a check that is
unknown, values added to a table that is not extensible, and a file it
names that does not exist or does not compile. What a platform's set
adds is its own tables, documents, hooks, functions, templates, sections
and `ownObjects`, values to the extensible tables of the mandatory set,
keys of the schemas and scope documents
([how the sets add up](#how-the-sets-add-up)).

## How the sets add up

The sets apply together, the mandatory set first, and each part of an
added set stands beside the mandatory set's. Three parts add to what
the sets read before give.

**Schemas.** `schemas.platform` and `schemas.mapping` of an added set
are merged into the schema of that name, then the merged document is
parsed once (`chart.ParseSchema`, never Helm's schema validation). The
merge goes through `properties` and `$defs`, at any depth. A key the
schema does not hold is added, with its defaults, which apply as the
mandatory schema's do. A key both hold is merged further when the
mandatory one has `properties` or `$defs`; otherwise it is a leaf, and
refused. Any other keyword of an added node that is merged must be the
mandatory node's, with the same value: an added schema changes no
`required`, `additionalProperties`, `type` or `default`. A key may so
join a map the mandatory schema closes (`additionalProperties: false`),
and that map refuses every other key as before. The error names the key
or the keyword by its JSON pointer, as in `schemas: platform:
#/properties/global/properties/okdp/properties/policy/additionalProperties:
given by another rule set`.

**Scopes.** `scopes` lists the documents, names of `documents` of the
set or of a set read before, that compute what a mapping sees as
`platform`. The compiler evaluates those of every set, in the order the
sets are read, each with the same input (`settings`, `release`), and
merges their `values.platform` key by key. A value two
documents give at one path, unless both are maps, is an error that names
both: `the rule set: scopes: platform.<path> is given by both <first>
and <second>`. A document is listed once. With the mandatory set alone,
`platform` is what its `scope` gives.

**Names.** Each set names itself: the mandatory set is `core`, and a set
added to it gives a `name`, a DNS label, that no other set has.
`RuleSet.Names` lists them in the order they are read,
`RuleSet.SetVersion` gives the version of each and `RuleSet.Version`
that of the last set read. A platform states the sets it requires in
`global.okdp.ruleSets` ([platform settings](#platform-settings)).

## Who applies it

Every compile: `Inputs.Rules` nil stands for the mandatory set, and a
set of `LoadRules` holds it. A host gives its compiles a set through
`Inputs.Rules`, or nil, and reads the packages for it
(`compiler.LoadDefinition`, `LoadInputs`). `okdp-compile` gives the
mandatory set, with the sets of `--rules` added. A host may
give the mandatory set with the security set added, or the set its
caller gives.

A program that calls `compiler.CompileAll` or `compiler.Compile` itself, this
module's `okdp-compile` among them, applies the sets it gives, their
checks and hooks among them, but not what a host adds through
`Inputs.Finalize` or checks of the layers it gives ([what a host
adds](#what-a-host-adds)).

## What is not in the rule set

These stay in the compiler's Go code and apply whatever the rule set:

- a release, a namespace and an instance name must be DNS names;
- claim binding, classes, consent across namespaces and claim IDs;
- no template code in the upstream values, in a connection nor in what
  a mapping receives of one (`chart.NoTemplate`);
- the roles of a group (`ParseAccess`, the `access` of a `Project`
  document): `viewer`, `editor`, `admin`;
- no two objects of one group, kind, namespace and name
  (`checkDuplicateObjects`);
- `okdpFullname`, `okdpSqlName`, `okdpObjectName`, and the walkers that
  call the checks below.

What each role may do is not in the rule set either: the host that
writes a namespace's roles (Kubernetes' ClusterRoles, Argo CD's
policies) fixes it. Nor is what a values layer may set: the compiler
merges every layer of `Inputs.Values` alike, any of them setting
parameters, upstream values, `global`, `global.okdp`, `connections` or
`claims`. A host that gives layers of several origins decides what each
may set ([the threat
model](../04_threat_model.md#what-a-host-checks-of-the-layers)).

## When each rule applies

Each check reports its first failure only, and a failure refuses the
instance. A project is privileged when its namespace is listed in
`global.okdp.policy.privileged`. `AllowPlainSecrets` is
`Inputs.AllowPlainSecrets`, `--allow-plain-secrets` of `okdp-compile`.

| Rule | Reads | Runs in | Privileged project | `AllowPlainSecrets` |
|---|---|---|---|---|
| `namespace` | the namespace | compiler | applies | applies |
| `platformNamespace` | the namespace | compiler | applies: a reservation wins over a privilege | applies |
| `release` | the release | compiler | applies | applies |
| `object` | each object, items of a List too | compiler | skipped | applies |
| `plainSecret` | each object, items of a List too | compiler | applies | skipped |
| hook `objects` | each object, items of a List too, after `object` | compiler | skipped | applies |
| hook `finalValues` | the final values of each chart key | compiler, in or after `Inputs.Finalize` | skipped | applies |
| a list document of `ownObjects` | the items of a section | compiler | as the document reads `privileged` | applies |

"Objects" are all those an instance produces: the `objects` section, the
objects of a set's sections (`ownObjects`), the credentials Secrets,
generated Secrets, PushSecrets and ExternalSecrets.

What a host runs through `Inputs.Finalize`, or checks of the layers it
gives, applies only where that host compiles ([what a host
adds](#what-a-host-adds)).

`namespace` and `platformNamespace` also run when a namespace is
created (`compiler.CheckNamespace`, for a host that creates namespaces),
with `namespace` in place of `Inputs.Namespace` in the message.

## Tables

A check reads a table as `sets.<name>` or `tables.<name>`. Go reads it
through `RuleSet.Has` or `RuleSet.List`.

The mandatory set gives the tables below, and no other: `LoadRules`
refuses a name it does not know there. A set added to it gives tables of
any name that no set read before gives, read the same ways. It adds
values to a table of the mandatory set only when that table is marked
`extensible: true`, by `add`:

```yaml
tables:
  colours: {values: [red, green]}            # a table of its own
  platformObjectNames: {add: [extra-object]} # values added, at the end
```

The extensible tables are those where more values mean more refusals:
`engineNamespaces`, `clusterScopedKinds`, `deployerApiGroups`,
`platformObjectNames` and `platformObjectSettings`. `add` on any other
table, or on a table no set read before gives, is an error; `values` on a
table a set read before gives is one too (`taken`). A value added twice
is kept once.

### engineNamespaces

`argocd`, `flux-system`, `okdp-releases`.

No project may have these namespaces (check `namespace`). A project's
releases there would rewrite the engine's sources, its Applications or
HelmReleases, or other projects' identities.

### clusterScopedKinds

An object whose `<group>/<kind>` is listed is refused to a project that
is not privileged (check `object`). The group is that of `apiVersion`,
empty for the core group. A kind not listed is assumed namespaced: the
engine's identity, confined to the namespace, refuses it in the cluster.

```text
/Namespace
/Node
/PersistentVolume
storage.k8s.io/StorageClass
storage.k8s.io/CSIDriver
storage.k8s.io/CSINode
storage.k8s.io/VolumeAttachment
storage.k8s.io/VolumeAttributesClass
rbac.authorization.k8s.io/ClusterRole
rbac.authorization.k8s.io/ClusterRoleBinding
apiextensions.k8s.io/CustomResourceDefinition
admissionregistration.k8s.io/ValidatingWebhookConfiguration
admissionregistration.k8s.io/MutatingWebhookConfiguration
admissionregistration.k8s.io/ValidatingAdmissionPolicy
admissionregistration.k8s.io/ValidatingAdmissionPolicyBinding
admissionregistration.k8s.io/MutatingAdmissionPolicy
admissionregistration.k8s.io/MutatingAdmissionPolicyBinding
scheduling.k8s.io/PriorityClass
networking.k8s.io/IngressClass
networking.k8s.io/IPAddress
networking.k8s.io/ServiceCIDR
node.k8s.io/RuntimeClass
apiregistration.k8s.io/APIService
certificates.k8s.io/CertificateSigningRequest
certificates.k8s.io/ClusterTrustBundle
flowcontrol.apiserver.k8s.io/FlowSchema
flowcontrol.apiserver.k8s.io/PriorityLevelConfiguration
policy/PodSecurityPolicy
resource.k8s.io/DeviceClass
resource.k8s.io/ResourceSlice
snapshot.storage.k8s.io/VolumeSnapshotClass
snapshot.storage.k8s.io/VolumeSnapshotContent
gateway.networking.k8s.io/GatewayClass
external-secrets.io/ClusterSecretStore
external-secrets.io/ClusterExternalSecret
external-secrets.io/ClusterPushSecret
generators.external-secrets.io/ClusterGenerator
cert-manager.io/ClusterIssuer
trust.cert-manager.io/Bundle
kyverno.io/ClusterPolicy
kyverno.io/ClusterCleanupPolicy
templates.gatekeeper.sh/ConstraintTemplate
keda.sh/ClusterTriggerAuthentication
cilium.io/CiliumClusterwideNetworkPolicy
crd.projectcalico.org/GlobalNetworkPolicy
projectcalico.org/GlobalNetworkPolicy
security.openshift.io/SecurityContextConstraints
quota.openshift.io/ClusterResourceQuota
argoproj.io/ClusterWorkflowTemplate
```

### deployerApiGroups

`helm.toolkit.fluxcd.io`, `kustomize.toolkit.fluxcd.io`,
`source.toolkit.fluxcd.io`, `notification.toolkit.fluxcd.io`,
`image.toolkit.fluxcd.io`, `fluxcd.controlplane.io`,
`source.extensions.fluxcd.io`, `argoproj.io`.

An object of one of these groups is refused to a project that is not
privileged (check `object`): it would drive the engine with the engine's
own identity.

### platformObjectNames

`okdp-project`, `okdp-control-plane`, `okdp-access-viewer`,
`okdp-access-editor`, `okdp-access-admin`, `okdp-secrets`.

The platform writes objects of these names in every project's namespace.
A release may not take one (check `release`). Extensible.

### platformObjectSettings

`secrets.store`, `secrets.serviceAccount`.

Paths under `global.okdp` of settings that name an object the platform
writes in every project's namespace. Check `release` takes their values
as it takes `platformObjectNames`. Extensible.

## Checks

A check is a document of `vars` and `errors` in the mapping language. It
sees its inputs, `sets` and `tables`, and these functions besides the
language's: `dig`, `pathKey`, `merge`, `rule`, `asList`, `asMap`,
`truthy`, `keys`, `sensitive`, `credentialReference`,
`credentialLiteral`, `leaves`, `fields` and `urlParse`. Checks run in a
session of their own, so their cost does not count against a package's
limit. The inputs are a closed list (`ruleInputs`, `check.go`): `added`,
`arg`, `at`, `field`, `host`, `items`, `key`, `listed`, `name`, `ns`,
`object`, `platform`, `privileged`, `raw`, `release`, `settings`,
`suffix`, `svc`, `value`.

| Function | Returns |
|---|---|
| `leaves(v)` | every scalar under `v`, null among them, as a list of `{path, value}`: depth first, map keys sorted, list items in order, a list index an int of `path`; an empty map or list gives nothing |
| `fields(s, seps)` | `s` split on any character of `seps`, empty fields dropped |
| `urlParse(s)` | `{ok, scheme, host, user}` of Go's `url.Parse`: `host` without its port, `user` whether user information is given; `ok` false, the rest empty, when it fails |

`rule(name, input)` evaluates another document on `input` alone, which
must pass it `sets` and `tables` if it reads them. When that document
refuses, the caller refuses with its message, as written.

The checks are the compiler's own, one per name below: a set added gives
none. Its own refusals are documents, which its hooks, `rule` or a host's
Go (`RuleSet.Refuse`, any document by name) call.

Below, `...` shortens a message; the full text is in the check's file.

### namespace

Input: `ns`. `checks/namespace.cel.yaml`. A DNS label check comes first,
in Go. `default` is allowed.

| Refused when | The reason given |
|---|---|
| `ns` is `platform` | `platform/ holds the platform's keys of the secret store, which no project writes` |
| `ns` starts with `kube-` | `kube-* is Kubernetes' own (kube-system, kube-public, kube-node-lease)` |
| `ns` is in `engineNamespaces` | `a deployer engine's, or the namespace its target writes in (...)` |

The message is `<field> "<ns>": the namespace is reserved: <reason>`.

### platformNamespace

Input: `field`, `ns`, `settings`. `checks/platform-namespace.cel.yaml`.

| Refused when | Message |
|---|---|
| `ns` is in `policy.reserved` | `<field> "<ns>": the namespace is reserved by the platform (global.okdp.policy.reserved): no project has it` and, when `policy.privileged` lists it too, that the reservation wins |

### release

Input: `field`, `release`, `settings`. `checks/release.cel.yaml`.

The names taken are `platformObjectNames` plus the values of the
settings that `platformObjectSettings` lists: `secrets.store` and
`secrets.serviceAccount`, and those a set added to it lists.
A package names its objects `<release>` and `<release>-<x>`, so a release
is refused when it equals a taken name or is the part before a `-` of
one: `okdp` is refused because of `okdp-project`.

| Refused when | Message |
|---|---|
| a taken name `n` has `n == release` or starts with `release + "-"` | `<field> "<release>": the name of an object the platform writes in every namespace (<n>), or its beginning up to a "-": ...` |

### object

Input: `svc`, `ns`, `object` (`id`, `kind`, `name`, `apiVersion`, `group`,
`ns`, `raw`). `checks/objects.cel.yaml`.

| Refused when | Message starts with |
|---|---|
| `<group>/<kind>` is in `clusterScopedKinds` | `<svc>: objects: <Kind>/<name>: a cluster-scoped kind (<apiVersion>): ...` |
| the group is in `deployerApiGroups` | `<svc>: objects: <Kind>/<name>: an object of the deployer's kinds (<apiVersion>) lets a project drive the platform's engine: ...` |
| a core `Service` sets a non-empty `spec.externalIPs` | `<svc>: objects: Service/<name>: spec.externalIPs claims addresses the cluster forwards to the Service ...` |
| `metadata.namespace` is set, not empty and not the instance's | `<svc>: objects: <Kind>/<name>: namespace <x>, not the instance's (<ns>): ...` |

### plainSecret

Input: `svc`, `object`. `checks/plain-secret.cel.yaml`. A message never
prints the value.

| Refused when | The plain value named |
|---|---|
| a core `Secret` has a non-empty `data` | `data` |
| a core `Secret` has a non-empty `stringData` | `stringData` |
| an `ExternalSecret` has, in `spec.target.template.data`, or a `ClusterExternalSecret` in `spec.externalSecretSpec.target.template.data`, a literal at a sensitive key | the key's path |
| a `Fake` generator has a non-empty `spec.data`, or a `ClusterGenerator` of `spec.kind: Fake` a non-empty `spec.generator.fakeSpec.data` | that path |

These use the compiler's `value` package:

- A key is sensitive when, without a final `s`, it is one of the
  package's `sensitiveKeys` (`password`, `token`, `secretkey`...), ends
  with `password`, `passwd`, `secret` or `token` in any case, or ends
  with `Key`, `_key` or `-key`.
- A key names a credential rather than holds one when it starts with
  `existing`, ends with `ref`, or is the key of a Secret field
  (`adminPasswordKey`, `tokenKey`). Those pass.
- A literal is a non-blank string with neither `{{` nor `${`, or a
  number.

## Hooks

`hooks` in `rules.yaml` lists, for each point the compiler calls a
set's documents at, documents of the sets read so far. A set added lists
its own after the mandatory set's, which lists none. Each document gives
`errors`; the first message refuses the instance, as the document writes
it: the compiler adds nothing to it. They run in the rule set's session
and are skipped for a privileged project.

| Hook | Called | Input |
|---|---|---|
| `objects` | on each object of an instance, items of a List too, once it passed check `object`; also by `compiler.CheckObjects`, on a host's objects | `svc`, `ns`, `object` (as check `object` reads it), `settings` |
| `finalValues` | on the final values of each chart key, keys in order, whether `Inputs.Finalize` or Helm's merge made them; also by `compiler.CheckFinalValues` | `svc`, `ns`, `key`, `value` (the key's values), `settings` |

`settings` is `global.okdp` of the values layers, never null. A
message in the shape of the compiler's own is written by the document:
`<svc>: objects: <Kind>/<name>: <path>: ...` (`object.id` is
`<Kind>/<name>`) and `<key>: <path>: ...`. The security set checks the
hosts of objects and of final values this way ([its
hooks](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/04_rules.md#hooks)).

The compile runs `finalValues` on the values a `Finalize` returns, after
the host's own checks. Without a `Finalize`, it runs the hook right
after Helm's merge.

## What a host adds

A host's `Inputs.Finalize` makes the final values in the host's own
Helm merge; the hooks of the set still run on what `Finalize` returns.
A host may also check the layers it gives before the compile: what
each may set, and `global` of those it does not review. It may do
both: render the upstream charts, whose manifests go through
`compiler.CheckObjects` (check `object`, then the hook `objects`), and
apply the security set's policy on upstream values and on `global`, the
package `policy` of okdp-compiler-security ([the upstream
policy](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/05_upstream_policy.md)).
A compile without such a host, `okdp-compile` among them, does neither.

## Platform settings

`schemas/platform.schema.json` reads `global.okdp` of the values
layers, merged in order (`Inputs.Values`). Keys it does not describe are
allowed (a key a mapping reads), except where a row says no other key
is accepted. A null deletes a default. A set added to the mandatory set
adds keys to it (`schemas.platform`): those of the security set are in
[its
reference](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/04_rules.md#platform-settings).

| Setting | Type | Default | Rule |
|---|---|---|---|
| `tls.clusterIssuer` | string | | the cert-manager ClusterIssuer |
| `tls.caBundleSecret` | string | | the Secret holding `ca.crt` and `bundle.p12` |
| `certificateIssuers` | | | refused, a former setting |
| `serviceCatalog` | | | refused, a former setting |
| `charts` | map | | only `objects` is accepted |
| `charts.objects` | `repository`, `chart`, `version` | see [charts](#charts) | `https://` or `oci://`, never `http://`; a pinned version |
| `proxy.httpProxy`, `httpsProxy`, `noProxy` | string | | |
| `provisioning.<contract>.default` | string | | a class of `classes` |
| `provisioning.<contract>.classes.<name>` | `provider` (required), `request` | | `<name>` is a DNS label; no other key is accepted |
| `secrets.store` | string | | |
| `secrets.provider` | map | | External Secrets' `spec.provider`, verbatim |
| `secrets.serviceAccount` | string | `okdp-secrets` | |
| `secrets.refreshInterval` | string | `1h` | |
| `access` | map | | no other key than the three below |
| `access.admins` | list of groups | | each `^[A-Za-z0-9][A-Za-z0-9._@:-]{0,254}$` |
| `access.groups` | map of groups to roles | | each key `^[A-Za-z0-9][A-Za-z0-9._@:-]{0,254}$`, each role `viewer`, `editor` or `admin`; a host gives them per namespace (`Inputs.Settings`) |
| `access.groupPrefix` | string | | `^[A-Za-z0-9._@:#/-]*$` |
| `network` | map | | no other key than the two below |
| `network.policy` | boolean | | |
| `network.ingressNamespaces` | list | | |
| `controlPlane.serviceAccount` | `namespace`, `name`, both required | | a DNS label, a DNS subdomain; no other key is accepted |
| `policy` | map | | no other key than the four below, and those a set added gives |
| `policy.privileged` | list of strings | | |
| `policy.reserved` | list of strings | | |
| `policy.projectGlobals` | unique strings, not `okdp` | | read by a host, never by the compiler |
| `policy.projectApiGroups` | list of strings | | the API groups added to the Role a project's releases are installed with (Flux; see [the deployer's Role](#the-deployers-role)) |
| `ruleSets[]` | `name` (required), `version` | | the rule sets the compile must be given; no other key is accepted |

`ruleSets` lists the rule sets the platform requires, each by its
`name`, `core` for the mandatory set, and optionally a constraint on its
`version`. A constraint is comparators separated by commas, all of
which must hold: each is `=`, `!=`, `>`, `>=`, `<`, `<=`, `~` (from the
version on, of the same major and minor) or `^` (from the version on, of
the same major, or of the same minor when the major is 0) followed by a
version, or a version alone, which stands for `=`. Versions compare as
Semantic Versioning 2.0.0 orders them, a `-prerelease` before its
release. Every compile and `SettingsOf` refuse settings that require a
set the compile was not given, or one of a version its constraint
refuses:

```text
global.okdp.ruleSets: the platform requires the rule set <name>, which the compile was not given: give it with okdp-compile --rules (compiler.LoadRules)
global.okdp.ruleSets: the platform requires the rule set <name> <constraint>, the compile was given its version <version>: give a matching one with okdp-compile --rules (compiler.LoadRules)
```

`global.okdp` takes keys its schema does not describe, so the settings
an added set reads would otherwise pass unread in a compile without it:
the requirement lives in the platform's values, and holds with the
compiler alone.

## The deployer's Role

The Role `okdp-project` of a project's namespace, which helm-controller
impersonates for its releases (Flux, the ServiceAccount a host writes),
is the document `objects/deployer-role.cel.yaml`, its input the groups of
`global.okdp.policy.projectApiGroups`: explicit API groups, every verb
on every resource of each, and `roles`, `rolebindings` of
`rbac.authorization.k8s.io` without `bind` nor `escalate`. RBAC has no
negation, so the list holds the groups the catalogs' charts render
objects of and leaves the engines' out (`deployerApiGroups`): a
`HelmRelease`, an `Application` of a project is not creatable. A
platform adds groups for the custom resources of its own charts; a chart
whose Role names a group not added is refused by the API server
(escalation prevention). The privileged projects keep helm-controller's
identity. Argo CD confines by the AppProject instead
(`clusterResourceWhitelist: []`).

## What a mapping sees as platform

`scope.cel.yaml`, the mandatory set's scope document (`scopes`),
computes `platform`; an added set's scope documents add keys to it
([how the sets add up](#how-the-sets-add-up)). Its inputs are
`settings`, the namespace's groups with their roles among them
(`access.groups`, which a host gives in `Inputs.Settings`), and
`release`.

| Value | How it is computed |
|---|---|
| `platform.access.platformAdmins` | `access.admins`, sorted and unique |
| `platform.access.viewers` | the administrators and every group of `access.groups` |
| `platform.access.editors` | the administrators and the `editor` and `admin` groups |
| `platform.access.admins` | the administrators and the `admin` groups |
| `platform.proxyEnv` | `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, each only when its setting is not empty |

## What a mapping writes

`schemas/mapping.schema.json` checks a mapping's result and the arguments
of the functions. The sections are the compiler's `output`, `instance`,
`provides` and `objects`, and those of `rules.yaml` `sections` of a set
added to the mandatory set, which gives none. `okdp.yaml` may hold the
sections of the rule set it is read for (`compiler.LoadDefinition`,
`LoadInputs`), and no other key; nil rules read it for the mandatory
set.

| Section | Rule |
|---|---|
| `output` | chart key to a map or null |
| `instance` | `url` and `usage`, strings; other keys ignored |
| `provides[]` | `contract` required; `name`, `values`, `secretRef`, `secret` |
| `provides[].secretRef.name` | a DNS subdomain, 253 at most |
| `provides[].secret` | `generate` (generated keys) and `stringData`; without `generate`, `stringData` holds strings only |
| `objects[]` | `kind` and `metadata.name` required, non-empty |

## Functions

`rules.yaml` declares what mappings call, in `functions`; the mandatory
set declares `okdpGeneratedSecret`, and a set added to it declares its
own. A fragment is
a template evaluated on its arguments, which are checked against the
mapping schema under the function's name. A document function evaluates
a document of `documents`. `result` names the value returned; without
it, all values are. A package that calls a function of a set compiles
only when given that set: otherwise the compile fails on an unknown
function. The security set declares `okdpPodSpec`, `okdpJob`,
`okdpSignInProxy`, `okdpWebWorkload` and `oidcClientSecret` ([its
functions](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/04_rules.md#functions)).

The mandatory set declares one fragment, `okdpGeneratedSecret`, which
the compiler also renders for a `provides` secret's `generate`:

| Function | File | Returns |
|---|---|---|
| `okdpGeneratedSecret` | `fragments/generated-secret.cel.yaml` | `objects` |

### okdpGeneratedSecret

External Secrets generates the values in the cluster, so Helm renders the
same objects each time. It returns one Password generator per key and an
ExternalSecret that writes the Secret `name`.

| Argument | Default |
|---|---|
| `keys[].length` | `32`, also for `0` |
| `keys[].digits` | `6` |
| `keys[].symbols` | `0` |
| `keys[].noUpper` | `false` |
| `keys[].transform` | none: `sha256` or `b64enc` |
| `namespace` | the release's; another one is for privileged projects only (check `object`) |
| `refreshInterval` | `"0"`, generated once |
| `apiVersion` | `external-secrets.io/v1` |

A generator is named `<name>-<key>`, lower-cased, with `_` and `.` as
`-`. Past 63 characters it is cut to 54, a trailing `-` is dropped, and 8
hex characters of the key's SHA-256 are added. Two keys that give one
generator name are refused. `stringData` holds static keys, and a
generated key replaces a static key of the same name.

## The secret store

Claims across namespaces carry credentials through the platform's store.
A rule set that does not define these documents cannot compile such a
claim.

The document `secretStore`, `objects/secret-store.cel.yaml`, reads
`secrets.*`:

| Value | Rule |
|---|---|
| `store` | present when `secrets.store` and `secrets.provider` are both set: the SecretStore the targets write in each project |
| `store.serviceAccount` | `secrets.serviceAccount`, else `okdp-secrets` |
| `ref.name` | `secrets.store` |
| `ref.kind` | `SecretStore` with a provider, else `ClusterSecretStore` |
| `refreshInterval` | `secrets.refreshInterval`, else `1h` |

| Object | Document | Rule |
|---|---|---|
| `external-secrets.io/v1alpha1` PushSecret, named after the claim's ID | `pushSecret`, `objects/push-secret.cel.yaml` | pushes each secret field of the provider's Secret as a property of the key; `updatePolicy: Replace`, `deletionPolicy: Delete` |
| `external-secrets.io/v1` ExternalSecret, named after the Secret | `pullSecret`, `objects/pull-secret.cel.yaml` | reads each field back into that Secret; `creationPolicy: Owner` |

The key in the store is `<provider namespace>/<claim ID>`, computed by
the compiler.

## Names, labels and charts

### names

`$<word>` stands for a value.

| Name | Pattern | Used for |
|---|---|---|
| `credentialsSecret` | `$output-$contract-credentials` | the Secret of a provider's output |
| `connectionSecret` | `$release.$connection-credentials` | the Secret of a connection held in the store |
| `claimSecret` | `$release-$secret` | a claimer's copy of a Secret from another namespace |
| `credentialsLabel` | `okdp.io/credentials-$contract` | the label of a credentials Secret, set to `"true"` |

### labels

| Group | Labels |
|---|---|
| selector, `okdpSelectorLabels` | `app.kubernetes.io/name: <service>`, `app.kubernetes.io/instance: <release>` |
| common, added by `okdpLabels` | `app.kubernetes.io/managed-by: Helm`, `app.kubernetes.io/part-of: okdp`, `okdp.io/instance: <release>`, `okdp.io/service: <service>` |
| with a component argument | `app.kubernetes.io/component: <component>` |

### keepSync

An object annotated `helm.sh/resource-policy: keep`, trimmed and in any
case, also gets `Prune=false` and `Delete=false` in
`argocd.argoproj.io/sync-options`. An option the object already sets by
that name is left alone. Argo CD reads none of Helm's annotations.

### charts

| Name | Chart | Override |
|---|---|---|
| `objects` | `oci://quay.io/okdp/charts`, `okdp-objects`, `0.1.0` | `global.okdp.charts.objects` |

`oci://quay.io/okdp/charts` is not published yet: the chart,
OKDP/okdp-objects-chart 0.1.0, is installed from a checkout ([how it
reaches the cluster](../03_okdp_objects.md#how-it-reaches-the-cluster)).
Only the sandbox's local registry holds `okdp-objects`, and the sandbox
overrides `charts.objects` to reach it.

### ownObjects

`ownObjects` of a set added to the mandatory set, which gives none, ties
a section of `sections` to a list document and a template:
`{section, list, objects}`. The list document reads the section's
`items`, `release`, `platform`, `privileged` and `settings`, and gives
an item per entry, `of` (where it is, for messages), `client` and
`names`, which the template `objects` renders into objects of the
instance. Its first message refuses the instance.

## What a platform can change

The platform's values (a `Settings` document) cannot add a check, a
function or a schema. A
platform's rule set (`LoadRules`) adds its own tables, documents, hooks,
functions, templates, sections, `ownObjects`, scope documents and keys
of the schemas, and values to the extensible tables, and changes none of
the rules below. Of them, the platform's values can only set these:

| Setting | Effect on the rules |
|---|---|
| `policy.reserved` | adds namespaces to `platformNamespace` |
| `policy.privileged` | exempts namespaces, as in [the table above](#when-each-rule-applies) |
| `policy.projectApiGroups` | adds API groups to the Role of a project's releases, never an engine's nor `*` (the document `deployerRole`) |
| `secrets.store`, `secrets.serviceAccount` | add names to check `release` |
| `charts.objects` | replaces the `objects` chart |
| `ruleSets` | refuses every compile without the sets it requires |

Nothing in the platform's values changes a table. What a platform sets of
the security set's rules: [its reference](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/04_rules.md#what-a-platform-can-change).

## Changing a rule

Edit [`internal/rules/mandatory`](../../internal/rules/mandatory) and
run `make check`. A rule's message is part of its contract: the tests of
`internal/rules`, `internal/okdp` and `compiler_test.go` compare them. A
new table or check name of the mandatory set also needs a change in
`internal/rules` (`ruleTables`, `ruleChecks`): `LoadRules` refuses names
it does not know there. A set added to it needs none for its tables,
documents and hooks.

A platform's own set changes the same way, in its own files: a key of a
schema comes in its `schemas`, merged, and a value a mapping sees as
`platform` in a document of its `scopes`. Raise its `version` with each
change, the major one when a change breaks what a platform relies on: a
platform that requires the set (`global.okdp.ruleSets`) states the versions it
accepts, and a compile given another is refused.

A release of the compiler carries the change, and a host takes it by
taking the release: its tests that compare the messages see it. There
is no copy to keep in sync.
