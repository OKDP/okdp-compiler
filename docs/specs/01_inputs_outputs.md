# Inputs and outputs

Normative reference of what a compile reads besides the service definition
(`service.yaml` and `values.cel.yaml`: [02_service_definition.md](02_service_definition.md)),
and of what it produces. The flags that pass these inputs to the binary are in
[01_usage.md](../01_usage.md); how the compiler works inside, in
[00_architecture.md](../00_architecture.md).

| Input | Field | Required | Section |
|---|---|---|---|
| Package files | `Definition.Files` (`fs.FS`) | yes | [Package files](#package-files) |
| Chart cache | `Inputs.Charts` (`fs.FS`) | yes | [Chart cache](#chart-cache) |
| Contracts | `Inputs.Contracts` (`fs.FS`) | yes | [Contracts](#contracts) |
| Platform values layers | `Inputs.Platform` (`[]map[string]any`) | no | [Parameters](#parameters), [Platform values](#platform-values), [Values files](#values-files) |
| Instance values layers | `Inputs.Values` (`[]map[string]any`) | no | [Parameters](#parameters), [Instance values](#instance-values), [Values files](#values-files) |
| Release name | `Inputs.Release` | yes | `release.name`, object names |
| Namespace | `Inputs.Namespace` | yes | `release.namespace`, object namespaces, `${namespace}` of contracts |

A missing required input fails before anything is read, in this order:
`no service definition` for a nil `*Definition`, `Definition.Files is
required`, `Inputs.Charts is required`, `Inputs.Contracts is required`,
`Inputs.Release is required`, `Inputs.Namespace is required`. Then the
names: the release is a release name Helm accepts, from which OKDP derives
object names (`<release>-okdp`, Secrets), and the namespace a namespace
name, both DNS-1123 labels (lowercase letters, digits and `-`, a letter or
a digit at each end):

| Case | Error |
|---|---|
| a release that is not a DNS-1123 label, or longer than 53 characters (Helm's limit) | `Inputs.Release "<name>": a release name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 53 characters at most (Helm's limit)` |
| a namespace that is not a DNS-1123 label, or longer than 63 characters | `Inputs.Namespace "<name>": a namespace name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 63 characters at most` |

Paths in an `fs.FS` are relative to its root.

## Package files

A package is a directory: the service definition and the parameters of the
service. `compiler.LoadDefinition(fsys)` reads `service.yaml` and
`values.cel.yaml` at the root of `fsys` ([service
definition](02_service_definition.md)) and keeps `fsys` as
`Definition.Files`, from which each compile reads the other files; a
`Definition` built in memory sets `Files` itself. The compiler reads these
files, and no other:

```
<package>/
  service.yaml                        the service, its charts and their sources
  values.cel.yaml                     the mapping
  config.schema.json                  schema of the parameters, their defaults, connection references
  <key>.defaults.yaml                 OKDP's defaults for the chart <key> (charts.<key>.defaults)
```

| File | Required | Read as | Used for |
|---|---|---|---|
| `service.yaml` | yes | YAML 1.2 | the service: name, versions, catalog metadata, charts ([service.yaml](02_service_definition.md#serviceyaml)) |
| `values.cel.yaml` | yes | the mapping language | the computed values, objects and outputs ([values.cel.yaml](02_service_definition.md#valuescelyaml)) |
| `config.schema.json` | yes | JSON | [parameter defaults](#defaults), [parameter check](#schema-check), [connection references](#declaring-a-reference) |
| defaults file | when `charts.<key>.defaults` is set | values file | second layer of the [final values](#merge-precedence) |

`<key>`: a chart key, a key of `charts` in `service.yaml`, the chart of an
OAuth client ([OAuth client charts](#oauth-client-charts)) or `okdp`
([the okdp chart](#the-okdp-chart)); it names the chart's values. A
compiled chart: one the mapping outputs (`output.<key>`, not left out by
`$if`, not null), the okdp chart, and in `dcr` mode the chart of each
OAuth client. Each is rendered by an upstream chart, its source
([chart index](#chart-index)); several keys may share one. The defaults file
may be any path of the package, `<key>.defaults.yaml` by convention; a
path out of it (`../app.yaml`) fails. The service's name, versions and catalog
metadata are fields of `service.yaml` ([Service](02_service_definition.md#service)).

`config.schema.json` is required, even for a service without parameters:
the defaults, the schema check and the connection walk read it. A missing
file fails with the error of the file system, prefixed with the chart key
for a defaults file:

```
open config.schema.json: no such file or directory
app: open app.defaults.yaml: no such file or directory
```

### Values files

Every values file is read as Helm 4 reads one (`loader.LoadValues`): the
upstream charts' `values.yaml` ([chart cache](#chart-cache)), the defaults
files, and the values layers read by `compiler.ParseValues` (the binary's
`--platform` and `-f` files).

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
from such files. Values built in memory and given in `Inputs.Platform` or
`Inputs.Values` are read as the values they would be once written to a
file and read back, at any depth, whatever their Go types
(`value.Normalize`):

- A map of any type is a map: `map[string]string`, `map[string][]string`,
  a named map type, `map[any]any`. Its keys are strings: a number or a
  boolean is printed as Helm reads a YAML key (`map[any]any{1: x}` and
  `map[int]string{1: x}` have the key `"1"`), an `encoding.TextMarshaler`
  is its text.
- A slice or an array is a list (`[]string`, `[2]int`); a `[]byte` is its
  base64 text, as `encoding/json` writes it.
- Every number is a float64, of any integer or float type (a `float32` as
  its shortest decimal: `float32(0.1)` is `0.1`); an integer past 2^53
  rounds, as Helm reads it. A `json.Number` is a number.
- A pointer or an interface is what it points to, `null` when nil; a nil
  map or slice is empty, not `null`.
- A struct, a `json.Marshaler` or an `encoding.TextMarshaler`
  (`time.Time`, `json.RawMessage`) is what `encoding/json` writes: a struct
  by its `json` tags.

What a values file cannot hold fails the compile, before the parameters
are computed, with the layer (`platform layer <n>`, `values layer <n>`:
the n-th of `Inputs.Platform`, of `Inputs.Values`) and the path of the
value: a func, a chan, a complex, a NaN or an infinity, a string or a key
that is not UTF-8, a key of another type (`null`, a struct), two keys that
read as the same (`1` and `"1"`), a map, slice or pointer that contains
itself:

```
values layer 2: hooks[0]: a func() cannot be a value
platform layer 1: global.proxy["no.proxy"]: not a finite number: NaN
values layer 1: labels: two keys read as "1"
```

JSON files (`config.schema.json` of the package, `values.schema.json` of the upstream charts,
the contracts) are strict JSON: no comments.

## Chart cache

`Inputs.Charts`: the upstream charts the compiled charts are rendered by,
each in its repository's directory, as `helm pull -d <cache>/<repository>`
writes them (`helm pull` does not create the directory: `mkdir -p` it
first), or unpacked. The binary reads a directory: `--charts`,
`$OKDP_CHARTS` by default ([command line](../01_usage.md#command-line)).

```
<cache>/
  <repository>/<chart>-<version>.tgz  a chart archive (helm pull -d <cache>/<repository>)
  <repository>/<chart>-<version>/     or the chart unpacked
  <chart>-<version>.tgz               the same, without the repository (helm pull -d <cache>)
  <chart>-<version>/
```

`<repository>` is the source's `repository` without its scheme (`oci://`,
`https://`, `http://`) and a trailing `/`: its host, port and path.
`oci://quay.io/adaltas` is `quay.io/adaltas/`, its oidc-dcr 0.5.0
`quay.io/adaltas/oidc-dcr-0.5.0.tgz`; `http://localhost:8080/charts` is
`localhost:8080/charts/`. A repository whose host and path are no
directory of the cache (empty; an empty, `.` or `..` segment; a `\`) is
refused with its source ([sources](02_service_definition.md#sources)). Two
repositories that differ only by their scheme share a directory.

A chart is found by its source: `<repository>/<chart>-<version>`, the
directory first, else the archive; else `<chart>-<version>` at the root, a
cache filled without the repository. When one compile uses one chart name
and version from two repositories (a fork, a generic name such as `app`),
a chart of them not in its repository's directory fails even when the root
holds it: the root entry could be the other repository's. Of a chart, the
compiler reads `values.yaml` and `values.schema.json` only: at the root of
the directory, or in the archive's top-level directory, not those of its
subcharts (`<chart>/charts/<sub>/values.yaml`). A chart without
`values.yaml` has no defaults, one without `values.schema.json` no
[schema check](#schema-check-of-the-final-values).

An archive is read as Helm's loader reads one (`LoadArchiveFiles`, the
same in Helm 3.19, 4.2 and 4.3): an entry's name is split on `\` when it
has one (an archive written on Windows), on `/` otherwise, and its first
segment, the chart's directory, dropped. An entry at the archive's root, an
absolute path, a path out of the chart (`..`) or a Windows drive (`C:/`)
fails in Helm's words, in any entry, read or not. A chart is capped at 100
MiB decompressed: an archive counts every file and fails at the cap, as
Helm does; an unpacked chart counts the two files read and fails over the
cap, as Helm 4.3 counts all of its files. Helm 4.2 and 3.19, which the
deployers embed
([GitOps](../01_usage.md#gitops)), also refuse a file over 5 MiB; Helm 4.3
no longer does, nor does the compiler: such a chart compiles, and fails to
render with them. A UTF-8 byte order mark at the start of either file is
dropped, as Helm drops it. Each chart is read once
per compile, whatever the number of keys it renders, and only when a chart
it renders is compiled: the oidc-dcr chart in `dcr` mode with a client, the
objects chart in every compile. `examples/02_advanced/input/charts/` holds the
example's charts unpacked, their `values.yaml` only: enough to compile, not
to render (`charts.example.org/app-1.8.0/`,
`registry.example.org/okdp/okdp-objects-1.0.0/`,
`quay.io/adaltas/oidc-dcr-0.5.0/`).

A chart the cache does not hold fails with the commands that download it
into its repository's directory, prefixed with the chart key; a chart name
and version of two repositories at the root only, with those of each; a
file that does not read, with its path:

```
app: chart app-1.8.0 is not in the chart cache: mkdir -p <chart cache>/charts.example.org && helm pull app --repo https://charts.example.org --version 1.8.0 -d <chart cache>/charts.example.org
oidc-dcr: chart oidc-dcr-0.5.0 is not in the chart cache: mkdir -p <chart cache>/quay.io/adaltas && helm pull oci://quay.io/adaltas/oidc-dcr --version 0.5.0 -d <chart cache>/quay.io/adaltas
app: chart app-1.8.0 comes from 2 repositories, and the chart cache holds it only as app-1.8.0, which cannot tell them apart: pull each into its repository's directory: mkdir -p <chart cache>/charts.example.org && helm pull app --repo https://charts.example.org --version 1.8.0 -d <chart cache>/charts.example.org; mkdir -p <chart cache>/fork.example.org && helm pull app --repo https://fork.example.org --version 1.8.0 -d <chart cache>/fork.example.org
app: charts.example.org/app-1.8.0/values.yaml: cannot unmarshal yaml document: ...
app: charts.example.org/app-1.8.0.tgz: gzip: invalid header
app: charts.example.org/app-1.8.0.tgz: chart illegally references parent directory
app: charts.example.org/app-1.8.0.tgz: decompressed chart is larger than the maximum size 104857600
app: charts.example.org/app-1.8.0/values.yaml: chart exceeds maximum decompressed size of 104857600 bytes
```

## Parameters

The parameters of an instance: the values layers, the platform's
(`Inputs.Platform`) then the instance's (`Inputs.Values`), merged and
coalesced over the defaults of the package's `config.schema.json`, as Helm
computes `.Values` of a chart from its `values.yaml` and `-f` files.
`global` is the platform's ([Platform values](#platform-values)).

### Defaults

The defaults are the `default` keywords of `config.schema.json`: that of
each property reached from the root through `properties` alone, at its
path, read as JSON (every number a float64). They play the part of a
chart's `values.yaml`:

```json
{
  "properties": {
    "replicas": {"type": "integer", "minimum": 1, "default": 1},
    "metrics": {"type": "object", "properties": {
      "enabled": {"type": "boolean", "default": false},
      "interval": {"type": "string", "default": "30s"}}},
    "upstream": {"type": "object", "default": {}}
  }
}
```

gives `replicas: 1`, `metrics: {enabled: false, interval: 30s}` and
`upstream: {}`. A default under `global` (`properties.global.properties`)
is one of a platform value: the platform's layers override it, and the key
it declares an instance may set ([Platform values](#platform-values)). The
`default` of an `x-*` keyword, of `enum`, `const` or `examples` is not
read, nor `x-ui-condition`: unlike a [contract's](#completion), a
parameter's default holds whatever the other values.

Each compile fails on a `default` that is not a parameter's, named by its
JSON pointer:

- at the root, or under any other keyword (`allOf`, `anyOf`, `oneOf`, `if`,
  `then`, `else`, `not`, `items`, `prefixItems`, `additionalProperties`,
  `patternProperties`, `$defs`, `definitions`, ...): whether it applies
  would depend on the values;
- under a property that has a `default`: the two would compete;
- one its property's schema rejects, compiled as for the [schema
  check](#schema-check), its violations printed as the schema check prints
  them.

```
config.schema.json#/allOf/0/properties/a/default: a default applies only to a property reached from the root through properties
config.schema.json#/properties/a/default: a property under it has a default too (#/properties/a/properties/b/default): keep one
config.schema.json#/properties/replicas/default: the default does not meet its property's schema:
- at '': minimum: got 0, want 1
```

### Layers and nulls

1. The layers are merged in order, as Helm merges `-f` files (`MergeMaps`):
   maps merge key by key, lists and scalars replace, a `null` replaces too
   and stays `null`.
2. The result is coalesced over the [defaults](#defaults)
   (`CoalesceValues`): a `null` over a default deletes the key; a `null`
   where there is no default is kept; the `null` defaults are dropped; when
   a value and its default are not both maps, the value wins.

| Defaults | Layers | Parameters |
|---|---|---|
| `metrics: {enabled: false, interval: 30s}` | `metrics: {enabled: true}` | `metrics: {enabled: true, interval: 30s}` |
| `metrics: {enabled: false, interval: 30s}` | `metrics: null` | no `metrics` key |
| no `foo` | `foo: null` | `foo: null` |
| `env: [A]` | `env: [B]` | `env: [B]` |

The corner cases (a `null` at the top level or in a nested map, over a
`null` default) follow Helm 4: `testdata/helm/values/nulls`. Helm 3 keeps
the `null` defaults the values leave unset, and deletes a key below the
top level that the defaults and the values both set to `null`
([Equivalence with Helm](#equivalence-with-helm)).

### Schema check

The parameters are checked against `config.schema.json` first, before the
[platform requirements](#requirements), the connections and the mapping,
as Helm 4 checks the values of a chart:

- the values checked: the parameters above, `global`, `connections` and
  `upstream` included, every connection reference still the string written;
- the draft: the schema's `$schema`, draft 2020-12 without one; `format` is
  asserted for drafts before 2019-09 only;
- `$ref`: within the schema only. A `urn:` reference accepts anything (as in
  Helm); a file or a URL fails the compile: `config.schema.json: failing
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

`testdata/helm/schema` holds what Helm reports on each case
(`TestHelmSchema`): Helm 4.3.0 and the versions the deployers embed, 4.2
and 3.19, report the same.

### Reserved keys

Three top-level keys are not parameters of the service:

| Key | Content | In the mapping's `values` |
|---|---|---|
| `global` | the platform values, `global.okdp`: set by the platform's layers, by an instance's only for the keys the schema declares ([Platform values](#platform-values)) | removed: given as `global` and `platform` |
| `connections` | the external connections, by name ([Connections](#connections)) | removed: the references are resolved in place |
| `upstream` | the instance's upstream values, by chart key ([Upstream values](#upstream-values)) | as written |

They are checked with the parameters: a schema with `additionalProperties:
false` declares them, `{"type": "object"}` at least
(`examples/02_advanced/input/service/config.schema.json`).

## Connections

A connection reference: a parameter that names a connection, either an entry
of `connections` (external) or an instance of the namespace (internal). The
compiler replaces it with the fields of the connection, completed against
its [contract](#contracts), before the mapping runs.

### Declaring a reference

In `config.schema.json`, on a string property:

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
reference that is `""` or `null` is not set, and left as it is. Any other
value that is not a string (a map, a number, a list: the schema check lets
it through when the property does not say `"type": "string"`) fails: it
would reach the mapping unresolved, past the contract
([Connection errors](#connection-errors)). The packages of platform-charts
declare references in lists of objects, e.g. the catalogs of trino:

```json
"hiveCatalogs": {"type": "array", "items": {"type": "object", "properties": {
  "metastore": {"type": "string", "x-okdp-connection-ref": {"contract": "hive"}},
  "storage":   {"type": "string", "x-okdp-connection-ref": {"contract": "s3"}}}}}
```

### Resolution

A reference `<name>` is external when `connections.<name>` is a map; any
other name is internal: an instance (a release) of the namespace, so a
release name: a DNS-1123 label of 53 characters at most (lowercase letters,
digits and `-`, a letter or digit at each end), as Helm accepts it. It
becomes hosts, Secret names and a [dependency](#dependencies).

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

Both are then [completed](#completion) against the contract. The result,
`secretRef` and `okdp` included, may not contain `{{` in a key or a string,
whatever it comes from: the instance's layers refuse it already
([Layers](#layers)), and a connection of the platform's layers, a
contract's default or `${namespace}` and `${suffix}` are checked here, as
the connection reaches the charts' values through the mapping.

### What the mapping receives

The parameter, replaced by a map: the contract fields, `secretRef` when
there is one, and `okdp`, where the connection comes from. With
`examples/02_advanced/input/contracts/database.schema.json`:

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

Every reference that does not resolve, or is not a string, is reported, at
once, one line each, sorted by parameter path (string order, as the
[dependencies](#dependencies)), as the violations of the parameter schema
are. A reference gives one error, the first in this order: its type, its
contract (unknown, unreadable; another one, for an external connection; no
internal naming convention, then a name that is not a release name, for an
internal one), the fields of an external connection in alphabetical order,
the `required` fields: every field not set, one line each, in alphabetical
order; then the contract's types for an external connection: every
violation, one line each, sorted by field then message ([Checks](#checks));
last, `{{` in the connection, the first one found by path (keys sorted,
items in order). The same parameters give the same report from one compile
to the next:

```
main.db: connection "shared-db": field "user" is not part of the database contract
replicas[1]: connection "store" has contract "s3", "database" is expected
replicas[2] (connection "pg"): field "engine" violates the database contract: value must be one of 'postgresql', 'mysql'
replicas[2] (connection "pg"): field "port" violates the database contract: got string, want integer
replicas[3]: "Demo_DB" is not a connection, nor a release name: lowercase letters, digits and "-", a letter or digit at each end, 53 characters at most
```

| Case | Error | `testdata/` |
|---|---|---|
| neither a string nor `null` | `<path>: a connection reference must be a string, got <value>` | |
| no `<contract>.schema.json` | `<path>: unknown contract "<contract>"` | |
| `<contract>.schema.json` that cannot be read, is not JSON, not an object or not a valid JSON Schema | `<path>: the <contract> contract cannot be read: <contract>.schema.json: <cause>` | |
| external, another contract | `<path>: connection "<name>" has contract "<c>", "<contract>" is expected` | `wrong-contract.yaml` |
| external, not a contract field | `<path>: connection "<name>": field "<key>" is not part of the <contract> contract` | `unknown-field.yaml` |
| external, secret field | `<path>: connection "<name>": "<key>" is a secret field` | `secret-in-connection.yaml` |
| internal, no `x-okdp-internal` | `<path>: "<name>" is not a connection, and the <contract> contract has no internal naming convention` | |
| internal, not a release name | `<path>: "<name>" is not a connection, nor a release name: lowercase letters, digits and "-", a letter or digit at each end, 53 characters at most` | |
| required field not set | `<path> (connection "<name>"): field "<key>" is required by the <contract> contract`, one line per field | |
| external, a value the contract's schema refuses | `<path> (connection "<name>"): field "<field>" violates the <contract> contract: <violation>`; `... the fields violate the <contract> contract: <violation>` for a keyword on the whole connection | `connection-types.yaml` |
| `{{` in the connection | `<path> (connection "<name>"): field "<field>": the value contains "{{"`; `... the key contains "{{"` for a key. `<field>`: a path in the connection (`host`, `secretRef.name`, `tags[1]`) | |

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
`provides` item names it. No such file: `unknown contract`; a file that
cannot be read, is not JSON, not an object or not a valid JSON Schema:
`the <contract> contract cannot be read: <contract>.schema.json: <cause>`.

### Format

A JSON Schema with OKDP keywords. The values
of a connection or an output are checked against the schema
([Checks](#checks)); besides, the compiler reads:

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

The other `x-*` keywords (`x-okdp-external`, `x-okdp-required`,
`x-okdp-endpoint-from`, `x-okdp-icon`, `x-okdp-category`,
`x-ui-placeholder`, `x-ui-widget`) are not read, nor checked: an external
connection is accepted for any contract, `x-okdp-external` or not.

`examples/02_advanced/input/contracts/database.schema.json`, trimmed:

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

A connection, consumed or provided, is completed over the fields not set,
secret fields excepted:

1. defaults: `default`, when its `x-ui-condition` holds (a derived field's
   `default` is not used);
2. derived: `x-okdp-derived`;
3. 1 and 2 again, until they set no field;
4. required: every `required` field must be set; the fields not set are
   reported at once, one line each, in alphabetical order.

A field set is kept as it is. The fields are visited in alphabetical order,
the passes repeated: a conditional default applies whether its condition
field sorts before it or after it, is defaulted (`sslMode` on the default
`engine`) or derived. A derived field may follow from a defaulted one
(`driver` from the default `engine`). An internal
reference has only its `x-okdp-internal` fields and the defaults: if they
leave a `required` field unset, every internal reference to the contract
fails.

### Checks

- Consumed, external: the `contract` of the entry, every other key a
  non-secret field, completed, then valid against the schema
  ([Connections](#connections)).
- Consumed, internal: the contract has `x-okdp-internal`, completed. Its
  values are not checked against the schema: they follow from the contract,
  and are those the instance publishes, checked as provided.
- Provided (`provides`): the contract exists, every key of `values` a
  non-secret field, completed, then valid against the schema; and an output
  published under the release name, of a contract with `x-okdp-internal`,
  has the values an internal reference to the release resolves to
  ([Descriptor ConfigMap](#descriptor-configmap)).

Valid against the schema: the completed values (no `secretRef`, no `okdp`),
validated as a chart's values against its `values.schema.json`
([Schema check](#schema-check): draft, formats, `$ref`), every keyword but
`x-*` applying (`type`, `enum`, `pattern`, `minimum`, `items`,
`additionalProperties`...). The secret fields, never in the values, are
left out of the top-level `required`. Every violation is reported, one line
each, sorted by field (a path in the values: `port`, `catalogs[1]`) then
message:

```
<where>: field "<field>" violates the <contract> contract: <violation>
<where>: the fields violate the <contract> contract: <violation>
```

`<where>`: `<path> (connection "<name>")` for a connection,
`provides "<name>" (<contract>)` for an output; the second form for a
keyword on the values as a whole (`minProperties`). The keywords under a
`$ref` or an `allOf` are reported each; a failed `anyOf` or `oneOf` is one
violation, without its alternatives.

## Platform values

`global.okdp`: the settings of the platform (ingress, certificate issuers,
OIDC, proxy, the platform's charts...), given as the platform's values
layers, `Inputs.Platform` (the binary's `--platform` files):
`examples/02_advanced/input/platform.yaml`, `platform/platform-values.yaml` in a
platform repository. They are merged in order over the package's
[defaults](#defaults) and before the instance's layers, `Inputs.Values`
([parameters](#parameters)); a later platform layer may change or delete
(`null`) a value of the previous ones, as
`testdata/platform/missing-platform-value.yaml` does.

```yaml
global:
  okdp:
    ingress: {suffix: okdp.example.org, className: nginx}
    certificateIssuers: {selfSigned: {name: okdp-ca}}
    charts:
      objects: {repository: oci://registry.example.org/okdp, chart: okdp-objects, version: 1.0.0}
    oidc: {issuerUri: https://sso.okdp.example.org/realms/okdp}
```

`global` is the platform's: it comes from the package's
[defaults](#defaults) and the platform's layers, `global.okdp` decides the instance's host, where its
OAuth clients register and the charts its objects and clients are rendered
with, and every chart receives `global` ([merge
precedence](#merge-precedence)). An instance layer that sets `global` fails
before the parameter schema is checked, naming the layer by its position in
`Inputs.Values`, from 1 (the binary's `-f` files, in order), and the first
key of `global` it may not set, in key order; `global` set to a non-map,
`null` included, fails as a whole (`testdata/instance-sets-global.yaml`):

```
values layer 1: global.okdp is set by the platform (--platform, Inputs.Platform)
values layer 2: global is set by the platform (--platform, Inputs.Platform)
```

The exception: a key the parameter schema declares under `global`
(`properties.global.properties.<key>`), `okdp` excepted, which an instance
layer may set, over the platform's value. A package opens a global value of
its charts to instances this way:

```json
"global": {"type": "object", "properties": {"imageRegistry": {"type": "string"}}}
```

A platform layer is a values layer: it may set more than `global`, for
every instance of the platform, under the instance's layers. The useful
case is `connections`: a connection every instance may reference by name,
as one of its own ([Resolution](#resolution)). An instance layer giving a
connection of the same name merges with it, key by key.

```yaml
# examples/02_advanced/input/platform.yaml
connections:
  platform-db:
    contract: database
    host: db.okdp.example.org
    dbName: shared
    secretRef: {name: platform-db-credentials}
```

The example's instance references both kinds: `database: demo-db`, an
instance of the namespace, and `audit: platform-db`, the platform's
connection. Parameters and `upstream` values are the package's: a
platform file every service shares keeps to `global` and `connections`.

### Requirements

`require` of `service.yaml`: dotted paths under `global.okdp`, each
required in every case, or when its condition holds
([require](02_service_definition.md#require)). A required key must be set
and not empty (missing, `null`, `false`, `""`, `0`, an empty list or map
fails). The keys of every case are checked after the [parameter
schema](#schema-check), before the connections and the mapping
(`testdata/platform/missing-platform-value.yaml`); the conditions, in
order, once the mapping's context is built (the platform context, the
connections), before the [platform charts](#platform-charts) and the
mapping. Both fail with the same message:

```
global.okdp.ingress.className is required. It is a platform value: set it in the platform's values (--platform, Inputs.Platform)
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
when set, after the [requirements](#requirements) of every case, in every
mode. A `null` is not set. A value of another type is refused rather than
read as not set: `oidc: false` would leave OIDC enabled, `proxy: <url>`
give no proxy environment. The first violation is reported, in the order of
the table (`charts` is checked later: [Platform charts](#platform-charts)):

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
| `objects` | none: required, unless the compile leaves the okdp chart out (`compiler.WithoutObjectsChart`) | the [okdp chart](#the-okdp-chart), OKDP's own objects: an okdp-objects chart (values `objects: []`; its template prints each item of `objects` as one YAML document) |
| `oidcDcr` | `{repository: oci://quay.io/adaltas, chart: oidc-dcr, version: 0.5.0}` (`compiler.DcrSource()`) | the [OAuth client charts](#oauth-client-charts), in `dcr` mode: an oidc-dcr chart that takes the values of oidc-dcr 0.5.0, which the embedded template writes |

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
global.okdp.charts.objects is required: the chart OKDP's own objects are rendered with (okdp-objects: repository, chart, version). It is a platform value: set it in the platform's values (--platform, Inputs.Platform)
global.okdp.charts.objects must be a map (repository, chart, version), got "okdp-objects"
global.okdp.charts.objects.version must be a string, got 1
global.okdp.charts.objects.version "1.x": a pinned chart version (1.2.3), not a range
global.okdp.charts.oidcDcr.repository "quay.io": the URL of a chart repository (https://... or oci://...)
```

The [chart cache](#chart-cache) holds each when its chart is compiled: the
objects chart in every compile, the oidc-dcr chart in `dcr` mode with a
client. Another repository with the same chart and version (a mirror) has
its own directory there; the root entry `<chart>-<version>` serves both,
unless one compile uses the two.

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
after the [requirements](#requirements) of every case; the first violation
is reported, in that order. The three numbers fail alike, and a
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
(`testdata/platform/dcr-without-registration-url.yaml`):

```
global.okdp.oidc.dcr.authMethod "bearer" is not supported: OKDP registers its OAuth clients anonymously
global.okdp.oidc.dcr.registrationUrl is required (clientProvisioning: dcr): the endpoint the oidc-dcr Jobs register the OAuth clients at
```

## Instance values

What an instance sets: its parameters, its external connections, and values
for the upstream charts.

### Layers

`Inputs.Values`: the instance's layers in order, the last one winning
(Helm's `-f` order), after the platform's; the binary's `-f` files, in the
order given, after its `--platform` files:

```bash
bin/okdp-compile ... --platform examples/02_advanced/input/platform.yaml -f examples/02_advanced/input/config.yaml
```

An instance layer sets no `global` but the keys the parameter schema
declares under it ([Platform values](#platform-values)). Merge, nulls and
schema: [Parameters](#parameters).

No key or string of an instance layer, at any depth, may contain `{{`,
whatever key it is under: parameters, `connections`, `upstream`. A
parameter reaches the charts' values through the mapping, and some
upstream charts pass their values to `tpl`, where template code could call
`lookup` and read the cluster's Secrets. Each layer is checked in order,
normalized as it is merged, before the [schema check](#schema-check); the
first `{{` in path order (keys sorted, items in order) fails, the layer
numbered from 1 and the path written as in [protect](#protect) errors, a
list item `[<i>]`:

```
values layer 1: database: the value contains "{{"
values layer 2: upstream.app.podAnnotations["example.org/{{ x }}"]: the key contains "{{"
```

A layer is normalized before it is checked: a Go value an in-memory layer
gives that values cannot hold has failed then, with its layer and path
([values files](#values-files)). The platform's layers and the package's
defaults are not checked: they are reviewed with the platform and the package
([security boundaries](../00_architecture.md#security-boundaries)). A
connection resolved from them is ([Resolution](#resolution)).

### Upstream values

Values given to an upstream chart as they are, over the values the mapping
computed: `upstream.<key>` of the parameters, for a chart whose
`charts.<key>.upstream` of `service.yaml` is `true`
([upstream](02_service_definition.md#upstream)). Upstream values are keyed
by the chart key, not by its source
([sources](02_service_definition.md#sources)). Values for a chart that
accepts none (an [OAuth client chart](#oauth-client-charts) and the [okdp
chart](#the-okdp-chart) included), or for a name that is not a chart, are
ignored without error. The mapping sees them as written
(`values.upstream.<key>`).

```yaml
# an instance's values
upstream:
  app:
    env:
      - {name: LOG_LEVEL, value: debug}   # appended: append: [env]
    resources:
      limits: {memory: 1Gi}               # merged over OKDP's defaults
```

They are checked, then merged over the computed values ([Merge
precedence](#merge-precedence)):

- templates: no key or string may contain `{{`, the first one found by
  path reported, nor a value of a type values do not hold. The values reach
  the chart as data, Helm does not render them. The values an instance
  layer gives are refused before, with the layer ([Layers](#layers)); this
  check covers those of the package's defaults and of the platform's
  layers;
- [protect](#protect): `global` and the protected paths are refused;
- [append](#append): the listed lists are appended to.

### protect

`charts.<key>.protect`: dotted paths the platform sets; `global` is always
protected. The upstream values fail when they set a protected path (to any value,
`null` included), or one of its parents to anything but a map (`null`
included). The keys beside a protected path are free:

| With `protect: [ingress.hosts]` | |
|---|---|
| `ingress: {hosts: [...]}` | refused |
| `ingress: null`, `ingress: off` | refused |
| `ingress: {className: other}` | accepted |

The error names the path set and the protected path: the first one hit,
`global` first, then in `protect` order
(`testdata/protected-upstream.yaml`):

```
app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed
```

A path listed in both `protect` and `append` is accepted when the
definition loads; `protect` is checked first, so upstream values setting
it fail.

### append

`charts.<key>.append`: dotted paths of lists. A list the upstream values
give at such a path is appended to the list the values have there
(upstream defaults, OKDP's defaults, computed values) instead of replacing
it. The value given must be a list; a `null` is refused too:
`upstream.app.env must be a list`.

With the example (`append: [env]`), the computed `OIDC_*` and `DB_*` items
come first, then `TZ` and `LOG_LEVEL`, of `upstream.app` in
`examples/02_advanced/input/config.yaml`
(`examples/02_advanced/output-no-dcr/helm-cmd/app.values.yaml`).

### Upstream errors

`<at>`: `upstream.<key>.<path>`.

| Case | Error |
|---|---|
| a template (in values an instance layer gives, the layer fails before: [Layers](#layers)) | `<key>: <at>: the value contains "{{"`, `<key>: <at>: the key contains "{{"`; `<key>: <at>: cannot be checked for "{{": <type> is not a values type` |
| a protected path | `<key>: <at>: <protected> is set by the platform and cannot be changed` |
| an appended path, not a list | `<key>: <at> must be a list` |

## Merge precedence

The final values of a compiled upstream chart (`Result.Values[<key>]`),
each layer over the previous ones:

| | Layer | From |
|---|---|---|
| 1 | upstream defaults | `values.yaml` of the key's source, from the [chart cache](#chart-cache), its `null` values removed |
| 2 | OKDP's defaults | the file `charts.<key>.defaults` |
| 3 | computed values | `output.<key>` of the mapping |
| 4 | upstream values | `upstream.<key>` of the parameters ([Upstream values](#upstream-values)) |

The source: the upstream chart of the key ([Chart index](#chart-index)).
Keys of one source take layer 1 from it, the other layers from their own
entries. The chart of an OAuth client and the okdp chart have layers 1 (the
defaults of their [platform chart](#platform-charts)) and 3 (the values
the compiler computes for them: [OAuth client charts](#oauth-client-charts),
[the okdp chart](#the-okdp-chart)) only.

Then `global` is replaced by: the `global` of layer 1, under the `global` of
the parameters (the platform's, [Platform values](#platform-values)), under
the `global` of layer 3. The `global` of OKDP's defaults is not kept; layer
4 may not set it (protected). Every chart thus gets the platform
values.

Layers 2 to 4 are merged as Helm merges `-f` files: maps merge key by key,
lists and scalars replace (except at [append](#append) paths), a `null`
replaces too and stays `null`. The result is a complete values file, the
upstream defaults included.

### Nulls

A `null` of layers 2 to 4 stays `null` in `Result.Values`; Helm gives it its
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
helm template <release> <source> -f <defaults> -f <computed> -f <upstream>
```

renders, `global` and the [appended](#append) lists aside. `<source>`:
`Result.Charts[key]`, `<chart> --repo <repository> --version <version>`,
`oci://<repository>/<chart> --version <version>`, or its archive in the
chart cache.
`testdata/helm/values` holds what Helm computes on each case
(`TestHelmValues`), with Helm 4.3.0 and the versions the deployers embed
([GitOps](../01_usage.md#gitops)): Helm 4.2.4, 4.2.1 and 4.2.0 render as
Helm 4.3.0. Helm 3.19.4 and 3.19.2 coalesce some nulls differently (a
`null` default the values leave unset is kept, a key below the top level
that `values.yaml` and the values both set to `null` is deleted:
`helm3.json`), and render the final values as they render the layers, but
for one case: Helm 3 reads the first document of a values file only. With
it, a chart whose `values.yaml` has several documents gets back the keys
of the first document that a later one deletes (`documents/helm3-final.json`):
such a chart needs Helm 4 ([Umbrella chart](#umbrella-chart)).

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
with the chart key (`app: charts.example.org/app-1.8.0/values.schema.json: ...`).

## Outputs

`compiler.Compile` returns a `*compiler.Result`:

| Field | Type | Content |
|---|---|---|
| `Values` | `map[string]map[string]any` | chart key -> its final values ([Merge precedence](#merge-precedence)) |
| `Charts` | `map[string]compiler.ChartRef` | chart key -> the upstream chart that renders its values ([Chart index](#chart-index)) |
| `Objects` | `[]map[string]any` | OKDP's own objects, whole manifests: the [descriptor ConfigMap](#descriptor-configmap), the [credentials Secrets](#credentials-secrets), then the [objects section](#objects-section); no two of them one object of the cluster |
| `Dependencies` | `[]compiler.Dependency` | the instances the parameters reference ([Dependencies](#dependencies)) |

`Values` and `Charts` have one entry per compiled chart: a chart the
mapping leaves out (`$if`) is in neither, and is not to be rendered; the
okdp chart is in both, and in `dcr` mode the chart of each OAuth client. Numbers are float64, as in Helm values.
The files the binary writes from them:
[command line](../01_usage.md#command-line), and the
[umbrella chart](#umbrella-chart); `examples/02_advanced/` holds those of
the example ([Example](#example)).

The keys of a compile are the chart keys of `service.yaml`, DNS labels
([chart keys](02_service_definition.md#chart-keys)), and the compiler's
(`okdp`, the OAuth client charts). `Files` and `Umbrella` name files and
dependency aliases after them: for a `Result` built otherwise, a key Helm
does not accept as an alias (letters, digits, `-`, `_`) fails,
`chart "<key>": a key of the result names a file (<key>.values.yaml) and an umbrella dependency alias: letters, digits, - and _`.

Each `<key>.values.yaml` of `Files` names, on the line after the one that
marks it generated, the upstream chart that renders it ([Chart
index](#chart-index)), as Helm takes it: the words between `helm template
<release>` and `-f <key>.values.yaml`, shell-quoted unless plain.

```yaml
# Code generated by okdp-compile. DO NOT EDIT.
# Chart: app --repo https://charts.example.org --version 1.8.0
```

A key without its entry in `Result.Charts` fails, `chart "<key>": not in
Result.Charts, the upstream chart that renders its values`, as for
`install.sh`; so does an entry with a control character, which would end
the comment.

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
| the chart of an OAuth client | `global.okdp.charts.oidcDcr`, `compiler.DcrSource()` by default ([Platform charts](#platform-charts)) |
| the okdp chart | `global.okdp.charts.objects` |

The final values of a key are rendered by its upstream chart, from its
repository, once per key: two keys of one source are two renders of it
([equivalence with Helm](#equivalence-with-helm)).

The example's in `dcr` mode (`Result.Charts`):

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
([sources](02_service_definition.md#sources)). The binary installs each
chart from it with `<output>/install.sh`
([`--mkinstall`](../01_usage.md#--mkinstall)), and lists them in the
umbrella's `Chart.yaml` ([umbrella chart](#umbrella-chart)); rendering
with it: [rendering with Helm](../01_usage.md#rendering-with-helm).

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
`security.serviceAccount` and `security.role`; a client name has at most
54 characters, its chart key a DNS-1123 label, and two clients whose
objects' names are the same once cut are refused, in every mode. In `dcr`
mode a client's `secret` replaces the Secret name, which
[`oidcClientSecret`](02_service_definition.md#oidcclientsecret) does not
follow, and two clients may not write one Secret
([compile errors](02_service_definition.md#compile-errors)).

A client chart has no `service.yaml` entry, and its key is reserved
([oidcClients](02_service_definition.md#oidcclients)): it takes no OKDP defaults
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
in `dcr` mode (`examples/02_advanced/output-dcr/helm-cmd/`), with the
platform's settings above (`examples/02_advanced/input/platform-dcr.yaml`,
after `platform.yaml`) and oidc-dcr 0.5.0 in the chart cache, trimmed:

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
| `metadata.name` | `<release>-okdp`, as `okdpFullname('okdp')` (never cut: the release has at most 53 characters) |
| `metadata.namespace` | `Inputs.Namespace` |
| `metadata.labels` | the labels, plus `okdp.io/provides-<contract>: "true"` per contract provided |
| `data.service` | `name` of `service.yaml` |
| `data.version` | `version` of `service.yaml` |
| `data.url` | `instance.url`, trimmed |
| `data.usage` | `instance.usage`, trimmed, with a final newline |
| `data.outputs.yaml` | the provided connections, a YAML list; `[]` when none |

The labels: the `labels` of the [context](02_service_definition.md#context),
from `name` of `service.yaml` and the release.

`outputs.yaml`: one item per `provides` item, keys sorted (Helm's `toYaml`):

| Key | Value |
|---|---|
| `contract` | `contract` of the item |
| `name` | `name` of the item; `<release>` by default |
| `secretRef` | `{name}` of `secretRef`, its `name` a non-empty string (`secretRef: {}` is not set); else `{name: <name>-<contract>-credentials}` when the item has a `secret`; else absent |
| `values` | `values` of the item, [completed](#completion) against the contract |

Every item is checked for its shape, then against its contract, in the
order of `provides`, the fields of its `values` and of the contract's
`x-okdp-internal` convention in alphabetical order. The first error is
reported, as for an expression of the mapping: `provides` is the package's
to fix, not the instance's, and the error is the same from one compile to
the next; the required fields that item lacks, and the schema's violations
of that item, all at once, one line each ([Checks](#checks)). `<out>`:
`provides "<name>" (<contract>)`; `<i>`: the item's position.

| Case | Error |
|---|---|
| an item that is not a map | `provides[<i>]: must be a map (contract, name, values, secretRef, secret), got <value>` |
| `values`, `secretRef` or `secret` set, not a map | `provides[<i>]: values: must be a map of contract fields, got <value>` ([provides](02_service_definition.md#provides)) |
| the `generate` of `secret` not a list, a key of it of another type; its `stringData` not a map, a value not a string (with `generate`, nor a number or a boolean) | `provides[<i>]: secret: generate must be a list of generated keys, got <value>`, `provides[<i>]: secret: generate[<j>]: length must be a whole number >= 0, got <value>`, `provides[<i>]: secret: stringData "<key>" must be a string, got <value>` ([secret](02_service_definition.md#secret)) |
| no `<contract>.schema.json` | `<out>: unknown contract` |
| `<contract>.schema.json` that cannot be read, is not JSON, not an object or not a valid JSON Schema | `<out>: the <contract> contract cannot be read: <contract>.schema.json: <cause>` |
| not a contract field | `<out>: field "<key>" is not part of the <contract> contract` |
| a secret field | `<out>: "<key>" is a secret field, put it in the credentials Secret` |
| required field not set | `<out>: field "<key>" is required by the <contract> contract`, one line per field |
| a value the contract's schema refuses | `<out>: field "<field>" violates the <contract> contract: <violation>`, one line each; `<out>: the fields violate the <contract> contract: <violation>` for a keyword on the whole `values` |
| internal convention | `<out>: <field> is "<value>" but internal references to "<name>" resolve it to "<expected>"` |
| a `secretRef` without a `name`, or whose `name` is not a non-empty string | `<out>: secretRef.name must be the name of a Secret, got <value>` |
| same contract and name twice | `two <contract> outputs are named "<name>"` |

Internal convention: an output named `<release>` (the default), of a contract
with `x-okdp-internal`, must hold, for each field of the convention, the
value an internal reference to `<release>` resolves to. An instance that
references this one by name gets the values it publishes.

### Credentials Secrets

The Secret of a `provides` item with a `secret`: named after the item's
`secretRef` (`<name>-<contract>-credentials` by default), labelled
`okdp.io/credentials-<contract>: "true"`. They follow the descriptor in
`Objects`, in `provides` order; two items naming one Secret fail the
compile ([objects section](#objects-section)).

| `secret` | Objects |
|---|---|
| `{stringData: {...}}` | a `v1` `Secret`, `type: Opaque`, `stringData` as given (strings); labels: the labels and the credentials label |
| `{generate: [<keys>], stringData: {...}}` | per key, a `generators.external-secrets.io/v1alpha1` `Password` named `<secret>-<key>`; then the `external-secrets.io/v1` `ExternalSecret` `<secret>` writing the Secret: the generated keys and the static `stringData` ones (`stringData` optional; strings, numbers or booleans, written as text) |
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

No two items of `Objects`, the descriptor and the credentials Secrets
included, may be one object of the cluster, which the second would replace
when they are applied: the same API group (of `apiVersion`, whatever its
version), `kind`, namespace (`Inputs.Namespace` for an object without one)
and name. The first such pair fails the compile:

```text
<object>: <other> is the same object, <kind> "<name>" in namespace "<namespace>": give each object its own name
```

`<object>`, `<other>`: `the descriptor`, `the credentials Secret of
provides "<name>" (<contract>)` (any object of it: a `Password` generator,
the `ExternalSecret`, the `Secret`), or `objects[<i>]`, the position in the
evaluated list; `<kind>` is followed by `.<group>` for a kind of an API
group (`ExternalSecret.external-secrets.io`).

### The okdp chart

`compiler.ObjectsChart` (`"okdp"`): the chart of OKDP's own objects, which
the compiler adds to every compile (unless `compiler.WithoutObjectsChart`), rendered by the platform's objects
chart (`global.okdp.charts.objects`: [Platform charts](#platform-charts)),
an okdp-objects chart (values `objects: []`; its template prints each item
of `objects` as one YAML document):

- its computed values are `objects`, a copy of `Objects`; its final values,
  those over the chart's defaults, and `global`
  ([merge precedence](#merge-precedence));
- it is compiled whatever the mapping outputs, also when it outputs no
  chart;
- the compiler owns it: no OKDP defaults, no instance upstream values;
  `service.yaml` may not list it (refused when it is loaded), nor the
  mapping's `output`:

```
service.yaml: charts.okdp: the chart of OKDP's own objects is added by the compiler: remove it from service.yaml
mapping output "okdp": the chart of OKDP's own objects is compiled from the objects section: remove it from output
```

The descriptor, the credentials Secrets and the objects section then reach
the cluster with the upstream charts. `Objects` holds them too, for a
caller that deploys them otherwise: `compiler.CompileContext` with
`compiler.WithoutObjectsChart()` then leaves the okdp chart out of `Charts`
and `Values`.

### Dependencies

`Dependencies`: one item per internal [connection reference](#resolution),
sorted by `field` (string order), its `instance` a release name. Two
references to one instance give two items; external and empty references
give none. `nil` when there is none.

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

`compiler.Umbrella(def, res, indent)` (`okdp-compile -o <dir> --umbrella`): the
instance as one chart, for one Helm release per instance, under the release
name the values were compiled for. Two files:

- `Chart.yaml`: `apiVersion: v2`; `name` and `version` of `service.yaml`
  (`version` must be SemVer); `dependencies`, one per key of `Values`,
  sorted by key: the [chart index](#chart-index) entry of the key (`name`,
  `repository`, `version`), and `alias: <key>` when the key is not the
  chart's name;
- `instance.values.yaml` (`compiler.UmbrellaValues`): `Values`, each key's
  final values under the key, each with its own `global`; none at the top.
  Its first line, as that of each `<key>.values.yaml` of `Files` and the
  second of `install.sh`, marks it generated: `# Code generated by
  okdp-compile. DO NOT EDIT.` (the line Go, GitHub, linters and editors
  recognize).

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
to merge over it. `testdata/helm/umbrella` checks it, the umbrella over
the charts of the values fixtures with one chart under two keys, with Helm
4.3.0 and the versions the deployers embed
([GitOps](../01_usage.md#gitops)): Helm 4.3.0, 4.2.4, 4.2.1 and 4.2.0
render one output (`helm.json`), Helm 3.19.4 and 3.19.2 another
(`helm3.json`). With each, a null of the final values deletes the upstream
default. Helm 3 differs from Helm 4 on these values:

| Values | Helm 4 | Helm 3.19 |
|---|---|---|
| a key of the first document of a `values.yaml` that a later document deletes ([Values files](#values-files)) | deleted | the first document's value: Helm 3 reads the first document only |
| a key below the top level, null in the chart's `values.yaml`, that the values set to null | kept, null | deleted |
| a null default nested in a map of the chart's `values.yaml`, the values leaving it unset | kept, null | dropped |

An upstream chart whose `values.yaml` has several documents needs Helm 4.
The other two are a null against an absent key, which a template reads the
same but for `hasKey`, `toYaml` and `range`.

### Example

`examples/02_advanced/output-no-dcr/helm-cmd/`, what `-o <dir>
--mkinstall` writes for the example: release `demo-webapp`, namespace `demo`, the instance
`examples/02_advanced/input/config.yaml` (`replicas: 2`,
`database: demo-db`, `audit: platform-db`, a memory limit and two `env`
entries in `upstream.app`), over
`examples/02_advanced/input/platform.yaml` (`existing` mode: no OAuth client
chart, [OAuth client charts](#oauth-client-charts); `platform-db`, the
connection the instance's `audit` references).
`examples/02_advanced/output-dcr/helm-cmd/` is the same compile in `dcr`
mode, with `examples/02_advanced/input/platform-dcr.yaml` after
`platform.yaml`: the OAuth client's chart, `oidc-dcr`
([its values](#oauth-client-charts)), installed first;
`examples/02_advanced/output-dcr/umbrella-chart/` is it as one chart,
`oidc-dcr` a dependency beside `app` and `okdp`, its Job a pre-install hook
that runs before the release's other resources.

`app.values.yaml`, annotated:

```yaml
# Code generated by okdp-compile. DO NOT EDIT.
# Chart: app --repo https://charts.example.org --version 1.8.0
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
  - name: AUDIT_DB_URL                    # the platform's connection, resolved
    value: jdbc:postgresql://db.okdp.example.org:5432/shared
  - name: AUDIT_DB_PASSWORD
    valueFrom:
      secretKeyRef:
        key: password
        name: platform-db-credentials
  - name: TZ                              # upstream.app, appended
    value: Europe/Paris
  - name: LOG_LEVEL                       # upstream.app, appended
    value: debug
fullnameOverride: demo-webapp-app         # computed
global:                                   # the parameters' global
  okdp:
    # ... the platform values
image:
  repository: example/webapp              # upstream default
  tag: "1.8"                              # OKDP's defaults
ingress:
  # ... computed
replicaCount: 2                           # computed: values.replicas
resources:                                # OKDP's defaults
  limits:
    memory: 512Mi                         # upstream.app
  requests:
    cpu: 100m
    memory: 128Mi
```

`okdp.values.yaml`: the values of the [okdp chart](#the-okdp-chart), the
descriptor ConfigMap under `objects`:

```yaml
# Code generated by okdp-compile. DO NOT EDIT.
# Chart: oci://registry.example.org/okdp/okdp-objects --version 1.0.0
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

`install.sh`:

```sh
#!/bin/sh
# Code generated by okdp-compile. DO NOT EDIT.
# Installs demo-webapp in the namespace demo, one Helm release per chart: its
# upstream chart with its values file. Recompile the instance to change it.
set -eu
cd "$(dirname "$0")"
helm upgrade --install demo-webapp-app app --repo https://charts.example.org --version 1.8.0 --namespace demo -f app.values.yaml
helm upgrade --install demo-webapp-okdp oci://registry.example.org/okdp/okdp-objects --version 1.0.0 --namespace demo -f okdp.values.yaml
```

With `--mkdag`, `okdp.dependencies.yaml` ([dependencies](#dependencies)):

```yaml
- instance: demo-db
  contract: database
  field: database
```

`examples/02_advanced/output-no-dcr/umbrella-chart/`, what `-o <dir>
--umbrella` writes: `instance.values.yaml`, the same values under their
keys (`app:`, `okdp:`), and `Chart.yaml`:

```yaml
apiVersion: v2
name: webapp
version: 1.0.0
dependencies:
  - name: app
    repository: https://charts.example.org
    version: 1.8.0
  - name: okdp-objects
    alias: okdp
    repository: oci://registry.example.org/okdp
    version: 1.0.0
```
