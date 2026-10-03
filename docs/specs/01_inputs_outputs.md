# Inputs and outputs

Normative reference of what a compile reads besides the service definition
(`service.yaml` and `mapping.cel.yaml`: [02_service_definition.md](02_service_definition.md)),
and of what it produces. The flags that pass these inputs to the binary are in
[01_usage.md](../01_usage.md); how the compiler works inside, in
[00_architecture.md](../00_architecture.md).

| Input | Field | Required | Section |
|---|---|---|---|
| Package files | `Definition.Files` (`fs.FS`) | yes | [Package files](#package-files) |
| Chart cache | `Inputs.Charts` (`fs.FS`) | yes | [Chart cache](#chart-cache) |
| Contracts | `Inputs.Contracts` (`fs.FS`) | yes | [Contracts](#contracts) |
| Values layers | `Inputs.Values` (`[]map[string]any`) | no | [Parameters](#parameters), [Platform values](#platform-values), [Instance values](#instance-values) |
| Instance files | `Inputs.Instance` (`fs.FS`) | no | [Upstream values](#upstream-values) |
| Release name | `Inputs.Release` | yes | `release.name`, object names |
| Namespace | `Inputs.Namespace` | yes | `release.namespace`, object namespaces, `${namespace}` of contracts |

A missing required input fails before anything is read, in this order:
`no service definition` for a nil `*Definition`, `Definition.Files is
required`, `Inputs.Charts is required`, `Inputs.Contracts is required`,
`Inputs.Release is required`, `Inputs.Namespace is required`. Paths in an
`fs.FS` are relative to its root.

## Package files

A package is a directory: the service definition and the parameters of the
service. `compiler.LoadDefinition(fsys)` reads `service.yaml` and
`mapping.cel.yaml` at the root of `fsys` ([service
definition](02_service_definition.md)) and keeps `fsys` as
`Definition.Files`, from which each compile reads the other files; a
`Definition` built in memory sets `Files` itself. The compiler reads these
files, and no other:

```
<package>/
  service.yaml                        the service, its charts and their sources
  mapping.cel.yaml                    the mapping
  values.yaml                         defaults of the parameters
  values.schema.json                  schema of the parameters, connection references
  base/<key>.yaml                     base values of the chart <key> (charts.<key>.base)
```

| File | Required | Read as | Used for |
|---|---|---|---|
| `service.yaml` | yes | YAML 1.2 | the service: name, versions, catalog metadata, charts ([service.yaml](02_service_definition.md#serviceyaml)) |
| `mapping.cel.yaml` | yes | the mapping language | the computed values, objects and outputs ([mapping.cel.yaml](02_service_definition.md#mappingcelyaml)) |
| `values.yaml` | yes | [values file](#values-files) | defaults of the [parameters](#parameters) |
| `values.schema.json` | yes | JSON | [parameter check](#schema-check), [connection references](#declaring-a-reference) |
| base file | when `charts.<key>.base` is set | values file | second layer of the [final values](#merge-precedence) |

`<key>`: a chart key, a key of `charts` in `service.yaml`, the chart of an
OAuth client ([OAuth client charts](#oauth-client-charts)) or `okdp`
([the okdp chart](#the-okdp-chart)); it names the chart's values. A
compiled chart: one the mapping outputs (`output.<key>`, not left out by
`$if`, not null), the okdp chart, and in `dcr` mode the chart of each
OAuth client. Each is rendered by an upstream chart, its source
([chart index](#chart-index)); several keys may share one. The base file
may be any path of the package, `base/<key>.yaml` by convention; a path out
of it (`../app.yaml`) fails. The service's name, versions and catalog
metadata are fields of `service.yaml` ([Service](02_service_definition.md#service)).

`values.schema.json` is required even for a service without connection
references: the schema check alone would skip a missing schema, the
connection walk reads it. A missing file fails with the error of the file
system, prefixed with the chart key for a base file:

```
open values.schema.json: no such file or directory
app: open base/app.yaml: no such file or directory
```

### Values files

Every values file is read as Helm 4 reads one (`loader.LoadValues`):
`values.yaml`, the upstream charts' `values.yaml` ([chart
cache](#chart-cache)), the base files, the
[instance's upstream files](#upstream-values), and the values layers read by
`compiler.ParseValues` (the binary's `-f` files).

- Documents: the file is split on the lines starting with `---`, as
  Kubernetes' YAMLReader splits it; only a comment may follow the `---`
  (otherwise `<file>: error reading yaml document: invalid Yaml document
  separator: <text>`); empty documents are skipped.
- Decoding: each document by `sigs.k8s.io/yaml`, YAML 1.1. A plain `y`,
  `yes`, `on`, `n`, `no`, `off` (and their capitalized forms) is a boolean,
  as a key too (`on:` is the key `"true"`); keys are strings; every number is
  a float64.
- Shape: a document must be a map (`cannot unmarshal yaml document: ...`); a
  null key fails (`unsupported map key`).
- The documents of a file are merged in order, as values layers are
  ([Layers and nulls](#layers-and-nulls)).

`testdata/helm/values/documents` and `yaml11` hold what Helm reads
from such files. Values built in memory and given in `Inputs.Values` are
normalized the same way: `map[any]any` keys printed as strings, `int`,
`int64`, `uint64` and `float32` as float64.

JSON files (`values.schema.json` of the package and of the upstream charts,
the contracts) are strict JSON: no comments.

## Chart cache

`Inputs.Charts`: the upstream charts the compiled charts are rendered by,
as `helm pull -d <cache>` writes them, or unpacked. The binary reads a
directory: `--charts`, `$OKDP_CHARTS` by default
([command line](../01_usage.md#command-line)).

```
<cache>/
  <chart>-<version>.tgz               a chart archive (helm pull -d <cache>)
  <chart>-<version>/                  or the chart unpacked
```

A chart is found by the `chart` and `version` of its source: the directory
first, else the archive; the repository plays no part. Of a chart, the
compiler reads `values.yaml` and `values.schema.json` only: at the root of
the directory, or in the archive's top-level directory, not those of its
subcharts (`<chart>/charts/<sub>/values.yaml`). A chart without
`values.yaml` has no defaults, one without `values.schema.json` no
[schema check](#schema-check-of-the-final-values). Each chart is read once
per compile, whatever the number of keys it renders, and only when a chart
it renders is compiled: the oidc-dcr chart in `dcr` mode with a client, the
objects chart in every compile. `example/input/charts/` holds the
example's charts unpacked, their `values.yaml` only: enough to compile, not
to render.

A chart the cache does not hold fails with the command that downloads it,
prefixed with the chart key; a file that does not read fails with its path:

```
app: chart app-1.8.0 is not in the chart cache: helm pull app --repo https://charts.example.org --version 1.8.0 -d <chart cache>
oidc-dcr: chart oidc-dcr-0.5.0 is not in the chart cache: helm pull oci://quay.io/adaltas/oidc-dcr --version 0.5.0 -d <chart cache>
app: app-1.8.0/values.yaml: cannot unmarshal yaml document: ...
app: app-1.8.0.tgz: gzip: invalid header
```

## Parameters

The parameters of an instance: the values layers (`Inputs.Values`) merged
and coalesced over the package's `values.yaml`, as Helm computes `.Values`
of a chart from its defaults and `-f` files.

### Layers and nulls

1. The layers are merged in order, as Helm merges `-f` files (`MergeMaps`):
   maps merge key by key, lists and scalars replace, a `null` replaces too
   and stays `null`.
2. The result is coalesced over `values.yaml` (`CoalesceValues`): a `null`
   over a default deletes the key; a `null` where there is no default is
   kept; the `null` defaults of `values.yaml` are dropped; when a value and
   its default are not both maps, the value wins.

| `values.yaml` | Layers | Parameters |
|---|---|---|
| `metrics: {enabled: false, interval: 30s}` | `metrics: {enabled: true}` | `metrics: {enabled: true, interval: 30s}` |
| `metrics: {enabled: false, interval: 30s}` | `metrics: null` | no `metrics` key |
| no `foo` | `foo: null` | `foo: null` |
| `env: [A]` | `env: [B]` | `env: [B]` |

The corner cases (a `null` at the top level or in a nested map, over a
`null` default) follow Helm 4: `testdata/helm/values/nulls`.

### Schema check

The parameters are checked against `values.schema.json` first, before the
[platform requirements](#requirements), the connections and the mapping,
as Helm 4 checks the values of a chart:

- the values checked: the parameters above, `global`, `connections` and
  `upstream` included, every connection reference still the string written;
- the draft: the schema's `$schema`, draft 2020-12 without one; `format` is
  asserted for drafts before 2019-09 only;
- `$ref`: within the schema only. A `urn:` reference accepts anything (as in
  Helm); a file or a URL fails the compile: `values.schema.json: failing
  loading "<url>": $ref <url>: the schema may only reference itself (the
  compile reads no file or URL)`.

Every violation is reported with its path, sorted by path, under the name of
the service, `name` of `service.yaml` (`testdata/schema-violation.yaml`):

```
values don't meet the specifications of the schema(s) in the following chart(s):
webapp:
- at '/metrics/enabled': got string, want boolean
- at '/replicas': minimum: got 0, want 1
```

### Reserved keys

Three top-level keys are not parameters of the service:

| Key | Content | In the mapping's `values` |
|---|---|---|
| `global` | the platform values, `global.okdp` ([Platform values](#platform-values)) | removed: given as `global` and `platform` |
| `connections` | the external connections, by name ([Connections](#connections)) | removed: the references are resolved in place |
| `upstream` | the inline upstream values, by chart key ([Upstream values](#upstream-values)) | for a chart that accepts upstream values and has some: its files, then its inline values, merged as they will be applied (`append` lists appended); the other keys as written |

They are checked with the parameters: a schema with `additionalProperties:
false` declares them, `{"type": "object"}` at least
(`example/input/package/values.schema.json`).

## Connections

A connection reference: a parameter that names a connection, either an entry
of `connections` (external) or an instance of the namespace (internal). The
compiler replaces it with the fields of the connection, completed against
its [contract](#contracts), before the mapping runs.

### Declaring a reference

In `values.schema.json`, on a string property:

```json
"database": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}
```

`x-okdp-connection-ref`: an object; `contract` names the contract, the file
`<contract>.schema.json` of the contracts. Its other keys are ignored; a
value that is not an object (`true`) declares no reference.

The references are found by walking the schema along the parameters, through
`properties` (the keys present in the values) and `items` (every item of a
list, with the one item schema). `$ref`, `allOf`, `anyOf`, `oneOf`,
`additionalProperties` and `patternProperties` are not followed.

| Schema | Parameters | Path |
|---|---|---|
| `properties.database` | `database: demo-db` | `database` |
| `properties.nested.properties.web` | `nested: {web: demo-web}` | `nested.web` |
| `properties.dbs.items` | `dbs: [demo-a, ext]` | `dbs[0]`, `dbs[1]` |
| `properties.backends.items.properties.database` | `backends: [{database: demo-db}]` | `backends[0].database` |

The path names the parameter in errors and [dependencies](#dependencies). A
reference that is `""` (not set), or not a string, is left as it is. The
packages of platform-charts declare them in lists of objects, e.g. the
catalogs of trino:

```json
"hiveCatalogs": {"type": "array", "items": {"type": "object", "properties": {
  "metastore": {"type": "string", "x-okdp-connection-ref": {"contract": "hive"}},
  "storage":   {"type": "string", "x-okdp-connection-ref": {"contract": "s3"}}}}}
```

### Resolution

A reference `<name>` is external when `connections.<name>` is a map; any
other name is internal: an instance (a release) of the namespace.

External: the entry of `connections`, as the layers give it.

```yaml
connections:
  shared-db:
    contract: database                       # must be the reference's contract
    description: the shared database         # dropped
    engine: mysql                            # contract fields
    host: db.example.org
    port: 3306
    dbName: webapp
    secretRef: {name: shared-db-credentials} # the credentials Secret
```

- `contract`: must be the contract of the reference.
- `description`: dropped.
- `secretRef`: kept as `{name}`, its other keys dropped. Without it, the
  connection has no `secretRef`.
- Any other key: a field of the contract, not an `x-okdp-secret` one.

Internal: the fields of the contract's `x-okdp-internal` naming convention,
`${ref}` replaced by the instance name, `${namespace}` by `Inputs.Namespace`,
`${suffix}` by `global.okdp.ingress.suffix`; and, when the contract has an
`x-okdp-secret` field, `secretRef: {name: <ref>-<contract>-credentials}`, the
default name of the [credentials Secret](#credentials-secrets) the instance
publishes. A contract without `x-okdp-internal` accepts external connections
only. The instance is not looked up: each internal reference is recorded as
a [dependency](#dependencies).

Both are then [completed](#completion) against the contract.

### What the mapping receives

The parameter, replaced by a map: the contract fields, `secretRef` when
there is one, and `okdp`, where the connection comes from. With
`example/input/contracts/database.schema.json`:

```yaml
# database: demo-db (internal)
database:
  host: demo-db-db.demo.svc        # x-okdp-internal
  dbName: demo-db                  # x-okdp-internal
  engine: postgresql               # default
  port: 5432                       # default
  driver: org.postgresql.Driver    # x-okdp-derived from engine
  secretRef: {name: demo-db-database-credentials}
  okdp: {ref: demo-db, contract: database, source: internal}

# database: shared-db (external, above)
database:
  engine: mysql
  host: db.example.org
  port: 3306
  dbName: webapp
  driver: com.mysql.cj.jdbc.Driver
  secretRef: {name: shared-db-credentials}
  okdp: {ref: shared-db, contract: database, source: external}
```

No secret field is ever inlined: a mapping reads the credentials from the
Secret `secretRef.name`. How the mapping uses `values`:
[Context](02_service_definition.md#context).

### Connection errors

`<path>`: the parameter path; `<name>`: the reference.

Every reference that does not resolve is reported, at once, one line each,
sorted by parameter path (string order, as the
[dependencies](#dependencies)), as the violations of the parameter schema
are. A reference gives one error, the first in this order: its contract
(unknown; another one, for an external connection; no internal naming
convention, for an internal one), the fields of an external connection in
alphabetical order, the `required` fields in the contract's order. The
same parameters give the same report from one compile to the next:

```
main.db: connection "shared-db": field "user" is not part of the database contract
replicas[1]: connection "store" has contract "s3", "database" is expected
```

| Case | Error | `testdata/` |
|---|---|---|
| no `<contract>.schema.json` | `<path>: unknown contract "<contract>"` | |
| external, another contract | `<path>: connection "<name>" has contract "<c>", "<contract>" is expected` | `wrong-contract.yaml` |
| external, not a contract field | `<path>: connection "<name>": field "<key>" is not part of the <contract> contract` | `unknown-field.yaml` |
| external, secret field | `<path>: connection "<name>": "<key>" is a secret field` | `secret-in-connection.yaml` |
| internal, no `x-okdp-internal` | `<path>: "<name>" is not a connection, and the <contract> contract has no internal naming convention` | |
| required field not set | `<path> (connection "<name>"): field "<key>" is required by the <contract> contract` | |

`testdata/no-secret-ref.yaml` (`database connection "shared-db" has no
secretRef`) is not a check of the compiler: an external connection without
`secretRef` resolves without one, and the example mapping refuses it in its
`errors` section ([Errors](02_service_definition.md#errors)). A mapping that
reads credentials checks `secretRef` itself.

## Contracts

A contract: the JSON Schema of a kind of connection (`database`, `s3`,
`hive`...), shared by the instances that provide it and the ones that
consume it.

### Location

`Inputs.Contracts`: a file system with `<contract>.schema.json` at its root;
the name of the contract is the name of the file. The binary reads a
directory: `--contracts`, `$OKDP_CONTRACTS` by default
([01_usage.md](../01_usage.md)). A contract is read when a reference or a
`provides` item names it.

### Format

A JSON Schema (draft-07 in okdp-lib-chart) with OKDP keywords. The compiler
reads:

| Keyword | On | Meaning |
|---|---|---|
| `properties` | the contract | its fields: the only keys a connection or an output may set |
| `required` | the contract | the fields that must be set after [completion](#completion): missing, `null`, `""`, `0`, an empty list or map fails; `false` is a value |
| `default` | a field | its value when not set |
| `x-ui-condition: {field, value}` | a field with a `default` | the default applies only when `field` prints as `value` |
| `x-okdp-derived: {from, map}` | a field | its value when not set: `map[<value of from>]`; none when that value is not a key of `map` |
| `x-okdp-secret: true` | a field | a credential: refused in a connection and in an output, it belongs to the credentials Secret (`secretRef`) |
| `x-okdp-internal: {<field>: <template>}` | the contract | the fields of an instance reached by its name: `${ref}`, `${namespace}`, `${suffix}` replaced ([Resolution](#resolution)) |
| `x-okdp-contract` | the contract | the name in the `required` error message |

Not read: `type`, `enum`, `pattern`, `minimum`, `maximum`,
`additionalProperties`, `title`, `description`, `x-okdp-external`,
`x-okdp-required`, `x-okdp-endpoint-from`, `x-okdp-icon`, `x-okdp-category`,
`x-ui-placeholder`, `x-ui-widget`. The values are not validated against the
schema (`port: "abc"`, `engine: oracle` pass), and an external connection is
accepted for any contract, `x-okdp-external` or not.

`example/input/contracts/database.schema.json`, trimmed:

```json
{
  "x-okdp-contract": "database",
  "x-okdp-internal": {"host": "${ref}-db.${namespace}.svc", "dbName": "${ref}"},
  "properties": {
    "engine":   {"type": "string", "enum": ["postgresql", "mysql"], "default": "postgresql"},
    "driver":   {"type": "string", "x-okdp-derived": {"from": "engine",
                 "map": {"postgresql": "org.postgresql.Driver", "mysql": "com.mysql.cj.jdbc.Driver"}}},
    "host":     {"type": "string"},
    "port":     {"type": "integer", "default": 5432},
    "dbName":   {"type": "string"},
    "password": {"type": "string", "x-okdp-secret": true}
  },
  "required": ["host", "dbName"]
}
```

A conditional default, in a `database-server` contract:

```json
"sslMode": {"type": "string", "default": "prefer", "x-ui-condition": {"field": "engine", "value": "postgresql"}}
```

### Completion

A connection, consumed or provided, is completed in three passes over the
fields not set, secret fields excepted:

1. defaults: `default`, when its `x-ui-condition` holds (a derived field's
   `default` is not used);
2. derived: `x-okdp-derived`;
3. required: every `required` field must be set.

A field set is kept as it is. The fields are visited in alphabetical order:
a conditional default whose condition field is itself defaulted applies only
when that field sorts first (`sslMode` after `engine`). A derived field may
follow from a defaulted one (`driver` from the default `engine`). An internal
reference has only its `x-okdp-internal` fields and the defaults: if they
leave a `required` field unset, every internal reference to the contract
fails.

### Checks

- Consumed, external: the `contract` of the entry, every other key a
  non-secret field, completed ([Connections](#connections)).
- Consumed, internal: the contract has `x-okdp-internal`, completed.
- Provided (`provides`): the contract exists, every key of `values` a
  non-secret field, completed; and an output published under the release
  name, of a contract with `x-okdp-internal`, has the values an internal
  reference to the release resolves to
  ([Descriptor ConfigMap](#descriptor-configmap)).

## Platform values

`global.okdp`: the settings of the platform (ingress, certificate issuers,
OIDC, proxy...), given as a values layer: `example/input/platform.yaml`,
`platform/platform-values.yaml` in a platform repository. The compiler gives
the layers no role: `global.okdp` is read from the
[parameters](#parameters), every layer merged over `values.yaml`. A later
layer may change or delete a platform value (`null`), as
`testdata/missing-platform-value.yaml` does; keeping the platform values
authoritative is the caller's job.

```yaml
global:
  okdp:
    ingress: {suffix: okdp.example.org, className: nginx}
    certificateIssuers: {selfSigned: {name: okdp-ca}}
    charts:
      objects: {repository: oci://registry.example.org/okdp, chart: okdp-objects, version: 1.0.0}
    oidc: {issuerUri: https://sso.okdp.example.org/realms/okdp}
```

### Requirements

`require` of `service.yaml`: dotted paths under `global.okdp`. Each must be
set and not empty (missing, `null`, `false`, `""`, `0`, an empty list or map
fails), checked after the [parameter schema](#schema-check), before the
connections and the mapping (`testdata/missing-platform-value.yaml`):

```
global.okdp.ingress.className is required. It is a platform value: set it in platform/platform-values.yaml
```

### Fields the compiler reads

| `global.okdp` | Type | Used for |
|---|---|---|
| `ingress.suffix` | string | `${suffix}` of `x-okdp-internal`: internal references, provided outputs |
| `certificateIssuers.selfSigned.name` | string | `platform.ingressAnnotations` (`cert-manager.io/cluster-issuer`) |
| `charts.objects`, `charts.oidcDcr` | map | the upstream charts of the [okdp chart](#the-okdp-chart) and of the [OAuth client charts](#oauth-client-charts) ([Platform charts](#platform-charts)) |
| `oidc.enabled` | `true` or `false` | `platform.oidc`, the Secret names (`platform.oidcClientSecret`, `oidcClientSecret`), whether the [OAuth client charts](#oauth-client-charts) are compiled |
| `oidc.clientProvisioning` | string | the same |
| `oidc.scope` | string | `platform.oidc.scope`; the scope a client chart registers when its client sets none |
| `oidc.dcr` | map | `platform.oidc.dcr`, the values of the OAuth client charts ([OAuth client settings](#oauth-client-settings)) |
| `oidc.issuerUri` | string | the OAuth client charts' wait for the provider (`waitForProvider.discoveryUrl`) |
| `proxy.httpProxy`, `proxy.httpsProxy`, `proxy.noProxy` | string | `platform.proxyEnv` |

Every compile checks the type of each of these settings when it is set,
and that `global`, `global.okdp` and each map on the way to a setting
(`oidc`, `proxy`, `ingress`, `certificateIssuers.selfSigned`...) are maps
when set, after the [requirements](#requirements), in every mode. A `null`
is not set. A value of another type is refused rather than read as not
set: `oidc: false` would leave OIDC enabled, `proxy: <url>` give no proxy
environment. The first violation is reported, in the order of the table
(`charts` is checked later: [Platform charts](#platform-charts)):

```
global.okdp.oidc must be a map, got false
global.okdp.oidc.enabled must be true or false, got "false"
global.okdp.proxy.noProxy must be a string, got []interface {}{".svc"}
```

The mapping sees `global.okdp` as written and `platform` as derived, with
the defaults, the check of `clientProvisioning` (`existing` or `dcr`) and
those of the keys of `oidc.dcr` (below):
[Context](02_service_definition.md#context). The other values are not
checked. `global` also reaches every upstream chart
([Merge precedence](#merge-precedence)).

### Platform charts

`global.okdp.charts`: the upstream charts that render the charts the
compiler adds itself, each a source as in `service.yaml`
([sources](02_service_definition.md#sources)): `repository`, `chart`,
`version`.

| Key | Default | Renders |
|---|---|---|
| `objects` | none: required | the [okdp chart](#the-okdp-chart), OKDP's own objects: an okdp-objects chart (values `objects: []`; its template prints each item of `objects` as one YAML document) |
| `oidcDcr` | `{repository: oci://quay.io/adaltas, chart: oidc-dcr, version: 0.5.0}` (`compiler.DcrSource`) | the [OAuth client charts](#oauth-client-charts), in `dcr` mode: an oidc-dcr chart that takes the values of oidc-dcr 0.5.0, which the embedded template writes |

Every compile checks them, after the [requirements](#requirements), the
types of the [settings the compiler reads](#fields-the-compiler-reads),
the [OAuth client settings](#oauth-client-settings) and the
[connections](#connections), before the mapping: `oidcDcr` when set, in
every mode. `charts` must be a map of these two keys, each chart a map;
`repository`, `chart` and `version` strings (other keys of a chart are
ignored), then a source: a repository URL (`https://`, `http://` or
`oci://`), the name of a chart, a pinned version.
The first violation is reported, in this order:

```
global.okdp.charts must be a map (objects, oidcDcr), got "okdp-objects"
global.okdp.charts: unknown key(s) oidcDCR (allowed: objects, oidcDcr)
global.okdp.charts.objects is required: the chart OKDP's own objects are rendered with (okdp-objects: repository, chart, version). It is a platform value: set it in platform/platform-values.yaml
global.okdp.charts.objects must be a map (repository, chart, version), got "okdp-objects"
global.okdp.charts.objects.version must be a string, got 1
global.okdp.charts.objects.version "1.x": a pinned chart version (1.2.3), not a range
global.okdp.charts.oidcDcr.repository "quay.io": the URL of a chart repository (https://... or oci://...)
```

The [chart cache](#chart-cache) holds each when its chart is compiled: the
objects chart in every compile, the oidc-dcr chart in `dcr` mode with a
client. Another repository with the same chart and version (a mirror) needs
no other cache entry.

### OAuth client settings

`global.okdp.oidc.dcr`: the settings of the oidc-dcr Jobs that register the
OAuth clients of a service in `dcr` mode
([OAuth client charts](#oauth-client-charts)). The mapping sees them as
`platform.oidc.dcr`, defaults applied, `enabled` set by the compiler
([Context](02_service_definition.md#context)). For example:

```yaml
global:
  okdp:
    oidc:
      clientProvisioning: dcr
      dcr:
        registrationUrl: https://sso.okdp.example.org/realms/okdp/clients-registrations/openid-connect
        authMethod: anonymous
        ttlSeconds: 300                # keep the finished Job and its logs 5 minutes
```

| Key | Type | Default | Client chart value |
|---|---|---|---|
| `registrationUrl` | string | none: required in `dcr` mode, with a client | `registrationUrl`: the registration endpoint |
| `authMethod` | string | none: `anonymous` required in `dcr` mode, with a client | none: OKDP registers anonymously |
| `image` | map: `registry`, `repository`, `tag`, `pullPolicy`, `pullSecrets` | the chart's | `image`, merged over the chart's |
| `caSecret` | string | `certs-bundle` | `tls.certificate`: the Secret holding the CA bundle the Jobs trust; a client's `caSecret` wins |
| `ttlSeconds` | whole number >= 0 | `30` | `ttlSeconds`: how long a finished Job, and its logs, is kept |
| `backoffLimit` | whole number >= 0 | `6` | `backoffLimit`: the Job's retries |
| `waitTtlSeconds` | whole number >= 0 | the chart's, 600 | `waitForProvider.ttlSeconds`: the longest wait for the provider's discovery document; the Jobs wait only when `oidc.issuerUri` is set |

A `null` takes the default, and so does a `caSecret` of `""`; `0` is a value.
`image`: the keys of oidc-dcr < 0.5.0 are renamed, `pull_policy` to
`pullPolicy` and `pull_secrets` to `pullSecrets`; when both spellings are
set, the camelCase one wins and the former is dropped. Its other keys are
passed as written. The other keys of `oidc.dcr` are kept in
`platform.oidc.dcr` and not used.

`oidc.dcr` itself is a map when set
([fields the compiler reads](#fields-the-compiler-reads)). Every compile
checks the types of `image`, `caSecret`, `ttlSeconds`, `backoffLimit` and
`waitTtlSeconds`, in every mode (`existing`, OIDC disabled, no client),
after the [requirements](#requirements); the first violation is reported,
in that order. The three numbers fail alike, and a
quoted number (`"600"`) is a string. `registrationUrl` and `authMethod` are
left as written, and checked only in `dcr` mode with a client (below):

```
global.okdp.oidc.dcr.image must be a map (registry, repository, tag, pullPolicy, pullSecrets), got "quay.io/x:1"
global.okdp.oidc.dcr.caSecret must be the name of a Secret, got true
global.okdp.oidc.dcr.ttlSeconds must be a whole number >= 0, got 1.5
```

In `dcr` mode ([OAuth client charts](#oauth-client-charts)), when the
mapping declares at least one OAuth client (after `$if`, once the clients
are checked), `authMethod` must be `anonymous` (unset, it is reported as
`""`), then `registrationUrl` a non-empty string
(`testdata/dcr-without-registration-url.yaml`):

```
global.okdp.oidc.dcr.authMethod "bearer" is not supported: OKDP registers its OAuth clients anonymously
global.okdp.oidc.dcr.registrationUrl is required (clientProvisioning: dcr): the endpoint the oidc-dcr Jobs register the OAuth clients at
```

## Instance values

What an instance sets: its parameters, its external connections, and values
for the upstream charts.

### Layers

`Inputs.Values`: the layers in order, the last one winning (Helm's `-f`
order); the binary's `-f` files, in the order given. The platform values
come first:

```bash
bin/okdp-compile ... -f example/input/platform.yaml -f example/input/instance.yaml
```

Merge, nulls and schema: [Parameters](#parameters).

### Upstream values

Values given to an upstream chart as they are, over the values the mapping
computed. Two sources, in this order:

1. files of the instance (`Inputs.Instance`, the binary's `--instance`
   directory), each a [values file](#values-files);
2. inline values: `upstream.<key>` of the parameters.

`charts.<key>.upstream` of `service.yaml` selects them: `true` reads
`upstream.<key>.yaml` when it exists, a path or a list of paths names the
files, `[]` takes the inline values only
([upstream](02_service_definition.md#upstream)). Paths are relative to the
root of the instance files. Upstream values are keyed by the chart key, not
by its source ([sources](02_service_definition.md#sources)). Inline values
for a chart that accepts none (an [OAuth client chart](#oauth-client-charts)
and the [okdp chart](#the-okdp-chart) included: they read no file either),
or for a name that is not a chart, are ignored without error.

```yaml
# an instance's values
upstream:
  app:
    env:
      - {name: LOG_LEVEL, value: debug}   # appended: append: [env]
    resources:
      limits: {memory: 1Gi}               # merged over the base values
```

Each source, file or inline, is checked, then merged over the values so far
([Merge precedence](#merge-precedence)):

- templates: no key or value may contain `{{`. The values reach the chart as
  data, Helm does not render them;
- [protect](#protect): `global` and the protected paths are refused;
- [append](#append): the listed lists are appended to.

### protect

`charts.<key>.protect`: dotted paths the platform sets; `global` is always
protected. A source fails when it sets a protected path (to any value,
`null` included), or one of its parents to anything but a map (`null`
included). The keys beside a protected path are free:

| With `protect: [ingress.hosts]` | |
|---|---|
| `ingress: {hosts: [...]}` | refused |
| `ingress: null`, `ingress: off` | refused |
| `ingress: {className: other}` | accepted |

The error names the source, the path set and the protected path: the first
one hit, `global` first, then in `protect` order
(`testdata/protected-upstream.yaml`):

```
app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed
app: bad.yaml: fullnameOverride: fullnameOverride is set by the platform and cannot be changed
```

A path listed in both `protect` and `append` is accepted when the
definition loads; `protect` is checked first, so a source setting it fails.

### append

`charts.<key>.append`: dotted paths of lists. A list a source gives at
such a path is appended to the list the values have there so far (upstream
defaults, base values, computed values, previous sources) instead of
replacing it. The value given must be a list; a `null` is refused too:
`upstream.app.env must be a list`.

With the example (`append: [env]`), the computed `OIDC_*` and `DB_*` items
come first and `LOG_LEVEL` follows
(`example/output/files/app.values.yaml`); when `upstream.app.yaml` and the
inline values both set `env`, the items of the file come before the inline
ones.

### Upstream errors

`<src>`: `upstream.<key>` for inline values, the file name for a file.
`<at>`: `upstream.<key>.<path>` for inline values, `<file>: <path>` for a
file.

| Case | Error |
|---|---|
| a template | `<key>: <src>: a key or value contains "{{"` |
| a protected path | `<key>: <at>: <protected> is set by the platform and cannot be changed` |
| an appended path, not a list | `<key>: <at> must be a list` |
| a listed file missing | `<key>: open <file>: ...` |
| a listed file, no instance files | `<key>: upstream file <file>: no instance files (Inputs.Instance)` |

## Merge precedence

The final values of a compiled upstream chart (`Result.Values[<key>]`),
each layer over the previous ones:

| | Layer | From |
|---|---|---|
| 1 | upstream defaults | `values.yaml` of the key's source, from the [chart cache](#chart-cache), its `null` values removed |
| 2 | base values | the file `charts.<key>.base` |
| 3 | computed values | `output.<key>` of the mapping |
| 4 | upstream files | the instance's files, in order ([Upstream values](#upstream-values)) |
| 5 | inline upstream values | `upstream.<key>` of the parameters |

The source: the upstream chart of the key ([Chart index](#chart-index)).
Keys of one source take layer 1 from it, the other layers from their own
entries. The chart of an OAuth client and the okdp chart have layers 1 (the
defaults of their [platform chart](#platform-charts)) and 3 (the values
the compiler computes for them: [OAuth client charts](#oauth-client-charts),
[the okdp chart](#the-okdp-chart)) only.

Then `global` is replaced by: the `global` of layer 1, under the `global` of
the parameters, under the `global` of layer 3. The `global` of the base
values is not kept; layers 4 and 5 may not set it (protected). Every chart
thus gets the platform values.

Layers 2 to 5 are merged as Helm merges `-f` files: maps merge key by key,
lists and scalars replace (except at [append](#append) paths), a `null`
replaces too and stays `null`. The result is a complete values file, the
upstream defaults included.

### Nulls

A `null` of layers 2 to 5 stays `null` in `Result.Values`; Helm gives it its
meaning when it renders: over a default, the key is deleted; with no
default, it is kept.

```yaml
# upstream: {app: {podAnnotations: null, newKey: null}}
podAnnotations: null   # the chart's default {}: deleted by Helm
newKey: null           # no default: kept by Helm
```

The `null` defaults of the upstream chart are left out (Helm 4 drops them,
Helm 3 copies them back).

### Equivalence with Helm

With Helm 3 or 4,

```bash
helm template <release> <source> -f <Result.Values[key]>
```

renders what

```bash
helm template <release> <source> -f <base> -f <computed> -f <upstream files...> -f <inline upstream>
```

renders, `global` and the [appended](#append) lists aside. `<source>`:
`Result.Charts[key]`, `<chart> --repo <repository> --version <version>`,
`oci://<repository>/<chart> --version <version>`, or its archive in the
chart cache.
`testdata/helm/values` holds what Helm computes on each case
(`TestHelmValues`).

### Schema check of the final values

Each compiled chart whose source has a `values.schema.json` in the
[chart cache](#chart-cache) is checked on its final values coalesced over
its defaults: what its templates see, a `null` that deletes a default
checked as absent. The check is the [parameter check](#schema-check); the
violations of every chart are reported together, sorted, under
`<key> (<chart>-<version>)` (here `app` and `canary` share the source
`app` 1.8.0):

```
compiled values don't meet the specifications of the schema(s) in the following chart(s):
app (app-1.8.0):
- at '/image/tag': 'latest' does not match pattern '^[0-9.]+$'
- at '/replicaCount': maximum: got 5, want 3
canary (app-1.8.0):
- at '/replicaCount': maximum: got 5, want 3
```

A schema that does not compile fails with its path in the cache, prefixed
with the chart key (`app: app-1.8.0/values.schema.json: ...`).

## Outputs

`compiler.Compile` returns a `*compiler.Result`:

| Field | Type | Content |
|---|---|---|
| `Values` | `map[string]map[string]any` | chart key -> its final values ([Merge precedence](#merge-precedence)) |
| `Computed` | `map[string]any` | chart key -> the values computed for it, before any merge: the mapping's `output.<key>`; for an [OAuth client chart](#oauth-client-charts), the values of the oidc-dcr template; for the [okdp chart](#the-okdp-chart), `objects`: a copy of `Objects` |
| `Charts` | `map[string]compiler.ChartRef` | chart key -> the upstream chart that renders its values ([Chart index](#chart-index)) |
| `Objects` | `[]map[string]any` | OKDP's own objects, whole manifests: the [descriptor ConfigMap](#descriptor-configmap), the [credentials Secrets](#credentials-secrets), then the [objects section](#objects-section) |
| `Dependencies` | `[]compiler.Dependency` | the instances the parameters reference ([Dependencies](#dependencies)) |

`Values`, `Computed` and `Charts` have one entry per compiled chart: a chart
the mapping leaves out (`$if`) is in none, and is not to be rendered; the
okdp chart is in all three, and in `dcr` mode the chart of each OAuth
client. Numbers are float64, as in Helm values.
The files the binary writes from them:
[command line](../01_usage.md#command-line), and the
[umbrella chart](#umbrella-chart); `example/output/` holds those of the
example ([Example](#example)).

### Chart index

`Result.Charts`: the upstream chart that renders each compiled chart, by
chart key.

```go
type ChartRef struct {
	Repository string `yaml:"repository" json:"repository"` // https://... or oci://...
	Chart      string `yaml:"chart" json:"chart"`           // the chart's name in the repository
	Version    string `yaml:"version" json:"version"`       // a pinned version
}
```

| Compiled chart | `Result.Charts[<key>]` |
|---|---|
| a chart of `service.yaml` | `charts.<key>.source` ([sources](02_service_definition.md#sources)) |
| the chart of an OAuth client | `global.okdp.charts.oidcDcr`, `compiler.DcrSource` by default ([Platform charts](#platform-charts)) |
| the okdp chart | `global.okdp.charts.objects` |

The final values of a key are rendered by its upstream chart, from its
repository, once per key: two keys of one source are two renders of it
([equivalence with Helm](#equivalence-with-helm)).

The example's in `dcr` mode (`okdp.charts.yaml`):

```yaml
app:
  repository: https://charts.example.org
  chart: app
  version: 1.8.0
oidc-dcr:
  repository: oci://quay.io/adaltas
  chart: oidc-dcr
  version: 0.5.0
okdp:
  repository: oci://registry.example.org/okdp
  chart: okdp-objects
  version: 1.0.0
```

Everything else is keyed by the key
([sources](02_service_definition.md#sources)). The binary writes this map to
`<emit-dir>/okdp.charts.yaml`; rendering with it:
[rendering with Helm](../01_usage.md#rendering-with-helm).

### OAuth client charts

In `dcr` mode (`platform.oidc.dcr.enabled`: `global.okdp.oidc.enabled`, true
by default, and `clientProvisioning: dcr`), the compiler adds one chart per
OAuth client of the mapping's `oidcClients` section
([oidcClients](02_service_definition.md#oidcclients)), rendered by the
platform's oidc-dcr chart (`global.okdp.charts.oidcDcr`,
`oci://quay.io/adaltas` `oidc-dcr` 0.5.0 by default:
[Platform charts](#platform-charts)): its Job, a pre-install/pre-upgrade
hook, registers the client anonymously at the platform's `registrationUrl`
and writes it to a Secret. In `existing` mode, or with OIDC disabled, no
chart is added and the [chart cache](#chart-cache) needs no oidc-dcr chart;
the clients are checked all the same.

| Client | Chart key | Job, ServiceAccount, Role | Secret, `dcr` | Secret, `existing` |
|---|---|---|---|---|
| main (no `name`) | `oidc-dcr` | `<release>-oidc-dcr` | `<release>-<namespace>-dcr` | `creds-<release>-oauth2` |
| `name: n` | `n-oidc-dcr` | `<release>-n-oidc-dcr` | `<release>-<namespace>-n-dcr` | `creds-<release>-oauth2-n` |

The objects' name, `<release>-<chart key>` cut to 63 characters without a
trailing `-` (as `okdpFullname`), is set as `fullnameOverride`,
`security.serviceAccount` and `security.role`. In `dcr` mode a client's
`secret` replaces the Secret name, which
[`oidcClientSecret`](02_service_definition.md#oidcclientsecret) does not
follow.

A client chart has no `service.yaml` entry, and its key is reserved
([oidcClients](02_service_definition.md#oidcclients)): it takes no base values
and no instance upstream values ([merge precedence](#merge-precedence)).

The computed values are not Go code: `internal/okdp/oidc-dcr.cel.yaml`, a
template in the mapping language embedded in the compiler, computes them
for each client from the client (its keys, checked, `null` keys left out),
its names (the chart key, the objects' name, the Secret) and the mapping's
context ([why](../00_architecture.md#the-oidc-dcr-template)):

| Value | From |
|---|---|
| `registrationUrl`, `ttlSeconds`, `backoffLimit`, `image` (when set) | the [OAuth client settings](#oauth-client-settings) |
| `request` | the registration request, OIDC Dynamic Client Registration fields: `application_type: web`; `client_name`: `clientName`; `grant_types`: `grantTypes`; `scope`: `scope` without `openid` (Keycloak refuses it as a client scope); `redirect_uris`: `redirectUris`, when not empty; `token_endpoint_auth_method: none` for a `public` client; then the client's `request` merged over them |
| `tls` | `insecure: false`; `certificate`: the client's `caSecret`, else the platform's |
| `secret`, `fullnameOverride`, `security.serviceAccount`, `security.role` | the names above |
| `mapping` | `useDefault: false`; `keyMapping`: `client_id`, and `client_secret` unless the client is `public` |
| `commonLabels` | `app.kubernetes.io/instance: <release>` |
| `waitForProvider` | when `global.okdp.oidc.issuerUri` is set: `discoveryUrl`, the issuer (one trailing `/` dropped) followed by `/.well-known/openid-configuration`; `ttlSeconds` when `waitTtlSeconds` is set |

The client keys, their types and defaults:
[oidcClients](02_service_definition.md#oidcclients). Maps merge over the
chart's defaults: its default `mapping.keyMapping` entries stay, so the
Secret also holds `registration_client_uri` and `registration_access_token`
of the registration response (oidc-dcr 0.5.0). The example's final values
in `dcr` mode, with the platform's settings above and oidc-dcr 0.5.0 in the
chart cache, trimmed:

```yaml
# app.values.yaml
env:
  - name: OIDC_CLIENT_ID
    valueFrom: {secretKeyRef: {key: client_id, name: demo-webapp-demo-dcr}}   # platform.oidcClientSecret
  ...
# oidc-dcr.values.yaml
backoffLimit: 6                        # default of the settings
commonLabels: {app.kubernetes.io/instance: demo-webapp}
enabled: true                          # upstream default
fullnameOverride: demo-webapp-oidc-dcr
global: {okdp: ...}
image: {pullPolicy: IfNotPresent, pullSecrets: [], registry: quay.io, repository: adaltas/oidc-dcr-job, tag: ""}   # upstream default
mapping:
  defaultKeys: {...}                   # upstream default, unused
  keyMapping:
    client_id: .client_id
    client_secret: .client_secret
    registration_access_token: .registration_access_token   # upstream default
    registration_client_uri: .registration_client_uri       # upstream default
  useDefault: false
registrationUrl: https://sso.okdp.example.org/realms/okdp/clients-registrations/openid-connect
request:
  application_type: web
  client_name: demo-webapp-demo
  grant_types: [authorization_code, refresh_token]
  redirect_uris: [https://demo-webapp.okdp.example.org/oauth2/callback]
  scope: profile email groups          # platform.oidc.scope, without openid
secret: demo-webapp-demo-dcr
security: {role: demo-webapp-oidc-dcr, serviceAccount: demo-webapp-oidc-dcr}
tls: {certificate: certs-bundle, insecure: false}
ttlSeconds: 300                        # global.okdp.oidc.dcr.ttlSeconds
waitForProvider:
  discoveryUrl: https://sso.okdp.example.org/realms/okdp/.well-known/openid-configuration
  ttlSeconds: 600                      # upstream default
```

### Descriptor ConfigMap

One per instance, first of `Objects`: what the console lists, and what other
instances reference by name. Built from the mapping's `instance` and
`provides` sections ([Sections](02_service_definition.md#sections)).

| Field | Value |
|---|---|
| `metadata.name` | `<release>-okdp`, cut to 63 characters |
| `metadata.namespace` | `Inputs.Namespace` |
| `metadata.labels` | the labels, plus `okdp.io/provides-<contract>: "true"` per contract provided |
| `data.service` | `name` of `service.yaml` |
| `data.version` | `version` of `service.yaml` |
| `data.url` | `instance.url`, trimmed |
| `data.usage` | `instance.usage`, trimmed, with a final newline |
| `data.outputs.yaml` | the provided connections, a YAML list; `[]` when none |

The labels: the `labels` of the [context](02_service_definition.md#context),
from `name` and `appVersion` of `service.yaml`.

`outputs.yaml`: one item per `provides` item, keys sorted (Helm's `toYaml`):

| Key | Value |
|---|---|
| `contract` | `contract` of the item |
| `name` | `name` of the item; `<release>` by default |
| `secretRef` | `{name}` of `secretRef`; else `{name: <name>-<contract>-credentials}` when the item has a `secret`; else absent |
| `values` | `values` of the item, [completed](#completion) against the contract |

Every item is checked for its shape, then against its contract, in the
order of `provides`, the fields of its `values` and of the contract's
`x-okdp-internal` convention in alphabetical order. The first error is
reported, as for an expression of the mapping: `provides` is the package's
to fix, not the instance's, and the error is the same from one compile to
the next. `<out>`: `provides "<name>" (<contract>)`; `<i>`: the item's
position.

| Case | Error |
|---|---|
| an item that is not a map | `provides[<i>]: must be a map (contract, name, values, secretRef, secret), got <value>` |
| `values`, `secretRef` or `secret` set, not a map | `provides[<i>]: values: must be a map of contract fields, got <value>` ([provides](02_service_definition.md#provides)) |
| no `<contract>.schema.json` | `<out>: unknown contract` |
| not a contract field | `<out>: field "<key>" is not part of the <contract> contract` |
| a secret field | `<out>: "<key>" is a secret field, put it in the credentials Secret` |
| required field not set | `<out>: field "<key>" is required by the <contract> contract` |
| internal convention | `<out>: <field> is "<value>" but internal references to "<name>" resolve it to "<expected>"` |
| same contract and name twice | `two <contract> outputs are named "<name>"` |

Internal convention: an output named `<release>` (the default), of a contract
with `x-okdp-internal`, must hold, for each field of the convention, the
value an internal reference to `<release>` resolves to. An instance that
references this one by name gets the values it publishes.

### Credentials Secrets

The Secret of a `provides` item with a `secret`: named after the item's
`secretRef` (`<name>-<contract>-credentials` by default), labelled
`okdp.io/credentials-<contract>: "true"`. They follow the descriptor in
`Objects`, in `provides` order.

| `secret` | Objects |
|---|---|
| `{stringData: {...}}` | a `v1` `Secret`, `type: Opaque`, `stringData` as given; labels: the labels and the credentials label |
| `{generate: [<keys>], stringData: {...}}` | per key, a `generators.external-secrets.io/v1alpha1` `Password` named `<secret>-<key>`; then the `external-secrets.io/v1` `ExternalSecret` `<secret>` writing the Secret: the generated keys and the static `stringData` ones (`stringData` optional) |
| neither, in a non-empty `secret` | error: `<service>: output "<name>": secret needs stringData or generate` (`<service>`: `name` of `service.yaml`) |

A generated Secret is what `okdpGeneratedSecret` renders
([Functions](02_service_definition.md#functions): key options, generator
names), the credentials label on the `ExternalSecret` and on the Secret it
writes, not on the `Password` generators. The random values are generated in
the cluster, once (`refreshInterval: "0"`): every render of the release is
the same. For these items:

```yaml
provides:
  - contract: s3
    name: store-a
    values: {apiUrl: https://a.example.org}
    secret:
      generate: [{key: accessKey, length: 20}, {key: secretKey, length: 40}]
      stringData: {region: eu}
  - contract: s3
    name: store-b
    values: {apiUrl: https://b.example.org}
    secret: {stringData: {accessKey: ak, secretKey: sk}}
```

`Objects`: `ConfigMap/demo-secrets-okdp`,
`Password/store-a-s3-credentials-accesskey`,
`Password/store-a-s3-credentials-secretkey`,
`ExternalSecret/store-a-s3-credentials`, `Secret/store-b-s3-credentials`.

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: store-a-s3-credentials
  namespace: demo
  labels:                               # the labels, and:
    okdp.io/credentials-s3: "true"
spec:
  refreshInterval: "0"
  target:
    name: store-a-s3-credentials
    creationPolicy: Owner
    template:
      engineVersion: v2
      metadata:
        labels:                           # the labels, and:
          okdp.io/credentials-s3: "true"
      data:
        accessKey: '{{ index . "accessKey" }}'
        region: eu
        secretKey: '{{ index . "secretKey" }}'
  dataFrom:
    - sourceRef:
        generatorRef: {apiVersion: generators.external-secrets.io/v1alpha1, kind: Password, name: store-a-s3-credentials-accesskey}
      rewrite: [{regexp: {source: ^password$, target: accessKey}}]
    - sourceRef:
        generatorRef: {apiVersion: generators.external-secrets.io/v1alpha1, kind: Password, name: store-a-s3-credentials-secretkey}
      rewrite: [{regexp: {source: ^password$, target: secretKey}}]
```

### Objects section

The mapping's `objects`, after the credentials Secrets, in order, as
computed: the compiler adds no namespace, label or name. Each is a whole
manifest, with a `kind` and a `metadata.name`
([objects](02_service_definition.md#objects)).

### The okdp chart

`compiler.ObjectsChart` (`"okdp"`): the chart of OKDP's own objects, which
the compiler adds to every compile, rendered by the platform's objects
chart (`global.okdp.charts.objects`: [Platform charts](#platform-charts)),
an okdp-objects chart (values `objects: []`; its template prints each item
of `objects` as one YAML document):

- its computed values are `objects`, a copy of `Objects`; its final values,
  those over the chart's defaults, and `global`
  ([merge precedence](#merge-precedence));
- it is compiled whatever the mapping outputs, also when it outputs no
  chart;
- the compiler owns it: no base values, no instance upstream values;
  `service.yaml` may not list it (refused when it is loaded), nor the
  mapping's `output`:

```
service.yaml: charts.okdp: the chart of OKDP's own objects is added by the compiler: remove it from service.yaml
mapping output "okdp": the chart of OKDP's own objects is compiled from the objects section: remove it from output
```

The descriptor, the credentials Secrets and the objects section then reach
the cluster with the upstream charts. `Objects` holds them too, for a
caller that deploys them otherwise.

### Dependencies

`Dependencies`: one item per internal [connection reference](#resolution),
sorted by `field` (string order). Two references to one instance give two
items; external and empty references give none. `nil` when there is none.

```go
type Dependency struct {
	Instance string `json:"instance" yaml:"instance"` // the referenced instance (its release name)
	Contract string `json:"contract" yaml:"contract"` // the contract it must provide
	Field    string `json:"field" yaml:"field"`       // the parameter that references it, e.g. backends[0].database
}
```

The compiler is not given the other instances: it does not check that the
instance exists, nor that it provides the contract. A deployer orders the
releases with the list ([GitOps](../01_usage.md#gitops)).

### Umbrella chart

`compiler.Umbrella(def, res, indent)` (`okdp-compile --umbrella`): the
instance as one chart, for one Helm release per instance, under the release
name the values were compiled for. Two files:

- `Chart.yaml`: `apiVersion: v2`; `name`, `version` and `appVersion` of
  `service.yaml` (`version` must be SemVer); `dependencies`, one per key of
  `Values`, sorted by key: the [chart index](#chart-index) entry of the key
  (`name`, `repository`, `version`), and `alias: <key>` when the key is not
  the chart's name (a key Helm accepts as an alias: letters, digits, `-`,
  `_`);
- `instance.values.yaml` (`compiler.UmbrellaValues`): `Values`, each key's
  final values under the key, each with its own `global`; none at the top.

The chart has no `values.yaml`. Its values are user values (`helm -f`,
Argo CD `valueFiles`, Flux `valuesFrom`): as the chart's own values they
would be defaults, and Helm drops the nulls of defaults
([Layers and nulls](#layers-and-nulls)).

Helm gives each subchart the final values of its key, as the chart alone
receives them ([Equivalence with Helm](#equivalence-with-helm)), and adds
what it adds to every subchart:

| Difference | The chart alone | As a subchart |
|---|---|---|
| `.Chart.Name` of an aliased key | the chart's name | the key: names derived from it change, unless the values set them (`fullnameOverride`, `nameOverride`) |
| `.Values.global` | absent when nothing sets it | an empty map |
| a null default nested in a map of the chart's `values.yaml`, the values leaving it unset | dropped | kept, null |

A template reads an absent key and a null the same way (`{{ .Values.x }}`,
`if`, `default`), but for `hasKey`, `toYaml`, and `range` over the map.
Each key's own `global` reaches its subchart, and the subcharts of that
chart, as when the chart is alone: there is no `global` at the top for Helm
to merge over it. `testdata/helm/umbrella` checks it with Helm 4.3: the
umbrella over the charts of the values fixtures, one chart under two keys.

### Example

`example/output/files/`, what `--emit-dir` writes for the example: release
`demo-webapp`, namespace `demo`, `example/input/instance.yaml`
(`replicas: 2`, `database: demo-db`, an `env` entry in `upstream.app`) over
`example/input/platform.yaml` (`existing` mode: no OAuth client chart,
[OAuth client charts](#oauth-client-charts)).

`app.values.yaml`, annotated:

```yaml
env:
  - name: OIDC_ISSUER                     # computed
    value: https://sso.okdp.example.org/realms/okdp
  - name: OIDC_CLIENT_ID                  # platform.oidcClientSecret, existing mode
    valueFrom:
      secretKeyRef:
        key: client_id
        name: creds-demo-webapp-oauth2
  - name: OIDC_CLIENT_SECRET
    valueFrom:
      secretKeyRef:
        key: client_secret
        name: creds-demo-webapp-oauth2
  - name: DB_URL                          # the database connection, resolved
    value: jdbc:postgresql://demo-db-db.demo.svc:5432/demo-db
  - name: DB_PASSWORD
    valueFrom:
      secretKeyRef:
        key: password
        name: demo-db-database-credentials
  - name: LOG_LEVEL                       # upstream.app, appended
    value: debug
fullnameOverride: demo-webapp-app         # computed
global:                                   # the parameters' global
  okdp:
    # ... the platform values
image:
  repository: example/webapp              # upstream default
  tag: "2.3"                              # computed: service.appVersion
ingress:
  # ... computed
replicaCount: 2                           # computed: values.replicas
resources:                                # base values
  limits:
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

`okdp.values.yaml`: the values of the [okdp chart](#the-okdp-chart), the
descriptor ConfigMap under `objects`:

```yaml
global:
  okdp:
    # ... the platform values
objects:
  - apiVersion: v1
    data:
      outputs.yaml: |
        - contract: http
          name: demo-webapp
          values:
            internalUrl: http://demo-webapp-app.demo.svc:8080
            url: https://demo-webapp.okdp.example.org
      service: webapp
      url: https://demo-webapp.okdp.example.org
      usage: |
        A web application. Open https://demo-webapp.okdp.example.org
      version: 1.0.0
    kind: ConfigMap
    metadata:
      labels:
        app.kubernetes.io/instance: demo-webapp
        # ...
        okdp.io/provides-http: "true"
        okdp.io/service: webapp
      name: demo-webapp-okdp
      namespace: demo
```

`okdp.charts.yaml` and `okdp.dependencies.yaml`:

```yaml
app:
  repository: https://charts.example.org
  chart: app
  version: 1.8.0
okdp:
  repository: oci://registry.example.org/okdp
  chart: okdp-objects
  version: 1.0.0
```

```yaml
- instance: demo-db
  contract: database
  field: database
```

`example/output/umbrella/`, what `--umbrella` writes: `instance.values.yaml`,
the same values under their keys (`app:`, `okdp:`), and `Chart.yaml`:

```yaml
apiVersion: v2
name: webapp
version: 1.0.0
appVersion: "2.3"
dependencies:
  - name: app
    repository: https://charts.example.org
    version: 1.8.0
  - name: okdp-objects
    alias: okdp
    repository: oci://registry.example.org/okdp
    version: 1.0.0
```
