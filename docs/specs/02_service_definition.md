# Service definition

Normative reference of a service definition: the two files at the root of
a package that `compiler.LoadDefinition(fsys)` reads.

| File | Content |
|---|---|
| [`service.yaml`](#serviceyaml) | the engine-neutral part: the service's name and versions, the upstream charts and their sources, their base values, what an instance may override, the platform values the service needs |
| [`mapping.cel.yaml`](#mappingcelyaml) | the mapping: YAML shaped like the upstream values, with `${{ CEL }}` expressions and directives; it computes the values of each chart, OKDP's own objects and what the instance publishes, and declares the service's OAuth clients |

Both files are required: a missing one fails `LoadDefinition` with the read
error (`open service.yaml: ...`), and a `service.yaml` that does not decode
fails with `service.yaml: <error>`. `LoadDefinition` only reads
`mapping.cel.yaml`: each `Compile` parses and evaluates it, so its errors
are compile errors ([Errors](#errors)). It keeps the package
(`Definition.Files`): each compile reads its other files, `values.yaml`,
`values.schema.json` and the base files
([package files](01_inputs_outputs.md#package-files)).

The other inputs of a compile (the chart cache, contracts, platform and
instance values) and its outputs are specified in
[01_inputs_outputs.md](01_inputs_outputs.md).

## service.yaml

Decoded by `gopkg.in/yaml.v3`: YAML 1.2, unlike values files and the
mapping (`upstream: yes` is the path `yes`). Unknown fields are ignored.

### Service

| Field | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | required | The service: [`service.name`](#context), labels `app.kubernetes.io/name` and `okdp.io/service`, descriptor `data.service`, heading of the parameter schema violations, `okdp-compile`'s default release `demo-<name>`. |
| `version` | string | required | The package's version, `<upstream>-<okdp semver>` by convention: `service.version`, descriptor `data.version`. |
| `appVersion` | string | `""` | The application's version: `service.appVersion`, label `app.kubernetes.io/version` (left out when empty). |
| `roles` | list of strings | `[]` | Catalog metadata: what the service provides. Not read by the compiler. |
| `dependsOn` | list of strings | `[]` | Catalog metadata: the platform services the service needs. Not read by the compiler. |
| `require` | list of strings | `[]` | Platform values the service needs: dotted paths under `global.okdp` (`ingress.suffix`), each of which must be set and not empty: [requirements](01_inputs_outputs.md#requirements). |
| `charts` | map: key → [chart](#chart) | `{}` | The upstream charts of the service, by key: chart `<key>` is `output.<key>` of the mapping, rendered by its [source](#sources). `okdp` and the charts of the OAuth clients are not listed: the compiler adds them ([the okdp chart](#the-okdp-chart), [oidcClients](#oidcclients)). |

The versions are strings, as written: `appVersion: 1.10` is `"1.10"`.
Without `name` or `version`, loading fails: `service.yaml: name is required`,
`service.yaml: version is required`.

### Chart

| Field | Type | Default | Meaning |
|---|---|---|---|
| `source` | `{repository, chart, version}` | required | The upstream chart that renders the key's values: [sources](#sources). |
| `base` | string | none | Path, in the package, of OKDP's default values for this chart (by convention `base/<key>.yaml`). Read as a Helm values file; a missing file, or a path out of the package, fails the compile (`<key>: open <path>: ...`). |
| `upstream` | `true`, `false`, a path, a list of paths or `{from: <key>}` | `false` | The instance upstream values the chart accepts: [upstream](#upstream). |
| `protect` | list of paths | `[]` | Values the instance upstream values may not set: [protect and append](#protect-and-append). |
| `append` | list of paths | `[]` | Lists the instance upstream values append to instead of replacing them: [protect and append](#protect-and-append). |

How the chart defaults, `base`, the computed values and the instance
upstream values combine: [merge precedence](01_inputs_outputs.md#merge-precedence).
A chart is rendered only when the mapping outputs it; its source must then
be in the chart cache ([chart cache](01_inputs_outputs.md#chart-cache)).

### Sources

```yaml
charts:
  polaris-admin:
    source: &admin {repository: https://charts.example.org, chart: polaris-admin, version: 1.0.0}
    base: base/polaris-admin-bootstrap.yaml
    upstream: true
    protect: [fullnameOverride, phase, realms]
  polaris-admin-principals:            # the values of a second render
    source: *admin                     # the same chart
    base: base/polaris-admin-principals.yaml
    upstream: {from: polaris-admin}    # the instance values of polaris-admin, with its protect
```

`source` names the upstream chart that renders the key's values, as its
repository publishes it: `repository`, a chart repository (`https://`) or an
OCI registry path (`oci://`); `chart`, the chart's name in it; `version`, a
pinned version. A deployer renders the chart from there. The compiler reads
only its `values.yaml` and `values.schema.json`, from the chart cache
(`<chart>-<version>.tgz` or `<chart>-<version>/`:
[chart cache](01_inputs_outputs.md#chart-cache)): the first layer of the
final values, and their schema
([merge precedence](01_inputs_outputs.md#merge-precedence)).
`Result.Charts` maps each compiled key to its source:
[chart index](01_inputs_outputs.md#chart-index).

Two keys of the same source are two renders of one upstream chart, each
with its own values (polaris-admin's bootstrap and principals phases).
Everything else stays keyed by the key: `output.<key>` of the mapping,
`Result.Values` and `Result.Computed`, the instance's `upstream.<key>.yaml`
and inline `upstream.<key>` values. `upstream: {from: <key>}` gives the
second render the instance values of the first ([upstream](#upstream)).

A source is checked when `service.yaml` is loaded, and by `Compile` for a
definition built in memory:

| Source | Error |
|---|---|
| absent, or all three fields empty | `service.yaml: charts.<key>.source is required: the upstream chart (repository, chart, version)` |
| `repository` not starting with `https://`, `http://` or `oci://` | `service.yaml: charts.<key>.source.repository "<repository>": the URL of a chart repository (https://... or oci://...)` |
| `chart` empty, holding a `/`, `.` or `..`, or with leading or trailing spaces | `service.yaml: charts.<key>.source.chart "<chart>": the name of a chart of the repository` |
| `version` not a SemVer version (`1.2.3`, `v1.2.3`, `1.2.3-rc.1`, `1.2.3+build`): a range, a partial version | `service.yaml: charts.<key>.source.version "<version>": a pinned chart version (1.2.3), not a range` |

### upstream

| Value | Instance upstream values read, in order |
|---|---|
| absent, `false`, `null` | none: the chart's files and inline `upstream.<name>` values are ignored |
| `true` | `upstream.<name>.yaml` of `Inputs.Instance` when it exists (the file, and `Inputs.Instance`, may be missing), then the inline `upstream.<name>` values |
| a path | that file of `Inputs.Instance`, which must exist, then the inline values |
| a list of paths | these files in order, each of which must exist, then the inline values |
| `[]` | the inline values only |
| `{from: <key>}` | those of the chart `<key>`, as its own entry reads them, with its `protect` and `append` |

A value of another form (`""`, a number, a map other than `{from: <key>}`,
or a list holding `""`) fails with
`service.yaml: line <n>: upstream must be true, false, a path, a list of paths or {from: <chart>}`.
How the instance values are written, and the errors of a missing file:
[upstream values](01_inputs_outputs.md#upstream-values).

`{from: <key>}` is for two renders of one upstream chart that an instance
sets with one set of values ([sources](#sources)): refused or appended the
same way in both, also when the mapping leaves `<key>` out. The mapping sees
them once, as `values.upstream.<key>`. `<key>` must accept upstream values of
its own, and the chart lists no `protect` or `append` of its own:

| Case | Error |
|---|---|
| `<key>` is the chart itself | `service.yaml: charts.<chart>.upstream.from: the chart itself (upstream: true accepts its own values)` |
| `<key>` is not a chart of `service.yaml` | `service.yaml: charts.<chart>.upstream.from "<key>": not a chart of service.yaml` |
| `<key>` accepts no upstream values of its own (none, or `from`) | `service.yaml: charts.<chart>.upstream.from "<key>": that chart accepts no upstream values of its own (upstream: true, or files)` |
| the chart has `protect` or `append` | `service.yaml: charts.<chart>: protect and append come with upstream.from, from charts.<key>: list them there` |

### protect and append

Paths of keys separated by dots (`ingress.hosts`). A key containing dots is
written in brackets, as one literal key (`config["spark.ui.filters"]`); a
bracketed key ending with `*` names every key starting with the text before
it (`ingress.annotations["cert-manager.io/*"]`; `["*"]` names every key);
`\"`, `\\` and `\*` write a quote, a backslash and a star in brackets. A
malformed path fails when `service.yaml` is loaded:
`service.yaml: charts.<key>.protect[<i>] "<path>": <reason>` (`append[<i>]`
alike). `global` is always protected. Both apply to each instance upstream
file, then to the inline values: [protect](01_inputs_outputs.md#protect),
[append](01_inputs_outputs.md#append).

### The okdp chart

`okdp` (`compiler.ObjectsChart`) is the compiler's: it adds the chart to
every compile, rendered by the platform's objects chart
(`global.okdp.charts.objects`), its values `Result.Objects`: [the okdp
chart](01_inputs_outputs.md#the-okdp-chart). `charts` may not declare it
(`service.yaml: charts.okdp: the chart of OKDP's own objects is added by the compiler: remove it from service.yaml`,
when the file is loaded), nor `output` compute it ([output](#output)).

### Example

```yaml
name: webapp
version: 2.3.0-1.0.0                   # <upstream>-<okdp semver>
appVersion: "2.3"
roles: [webapp]                        # catalog metadata
dependsOn: [database-server, ingress]
require:                               # global.okdp.<path>, each non-empty
  - ingress.suffix
  - certificateIssuers.selfSigned.name
charts:
  app:                                 # output.app of the mapping
    source: {repository: https://charts.example.org, chart: app, version: 1.8.0}
    base: base/app.yaml                # OKDP's defaults, over the chart's values.yaml
    upstream: true                     # upstream.app.yaml when it exists, then inline upstream.app
    protect: [fullnameOverride, ingress.enabled, ingress.hosts, 'ingress.annotations["cert-manager.io/*"]']
    append: [env]                      # instance lists appended to the computed ones
  admin:
    source: &admin {repository: oci://registry.example.org/charts, chart: admin, version: 0.3.1}
    base: base/admin.yaml
    upstream: []                       # inline upstream.admin only
  admin-users:                         # output.admin-users, a second render of the admin chart
    source: *admin
    base: base/admin-users.yaml
    upstream: {from: admin}            # the instance values of admin
  metrics:                             # no base values, no instance values
    source: {repository: oci://registry.example.org/charts, chart: metrics, version: 0.4.2}
```

## mapping.cel.yaml

A YAML map: values shaped like the upstream values, where `${{ <CEL> }}`
computes a value ([Expressions](#expressions)) and `$`-keys shape the
structure ([Directives](#directives)). Its top-level keys are the
[sections](#sections):

| Key | Type | Content |
|---|---|---|
| [`let`](#let) | map | named values, evaluated in order |
| [`errors`](#the-errors-section) | list | rules (`$if`, `message`) and expressions returning lists of messages: any message fails the compile |
| [`output`](#output) | map | chart → computed values |
| [`objects`](#objects) | list | OKDP's own objects, whole manifests |
| [`instance`](#instance) | map | `url` and `usage` of the instance |
| [`provides`](#provides) | list | the connections the instance publishes |
| [`oidcClients`](#oidcclients) | list | the OAuth clients of the service |

Every section is optional. Other top-level keys are ignored, not evaluated.

```yaml
let:
  host: ${{ release.name }}.${{ global.okdp.ingress.suffix }}
  db: '${{ type(values.database) == map ? values.database : {} }}'
errors:
  - $if: ${{ values.replicas > 1 && db.size() == 0 }}
    message: "webapp: replicas > 1 needs a database"
output:
  app:
    fullnameOverride: ${{ release.name }}-app
    replicaCount: ${{ values.replicas }}          # a number: the expression is the whole scalar
    image:
      tag: ${{ service.appVersion }}
    ingress:
      annotations: ${{ platform.ingressAnnotations }}
      hosts:
        - host: ${{ host }}
    env:
      - $if: ${{ db.size() > 0 }}                 # the item is left out when false
        name: DB_URL
        value: jdbc:${{ db.engine }}://${{ db.host }}:${{ str(db.port) }}/${{ db.dbName }}
  metrics:
    $if: ${{ values.metrics.enabled }}            # the chart is not rendered when false
    interval: ${{ values.metrics.interval }}
objects:
  - $spread: >-
      ${{ okdpGeneratedSecret({'name': okdpFullname('session'), 'keys': [{'key': 'session-key'}]}) }}
instance:
  url: https://${{ host }}
  usage: |
    A web application. Open https://${{ host }}
provides:
  - contract: http
    values:
      url: https://${{ host }}
oidcClients:
  - redirectUris: ['https://${{ host }}/oauth2/callback']
    grantTypes: [authorization_code, refresh_token]
```

## Sections

### Evaluation order

1. `let`, entry by entry: each sees the [context](#context) and the
   bindings before it.
2. `errors`, every entry, then the messages fail the compile together
   ([the errors section](#the-errors-section)).
3. `output`, `instance`, `provides`, `objects`, `oidcClients`, in this
   order: each sees the context and every `let` binding, and is checked
   for its shape once evaluated.
4. The objects are checked (kind and name); then the OAuth clients, whose
   charts are added in `dcr` mode ([oidcClients](#oidcclients)); then the
   compile builds the descriptor from `instance` and `provides`, checked
   against the contracts, and the final values of each chart in `output`,
   of each client chart and of the `okdp` chart.

A section does not see the others: there is no `output` or `instance`
variable. A value used by several sections is a `let` binding.

Each section has a shape: `output` and `instance` are maps, `provides`,
`objects` and `oidcClients` lists. Absent or null (left out by `$if`, a
null expression), a section is empty. A value of another shape fails the
compile, `<section>: must be <shape>, got <value>`, rather than read as
empty: `output: [app]` would otherwise render no chart. `<value>`: the
value as Go's `%#v` prints it (`"x"`, `false`, `[]interface {}{"app"}`).

A mapping that is not a YAML map fails with `the mapping must be a YAML map`;
invalid YAML with the parser's error (`yaml: line <n>: ...`).

### let

```yaml
let:
  host: ${{ release.name }}.${{ global.okdp.ingress.suffix }}
  settingsName: ${{ okdpFullname('settings') }}
  settings:                                   # any node: a map, a list, directives
    APP_URL: https://${{ host }}              # host: the binding above
```

A map of bindings, evaluated in order. Each value is a node, evaluated as
any other ([Expressions](#expressions), [Directives](#directives)); a value
left out by `$if` binds null. The binding is named by the key as written
([YAML reading](#yaml-reading)); it is referenced by that name, which must
be a CEL identifier (`my-var` reads as `my - var`). A binding referenced
before its entry fails: `undeclared reference`.

A binding with the name of a context variable replaces it for every later
expression (a `let` named `labels` wins over the context's `labels`), not
for the host functions ([Context](#context)). A `let` that is not a map is
ignored.

### output

A map: chart name → the values the mapping computes for that chart.
Another value fails with
`output: must be a map of charts to their values, got <value>`.

- The values of a chart are a map:
  `output: <key>: must be a map of the chart's values, got <value>`. A
  scalar or a list is refused, not compiled as the chart's defaults alone;
  a null (a null expression, `~`) leaves the chart out, as `$if` does.
- Every key must be a chart of `service.yaml`:
  `mapping output "<key>": not a chart of service.yaml`. A key may not be
  the chart of a declared OAuth client ([oidcClients](#oidcclients)), nor
  `okdp`: `mapping output "okdp": the chart of OKDP's own objects is compiled from the objects section: remove it from output`
  ([the okdp chart](#the-okdp-chart)).
- A chart the mapping leaves out (absent, dropped by `$if`, or null) is
  not rendered: no `Result.Values` entry.
- The values are the computed layer of the chart's final values:
  [merge precedence](01_inputs_outputs.md#merge-precedence).

### objects

```yaml
objects:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: ${{ okdpFullname('settings') }}
      namespace: ${{ release.namespace }}
      labels: ${{ okdpLabels('settings') }}
    data: ${{ settings }}
  - $spread: "${{ okdpGeneratedSecret({'name': okdpFullname('session'), 'keys': [{'key': 'session-key'}]}) }}"
```

A list of OKDP's own objects, whole manifests: the compiler adds nothing to
them (namespace and labels are the mapping's). After evaluation, every item
must be a map with a non-empty `kind` and `metadata.name`:
`objects[<i>]: not an object with a kind and a metadata.name: <object>`.
A value that is not a list fails with
`objects: must be a list of objects, got <value>`; absent or null, there
are none. They follow the descriptor and the
credentials Secrets in `Result.Objects` and in the `okdp` chart:
[outputs](01_inputs_outputs.md#outputs).

### instance

| Key | Meaning |
|---|---|
| `url` | the instance's URL, as text, trimmed |
| `usage` | how to use the instance, as text, trimmed, with one final newline |

Other keys are ignored. Both go to the descriptor ConfigMap:
[outputs](01_inputs_outputs.md#outputs). A value that is not a map fails
with `instance: must be a map (url, usage), got <value>`; absent or null,
both are empty.

### provides

```yaml
provides:
  - contract: iceberg-catalog                 # published under the release name
    values:
      uri: ${{ apiUrl }}/api/catalog
      realm: ${{ str(values.realm) }}
  - contract: s3
    name: store-a
    values: {apiUrl: 'https://a.${{ global.okdp.ingress.suffix }}'}
    secret:                                   # Secret store-a-s3-credentials, generated
      generate: [{key: accessKey, length: 20}, {key: secretKey, length: 40}]
      stringData: {region: eu}
```

A list of outputs, the connections the instance publishes. Each item:

| Field | Type | Default | Meaning |
|---|---|---|---|
| `contract` | string | required | The contract: `<contract>.schema.json` of `Inputs.Contracts`. |
| `name` | string | the release name | The output's name, the one other instances reference. Unique per contract: `two <contract> outputs are named "<name>"`. |
| `values` | map | `{}` | The contract's fields. Checked and completed against the contract (no unknown or secret field, defaults, required fields; under the release name, the values the contract's `x-okdp-internal` convention gives): [contracts](01_inputs_outputs.md#contracts). |
| `secretRef` | `{name}` | see `secret` | The Secret holding the secret fields. Only `name` is kept. |
| `secret` | map | none | The credentials Secret the compiler renders, below. |

Other keys are ignored. A value that is not a list fails with
`provides: must be a list of outputs, got <value>`; absent or null, there
are none. Each item must be a map, and its `values`, `secretRef` and
`secret` maps when set (null is not set); `<i>` is the item's position in
the evaluated list:

```text
provides[<i>]: must be a map (contract, name, values, secretRef, secret), got <value>
provides[<i>]: values: must be a map of contract fields, got <value>
provides[<i>]: secretRef: must be a map (name), got <value>
provides[<i>]: secret: must be a map (generate, stringData), got <value>
```

The contract checks then report
`provides "<name>" (<contract>): <message>`. The items are checked in
order, their fields in alphabetical order, and the first error is
reported ([descriptor ConfigMap](01_inputs_outputs.md#descriptor-configmap)).

#### secret

`{generate: [<key>, ...], stringData: {...}}` renders a generated Secret
([`okdpGeneratedSecret`](#okdpgeneratedsecret), `keys` = `generate`, same
key fields; `stringData` optional), `{stringData: {...}}` a plain Secret
with these keys.

The Secret is named after `secretRef.name` when given, else
`<name>-<contract>-credentials`, which then becomes the output's
`secretRef`. Without `secret` (or with `secret: {}`), no Secret is rendered
and the output has a `secretRef` only when the mapping gives one. A
`secret` with neither a non-empty `generate` nor a non-empty `stringData`
fails with `<service>: output "<name>": secret needs stringData or generate`
(`<service>`: the `name` of `service.yaml`). The rendered objects and their
labels: [credentials Secrets](01_inputs_outputs.md#credentials-secrets).

### oidcClients

```yaml
oidcClients:
  - redirectUris: ['https://${{ host }}/oauth2/callback']   # the main client: no name
    grantTypes: [authorization_code, refresh_token]
    scope: ${{ platform.oidc.scope }} offline_access
  - $if: ${{ values.console.enabled }}                       # the client is left out when false
    name: console                                            # chart console-oidc-dcr
    public: true
    redirectUris: ['https://${{ consoleHost }}/auth/callback']
    grantTypes: [authorization_code]
```

A list of the OAuth clients of the service, each a map. Items are
evaluated as any other: `$if` leaves a client out, `$for` with `$each`, or
`$spread`, gives several. A value that is not a list fails with
`oidcClients: must be a list of OAuth clients, got <value>`; absent or
null, there are none. The platform provides the clients, or each service
registers its own by OIDC Dynamic Client Registration, per
`global.okdp.oidc.clientProvisioning` ([context](#context)):

| Mode | The compiler |
|---|---|
| `dcr` (`platform.oidc.dcr.enabled`) | adds one chart per client, rendered by the platform's oidc-dcr chart (`global.okdp.charts.oidcDcr`, oidc-dcr 0.5.0 by default: [platform charts](01_inputs_outputs.md#platform-charts)): `oidc-dcr` for the main client, `<name>-oidc-dcr` for a named one. Its Job registers the client and writes its Secret ([OAuth client charts](01_inputs_outputs.md#oauth-client-charts)); the platform's registration settings are then required ([OAuth client settings](01_inputs_outputs.md#oauth-client-settings)). |
| otherwise (`existing`, or `platform.oidc.enabled` false) | adds no chart: the platform provides each client's Secret |

| Key | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | `""` | `""`: the service's main client, one at most. Else the client's name, unique: lowercase letters, digits and `-`, starting and ending with a letter or a digit. It names the client's chart, its objects and its Secret. |
| `grantTypes` | list of strings | required | The OAuth grant types the client registers (`authorization_code`, `refresh_token`, `client_credentials`...), a non-empty list of non-empty strings. |
| `redirectUris` | list of strings | `[]` | Its redirect URIs, each non-empty. |
| `scope` | string | `platform.oidc.scope` | The scope it requests, space separated. |
| `clientName` | string | `<release>-<namespace>`, then `-<name>` for a named client | Its name at the identity provider. |
| `public` | boolean | `false` | A public client (a browser application), without a client secret. |
| `request` | map | `{}` | Fields of the registration request (snake_case, as OIDC Dynamic Client Registration names them): they override the computed ones. |
| `secret` | string | [`oidcClientSecret(<name>)`](#oidcclientsecret) | The Secret the Job writes the client to. |
| `caSecret` | string | `platform.oidc.dcr.caSecret` | The Secret holding the CA bundle the Job trusts. |

A key whose value is null (left out by `$if`, a null expression) is not
set, and `""` takes the default of a string key. Only `dcr` mode uses the
keys; how they become the values of the client's chart:
[OAuth client charts](01_inputs_outputs.md#oauth-client-charts).

Every mode checks the clients, after the mapping: each client is a map of
these keys only, each of its type, `name` of the form above, `grantTypes`
given; one client at most has no name, and names are unique. As soon as the
mapping declares a client, its chart key belongs to the compiler:
`service.yaml` may not list it, nor `output` compute it. Only `dcr` mode
reads the oidc-dcr chart, from the chart cache
([chart cache](01_inputs_outputs.md#chart-cache)). The first failing
check stops the compile ([compile errors](#compile-errors)).

The mapping reads the name of a client's Secret with
[`oidcClientSecret(name)`](#oidcclientsecret) (the main client's is also
`platform.oidcClientSecret`), in any section; a client that sets `secret`
references that name itself. The keys the Secret holds:
[OAuth client charts](01_inputs_outputs.md#oauth-client-charts).

## Expressions

### Lone expressions and interpolation

| Scalar | Value |
|---|---|
| `${{ expr }}`, the whole scalar | the expression's value, with its type: number, boolean, string, list, map or null |
| text and expressions (`https://${{ host }}`, `${{ a }}${{ b }}`) | a string: the text, and each expression's value as text ([`str`](#str-and-first)) |
| `$${{` | a literal `${{` |
| no `${{` | the scalar as YAML reads it ([YAML reading](#yaml-reading)) |

The whole scalar means nothing around the expression, not even a space
inside the quotes (`' ${{ 1 }}'` is `" 1"`) or the final newline of a `|`
or `>` block scalar (`"1\n"`); `>-` and `|-` keep a lone expression. `$`,
`${VAR}` and `{{ }}` without `${{` are text: shell variables and Go
templates pass through.

The `}}` closing an expression is found skipping quoted strings and nested
braces: `${{ {"k": "}}"}.k }}` is one expression. A scalar without the
closing `}}` fails with `unterminated ${{ in "<scalar>"`.

Map keys are never evaluated: `${{ x }}: 1` is the key `${{ x }}`. Computed
keys come from [`$for`](#for) (`$key`) or [`$merge`](#merge).

An expression is compiled when it is evaluated: one under a false `$if`,
in a `$for` over no element, in a key a directive ignores or in an unknown
section is never compiled, and its errors go unnoticed.

The value of an expression is plain data: every number (int, uint, double)
becomes a double, written without decimals when integral; a duration or a
timestamp becomes its text (`3600s`, RFC 3339), bytes their base64 text, an
optional its value or null.

### Numbers

Every number of the context (values are read as Helm reads them) and of the
mapping's YAML (`1`, `0x1F`) is a CEL `double`; a CEL literal `1` is an
`int`. Comparisons mix them (`values.replicas > 1`, `n == 1`); arithmetic
does not: `values.replicas + 1` fails with `no such overload`. Write
`values.replicas + 1.0` or `int(values.replicas) + 1`.

As text (interpolation, `str`), a number prints as Go prints a float64, as
Helm prints a values number: `8080`, `2.5`, `1e+06` (from one million on),
`1e-05`. `string(int(x))` prints every digit.

### YAML reading

The mapping's scalars and keys are read as Helm reads a values file
(sigs.k8s.io/yaml, YAML 1.1): a snippet copied from a `values.yaml` means
the same in the mapping.

| Plain scalar | Value |
|---|---|
| `y`, `Y`, `yes`, `Yes`, `YES`, `on`, `On`, `ON` | `true` |
| `n`, `N`, `no`, `No`, `NO`, `off`, `Off`, `OFF` | `false` |
| `"yes"`, `'no'`, `!!str on` (quoted or tagged) | the string |
| `0777`, `0x1F`, `0b101`, `1_000` | `511`, `31`, `5`, `1000` |
| `2001-12-14` | the string: timestamps are not decoded |
| `~`, `null`, empty | null |

Keys are decoded the same way, then written as text as sigs.k8s.io/yaml
writes them: `on:` is the key `"true"`, `1.0:` is `"1"`, `0x10:` is `"16"`.
A null key fails with `line <n>: unsupported map key "~"`, a key that is
not a scalar with `line <n>: a map key must be a scalar`. A key given twice:
the last value wins, at the position of the first. Anchors and aliases are
resolved; merge keys are not: `<<` stays a key.

Binding names are the exception: the keys directly under `let` and `$let`
are taken as written, so a binding `y`, `n` or `on` keeps its name.

### Quoting

YAML ends or splits a plain scalar on characters CEL uses. Quote the scalar,
or write a long expression as a `>-` block scalar.

| Pitfall | Fails | Write |
|---|---|---|
| `: ` in a plain scalar (a ternary, a map literal) | `v: ${{ on ? 'a' : 'b' }}`: `yaml: line <n>: mapping values are not allowed in this context` | `v: "${{ on ? 'a' : 'b' }}"` |
| ` #` starts a comment | `v: ${{ 'a #b' }}`: `unterminated ${{` | `v: "${{ 'a #b' }}"` |
| `,` `[` `]` `{` `}` inside a flow collection | `{name: ${{ n }}}`: `yaml: line <n>: did not find expected ',' or '}'` | `{name: "${{ n }}"}` |
| a final newline (`\|`, `>`) makes text | `v: >` then `${{ 1 }}`: `"1\n"` | `v: >-` |
| quotes of the scalar's own kind | `'${{ x == 'a' }}'` | `"${{ x == 'a' }}"`, or `'${{ x == ''a'' }}'` |

## Directives

| Directive | In | Value | Result |
|---|---|---|---|
| [`$if`](#if) | a map | a condition | the map is left out when the condition is not true |
| [`$value`](#value) | a map | any node | the map is this value |
| [`$for`](#for) with `$key`, `$value` | a map | `<name> in <expression>` | a map, one key per element |
| [`$for`](#for) with `$each` | a list item | `<name> in <expression>` | items per element |
| [`$let`](#let-1) | next to `$for` | a map of bindings | bindings per element |
| [`$merge`](#merge) | a map | an expression returning a map | merged over the map's keys |
| [`$spread`](#spread) | a list item | an expression returning a list | its items, spliced in |

A map is evaluated in this order: `$if`; then `$for` (with `$key`,
`$value`, `$let`); else `$value`; else each key in order, then `$merge`. A
list item that is a map: `$for` (with `$each`, `$let`); else `$spread`;
else the item is evaluated as a map. `$if`, `$value`, `$for` and `$merge`
are thus reserved in any map, `$for` and `$spread` in a list item; `$key`,
`$each` and `$let` elsewhere are ordinary keys. Directives nest: a `$for`
in an `$each` sees the outer variable.

### `$if`

```yaml
metrics:
  $if: ${{ values.metrics.enabled }}      # output.metrics: the chart is not rendered
  target: http://${{ release.name }}-app:8080/metrics
env:
  - $if: ${{ db.size() > 0 }}             # the item is left out
    name: DB_DRIVER
    value: ${{ db.driver }}
labels:
  tier: {$if: "${{ values.replicas > 1 }}", $value: ha}
```

The value is evaluated as a scalar, then tested as a Helm condition: null,
`false`, `0`, `""`, `[]` and `{}` are false, anything else is true. It must
be an expression: a literal is text, so `$if: false` (or `no`, `0`) is the
non-empty text `"false"` and keeps the map; interpolated text
(`x${{ c }}`) is always true. Only an empty or null literal is false.

On a map, `$if` guards everything else (`$for`, `$value`, `$merge`, the
keys). A map left out is:

| As | Result |
|---|---|
| a map value | the key is left out |
| a list item | the item is left out |
| an `$each` | no item for this element |
| a `$value` of a `$for` map | the key, with a null value |
| a `let` or `$let` value | the binding is null |
| a section (`output`, ...) | the section is empty |

On a list item carrying `$for` or `$spread`, `$if` is ignored: put the
condition in the expression (`$for: "s in (oidcOn ? [platform.oidcClientSecret] : [])"`,
`$spread: ${{ cond ? list : [] }}`).

### `$value`

The map stands for the value of `$value`, which need not be a map:
`{$if: cond, $value: x}` is a conditional scalar or list. Every other key of
the map, `$merge` included, is ignored. In a list, a `$value` list is one
item, not spliced.

### `$for`

`$for: <name> in <expression>`: the text before the first ` in `, trimmed,
is the variable; the rest is a CEL expression, written without `${{ }}`
(`c in ${{ list }}` fails). Quote the scalar when the expression holds
YAML characters: `$for: "k in ['a', 'b']"`. The expression must return a
list, or null (no element). Errors:
`$for: "<spec>" is not "<name> in <expression>"`, `$for "<spec>": not a list`
(a map is not a list).

For each element, in order, the scope is the enclosing one, the variable
bound to the element, then the [`$let`](#let-1) bindings. The variable and
the bindings shadow the context and `let`, and exist only inside the
`$for`.

In a map, `$key` and `$value` give one entry per element:

```yaml
dags:
  gitSync:
    $for: e in gitSync                    # a let binding: [{k, v}, ...]
    $key: ${{ e.k }}
    $value: ${{ e.v }}
```

`$key` is converted to text ([`str`](#str-and-first)); a later element with
the same key wins. The result has these keys only: other keys of the map,
`$merge` included, are ignored. No element gives `{}`.

In a list item, `$each` gives the items of each element:

```yaml
env:
  - name: FIXED
    value: "1"
  - $for: "k in ['HTTPS_PROXY', 'HTTP_PROXY', 'NO_PROXY'].filter(p, p in platform.proxyEnv)"
    $each:                                # a list: spliced, two items per element
      - {name: "${{ k }}", value: "${{ str(platform.proxyEnv[k]) }}"}
      - {name: "${{ k.lowerAscii() }}", value: "${{ str(platform.proxyEnv[k]) }}"}
```

A list value of `$each` (a YAML list, or an expression returning a list) is
spliced: its items are added; any other value is one item; a value left out
by `$if` adds nothing. A list as one item is nested: `$each: [[a, b]]`.
Other keys of the item, `$if` included, are ignored.

### `$let`

```yaml
pyspark:
  - $for: name in pysparkNames
    $let:
      matches: ${{ sparkConnections.filter(c, c.?name.orValue('') == name) }}
      c: "${{ matches.size() > 0 ? matches[matches.size() - 1] : {} }}"
      prefix: ${{ name.replace('-', '_').upperAscii() }}
    $each:
      name: ${{ prefix }}
      value: ${{ str(c.?properties.orValue('')) }}
```

With `$for` only: a map of bindings, evaluated for each element, in order,
after the variable; each sees the enclosing scope, the variable and the
bindings before it. Values are nodes (directives included; left out by
`$if`, null). Names are taken as written. A `$let` that is not a map is
ignored; without `$for`, `$let` is an ordinary key. An error is reported as
`$let <name>: <error>`.

### `$merge`

```yaml
ingress:
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: 64m
    $merge: ${{ platform.ingressAnnotations }}  # the platform's annotations win
spark-defaults:
  $merge: ${{ values['spark-defaults'] }}
```

In a map, after its other keys are evaluated, the map the expression
returns is merged over them, wherever `$merge` is written: maps merge key
by key at any depth; lists, scalars and nulls replace (Helm's merge of `-f`
files). A value that is not a map merges nothing, a literal YAML map
included (`$merge` takes an expression).

### `$spread`

```yaml
objects:
  - $spread: >-
      ${{ okdpGeneratedSecret({'name': rootSecret, 'keys': [{'key': 'client_secret'}],
          'stringData': {'client_id': 'root'}}) }}
```

A list item replaced by the items of the list the expression returns. A
value that is not a list (null included) adds nothing. Other keys of the
item, `$if` included, are ignored.

## Context

The variables of every expression:

| Variable | Value |
|---|---|
| `release` | `{name, namespace}`: `Inputs.Release`, `Inputs.Namespace` |
| `values` | The parameters: the package's `values.yaml` and the instance values layers, merged and checked against `values.schema.json` ([parameters](01_inputs_outputs.md#parameters)), each connection reference replaced by its resolved connection, `""` when not set ([connections](01_inputs_outputs.md#connections)). Without `global` and `connections`. `upstream.<key>` of a chart that accepts upstream values is its files then its inline values, merged as they will be applied (the `append` lists appended); the other keys of `upstream` stay as written. |
| `global` | `{okdp: ...}`: `global.okdp` of the same merged values, as written ([platform values](01_inputs_outputs.md#platform-values)). Other keys of `global` are not visible. |
| `platform` | Derived from `global.okdp`, below. |
| `service` | `{name, version, appVersion}` of `service.yaml`, as written ([service](#service)). |
| `labels` | the labels of OKDP's objects, below, without component. |
| `selectorLabels` | their selector labels, below, without component. |
| `let` bindings | [let](#let), in order |
| `$for` variables, `$let` bindings | inside their [`$for`](#for) |

A missing key is an evaluation error (`no such key: <key>`): test it with
`has(values.x)`, or read it with `values.?x.orValue(<default>)`,
`m[?'k'].orValue(<default>)`. A name that is not in scope fails at compile
time: `undeclared reference to '<name>'`.

`platform`, derived from `global.okdp` by the compiler:

| Variable | Value |
|---|---|
| `platform.oidc` | `global.okdp.oidc`, with: `enabled` true when the key is absent; `scope` `"openid profile email groups"` when empty; `clientProvisioning` `"existing"` when empty, else `existing` or `dcr` (any other value fails the compile: `global.okdp.oidc.clientProvisioning "<v>" is not one of existing, dcr`); `dcr`: `global.okdp.oidc.dcr`, the settings of the oidc-dcr Jobs, defaults applied ([OAuth client settings](01_inputs_outputs.md#oauth-client-settings)), and `enabled` set to `enabled && clientProvisioning == "dcr"` (a written value is replaced); `existing`: `enabled && clientProvisioning == "existing"`. |
| `platform.oidcClientSecret` | The Secret holding the instance's main OAuth client: [`oidcClientSecret('')`](#oidcclientsecret). |
| `platform.ingressAnnotations` | `{"cert-manager.io/cluster-issuer": <global.okdp.certificateIssuers.selfSigned.name>}`, `""` when unset. |
| `platform.proxyEnv` | `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`: `global.okdp.proxy.httpProxy`, `httpsProxy`, `noProxy`, each only when non-empty; `{}` when none. |

The settings these are derived from are checked against their types first
(`global.okdp.oidc must be a map, got false`):
[fields the compiler reads](01_inputs_outputs.md#fields-the-compiler-reads).
`enabled` is `true` or `false`; a `null` is kept, and reads as false.

`labels` and `selectorLabels` (and the functions
[`okdpLabels`, `okdpSelectorLabels`](#okdplabels-and-okdpselectorlabels)),
the labels of every object OKDP renders itself:

| Label | Value | `selectorLabels` |
|---|---|---|
| `app.kubernetes.io/name` | `service.name` | yes |
| `app.kubernetes.io/instance` | `release.name` | yes |
| `app.kubernetes.io/component` | the component, when not empty (functions only) | yes |
| `app.kubernetes.io/version` | `service.appVersion`, when not empty | no |
| `app.kubernetes.io/managed-by` | `Helm` (the okdp chart renders them) | no |
| `app.kubernetes.io/part-of` | `okdp` | no |
| `okdp.io/instance` | `release.name` | no |
| `okdp.io/service` | `service.name` | no |

No `helm.sh/chart`: the objects belong to no chart of the service.

Bindings shadow context variables of the same name in expressions. The host
functions do not see bindings: `okdpFullname`, `okdpLabels`,
`okdpSelectorLabels` and `okdpGeneratedSecret` read `release` and `service`
of the compile context, `oidcClientSecret` its `release` and
`platform.oidc.dcr.enabled`.

## Functions

### CEL libraries

Besides the CEL standard library (operators, `has`, `size`, `type`,
`matches`, `contains`, `startsWith`, `endsWith`, conversions `int`,
`double`, `string`, `bool`, `bytes`, `duration`, `timestamp`, macros `all`,
`exists`, `exists_one`, `map`, `filter`), the environment enables, at their
latest version:

| Library | Functions |
|---|---|
| strings (`ext.Strings`) | `charAt`, `indexOf`, `lastIndexOf`, `lowerAscii`, `upperAscii`, `replace`, `split`, `substring`, `trim`, `join`, `format`, `reverse`, `strings.quote` |
| lists (`ext.Lists`) | `lists.range`, `slice`, `flatten`, `distinct`, `reverse`, `sort`, `sortBy` |
| bindings (`ext.Bindings`) | `cel.bind(name, value, expr)` |
| two-variable comprehensions (`ext.TwoVarComprehensions`) | `all(k, v, ...)`, `exists(k, v, ...)`, `existsOne`, `transformList`, `transformMap`, `transformMapEntry` |
| encoders (`ext.Encoders`) | `base64.encode`, `base64.decode` |
| optional types (`cel.OptionalTypes`) | `x.?field`, `m[?key]`, `l[?index]`, `orValue`, `hasValue`, `value`, `or`, `optional.of`, `optional.none`, `optional.ofNonZeroValue`, optional entries `{?'k': opt}` |

The math, sets and regex extensions are not enabled.

### Host functions

| Function | Returns | Helm equivalent |
|---|---|---|
| `str(x)` | string | printing `{{ x }}` |
| `first(list)` | any | `a \| default b` |
| `sha256(x)` | string | `sha256sum` |
| `toJson(x)` | string | `toJson` |
| `toPrettyJson(x)` | string | `toPrettyJson` |
| `toYaml(x)` | string | `toYaml` |
| `quote(x)` | string | `quote` |
| `okdpFullname()`, `okdpFullname(suffix)` | string | `okdp.fullname` |
| `okdpLabels(component)` | map | `okdp.labels`, without `helm.sh/chart` |
| `okdpSelectorLabels(component)` | map | `okdp.selectorLabels` |
| `okdpGeneratedSecret(args)` | list of objects | `okdp.generatedSecret` |
| `oidcClientSecret(name)` | string | `okdp.oidc.clientSecret` (the main client) |

Each takes one argument (`okdpFullname`: zero or one; `first`: a list) and
is deterministic. A call matching none of these fails:
`found no matching overload for '<function>' applied to '(<types>)'`.

### `str` and `first`

- `str(x)`: `x` as text, as Go templates print it: null is `""`, a string
  itself, a number as a float64 ([Numbers](#numbers)), `true`, a list
  `[a b]`, a map `map[k:v]`. Interpolation converts every expression's
  value with it.
- `first(list)`: the first element that is not empty (null, `false`, `0`,
  `""`, `[]`, `{}`), else `""`. `first([x, d])` is Helm's `x | default d`.
  The argument must be a list.

### `sha256`, `toJson`, `toPrettyJson`, `toYaml`, `quote`

| Function | Result |
|---|---|
| `sha256(x)` | hex SHA-256 of `str(x)`; bytes are hashed as they are, null as `""` (Helm hashes `<nil>`) |
| `toJson(x)` | Go's `encoding/json`: compact, keys sorted, `<`, `>`, `&`, U+2028 and U+2029 escaped |
| `toPrettyJson(x)` | the same, indented by two spaces |
| `toYaml(x)` | sigs.k8s.io/yaml: keys sorted, YAML 1.1 quoting (`"yes"`, `"1.0"`), no final newline |
| `quote(x)` | Go's `%q` of `str(x)` (bytes as they are): `"a\"b"`; null gives `""`, nothing rather than two quotes |

An encoding that fails (a string `toYaml` cannot write) fails the compile:
`toYaml: <error>` (Helm would print `""`).

```yaml
podAnnotations:
  checksum/settings: ${{ sha256(toJson(settings)) }}   # restarts the pods on a change
```

### `okdpFullname`

`okdpFullname(suffix)`: `<release>-<suffix>`, cut to 63 bytes, without a
trailing `-`. `okdpFullname()` and `okdpFullname('')` give the release name
alone, cut the same way. The suffix is converted with `str`. Named
`okdpFullname`, not `fullname`, so that a mapping can bind its own
`fullname`.

### `okdpLabels` and `okdpSelectorLabels`

`okdpLabels(component)`, `okdpSelectorLabels(component)`: the
[labels](#context) of the context, with `app.kubernetes.io/component`
set to `str(component)`, left out when it is `''`.

### `okdpGeneratedSecret`

The objects of a Secret whose random values External Secrets Operator
generates in the cluster, as `okdp.generatedSecret` renders them, for
`$spread` in [`objects`](#objects). The `provides[].secret` `generate` form
uses it too.

| Key of `args` | Default | Meaning |
|---|---|---|
| `name` | required | name of the Secret, and of the ExternalSecret writing it |
| `keys` | `[]` | the generated keys, below |
| `stringData` | `{}` | static keys, as text (`str`; null is `<nil>`); a value containing `{{` is written as a template string literal |
| `labels` | `{}` | extra labels of the ExternalSecret and of the Secret, over [`labels`](#context) |
| `annotations` | `{}` | annotations of every object |
| `type` | none | type of the Secret |
| `namespace` | `release.namespace` | namespace of every object |
| `refreshInterval` | `"0"` | refresh interval of the ExternalSecret: `"0"` generates once |
| `apiVersion` | `external-secrets.io/v1` | apiVersion of the ExternalSecret |

| Key of `keys[]` | Default | Meaning |
|---|---|---|
| `key` | required | the Secret's key |
| `length` | `32` | length of the generated value |
| `digits` | `6` | number of digits; `0` when given as `0` |
| `symbols` | `0` | number of symbols |
| `noUpper` | `false` | no upper-case letters |
| `transform` | none | `sha256` (the key holds the value's `sha256sum`) or `b64enc` |

An empty value (null, `""`, `0`, `false`) takes the default, as Helm's
`default` does, except `digits`.

It returns one `generators.external-secrets.io/v1alpha1` `Password` per
key, in order, then the `ExternalSecret`. A Password is named
`<name>-<key>`, the key lower-cased with `_` and `.` as `-`; a name longer
than 63 characters is cut to 54, without a trailing `-`, and suffixed with
`-` and 8 hex characters of the key's SHA-256. Passwords carry
[`labels`](#context). The Secret's data are the `stringData` keys, then each
generated key; a generated key replaces a `stringData` key of the same name.

Errors: `okdp.generatedSecret: name is required`,
`okdp.generatedSecret: every key needs a name`,
`okdp.generatedSecret: <name>: keys "<a>" and "<b>" give the same generator name "<n>"`,
`okdp.generatedSecret: unknown transform "<t>" (supported: sha256, b64enc)`.

### `oidcClientSecret`

`oidcClientSecret(name)`: the name of the Secret holding the OAuth client
`name` of [`oidcClients`](#oidcclients), `''` (or null) for the main client;
`name` is converted with `str`. In `dcr` mode (`platform.oidc.dcr.enabled`),
the Secret its oidc-dcr Job writes, `<release>-<namespace>[-<name>]-dcr`;
otherwise, the Secret the platform provides, `creds-<release>-oauth2[-<name>]`
([OAuth client charts](01_inputs_outputs.md#oauth-client-charts)).
`oidcClientSecret('')` is `platform.oidcClientSecret`.

```yaml
env:
  - name: CONSOLE_CLIENT_ID
    valueFrom: {secretKeyRef: {name: "${{ oidcClientSecret('console') }}", key: client_id}}
```

It depends on the mode and the name only: it does not check that `name` is
a client of `oidcClients`, and does not know a client's `secret`. It is
available in every expression, `let` and `errors` included.

## Errors

### The errors section

```yaml
errors:
  - $if: ${{ values.replicas > 1 && db.size() == 0 }}
    message: "webapp: replicas > 1 needs a database"
  - $for: p in ['s3SecretRef', 'trinoOidcSecretRef']
    $each:
      $if: ${{ first([values[p]]) == '' }}
      message: "okdp-examples: ${{ p }} is required"
  - >-
    ${{ realms.filter(r, r.admin == null)
        .map(r, 'okdp-examples: realm "' + r.name + '" has no catalog_admin') }}
```

`errors` is a list, evaluated after `let`, before the other sections, as
any list ([directives](#directives)): `$if` leaves an item out, `$for` and
`$spread` give several. Each value it gives is:

- a rule, `{message: <text>}`: its message. A rule is written with its
  condition, `$if`: the message is evaluated only when the condition holds,
  and may read what exists only then (`db.okdp.ref` of a database
  connection);
- a list: its messages, each converted with `str`. An expression returning
  the messages of a check, `[]` when it passes, for messages computed
  together (`filter` and `map`).

Anything else fails the compile: `errors[<i>]: a rule is {$if: <condition>,
message: <text>}, or an expression returning a list of messages, got
<value>`, as does a message that is not text (`message must be text, got
<value>`) or is empty (`an empty message`). Every item is evaluated; when
there is any message, the compile fails with the messages joined by
newlines, without prefix:

```text
webapp: replicas > 1 needs a database
okdp-examples: s3SecretRef is required
okdp-examples: realm "data" has no catalog_admin
```

An `errors` that is not a list is ignored. An item whose evaluation fails
stops the compile with that error, prefixed with the position of the item
as written (`errors[1]: `, `errors[1]: message: `).

### Compile errors

The first error stops the compile (the `errors` section and the schema
violations excepted). An error of an expression reads
`<where>${{ <expression> }}: <cause>`, `<where>` being:

| Expression in | `<where>` |
|---|---|
| a `let` binding | `let <name>: ` and the path below it |
| a `$let` binding | the path to its `$for`, then `$let <name>: ` |
| a section | `<section>: ` and the path to the expression: `output: app: env[2]: value: ` |
| an `errors` item | `errors[<i>]: ` and the path below it |

The path names each map key, followed by `: `, and each list position, as
`[<i>]` (from 0) right after the key of its list: `hosts[0]: `,
`objects[1]: `, `l[0][1]: ` for a list in a list. A position counts the
items of the YAML list as written: an item carrying `$for` or `$spread` is
one position, whatever it produces.

The cause is either a compile-time error of CEL, with its position and the
source line, or an evaluation error:

```text
output: app: ingress: hosts[0]: host: ${{ hots }}: ERROR: <input>:1:2: undeclared reference to 'hots' (in container '')
 |  hots
 | .^
let db: ${{ values.databse }}: no such key: databse
output: app: replicaCount: ${{ values.replicas + 1 }}: no such overload
objects[1]: ${{ okdpGeneratedSecret({'keys': []}) }}: okdp.generatedSecret: name is required
```

Other errors of the mapping:

| Message | Cause |
|---|---|
| `yaml: line <n>: <message>` | invalid YAML, often an unquoted expression holding `: ` ([quoting](#quoting)); the message names no file |
| `the mapping must be a YAML map` | the document is not a map |
| `line <n>: a map key must be a scalar`, `line <n>: unsupported map key "<key>"` | a list, map or null key |
| `<where>unterminated ${{ in "<scalar>"` | an expression without its `}}` |
| `<where>$for: "<spec>" is not "<name> in <expression>"`, `<where>$for "<spec>": not a list` | [`$for`](#for) |
| `output: must be a map of charts to their values, got <value>`, `instance: must be a map (url, usage), got <value>`, `provides: must be a list of outputs, got <value>` | a section of another shape ([sections](#evaluation-order)) |
| `objects: must be a list of objects, got <value>`, `objects[<i>]: not an object with a kind and a metadata.name: <object>` | [`objects`](#objects) |
| `output: <key>: must be a map of the chart's values, got <value>` | a chart of [`output`](#output) whose values are a scalar or a list |
| `mapping output "<name>": not a chart of service.yaml` | [`output`](#output) |
| `mapping output "okdp": the chart of OKDP's own objects is compiled from the objects section: remove it from output` | [the okdp chart](#the-okdp-chart) |
| `provides[<i>]: must be a map (contract, name, values, secretRef, secret), got <value>`, `provides[<i>]: <key>: must be a map ..., got <value>` | a [`provides`](#provides) item, or its `values`, `secretRef` or `secret`, of another shape |
| `provides "<name>" (<contract>): <message>`, `two <contract> outputs are named "<name>"`, `<service>: output "<name>": secret needs stringData or generate` | [`provides`](#provides) |
| `oidcClients: must be a list of OAuth clients, got <value>` | [`oidcClients`](#oidcclients) is not a list |
| `<client>: must be a map (name, grantTypes, redirectUris, scope, clientName, public, request, secret, caSecret), got <value>`, `<client>: unknown key(s) <keys> (allowed: name, grantTypes, ...)` | a client that is not a map, or has another key |
| `<client>: name <value> must be lowercase letters, digits and "-" (it names the chart <name>-oidc-dcr, its objects and its Secret)` | a `name` that is not a string of that form |
| `<client>: grantTypes is required: a list of OAuth grant types (authorization_code, refresh_token, client_credentials...)` | `grantTypes` absent, empty, or holding an item that is not a non-empty string |
| `<client>: redirectUris must be a list of URLs, got <value>`, `<client>: <key> must be a string, got <value>`, `<client>: public must be true or false, got <value>`, `<client>: request must be a map of registration fields, got <value>` | a key of another type (`<key>`: `scope`, `clientName`, `secret`, `caSecret`) |
| `<client>: <other> has no name either: only the service's main client (chart oidc-dcr) has none, name the others`, `<client>: <other> has the same name: client names must be unique` | two clients without a name, two clients of the same name |
| `service.yaml: charts.<chart>: the chart of <client> is added by the compiler: remove it from service.yaml`, `mapping output "<chart>": the chart of <client> is compiled from oidcClients: remove it from output` | a client's chart listed in `service.yaml` or computed in `output` |
| `<client>: oidc-dcr values: <error>` | an expression of the client chart's values template failing on the context, e.g. a `global.okdp.oidc.scope` that is not a string |

`<client>`, `<other>`: `oidcClients[<i>]`, the client's position in the
evaluated list, followed by ` (<name>)` once its name is checked. `<value>`:
the value as Go's `%#v` prints it (`"x"`, `true`). The errors of the
platform's `global.okdp.oidc.dcr` settings:
[OAuth client settings](01_inputs_outputs.md#oauth-client-settings).

The `okdp-compile` binary prints them after `error: `
([usage](../01_usage.md)).
