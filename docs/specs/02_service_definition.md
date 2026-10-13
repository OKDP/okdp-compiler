# Service definition

Normative reference of a service definition: the two files at the root of
a package that `compiler.LoadDefinition(fsys)` reads.

| File | Content |
|---|---|
| [`service.yaml`](#serviceyaml) | the engine-neutral part: the service's name and versions, the upstream charts and their sources, OKDP's defaults for them, what an instance may override, the platform values the service needs |
| [`values.cel.yaml`](#valuescelyaml) | the mapping: YAML shaped like the upstream values, with `${{ CEL }}` expressions and directives; it computes the values of each chart, OKDP's own objects and what the instance publishes, and declares the service's OAuth clients |

Both files are required: a missing one fails `LoadDefinition` with the read
error (`open service.yaml: ...`), and a `service.yaml` that does not decode
fails with `service.yaml: <error>`. `LoadDefinition` only reads
`values.cel.yaml`: each `Compile` parses and evaluates it, so its errors
are compile errors ([Errors](#errors)). It keeps the package
(`Definition.Files`): each compile reads its other files,
`config.schema.json` and the defaults files
([package files](01_inputs_outputs.md#package-files)).

The other inputs of a compile (the chart cache, contracts, platform and
instance values) and its outputs are specified in
[01_inputs_outputs.md](01_inputs_outputs.md).

## service.yaml

Decoded by `go.yaml.in/yaml/v3`: YAML 1.2, unlike values files and the
mapping (a plain `upstream: no` is a string, refused:
[upstream](#upstream)). Strictly: a field the tables below
do not list fails rather than being ignored, where a misspelt `protect`
would leave its paths unprotected, and so does a key that only holds an
anchor (write the anchor at its first use, as in [sources](#sources)).
Each unknown field is reported, one per line, as yaml.v3 reports it, with
the Go type of its place (`okdp.Service` the top level, `chart.Spec` a
chart, `chart.Ref` a `source`); [`upstream`](#upstream) checks its own
form:

```text
service.yaml: yaml: unmarshal errors:
  line 7: field protcet not found in type chart.Spec
```

The file is one YAML document: a second one, after `---`, is ignored. An
empty file is a service without a name (`service.yaml: name is required`).

### Service

| Field | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | required | The service: [`service.name`](#context), labels `app.kubernetes.io/name` and `okdp.io/service`, descriptor `data.service`, heading of the parameter schema violations. |
| `version` | string | required | The package's version, `<upstream>-<okdp semver>` by convention: `service.version`, descriptor `data.version`. |
| `roles` | list of strings | `[]` | Catalog metadata: what the service provides. Not read by the compiler. |
| `dependsOn` | list of strings | `[]` | Catalog metadata: the platform services the service needs. Not read by the compiler. |
| `require` | list of keys | `[]` | Platform values the service needs: dotted paths under `global.okdp` (`ingress.suffix`), each of which must be set and not empty: [require](#require), [requirements](01_inputs_outputs.md#requirements). |
| `charts` | map: key → [chart](#chart) | `{}` | The upstream charts of the service, by key, a DNS label ([chart keys](#chart-keys)): chart `<key>` is `output.<key>` of the mapping, rendered by its [source](#sources). `okdp` and the charts of the OAuth clients are not listed: the compiler adds them ([the okdp chart](#the-okdp-chart), [oidcClients](#oidcclients)). |

Without `name` or `version`, loading fails: `service.yaml: name is required`,
`service.yaml: version is required`.

### require

An entry is a key under `global.okdp`, required in every case:

```yaml
require:
  - ingress.suffix
  - oidc.issuerUri
```

Every compile checks the entries in order, once the context is built and
before the mapping: [requirements](01_inputs_outputs.md#requirements). A
scalar entry is its key (`true` is the key `true`); an empty key is the
whole of `global.okdp`. A value the platform sets in one mode only is read
by the mapping, which fails in its `errors` section when that mode lacks it
([recipe](../02_writing_package.md#require-a-platform-value-in-one-mode)).
`require` of another form, a map or an entry that is a list or a map,
fails when `service.yaml` is loaded, with yaml.v3's error:

| Case | Error |
|---|---|
| `require` not a list | ``service.yaml: yaml: unmarshal errors:`` then ``line <n>: cannot unmarshal !!str `a` into []string`` |
| an entry a list or a map | `service.yaml: yaml: unmarshal errors:` then `line <n>: cannot unmarshal !!map into string` |

### Chart

| Field | Type | Default | Meaning |
|---|---|---|---|
| `source` | `{repository, chart, version}` | required | The upstream chart that renders the key's values: [sources](#sources). |
| `defaults` | string | none | Path, in the package, of OKDP's defaults for this chart, over its `values.yaml` and under the computed values (by convention `<key>.defaults.yaml`). Read as a Helm values file; a missing file, or a path out of the package, fails the compile (`<key>: open <path>: ...`). |
| `upstream` | `true` or `false` | `false` | The instance upstream values the chart accepts: [upstream](#upstream). |
| `protect` | list of paths | `[]` | Values the instance upstream values may not set: [protect and append](#protect-and-append). |
| `append` | list of paths | `[]` | Lists the instance upstream values append to instead of replacing them: [protect and append](#protect-and-append). |

How the chart's own defaults, OKDP's (`defaults`), the computed values and the instance
upstream values combine: [merge precedence](01_inputs_outputs.md#merge-precedence).
A chart is rendered only when the mapping outputs it; its source must then
be in the chart cache ([chart cache](01_inputs_outputs.md#chart-cache)).

### Chart keys

A chart key is a DNS label (RFC 1123): lowercase letters, digits and `-`,
a letter or a digit at each end, 63 characters at most (`app`,
`admin-users`). It names the umbrella's dependency alias
([umbrella chart](01_inputs_outputs.md#umbrella-chart)). Another key
(`""`, `../app`, `my_app`, `App`) fails when `service.yaml` is loaded, and
by `Compile` for a definition built in memory:

```text
service.yaml: charts: key "../app": a DNS label (lowercase letters, digits and -, a letter or digit at each end, 63 characters at most): the key names the umbrella's dependency alias
```

### Sources

```yaml
charts:
  polaris-admin:
    source: &admin {repository: https://charts.example.org, chart: polaris-admin, version: 1.0.0}
    defaults: polaris-admin.defaults.yaml
    upstream: true
    protect: [fullnameOverride, phase, realms]
  polaris-admin-principals:            # the values of a second render
    source: *admin                     # the same chart
    defaults: polaris-admin-principals.defaults.yaml
```

`source` names the upstream chart that renders the key's values, as its
repository publishes it: `repository`, a chart repository (`https://`) or an
OCI registry path (`oci://`); `chart`, the chart's name in it; `version`, a
pinned version. A deployer renders the chart from there. The compiler reads
only its `values.yaml` and `values.schema.json`, from the chart cache
(`<repository>/<chart>-<version>/<chart>/`, `<repository>` the URL
without its scheme:
[chart cache](01_inputs_outputs.md#chart-cache)): the first layer of the
final values, and their schema
([merge precedence](01_inputs_outputs.md#merge-precedence)).
`Result.Charts` maps each compiled key to its source:
[chart index](01_inputs_outputs.md#chart-index).

Two keys of the same source are two renders of one upstream chart, each
with its own values (polaris-admin's bootstrap and principals phases).
Everything else stays keyed by the key: `output.<key>` of the mapping,
`Result.Values`, the instance's `upstream.<key>` values.

A source is checked when `service.yaml` is loaded, and by `Compile` for a
definition built in memory:

| Source | Error |
|---|---|
| absent, or all three fields empty | `service.yaml: charts.<key>.source is required: the upstream chart (repository, chart, version)` |
| `repository` not starting with `https://`, `http://` or `oci://`, or whose host and path are no directory of the [chart cache](01_inputs_outputs.md#chart-cache) (empty; an empty, `.` or `..` segment; a `\`) | `service.yaml: charts.<key>.source.repository "<repository>": the URL of a chart repository (https://... or oci://...)` |
| `chart` empty, holding a `/`, `.` or `..`, or with leading or trailing spaces | `service.yaml: charts.<key>.source.chart "<chart>": the name of a chart of the repository` |
| `version` not a SemVer version (`1.2.3`, `v1.2.3`, `1.2.3-rc.1`, `1.2.3+build`): a range, a partial version | `service.yaml: charts.<key>.source.version "<version>": a pinned chart version (1.2.3), not a range` |

### upstream

| Value | Instance upstream values read |
|---|---|
| absent, `false`, `null` | none: the instance's `upstream.<name>` values are ignored |
| `true` | the instance's `upstream.<name>` values |

A boolean of YAML 1.1 (`y`, `yes`, `n`, `no`, `on`, `off`, capitalized or
upper case) reads as the boolean it spells, as yaml.v3 decodes a boolean
field. A value of another form (a string, a number, a list, a map) fails
with yaml.v3's error:

```
service.yaml: line 7: cannot unmarshal !!map into bool
```
How the instance values are written:
[upstream values](01_inputs_outputs.md#upstream-values).

### protect and append

Paths of keys separated by dots (`ingress.hosts`). A key containing dots is
written in brackets, as one literal key (`config["spark.ui.filters"]`); a
bracketed key ending with `*` names every key starting with the text before
it (`ingress.annotations["cert-manager.io/*"]`; `["*"]` names every key);
no key holds a quote, a backslash or a star but a glob's. A
malformed path fails when `service.yaml` is loaded:
`service.yaml: charts.<key>.protect[<i>] "<path>": not a path: keys joined by dots, a key with a dot in brackets (a["b.c"]), a family of keys with a final * (a["prefix*"])` (`append[<i>]`
alike). `global` is always protected. Both apply to each instance upstream
file, then to the inline values: [protect](01_inputs_outputs.md#protect),
[append](01_inputs_outputs.md#append).

### The okdp chart

`okdp` is the compiler's: it adds the chart to
every compile, rendered by the platform's objects chart
(`global.okdp.charts.objects`), its values `Result.Objects`: [the okdp
chart](01_inputs_outputs.md#the-okdp-chart). `charts` may not declare it
(`service.yaml: charts.okdp: the chart of OKDP's own objects is added by the compiler: remove it from service.yaml`,
when the file is loaded), nor `output` compute it ([output](#output)).

### Example

```yaml
name: webapp
version: 2.3.0-1.0.0                   # <upstream>-<okdp semver>
roles: [webapp]                        # catalog metadata
dependsOn: [database-server, ingress]
require:                               # global.okdp.<path>, each non-empty
  - ingress.suffix
  - certificateIssuers.selfSigned.name
  - key: oidc.issuerUri                # only when OIDC is enabled
    $if: ${{ platform.oidc.enabled }}
charts:
  app:                                 # output.app of the mapping
    source: {repository: https://charts.example.org, chart: app, version: 1.8.0}
    defaults: app.defaults.yaml        # OKDP's defaults, over the chart's values.yaml
    upstream: true                     # the instance's upstream.app
    protect: [fullnameOverride, ingress.enabled, ingress.hosts, 'ingress.annotations["cert-manager.io/*"]']
    append: [env]                      # instance lists appended to the computed ones
  admin:
    source: &admin {repository: oci://registry.example.org/charts, chart: admin, version: 0.3.1}
    defaults: admin.defaults.yaml
    upstream: true                     # the instance's upstream.admin
  admin-users:                         # output.admin-users, a second render of the admin chart
    source: *admin
    defaults: admin-users.defaults.yaml
  metrics:                             # no OKDP defaults, no instance values
    source: {repository: oci://registry.example.org/charts, chart: metrics, version: 0.4.2}
```

## values.cel.yaml

A YAML map: values shaped like the upstream values, where `${{ <CEL> }}`
computes a value ([Expressions](#expressions)) and `$`-keys shape the
structure ([Directives](#directives)). Its top-level keys are the
[sections](#sections):

| Key | Type | Content |
|---|---|---|
| [`vars`](#vars) | map | named values, evaluated in order |
| [`errors`](#the-errors-section) | list | messages, each kept by its `$if`: any message fails the compile |
| [`output`](#output) | map | chart → computed values |
| [`objects`](#objects) | list | OKDP's own objects, whole manifests |
| [`instance`](#instance) | map | `url` and `usage` of the instance |
| [`provides`](#provides) | list | the connections the instance publishes |
| [`oidcClients`](#oidcclients) | list | the OAuth clients of the service |

Every section is optional. Any other top-level key, a directive included,
fails rather than being ignored (`outptu:` would render no chart),
`values.cel.yaml: <key>: unknown section (allowed: vars, errors, output, instance, provides, objects, oidcClients)`.

```yaml
vars:
  host: ${{ release.name }}.${{ global.okdp.ingress.suffix }}
  db: '${{ type(values.database) == map ? values.database : {} }}'
errors:
  - $if: ${{ values.replicas > 1 && db.size() == 0 }}
    $value: "webapp: replicas > 1 needs a database"
output:
  app:
    fullnameOverride: ${{ release.name }}-app
    replicaCount: ${{ values.replicas }}          # a number: the expression is the whole scalar
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

`examples/03_full/input/service/values.cel.yaml` uses every section,
directive and host function, most of the CEL libraries and of the YAML
reading (`$$`, `$${{`), each commented; its
`service.yaml` two renders of one chart, `protect` paths in brackets and
with a glob.

## Sections

### Evaluation order

The mapping is loaded first: read ([YAML reading](#yaml-reading)), its
top-level keys checked, then every [directive](03_control_flow.md#loading-and-evaluation) checked and
every [expression](#lone-expressions-and-interpolation) compiled, in the
whole document, the branches no instance takes included. The first
mistake fails the compile before anything is evaluated. Then:

1. `vars`, entry by entry: each sees the [context](#context) and the
   bindings before it.
2. `errors`, every entry, then the messages fail the compile together
   ([the errors section](#the-errors-section)).
3. `output`, `instance`, `provides`, `objects`, `oidcClients`, in this
   order: each sees the context and every `vars` binding. Then they are
   checked together for their shapes.
4. The OAuth clients are checked across clients, their charts added in
   `dcr` mode ([oidcClients](#oidcclients)); then the
   compile builds the descriptor from `instance` and `provides`, checked
   against the contracts, and the final values of each chart in `output`,
   of each client chart and of the `okdp` chart.

A section does not see the others: there is no `output` or `instance`
variable. A value used by several sections is a `vars` binding.

Each section has a shape: `output` and `instance` are maps, `provides`,
`objects` and `oidcClients` lists. So have the items and keys the compiler
reads in them, described by the mapping schema the compiler embeds,
[`internal/okdp/mapping.schema.json`](../../internal/okdp/mapping.schema.json):
their types, the keys required, the keys allowed where another is
refused, what each means in its `description`. It is the package authors'
reference. Absent or null (left out by `$if`, a null expression), a
section is empty. Below it, a null is read as in a chart's values: it
deletes a default the schema gives, and is refused by the type of a key
that has none (`- at '/output/app': got null, want object`); leave a key
out with `$if`. A value of another shape fails the compile rather than
read as empty: `output: [app]` would otherwise render no chart. Every violation
is reported at once, sorted by path, in Helm's words, as those of the
[parameters](01_inputs_outputs.md#schema-check):

```text
the mapping's sections don't meet the specifications of mapping.schema.json:
- at '/instance/url': got number, want string
- at '/oidcClients/0': missing property 'grantTypes'
- at '/output/app': got string, want object
```

A mapping that is not a YAML map fails with
`values.cel.yaml: the mapping must be a YAML map`; invalid YAML with the
parser's error (`values.cel.yaml: yaml: line <n>: ...`). The mapping
is its first YAML document: a second one, after `---`, is ignored. An
empty or null document (comments, `---` or `~` at most) has no section.

### vars

```yaml
vars:
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
before its entry fails: `undeclared reference`. Of a name written twice,
the later binding wins, as of any key ([YAML reading](#yaml-reading)).

A binding with the name of a context variable replaces it for every later
expression (a `vars` binding named `labels` wins over the context's
`labels`), not for the host functions ([Context](#context)). A `vars`
section that is neither a map nor null fails:
`values.cel.yaml: vars: must be a map of bindings, got <value>`.

### output

A map: chart name → the values the mapping computes for that chart.
Another value fails ([sections](#evaluation-order)).

- The values of a chart are a map (`- at '/output/<key>': got string, want object`):
  a scalar, a list or a null is refused, not compiled as the chart's
  defaults alone; `$if` leaves the chart out.
- Every key must be a chart of `service.yaml`:
  `mapping output "<key>": not a chart of service.yaml`. A key may not be
  the chart of a declared OAuth client ([oidcClients](#oidcclients)), nor
  `okdp`: `mapping output "okdp": the chart of OKDP's own objects is compiled from the objects section: remove it from output`
  ([the okdp chart](#the-okdp-chart)).
- A chart the mapping leaves out (absent, dropped by `$if`) is not
  rendered: no `Result.Values` entry.
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
must be a map with a non-empty `kind` and `metadata.name`
(`- at '/objects/<i>': missing property 'kind'`). A value that is not a
list fails; absent or null, there are none. They follow the descriptor and the
credentials Secrets in `Result.Objects` and in the `okdp` chart:
[outputs](01_inputs_outputs.md#outputs).

### instance

| Key | Meaning |
|---|---|
| `url` | the instance's URL, a string, trimmed |
| `usage` | how to use the instance, a string, trimmed, with one final newline |

Other keys are ignored. Both go to the descriptor ConfigMap:
[outputs](01_inputs_outputs.md#outputs). A value that is not a map fails;
absent or null, both are empty. `url` and `usage` are strings when set:
another value, a null included, fails rather than is printed as text,
`- at '/instance/url': got number, want string`.

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
| `values` | map | `{}` | The contract's fields. Checked and completed against the contract (no unknown or secret field, defaults, required fields, valid against its schema; under the release name, the values the contract's `x-okdp-internal` convention gives): [contracts](01_inputs_outputs.md#contracts). |
| `secretRef` | `{name}` | see `secret` | The Secret holding the secret fields. Only `name` is kept, required: a non-empty string. `{}` is not set. |
| `secret` | map | none | The credentials Secret the compiler renders, below. |

Other keys are ignored. A value that is not a list fails; absent or
null, there are none. Each item must be a map with a `contract` string,
and its `name` a string, its `values`, `secretRef` and `secret` maps when
set (a null included), the `generate` and `stringData` of its `secret` of
their types ([secret](#secret)), as the mapping schema describes them
([sections](#evaluation-order)):

```text
- at '/provides/0/values': got array, want object
- at '/provides/0/secret/generate/0/length': got string, want integer
- at '/provides/1/secret/stringData/token': got number, want string
- at '/provides/1/secretRef': missing property 'name'
```

A key of `generate` is checked as one of `okdpGeneratedSecret`'s `keys`
([`okdpGeneratedSecret`](04_functions.md#okdpgeneratedsecret)). With a `generate`,
`stringData` takes numbers and booleans too. The contract checks then report
`provides "<name>" (<contract>): <message>`. The items are checked in
order, their fields in alphabetical order, and the first error is
reported; the required fields the item lacks, and the schema's violations
of that item, all at once, one line each
([descriptor ConfigMap](01_inputs_outputs.md#descriptor-configmap)).

#### secret

`{generate: [<key>, ...], stringData: {...}}` renders a generated Secret
([`okdpGeneratedSecret`](04_functions.md#okdpgeneratedsecret), `keys` = `generate`, same
key fields, checked the same way; `stringData` optional, strings, numbers
or booleans, written as text), `{stringData: {...}}` a plain Secret with
these keys, strings only: a Secret's `stringData` holds strings, the API
server refuses a number. A `generate` that is not a list, a `stringData`
that is not a map, fail rather than render a Secret without these keys,
before the contract checks, a null too.

The Secret is named after `secretRef.name` when given, else
`<name>-<contract>-credentials`, which then becomes the output's
`secretRef`. Without `secret` (or with `secret: {}`), no Secret is rendered
and the output has a `secretRef` only when the mapping gives one. A
`secret` with neither a non-empty `generate` nor a non-empty `stringData`
fails with `<service>: output "<name>": secret needs stringData or generate`
(`<service>`: the `name` of `service.yaml`). The rendered objects
and their labels: [credentials Secrets](01_inputs_outputs.md#credentials-secrets).

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
`$spread`, gives several. A value that is not a list fails; absent or
null, there are none. The platform provides the clients, or each service
registers its own by OIDC Dynamic Client Registration, per
`global.okdp.oidc.clientProvisioning` ([context](#context)):

| Mode | The compiler |
|---|---|
| `dcr` (`platform.oidc.dcr.enabled`) | adds one chart per client, rendered by the platform's oidc-dcr chart (`global.okdp.charts.oidcDcr`, oidc-dcr 0.5.0 by default: [platform charts](01_inputs_outputs.md#platform-charts)): `oidc-dcr` for the main client, `<name>-oidc-dcr` for a named one. Its Job registers the client and writes its Secret ([OAuth client charts](01_inputs_outputs.md#oauth-client-charts)); the platform's registration settings are then required ([OAuth client settings](01_inputs_outputs.md#oauth-client-settings)). |
| otherwise (`existing`, or `platform.oidc.enabled` false) | adds no chart: the platform provides each client's Secret |

| Key | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | `""` | `""`: the service's main client, one at most. Else the client's name, unique: lowercase letters, digits and `-`, starting and ending with a letter or a digit, 54 characters at most (its chart key `<name>-oidc-dcr` is a DNS-1123 label). It names the client's chart, its objects and its Secret. |
| `grantTypes` | list of strings | required | The OAuth grant types the client registers (`authorization_code`, `refresh_token`, `client_credentials`...), a non-empty list of non-empty strings. |
| `redirectUris` | list of strings | `[]` | Its redirect URIs, each non-empty. |
| `scope` | string | `platform.oidc.scope` | The scope it requests, space separated. |
| `clientName` | string | `<release>-<namespace>`, then `-<name>` for a named client | Its name at the identity provider. |
| `public` | boolean | `false` | A public client (a browser application), without a client secret. |
| `request` | map | `{}` | Fields of the registration request (snake_case, as OIDC Dynamic Client Registration names them): they override the computed ones. |
| `secret` | string | [`oidcClientSecret(<name>)`](04_functions.md#oidcclientsecret) | The Secret the Job writes the client to. |
| `caSecret` | string | `platform.oidc.dcr.caSecret` | The Secret holding the CA bundle the Job trusts. |

A key left out by `$if` is not set, a null one is refused by its type,
and `""` takes the default of a string key. Only `dcr` mode uses the
keys; how they become the values of the client's chart:
[OAuth client charts](01_inputs_outputs.md#oauth-client-charts).

Every mode checks the clients, after the mapping: each client is a map,
its `name` of the form above, of these keys only, each of its type,
`grantTypes` given and not empty ([sections](#evaluation-order)); then
one client at most has no name, and names are unique. In
`dcr` mode, no two clients write one Secret. As soon as the
mapping declares a client, its chart key belongs to the compiler:
`service.yaml` may not list it, nor `output` compute it. Only `dcr` mode
reads the oidc-dcr chart, from the chart cache
([chart cache](01_inputs_outputs.md#chart-cache)). The first failing
check stops the compile ([compile errors](#compile-errors)).

The mapping reads the name of a client's Secret with
[`oidcClientSecret(name)`](04_functions.md#oidcclientsecret) (the main client's is also
`platform.oidcClientSecret`), in any section; a client that sets `secret`
references that name itself. The keys the Secret holds:
[OAuth client charts](01_inputs_outputs.md#oauth-client-charts).

## Expressions

### Lone expressions and interpolation

| Scalar | Value |
|---|---|
| `${{ expr }}`, the whole scalar | the expression's value, with its type: number, boolean, string, list, map or null |
| text and expressions (`https://${{ host }}`, `${{ a }}${{ b }}`) | a string: the text, and each expression's value as text ([`str`](04_functions.md#str-and-first)) |
| `$${{` | a literal `${{` |
| no `${{` | the scalar as YAML reads it ([YAML reading](#yaml-reading)) |

The whole scalar means nothing around the expression, not even a space
inside the quotes (`' ${{ 1 }}'` is `" 1"`) or the final newline of a `|`
or `>` block scalar (`"1\n"`); `>-` and `|-` keep a lone expression. `$`,
`${VAR}` and `{{ }}` without `${{` are text: shell variables and Go
templates pass through.

The `}}` closing an expression is found skipping quoted strings (`'...'`,
`"..."`, a `\` escaping the next character) and nested braces:
`${{ {"k": "}}"}.k }}` is one expression. Nothing else of CEL is read: a
quote or a brace in a `//` comment counts (`// the user's name` leaves the
expression unterminated), a `}}` in one closes the expression
(`${{ x // a note }}`), and a raw or triple-quoted string is read as
quoted strings (`r'\'` and `'''it's'''` are unterminated). A scalar
without the closing `}}` fails with `unterminated ${{ in "<scalar>"`.

Map keys are never evaluated: `${{ x }}: 1` is the key `${{ x }}`. Computed
keys come from [`$for`](03_control_flow.md#for) (`$key`) or [`$merge`](03_control_flow.md#merge). A key
starting with `$` is a [directive](03_control_flow.md#places); `$$` writes one:
`$$schema` is the key `$schema`.

Every expression is compiled once, when the mapping is loaded, before any
is evaluated: one under a false `$if` or in a `$for` over no element too.
A syntax error, an undeclared name or an unknown function fails the
compile whether an instance takes the branch or not. Variables are
dynamic ([Context](#context)): a type error (`no such overload`) or a
missing key (`no such key`) is an evaluation error, found only when the
expression is evaluated.

The value of an expression is converted to data, as values hold it: maps
with string keys, lists, numbers, strings, booleans and null, at any depth,
as cel-go converts it to JSON (`google.protobuf.Value`).

| CEL value | Data |
|---|---|
| int, uint, double | a double, written without decimals when integral; an integer beyond 2^53 is its digits, as text (`"9007199254740993"`) |
| a NaN or an infinite double (`0.0/0.0`) | the text `"NaN"`, `"Infinity"`, `"-Infinity"` |
| duration, timestamp | its text, as `string()` writes it: `3600s`, RFC 3339 |
| bytes | their base64 (`b'abc'` is `"YWJj"`) |
| optional | its value: `{'o': values.?extra.k}` is `{o: v}` |

What has no data form fails the expression:

| Value | Error |
|---|---|
| a map key that is not a string (`[{1: 2}]`) | `unsupported type conversion from 'int' to string` |
| an optional without a value (`values.?nope`) | `optional.none() dereference` |
| a type (`type(x)`) | `type conversion not supported for 'type'` |

A host function's argument is converted the same way, and fails the same
way: `str(type(x))` fails.

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

The mapping is read as YAML 1.2, as yaml.v3 decodes it, its maps in the
order written. A snippet copied from a `values.yaml`, which Helm reads as
YAML 1.1, means the same unless it holds YAML 1.1's booleans or keys Helm
converts (`on:`, `1.0:`): write `true` and `false`, and the keys as Helm
reads them.

| Plain scalar | Value |
|---|---|
| `true`, `false`, `True`, `FALSE` | the boolean |
| `y`, `yes`, `on`, `n`, `no`, `off` (any case) | the string |
| `0777`, `0o17`, `0x1F`, `0b101`, `1_000` | `511`, `15`, `31`, `5`, `1000` |
| `2001-12-14` | the string: timestamps are not decoded |
| `~`, `null`, empty | null |
| `.nan`, `.inf`, `-.inf` | refused, values cannot hold them: `values.cel.yaml: line <n>: json: cannot marshal from Go float64: unsupported value: NaN` |

A tagged scalar is decoded as its tag says (`!!str 1` is the string `"1"`),
and fails when it cannot be: `!!bool yes` fails with
``values.cel.yaml: line <n>: yaml: cannot decode !!str `yes` as a !!bool``.
Text that is not UTF-8 (a `!!binary`) is refused too.

Keys are taken as written: `on:` is the key `on`, `1.0:` the key `1.0`,
binding names included. Of a key written twice, the later value wins. A
second YAML document, after `---`, is ignored. As in a values file Helm
reads, a `|` or `>` block scalar that ends the file without a final line
break keeps its newline.

Aliases are refused, where Helm resolves them:
`values.cel.yaml: line <n>: aliases are not read: bind the value in vars`,
`<n>` the line of the alias. A value used twice is a [`vars`](#vars)
binding, read by name (`${{ small }}`, `${{ small.requests }}`). An anchor
is ignored, and a merge key `<<` is a plain key.

### Quoting

YAML ends or splits a plain scalar on characters CEL uses. Quote the scalar,
or write a long expression as a `>-` block scalar.

| Pitfall | Fails | Write |
|---|---|---|
| `: ` in a plain scalar (a ternary, a map literal) | `v: ${{ on ? 'a' : 'b' }}`: `values.cel.yaml: yaml: line <n>: mapping values are not allowed in this context` | `v: "${{ on ? 'a' : 'b' }}"` |
| ` #` starts a comment | `v: ${{ 'a #b' }}`: `unterminated ${{` | `v: "${{ 'a #b' }}"` |
| `,` `[` `]` `{` `}` inside a flow collection | `{name: ${{ n }}}`: `values.cel.yaml: yaml: line <n>: did not find expected ',' or '}'` | `{name: "${{ n }}"}` |
| a final newline (`\|`, `>`) makes text | `v: >` then `${{ 1 }}`: `"1\n"` | `v: >-` |
| quotes of the scalar's own kind | `'${{ x == 'a' }}'` | `"${{ x == 'a' }}"`, or `'${{ x == ''a'' }}'` |

## Directives

Keys starting with `$` shape the structure: `$if` leaves a map or an item
out, `$value` makes a map a value of any type, `$else` another value when
the condition of its `$if` is not true, `$for` builds a map
(`$key`, `$value`) or list items (`$each`) from a list, with `$vars`
bindings per element, `$merge` merges a computed map over a map's keys,
`$spread` splices a computed list into a list. Their places, scope,
evaluation and errors: [control flow](03_control_flow.md).

## Context

The variables of every expression:

| Variable | Value |
|---|---|
| `release` | `{name, namespace}`: `Inputs.Release`, `Inputs.Namespace`, DNS-1123 labels, the name 53 characters at most ([inputs](01_inputs_outputs.md)) |
| `values` | The parameters: the defaults of `config.schema.json`, the context's values layers, by level, and the instance's, merged and checked against it ([parameters](01_inputs_outputs.md#parameters)), each connection reference replaced by its resolved connection, `""` or `null` when not set ([connections](01_inputs_outputs.md#connections)); no `{{` in the instance's layers nor in a connection. Without `global` and `connections`. `upstream` stays as written: `upstream.<key>` of a chart that accepts upstream values is what will be applied over its computed values. |
| `global` | `{okdp: ...}`: `global.okdp` of the same merged values, as written ([context values](01_inputs_outputs.md#context-values)). Other keys of `global` are not visible. |
| `platform` | Derived from `global.okdp`, below. |
| `service` | `{name, version}` of `service.yaml`, as written ([service](#service)). |
| `labels` | the labels of OKDP's objects, below, without component. |
| `selectorLabels` | their selector labels, below, without component. |
| `vars` bindings | [vars](#vars), in order |
| `$for` variables, `$vars` bindings | inside their [`$for`](03_control_flow.md#for) |

A missing key is an evaluation error (`no such key: <key>`): test it with
`has(values.x)`, or read it with `values.?x.orValue(<default>)`,
`m[?'k'].orValue(<default>)`. A name that is not in scope fails when the
mapping is loaded, in any branch: `undeclared reference to '<name>'`.

`platform`, derived from `global.okdp` by the compiler:

| Variable | Value |
|---|---|
| `platform.oidc` | `global.okdp.oidc`, its defaults applied, a `null` deleting a default ([fields the compiler reads](01_inputs_outputs.md#fields-the-compiler-reads)): `enabled` true when the key is absent; `scope` `"openid profile email groups"`; `clientProvisioning` `"existing"`, else `existing` or `dcr` (any other value fails the compile); `dcr`: `global.okdp.oidc.dcr`, the settings of the oidc-dcr Jobs, defaults applied ([OAuth client settings](01_inputs_outputs.md#oauth-client-settings)), and `enabled` set to `enabled && clientProvisioning == "dcr"` (a written value is replaced); `existing`: `enabled && clientProvisioning == "existing"`. |
| `platform.oidcClientSecret` | The Secret holding the instance's main OAuth client: [`oidcClientSecret('')`](04_functions.md#oidcclientsecret). |
| `platform.ingressAnnotations` | `{"cert-manager.io/cluster-issuer": <global.okdp.certificateIssuers.selfSigned.name>}`, `""` when unset. |
| `platform.proxyEnv` | `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`: `global.okdp.proxy.httpProxy`, `httpsProxy`, `noProxy`, each only when non-empty; `{}` when none. |

The settings these are derived from are checked against the platform
schema first (`- at '/global/okdp/oidc': got boolean, want object`):
[fields the compiler reads](01_inputs_outputs.md#fields-the-compiler-reads).
`enabled` is `true` or `false`; a `null`, unlike another setting's, is
kept, and reads as false.

`labels` and `selectorLabels` (and the functions
[`okdpLabels`, `okdpSelectorLabels`](04_functions.md#okdplabels-and-okdpselectorlabels)),
the labels of every object OKDP renders itself:

| Label | Value | `selectorLabels` |
|---|---|---|
| `app.kubernetes.io/name` | `service.name` | yes |
| `app.kubernetes.io/instance` | `release.name` | yes |
| `app.kubernetes.io/component` | the component, when not empty (functions only) | yes |
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

Besides CEL's standard library, an expression calls the CEL libraries the
language enables, each at a pinned version, and the host functions: `str`,
`first`, `sha256`, `toJson`, `toPrettyJson`, `toYaml`, `quote`,
`okdpFullname`, `okdpLabels`, `okdpSelectorLabels`, `okdpGeneratedSecret`,
`oidcClientSecret`: [functions](04_functions.md).

## Errors

### The errors section

```yaml
errors:
  - $if: ${{ values.replicas > 1 && db.size() == 0 }}
    $value: "webapp: replicas > 1 needs a database"
  - $for: p in ['s3SecretRef', 'trinoOidcSecretRef']
    $each:
      $if: ${{ first([values[p]]) == '' }}
      $value: "okdp-examples: ${{ p }} is required"
  - $for: r in realms
    $each:
      $if: ${{ r.admin == null }}
      $value: 'okdp-examples: realm "${{ r.name }}" has no catalog_admin'
```

`errors` is evaluated after `vars`, before the other sections, as any node
([control flow](03_control_flow.md)): a list, whose items `$if` leaves out
and `$for` and `$spread` multiply, or one expression giving a list
(`errors: ${{ ... }}`). Each item it gives that is not empty is a message,
as text ([`str`](04_functions.md#str-and-first)). A message is the `$value`
of an item with its condition, `$if`: it is evaluated only when the
condition holds, and may read what exists only then (`db.okdp.ref` of a
database connection). Every item is evaluated; when there is any message,
the compile fails with the messages joined by newlines, without prefix:

```text
webapp: replicas > 1 needs a database
okdp-examples: s3SecretRef is required
okdp-examples: realm "data" has no catalog_admin
```

A value that is not a list (null included) gives no message, and an item
of another form is printed as text: a map, `{$if: ..., message: ...}`,
prints as `map[message:...]`. An item whose evaluation fails stops the
compile with that error, prefixed with the file and the position of the
item as written (`values.cel.yaml: errors[1]: `).

### Compile errors

The first error stops the compile (the `errors` section and the schema
violations excepted). An error found reading or evaluating the mapping
starts with the name of its file, `values.cel.yaml: `, the messages of its
`errors` section excepted; one of a template of the compiler, with the
template's (`oidc-dcr.cel.yaml: `). An error of an expression reads
`<where>${{ <expression> }}: <cause>`, `<where>` being:

| Expression in | `<where>` |
|---|---|
| a `vars` binding | `values.cel.yaml: vars <name>: ` and the path below it |
| a `$vars` binding | `values.cel.yaml: `, the path to its `$for`, then `$vars <name>: ` |
| a section | `values.cel.yaml: <section>: ` and the path to the expression: `values.cel.yaml: output: app: env[2]: value: ` |
| an `errors` item | `values.cel.yaml: errors[<i>]: ` and the path below it |

The path names each map key, followed by `: `, and each list position, as
`[<i>]` (from 0) right after the key of its list: `hosts[0]: `,
`objects[1]: `, `l[0][1]: ` for a list in a list. A position counts the
items of the YAML list as written: an item carrying `$for` or `$spread` is
one position, whatever it produces.

The cause is either a compile-time error of CEL, with its position and the
source line, reported when the mapping is loaded whatever branch holds the
expression, or an evaluation error:

```text
values.cel.yaml: output: app: ingress: hosts[0]: host: ${{ hots }}: ERROR: <input>:1:2: undeclared reference to 'hots' (in container '')
 |  hots
 | .^
values.cel.yaml: vars db: ${{ values.databse }}: no such key: databse
values.cel.yaml: output: app: replicaCount: ${{ values.replicas + 1 }}: no such overload
values.cel.yaml: objects[1]: ${{ okdpGeneratedSecret({'keys': []}) }}: okdpGeneratedSecret: the arguments don't meet the specifications of mapping.schema.json:
- at '/okdpGeneratedSecret': missing property 'name'
values.cel.yaml: vars host: ${{ values.?host }}: optional.none() dereference
```

Other errors of the mapping:

| Message | Cause |
|---|---|
| `values.cel.yaml: yaml: line <n>: <message>` | invalid YAML, often an unquoted expression holding `: ` ([quoting](#quoting)) |
| `values.cel.yaml: the mapping must be a YAML map` | the document is not a map |
| `values.cel.yaml: line <n>: aliases are not read: bind the value in vars` | an alias ([YAML reading](#yaml-reading)) |
| ``values.cel.yaml: line <n>: yaml: cannot decode !!str `<scalar>` as a !!bool``, `values.cel.yaml: line <n>: json: cannot marshal from Go float64: unsupported value: NaN` | a tagged scalar of another form, `.nan`, `.inf`, `-.inf` ([YAML reading](#yaml-reading)) |
| `values.cel.yaml: <key>: unknown section (allowed: vars, errors, output, instance, provides, objects, oidcClients)`, `values.cel.yaml: vars: must be a map of bindings, got <value>` | the top level ([values.cel.yaml](#valuescelyaml)) |
| `<where><key>: not allowed here (allowed: <directives>)`, `<where>$for: in a map, needs $key and $value`, `<where>$for: in a list item, needs $each` | a directive ([errors](03_control_flow.md#errors)) |
| `<where>unterminated ${{ in "<scalar>"` | an expression without its `}}` |
| `<where>$for: "<spec>" is not "<name> in <expression>"`, `<where>$for "<spec>": not a list` | [`$for`](03_control_flow.md#for) |
| `the mapping's sections don't meet the specifications of mapping.schema.json:` then `- at '<path>': <violation>`, one line each | a section, or an item or a key of one, of another shape, a key it requires missing, a key it does not allow ([sections](#evaluation-order)): [`output`](#output), [`instance`](#instance), [`objects`](#objects), [`provides`](#provides), [`oidcClients`](#oidcclients) |
| `<where>${{ <expression> }}: okdpGeneratedSecret: the arguments don't meet the specifications of mapping.schema.json:` then `- at '/okdpGeneratedSecret<path>': <violation>`, one line each | an argument of [`okdpGeneratedSecret`](04_functions.md#okdpgeneratedsecret) missing, unknown or of another type |
| `mapping output "<name>": not a chart of service.yaml` | [`output`](#output) |
| `mapping output "okdp": the chart of OKDP's own objects is compiled from the objects section: remove it from output` | [the okdp chart](#the-okdp-chart) |
| `provides "<name>" (<contract>): <message>`, `two <contract> outputs are named "<name>"`, `<service>: output "<name>": secret needs stringData or generate` | [`provides`](#provides) |
| `<client>: <other> has no name either: only the service's main client (chart oidc-dcr) has none, name the others`, `<client>: <other> has the same name: client names must be unique` | two clients without a name, two clients of the same name |
| `<client>: <other> writes the same Secret, "<secret>": give each client its own secret` | `dcr` mode: a client's `secret` is the Secret of another client, given or [`oidcClientSecret`](04_functions.md#oidcclientsecret)'s |
| `service.yaml: charts.<chart>: the chart of <client> is added by the compiler: remove it from service.yaml`, `mapping output "<chart>": the chart of <client> is compiled from oidcClients: remove it from output` | a client's chart listed in `service.yaml` or computed in `output` |
| `<client>: oidc-dcr.cel.yaml: <error>` | an expression of the client chart's values template failing on the context, e.g. a `global.okdp.oidc.scope` that is not a string |

`<client>`, `<other>`: `oidcClients[<i>]`, the client's position in the
evaluated list, followed by ` (<name>)` for a named client.
`<value>`: the value as Go's `%#v` prints it (`"x"`, `true`). The errors of the
platform's `global.okdp.oidc.dcr` settings:
[OAuth client settings](01_inputs_outputs.md#oauth-client-settings).

The `okdp-compile` binary prints them after `error: `
([usage](../01_usage.md)).
