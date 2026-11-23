# The rule set

The compiler's rule set says what a package's mapping may call, how OKDP
names its objects and what a compile refuses of a platform. It is data,
one set, the mandatory set, embedded in this module
([`internal/rules/mandatory`](../../internal/rules/mandatory)) and read
once (`compiler.Mandatory()`): every compile applies it.
`compiler.LoadRules` adds a platform's own sets to it, in order, and
replaces none of its rules. This page is the reference of the set: every
hook, schema, function and document, where each is applied, and how a
set added to it extends it, its tables among them.

The mandatory set refuses only the namespaces OKDP keeps on every
platform, `platform` and `kube-*`. What a host keeps, the namespaces of
its deployer engines and the names of the objects it writes in every
namespace, is its own rule set's. The platform's policy is the security
set's, added this way: the objects of the projects that are not
privileged (no cluster-scoped kind, no engine's kind, no other
namespace), no credential in clear, the namespaces the platform
reserves, the hosts a project serves, the groups of each role
(`platform.access`), the OAuth clients, the hardened pod and the policy
on upstream values: [its
reference](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/04_rules.md).
A compile without it accepts all of that; a platform requires it
(`global.okdp.ruleSets`).

- [How the sets add up](#how-the-sets-add-up)
- [Who applies it](#who-applies-it)
- [What is not in the rule set](#what-is-not-in-the-rule-set)
- [When each rule applies](#when-each-rule-applies)
- [Tables](#tables)
- [Documents](#documents)
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
  rules.yaml                  its hooks, and every file below by name
  scope.cel.yaml              what a mapping sees as platform
  schemas/
    platform.schema.json      global.okdp, the settings
    mapping.schema.json       what a mapping writes, and the functions' arguments
  checks/
    namespace.cel.yaml        hook namespace: the namespaces OKDP keeps
  fragments/
    generated-secret.cel.yaml okdpGeneratedSecret
  objects/
    secret-store.cel.yaml     the platform's secret store
    push-secret.cel.yaml      the PushSecret of a claim of another namespace
    pull-secret.cel.yaml      the ExternalSecret that reads it
```

Every rule file this page names is of this tree, at its relative path;
`rules.schema.json` is beside it, in `internal/rules`.

`rules.yaml` is `apiVersion: okdp-compiler/rules.v1`, `name: core`,
`version: 1.0.0`. `compiler.LoadRules(sets ...fs.FS)` reads the sets of
a platform, each `rules.yaml` at its root, adds them to the mandatory set
in the order given and fails closed; `okdp-compile --rules` gives it one.
It refuses a file that does not meet its `rules.schema.json` (a `checks`
section among them: there are none, only documents and hooks), another
major `apiVersion`, a set without a `name` or of a name a set read
before has, a `version` that is not `MAJOR.MINOR.PATCH` with an optional
`-prerelease`, a rule a set read before gives (`a rule set adds rules, it
replaces none`): a table, a key of a schema, a chart, a document, a
document listed again at a hook, a scope document, a function, a
template, a section, `names` or `labels`; values added to a table that is
not extensible, and a file it names that does not exist or does not
compile. What a set adds is its own tables, documents, hooks, inputs,
functions, templates, sections and `ownObjects`, values to the
extensible tables of the sets read before, keys of the schemas and scope
documents ([how the sets add up](#how-the-sets-add-up)).

The error of an added set names it: `the rule set <name>: rules.yaml:
...`, or `the added rule set <n>: ...`, its place among the sets given,
when its name is not read yet. A rule taken is `rules.yaml: <section>:
<rule>: given by a rule set read before (<names>): a rule set adds rules,
it replaces none`.

## How the sets add up

The sets apply together, the mandatory set first, then the added ones
in the order `LoadRules` is given them, and each part of a set stands
beside those of the sets before it. A later set adds to an earlier
added set's rules as it adds to the mandatory set's, and replaces none
of them. Four parts add to what the sets read before give, besides the
tables a set marks extensible ([tables](#tables)).

**Schemas.** `schemas.platform` and `schemas.mapping` of an added set
are merged into the schema of that name, then the merged document is
parsed once (`chart.ParseSchema`, never Helm's schema validation). The
merge goes through `properties` and `$defs`, at any depth. A key the
schema does not hold is added, with its defaults, which apply as the
first schema's do. A key both hold is merged further when the one read
before has `properties` or `$defs`; otherwise it is a leaf, and refused.
Any other keyword of an added node that is merged must be the node's
read before, with the same value: an added schema changes no `required`,
`additionalProperties`, `type` or `default`. A key may so join a map a
set read before closes (`additionalProperties: false`), and that map
refuses every other key as before: a host's set adds its keys to the
security set's `access` and `policy`. The error names the key or the
keyword by its JSON pointer, as in `schemas: platform:
#/properties/global/properties/okdp/properties/charts/additionalProperties:
given by a rule set read before (core)`.

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
`RuleSet.Names` gives them in the order they are read, and
`RuleSet.SetVersion` the version of each. A platform states the sets it
requires in `global.okdp.ruleSets` ([platform
settings](#platform-settings)).

**Hooks and inputs.** A set lists documents at each hook, its own or
those of a set read before, after those of the sets before it
([hooks](#hooks)). It declares in `inputs` the names of the inputs its
documents give one another through `rule` ([documents](#documents)):
every document of every set may read them.

## Who applies it

Every compile: `Inputs.Rules` nil stands for the mandatory set, and a
set of `LoadRules` holds it. A host gives its compiles a set through
`Inputs.Rules`, or nil, and reads the packages for it
(`compiler.LoadDefinition`, `LoadInputs`). `okdp-compile` gives the
mandatory set, with the set of `--rules` added. A host may give the
mandatory set with the security set added, and sets of its own after it,
or the sets its caller gives.

A program that calls `compiler.CompileAll` or `compiler.Compile` itself, this
module's `okdp-compile` among them, applies the sets it gives, their
hooks among them, but not what a host adds through
`Inputs.Finalize` or checks of the layers it gives ([what a host
adds](#what-a-host-adds)).

## What is not in the rule set

These stay in the compiler's Go code and apply whatever the rule set:

- a release, a namespace and an instance name must be DNS names;
- claim binding, classes, consent across namespaces and claim IDs;
- no template code in the upstream values, in a connection nor in what
  a mapping receives of one (`chart.NoTemplate`);
- no `provides` secret written in clear (`stringData` without
  `generate`) unless `Inputs.AllowPlainSecrets`;
- no two objects of one group, kind, namespace and name
  (`checkDuplicateObjects`);
- `okdpFullname`, `okdpSqlName`, `okdpObjectName`, the functions the
  documents call ([documents](#documents)), and the walkers that call
  the hooks below.

The compiler knows no privileged project: each document that exempts
the platform's own projects reads the setting that lists them (the
security set's `global.okdp.policy.privileged`) itself. What each role
of a group may do is not in the rule set either: the host that writes a
namespace's roles (Kubernetes' ClusterRoles, Argo CD's policies) fixes
it. Nor is what a values layer may set: the compiler
merges every layer of `Inputs.Values` alike, any of them setting
parameters, upstream values, `global`, `global.okdp`, `connections` or
`claims`. A host that gives layers of several origins decides what each
may set ([the threat
model](../04_threat_model.md#what-a-host-checks-of-the-layers)).

## When each rule applies

Each hook's document reports its first failure only, and a failure
refuses the instance. The compiler calls every hook for every project:
the documents decide whom they exempt. `AllowPlainSecrets` is
`Inputs.AllowPlainSecrets`, `--allow-plain-secrets` of `okdp-compile`,
which the compiler gives the `objects` hook.

| Rule | Reads | Runs |
|---|---|---|
| hook `namespace` | the namespace | once the settings are read, before the connections; also `compiler.CheckNamespace` and `compiler.SettingsOf` |
| hook `release` | the release | right after hook `namespace`; no document of the mandatory set |
| hook `objects` | each object, items of a List too | once the objects are collected; also `compiler.CheckObjects` |
| hook `finalValues` | the final values of each chart key | in or after `Inputs.Finalize`; also `compiler.CheckFinalValues` |
| a list document of `ownObjects` | the items of a section | after the mapping |

"Objects" are all those an instance produces: the `objects` section, the
objects of a set's sections (`ownObjects`), the credentials Secrets,
generated Secrets, PushSecrets and ExternalSecrets.

What a host runs through `Inputs.Finalize`, or checks of the layers it
gives, applies only where that host compiles ([what a host
adds](#what-a-host-adds)).

The hook `namespace` also runs when a namespace is created
(`compiler.CheckNamespace`, for a host that creates namespaces), with
`namespace` in place of `Inputs.Namespace` in the message, on the
settings of the values it is given, as written. A namespace that is no
DNS label is refused in Go first.

## Tables

A document reads a table as `sets.<name>` or `tables.<name>`. Go reads
it through `RuleSet.List`.

The mandatory set gives none. A set gives tables of any name that no
set read before gives, read the same ways; the compiler requires none by
name. A document that reads a table no set gives fails to evaluate, and
refuses: a table left out turns no rule off. A set adds values to a
table of a set read before only when that table is marked `extensible:
true`, by `add`, as a set read after the security set may:

```yaml
tables:
  colours: {values: [red, green]}                 # a table of its own
  clusterScopedKinds: {add: [example.com/Widget]} # values added, at the end
```

The extensible tables are those where more values mean more refusals,
which their set marks so: the security set's `clusterScopedKinds` and
`deployerApiGroups`, a host's set's namespaces and names it keeps. `add`
on any other table, or on a table no set read before gives, is an error;
`values` on a table a set read before gives is one too (`taken`). A
value added twice is kept once.

## Documents

A document is a file of `vars`, `errors` and `values` in the mapping
language, named in `documents` of `rules.yaml`: a hook's, a scope, a
function's, the list of a section, or one the others call. It sees its
inputs, `sets` and `tables`, and these functions besides the language's:
`dig`, `pathKey`, `merge`, `rule`, `asList`, `asMap`, `truthy`, `keys`,
`sensitive`, `credentialReference`, `credentialLiteral`, `leaves`,
`fields` and `urlParse`, which the compiler implements in Go for every
set: a set registers no function of its own. Documents run in a session
of their own, so their cost does not count against a package's limit.

The inputs a document may read are declared, the compiler's own
(`ruleInputs`, `check.go`) and those the sets add:

| Inputs | Given by |
|---|---|
| `allowPlainSecrets`, `field`, `key`, `ns`, `object`, `release`, `settings`, `svc`, `value` | the compiler's hooks |
| `settings`, `release` | the scopes |
| `arg`, `release`, `platform` | the document functions |
| `arg`, `settings` | the secret store's documents |
| `items`, `release`, `platform`, `settings` | the list documents of `ownObjects` |
| `sets`, `tables` | the rule set, to every document |
| `inputs` of `rules.yaml` | a set, for what its documents give one another through `rule` |

A document that reads a name not declared does not compile, and
`LoadRules` refuses its set. The security
set declares `added`, `at`, `host`, `listed`, `raw` and `suffix`.

| Function | Returns |
|---|---|
| `leaves(v)` | every scalar under `v`, null among them, as a list of `{path, value}`: depth first, map keys sorted, list items in order, a list index an int of `path`; an empty map or list gives nothing |
| `fields(s, seps)` | `s` split on any character of `seps`, empty fields dropped |
| `urlParse(s)` | `{ok, scheme, host, user}` of Go's `url.Parse`: `host` without its port, `user` whether user information is given; `ok` false, the rest empty, when it fails |

`rule(name, input)` evaluates another document on `input` alone, which
must pass it `sets` and `tables` if it reads them. When that document
refuses, the caller refuses with its message, as written. A host's Go
calls any document by name too: `RuleSet.Refuse` as a check, the first
message refusing, `RuleSet.Values` for its values.

## Hooks

`hooks` in `rules.yaml` lists, for each point the compiler calls the
sets' documents at, documents of the set or of a set read before. The
compiler runs those of every set, in the order the sets are read, the
mandatory set's first. Each document gives `errors`; the first message
refuses the instance, as the document writes it: the compiler adds
nothing to it. A document is listed once per hook. They run in the rule
set's session, for every project.

| Hook | Called | Input |
|---|---|---|
| `namespace` | on the namespace of every instance, once its settings are read; by `compiler.CheckNamespace` and `compiler.SettingsOf` | `field` (`Inputs.Namespace`, or `namespace`), `ns`, `settings` |
| `release` | on the release of every instance, right after `namespace` | `field` (`Inputs.Release`), `release`, `settings` |
| `objects` | on each object of an instance, items of a List too; also by `compiler.CheckObjects`, on a host's objects | `svc`, `ns`, `object` (`id`, `kind`, `name`, `apiVersion`, `group` and `ns` as text, `raw` the object itself), `settings`, `allowPlainSecrets` |
| `finalValues` | on the final values of each chart key, keys in order, whether `Inputs.Finalize` or Helm's merge made them; also by `compiler.CheckFinalValues` | `svc`, `ns`, `key`, `value` (the key's values), `settings` |

`settings` is `global.okdp` of the values layers, checked against the
platform schema and its defaults applied; `compiler.CheckNamespace`
gives it as written. It is never null. A message in the shape of the
compiler's own is written by the document: `<field> "<ns>": ...`,
`<svc>: objects: <Kind>/<name>: <path>: ...` (`object.id` is
`<Kind>/<name>`) and `<key>: <path>: ...`.

The mandatory set lists one document, at `namespace`, below, and none
at `release`, a hook for the sets added: a host's set refuses there a
release named like an object it writes in every namespace. The security
set lists its own at `namespace`, `objects` and `finalValues`: the
namespaces the platform reserves, the objects of the projects that are
not privileged, the credentials in clear, the hosts of objects and of
final values ([its
hooks](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/04_rules.md#hooks)).

The compile runs `finalValues` on the values a `Finalize` returns, after
the host's own checks. Without a `Finalize`, it runs the hook right
after Helm's merge.

Below, `...` shortens a message; the full text is in the document's
file.

### namespace

`checks/namespace.cel.yaml`. A DNS label check comes first, in Go.
`default` is allowed.

| Refused when | The reason given |
|---|---|
| `ns` is `platform` | `platform/ holds the platform's keys of the secret store, which no project writes` |
| `ns` starts with `kube-` | `kube-* is Kubernetes' own (kube-system, kube-public, kube-node-lease)` |

The message is `<field> "<ns>": the namespace is reserved: <reason>`.
The namespaces of a host's deployer engines, and those it writes their
objects in, are its own set's to refuse, at this hook.

## What a host adds

A host's `Inputs.Finalize` makes the final values in the host's own
Helm merge; the hooks of the sets still run on what `Finalize` returns.
A host may also check the layers it gives before the compile: what
each may set, and `global` of those it does not review. It may do
both: render the upstream charts, whose manifests go through
`compiler.CheckObjects` (the hook `objects`), and
apply the security set's policy on upstream values and on `global`, the
package `policy` of okdp-compiler-security ([the upstream
policy](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/05_upstream_policy.md)).
A compile without such a host, `okdp-compile` among them, does neither.

## Platform settings

`schemas/platform.schema.json` reads `global.okdp` of the values
layers, merged in order (`Inputs.Values`), with the host's settings
(`Inputs.Settings`, [host
settings](01_inputs_outputs.md#host-settings)). Keys it does not
describe are allowed (a key a mapping reads), except where a row says
no other key is accepted. A null deletes a default. A set added to the mandatory set
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
compiler alone. It is read on the settings as written, before the
schema.

The mandatory schema describes neither `access` nor `policy`: the
security set defines both and closes them ([its
settings](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/04_rules.md#platform-settings)),
and a host's set adds its own keys to them. The settings a host's
writers read and the compiler never does (a network policy, a control
plane's identity, the prefix of the groups in RoleBindings, the API
groups of a project's Role) are a host's set's too. A compile without
the security set takes `access` and `policy` unread; one with it, but
not the host's set, refuses the host's keys there.

## The deployer's Role

The Role of a project's namespace that helm-controller impersonates for
its releases (Flux, the ServiceAccount a host writes) is a host's too,
and the rule sets give what it reads: the groups a platform adds, a
setting of the host's own set (`global.okdp.policy.projectApiGroups`),
and the security set's table `deployerApiGroups`, which it never adds.
The Role holds explicit API groups, every verb on every resource of
each, and `roles`, `rolebindings` of `rbac.authorization.k8s.io` without
`bind` nor `escalate`. RBAC has no negation, so the list holds the
groups the catalogs' charts render objects of and leaves the engines'
out: a `HelmRelease`, an `Application` of a project is not creatable. A
platform adds groups for the custom resources of its own charts; a chart
whose Role names a group not added is refused by the API server
(escalation prevention). The privileged projects (the security set's
`policy.privileged`) keep helm-controller's identity. Argo CD confines by
the AppProject instead (`clusterResourceWhitelist: []`).

## What a mapping sees as platform

`scope.cel.yaml`, the mandatory set's scope document (`scopes`),
computes `platform`; an added set's scope documents add keys to it
([how the sets add up](#how-the-sets-add-up)). Its inputs are
`settings` and `release`.

| Value | How it is computed |
|---|---|
| `platform.proxyEnv` | `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, each only when its setting is not empty |

`platform.access`, the groups of each role, is the security set's, of
its settings `global.okdp.access.groups` and `access.admins`: a compile
without it gives a mapping none, and a package that reads it compiles
only with it.

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
| `namespace` | the release's; another one is, with the security set, for privileged projects only (its hook `objects`) |
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
`secrets.*`. The compiler reads `ref` and `refreshInterval`; a host
reads `store` (`RuleSet.Values`) and writes it:

| Value | Rule |
|---|---|
| `store` | present when `secrets.store` and `secrets.provider` are both set: the SecretStore a host writes in each project |
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
`items`, `release`, `platform` and `settings`, and gives
an item per entry, `of` (where it is, for messages), `client` and
`names`, which the template `objects` renders into objects of the
instance. Its first message refuses the instance.

## What a platform can change

The platform's values (a `Settings` document) cannot add a hook, a
function or a schema. A platform's rule sets (`LoadRules`) add their own
tables, documents, hooks, inputs, functions, templates, sections,
`ownObjects`, scope documents and keys of the schemas, and values to the
extensible tables, and change none of the rules below. Of them, the
platform's values can only set these:

| Setting | Effect on the rules |
|---|---|
| `charts.objects` | replaces the `objects` chart |
| `ruleSets` | refuses every compile without the sets it requires |

Nothing in the platform's values changes a table. What a platform sets of
the security set's rules: [its reference](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/04_rules.md#what-a-platform-can-change).

## Changing a rule

Edit [`internal/rules/mandatory`](../../internal/rules/mandatory) and
run `make check`. A rule's message is part of its contract: the tests of
`internal/rules`, `internal/okdp` and `compiler_test.go` compare them. A
document or a hook's document of the mandatory set needs no change in
Go, nor does a set added to it, its tables; a new hook, or an input the
compiler gives, does (`internal/rules`, `hooks.go`, `check.go`). Moving
a document from the mandatory set to another set, at the same hook, is
data alone.

A platform's own set changes the same way, in its own files: a key of a
schema comes in its `schemas`, merged, and a value a mapping sees as
`platform` in a document of its `scopes`. Raise its `version` with each
change, the major one when a change breaks what a platform relies on: a
platform that requires the set (`global.okdp.ruleSets`) states the versions it
accepts, and a compile given another is refused.

A release of the compiler carries the change, and a host takes it by
taking the release: its tests that compare the messages see it. There
is no copy to keep in sync.
