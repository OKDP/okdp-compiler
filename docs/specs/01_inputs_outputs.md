# Inputs and outputs

Normative reference of what a compile reads besides the service definition
(`Chart.yaml`, `okdp.yaml` and `values.cel.yaml` or `values.yaml`:
[02_service_definition.md](02_service_definition.md)),
and of what it produces. The flags that pass these inputs to the binary are in
[01_usage.md](../01_usage.md); how the compiler works inside, in
[00_architecture.md](../00_architecture.md).

| Input | Field | Required | Section |
|---|---|---|---|
| Package files | `Definition.Files` (`fs.FS`) | yes | [Package files](#package-files) |
| Chart cache | `Inputs.Charts` (`fs.FS`) | no: the checks of the final values | [Chart cache](#chart-cache) |
| Contracts | `Inputs.Contracts` (`fs.FS`) | yes | [Contracts](#contracts) |
| Context values layers | `Inputs.Context` (`[]ContextLayer`) | no | [Parameters](#parameters), [Context values](#context-values), [Values files](#values-files) |
| Instance values layers | `Inputs.Values` (`[]map[string]any`) | no | [Parameters](#parameters), [Instance values](#instance-values), [Values files](#values-files) |
| Release name | `Inputs.Release` | yes | `release.name`, object names; a tree's instance's, `<project>-<instance>` ([repository layout](../01_usage.md#repository-layout)) |
| Namespace | `Inputs.Namespace` | yes | `release.namespace`, object namespaces, `${namespace}` of contracts |
| Instance name | `Inputs.Name` | no: the release; `CompileAll` sets it (`Instance.Name`) | the name of the outputs that name none ([provides](02_service_definition.md#provides)), of the instance's claims' IDs ([claims](#claims)); a tree's instance's, its directory's |
| What the other instances of the namespace publish | `Inputs.Published` (`map[string]Info`, by release) | no: connections and claims only; `CompileAll` sets it | [Resolution](#resolution), [Compiling instances together](#compiling-instances-together) |
| The claims bound to the instance, a provider | `Inputs.Claims` (`[]Claim`) | no; `CompileAll` sets them | [Claims](#claims) |
| The namespaces that may claim of the instance, a provider, from another namespace | `Inputs.Claimers` (`[]string`) | no: its package's (`provisions`), none by default; a tree's instance's, its `instance.yaml`'s `claimers` | [Providers](#providers) |
| The providers the instance's claims are bound to | `Inputs.Bindings` (`map[string]Binding`, by parameter path) | no: a claim then fails; `CompileAll` sets them | [Claims](#claims) |
| Credentials in clear accepted | `Inputs.AllowPlainSecrets` (`bool`) | no: a plain Secret, of a `provides` `secret` or of the objects, fails | [Credentials Secrets](#credentials-secrets), [Objects section](#objects-section) |

A nil `*Definition`, context or file system panics: the caller gives them.
The names are checked before anything is read: the release is a release
name Helm accepts, from which OKDP derives object names (`okdpFullname`,
Secrets), and the namespace a namespace name, both DNS-1123 labels
(lowercase letters, digits and `-`, a letter or a digit at each end); an
empty one fails as another:

| Case | Error |
|---|---|
| a release that is not a DNS-1123 label, or longer than 53 characters (Helm's limit) | `Inputs.Release "<name>": a release name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 53 characters at most (Helm's limit)` |
| a namespace that is not a DNS-1123 label, or longer than 63 characters | `Inputs.Namespace "<name>": a namespace name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 63 characters at most` |
| the namespace `platform`, reserved: the platform's keys of the secret store are under `platform/` ([what the store must enforce](#what-the-store-must-enforce); `testdata/cli/secrets.txtar`) | `Inputs.Namespace "platform": the namespace is reserved: platform/ holds the platform's keys of the secret store, which no project writes` |
| an instance name that is set and not a DNS-1123 label | `Inputs.Name "<name>": an instance name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 63 characters at most` |

Paths in an `fs.FS` are relative to its root.

## Package files

A package is a directory: the service definition and the parameters of the
service. `compiler.LoadDefinition(fsys)` reads `Chart.yaml`, `okdp.yaml`
and the values file, `values.cel.yaml` or `values.yaml`, at the root of
`fsys` ([service definition](02_service_definition.md)) and keeps `fsys` as
`Definition.Files`, from which each compile reads the other files; a
`Definition` built in memory sets `Files` itself. The compiler reads these
files, and no other:

```
<package>/
  Chart.yaml                          the service's name and version, its upstream charts (dependencies)
  okdp.yaml                           platform values required, OKDP's defaults, upstream values; the mapping's sections
  values.cel.yaml                     the values file: the computed values of each dependency
  values.yaml                         or the values file under this name, without expressions: one of them
  config.schema.json                  schema of the parameters, their defaults, connection references
  <key>.defaults.yaml                 OKDP's defaults for the chart <key> (defaults.<key> of okdp.yaml)
```

| File | Required | Read as | Used for |
|---|---|---|---|
| `Chart.yaml` | yes | YAML 1.2, Helm's chart metadata | the service: name, version, its upstream charts ([Chart.yaml](02_service_definition.md#chartyaml)) |
| `okdp.yaml` | no | YAML 1.2; its sections, the mapping language | requirements, catalog metadata, OKDP's defaults, upstream values accepted; `vars`, `errors`, objects and outputs ([okdp.yaml](02_service_definition.md#okdpyaml)) |
| `values.cel.yaml` | no | the mapping language | the computed values ([values.cel.yaml](02_service_definition.md#valuescelyaml)) |
| `values.yaml` | no, and not with `values.cel.yaml` | the mapping language; holding expressions or `$`-keys, a warning | the same, under the name of an umbrella chart's values ([values.yaml](02_service_definition.md#valuesyaml)) |
| `config.schema.json` | no: `{}` when absent | JSON | [parameter defaults](#defaults), [parameter check](#schema-check), [connection references](#declaring-a-reference) |
| defaults file | when `defaults.<key>` of `okdp.yaml` names it | values file | second layer of the [final values](#merge-precedence) |

`<key>`: a chart key, the key of a `Chart.yaml` dependency (its `alias`,
else its `name`) or the chart of an
OAuth client ([OAuth client charts](#oauth-client-charts)); it names the
chart's values. A
compiled chart: a dependency the values file does not leave out (every
one, but a key it writes that is left out by `$if` or null:
[rendered dependencies](02_service_definition.md#rendered-dependencies)),
and in `dcr` mode the chart of each OAuth client. Each is
rendered by an upstream chart, its source
([chart index](#chart-index)); several keys may share one. The defaults file
may be any path of the package, `<key>.defaults.yaml` by convention; a
path out of it (`../app.yaml`) fails. The service's name and version are
those of `Chart.yaml` ([Chart.yaml](02_service_definition.md#chartyaml)),
its catalog metadata in `okdp.yaml`.

`config.schema.json` is optional: a package without it has the empty
schema, `{}`. No parameter then has a [default](#defaults) or is a
[connection reference](#declaring-a-reference); the [schema
check](#schema-check) accepts any values: any parameter,
`upstream.<key>` of any key (applied to the charts that accept upstream
values: [Upstream values](#upstream-values)); and no key of `global` is
declared, so an instance sets none ([Context values](#context-values)).
Each compile reads the schema once, first, before the values layers: one
that is not valid, or that cannot be read for another reason than its
absence (a directory, a file it may not read), fails before an error of
the layers. A missing defaults file fails with the error of the file
system, prefixed with the chart key:

```
app: open app.defaults.yaml: no such file or directory
```

### Values files

Every values file is read as Helm 3 reads one: the upstream charts'
`values.yaml` a check reads ([chart cache](#chart-cache)), the defaults
files, and the values layers read by `compiler.ParseValues` (a tree's
context files and `config.yaml`).

- Documents: the first YAML document of the file only. Helm 4 merges the
  later ones; the compiler ignores them: of an upstream chart whose
  `values.yaml` has several documents, it reads, checks and writes the
  defaults of the first document, and a key of the first document that a
  later one deletes is back in the final values, under Helm 4 too.
- Decoding: by `sigs.k8s.io/yaml`, YAML 1.1. A plain `y`,
  `yes`, `on`, `n`, `no`, `off` (and their capitalized forms) is a boolean,
  as a key too (`on:` is the key `"true"`); keys are strings; every number is
  a float64.
- Shape: the document must be a map (`cannot unmarshal yaml document: ...`); a
  null key fails (`unsupported map key`).

`testdata/helm/values/yaml11` holds what Helm reads from such a file.
Values built in memory and given in `Inputs.Context`
(`ContextLayer.Values`) or `Inputs.Values` are read as the values they
would be once written to a file and read back, at any depth, whatever
their Go types (`value.Normalize`): written by `encoding/json/v2`, map
keys sorted, and read back. A map of any type is a map, its keys strings
(`map[int]string{1: x}` has the key `"1"`); a slice or an array is a list;
every number is a float64 (an integer past 2^53 rounds, as Helm reads it);
a nil map or slice is empty, not `null`; a struct, a `[]byte` or a
`time.Time` is what `encoding/json/v2` writes.

What a values file cannot hold fails the compile, before the parameters
are computed, with the layer (`context layer <n> (level <l>)`: the n-th of
`Inputs.Context`, at level `<l>`; `values layer <n>`: the n-th of
`Inputs.Values`) and the error of `encoding/json/v2`, the value's path a
JSON pointer: a func, a chan, a complex, a NaN or an infinity, a string or
a key that is not UTF-8, a key that is not text or a number (a boolean or
`null` key of a `map[any]any`), two keys that read as the same (`1` and
`"1"`), a value that contains itself:

```
values layer 2: json: cannot marshal from Go func() within "/hooks/0"
context layer 1 (level 0): json: cannot marshal from Go float64 within "/global/proxy/no.proxy": unsupported value: NaN
values layer 1: jsontext: duplicate object member name "1" within "/labels"
```

JSON files (`config.schema.json` of the package, `values.schema.json` of
the upstream charts, the contracts) are strict JSON: no comments.

## Chart cache

`Inputs.Charts`, optional: the upstream charts the compiled charts are
rendered by, for the [checks of the final values](#checks-of-the-final-values).
Without it, a compile reads no chart; with it, its final values are the
same, checked. Each chart is unpacked under its repository's directory, as
`helm pull --untar -d <cache>/<repository>/<chart>-<version>` writes it
(`helm pull` creates the directory). The binary reads a directory with
`--check` only: `--charts`, `$OKDP_CHARTS` by default
([`--check`](../01_usage.md#--check)).

```
<cache>/
  <repository>/<chart>-<version>/<chart>/  the chart unpacked (helm pull --untar -d <cache>/<repository>/<chart>-<version>)
```

`<repository>` is the source's `repository` without its scheme (`oci://`,
`https://`) and a trailing `/`: its host, port and path.
`oci://quay.io/adaltas` is `quay.io/adaltas/`, its oidc-dcr 0.5.0
`quay.io/adaltas/oidc-dcr-0.5.0/oidc-dcr/`; `https://localhost:8080/charts`
is `localhost:8080/charts/`. A repository whose host and path are no
directory of the cache (empty; an empty, `.` or `..` segment; a `\`) is
refused, with or without a chart cache, and so is an `http://` one, the
chart fetched in clear
([dependencies](02_service_definition.md#dependencies),
[platform charts](#platform-charts)). Two repositories that differ only by
their scheme share a directory.

A chart is found by its source: `<repository>/<chart>-<version>/<chart>`,
and nowhere else: a chart archive (`helm pull -d` without `--untar`), a
chart at the root of the cache or unpacked directly in
`<chart>-<version>/` is not read, and one chart name and version from two
repositories (a fork, a generic name such as `app`) are two entries. Of a
chart, a check reads `values.yaml` and `values.schema.json` only, at
the root of its directory, not those of its subcharts
(`<chart>/charts/<sub>/values.yaml`). A chart without `values.yaml` has no
defaults, one without `values.schema.json` no schema check
([checks of the final values](#checks-of-the-final-values)).

The files are read without a cap. Helm 4.2 and 3.19, which the
deployers embed
([GitOps](../01_usage.md#gitops)), refuse a file over 5 MiB; Helm 4.3
no longer does, nor does the compiler: such a chart compiles, and fails to
render with them. A UTF-8 byte order mark at the start of either file is
dropped, as Helm drops it. Each chart is read once
per compile, whatever the number of keys it renders, and only when a chart
it renders is compiled: the oidc-dcr chart in `dcr` mode with a client.
`examples/03_full/.charts/` holds the
full example's charts unpacked, their `values.yaml` and the workspace
chart's `values.schema.json` only: enough to check, not to render
(`charts.example.org/workspace-3.2.0/workspace/`,
`registry.example.org/mirror/oidc-dcr-0.5.0/oidc-dcr/`...).

A chart its directory does not hold, even when the cache holds its archive
or the chart elsewhere, fails with the command that downloads it there,
prefixed with the chart key; a file that does not read, with its path:

```
app: chart app-1.8.0 is not in the chart cache: helm pull app --repo https://charts.example.org --version 1.8.0 --untar -d <chart cache>/charts.example.org/app-1.8.0
oidc-dcr: chart oidc-dcr-0.5.0 is not in the chart cache: helm pull oci://quay.io/adaltas/oidc-dcr --version 0.5.0 --untar -d <chart cache>/quay.io/adaltas/oidc-dcr-0.5.0
app: charts.example.org/app-1.8.0/app/values.yaml: cannot unmarshal yaml document: ...
```

## Parameters

The parameters of an instance: the values layers, the context's
(`Inputs.Context`, by level: [Levels](#levels)), then the instance's
(`Inputs.Values`), merged and coalesced over the defaults of the package's
`config.schema.json`, as Helm computes `.Values` of a chart from its
`values.yaml` and `-f` files. `global` and `connections` are the context's
([Context values](#context-values)).

### Defaults

The defaults are the `default` keywords of `config.schema.json`, none
without it: that of each property reached from the root through
`properties` alone, at its path, read as JSON (every number a float64).
They play the part of a chart's `values.yaml`:

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
is one of a context value: the context's layers, of any level, override
it, and the key it declares an instance may set ([Context
values](#context-values)). The `default` of an `x-*` keyword, of `enum`,
`const` or `examples` is not read, nor `x-ui-condition`: unlike a
[contract's](#completion), a parameter's default holds whatever the other
values.

A `default` anywhere else is not read:

- at the root, or under any other keyword (`allOf`, `anyOf`, `oneOf`, `if`,
  `then`, `else`, `not`, `items`, `prefixItems`, `additionalProperties`,
  `patternProperties`, `$defs`, `definitions`, ...): whether it applies
  would depend on the values;
- under a property that has a `default`: the outermost gives the whole
  value.

A default is checked with the values it ends up in: one its property's
schema rejects fails the [schema check](#schema-check) of an instance that
does not override it.

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
connections, the [platform requirements](#requirements) and the mapping,
as Helm 4 checks the values of a chart:

- the values checked: the parameters above, `global` and `upstream`
  included, `connections` left out ([Reserved keys](#reserved-keys)), every
  connection reference still the string written;
- the draft: the schema's `$schema`, draft 2020-12 without one; `format` is
  asserted for drafts before 2019-09 only;
- `$ref`: within the schema only. A file, a URL or a `urn:` (which Helm
  accepts as anything) fails the compile: `config.schema.json: failing
  loading "<url>": no URLLoader registered for "<url>"`.

Every violation is reported with its path, sorted by path (and the keys of
`additional properties ... not allowed` sorted: Helm's order varies), under
the name of the service, `name` of `Chart.yaml`
(`testdata/schema-violation.yaml`):

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
| `global` | the context values, `global.okdp` among them: set by the context's layers, each level adding to the lower ones', by an instance's only for the keys the schema declares ([Context values](#context-values), [Levels](#levels)) | removed: given as `global` and `platform` |
| `connections` | the external connections, by name: given by the context's layers only, an instance's own at a level of its own ([Connections](#connections), [Levels](#levels)) | removed: the references are resolved in place |
| `upstream` | the instance's upstream values, by chart key ([Upstream values](#upstream-values)) | as written |

`global` and `upstream` are checked with the parameters: a schema with
`additionalProperties: false` declares them, `{"type": "object"}` at least
(`examples/03_full/input/db/catalog/workspace/3.2.0-1.4.0/config.schema.json`).
`connections` is not: a schema does not declare it, and a declaration of
`connections` has no effect, its `default` included. A connection is
checked against its contract when a reference resolves to it
([Checks](#checks)).

## Connections

A connection reference: a parameter that names a connection: an entry of
`connections` (external), a named claim of the namespace, an entry of
`claims`, or an output another instance of the namespace publishes
(output); or that claims one of a provider, a claim template
([claims](#claims)).
The
compiler replaces it with the fields of the connection, completed against
its [contract](#contracts), before the mapping runs.

`connections` is given by the context's layers only
([Context values](#context-values)): the platform's, a project's, and the
instance's own, a layer of a level of its own, above its project's
([Levels](#levels)). A level adds connections, it changes none of a lower
level's: an instance does not redefine the platform's connections.

```yaml
# examples/03_full/input/db/projects/demo/instances/workspace/connections.yaml,
# level 2: the instance's own
connections:
  scratch:
    contract: s3
    provider: ceph
    endpoint: https://rgw.demo.example.org
    bucket: workspace-scratch
    secretRef: {name: scratch-credentials}
```

### Declaring a reference

In `config.schema.json`, on a string property:

```json
"database": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}
```

`x-okdp-connection-ref`: an object; `contract` names the contract, the file
`<contract>.schema.json` of the contracts; `provision: true` lets the
reference take a [claim](#claims), an object, in place of a name: the
property then accepts both (`"type": ["string", "object"]`); `request`,
an object, what the package fixes of the request of every claim
template of the reference, the instance's merged over it
([claims](#claims)); `require`,
a JSON Schema, what the package requires of the connection beyond its
contract ([what a reference requires](#what-a-reference-requires)). Its
other keys are ignored; a value that is not an object (`true`) declares
no reference.

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
it through when the property does not say `"type": "string"`) fails, but a
claim's map of a reference that takes them: it would reach the mapping
unresolved, past the contract
([Connection errors](#connection-errors)). The packages of platform-charts
declare references in lists of objects, e.g. the catalogs of trino:

```json
"hiveCatalogs": {"type": "array", "items": {"type": "object", "properties": {
  "metastore": {"type": "string", "x-okdp-connection-ref": {"contract": "hive"}},
  "storage":   {"type": "string", "x-okdp-connection-ref": {"contract": "s3"}}}}}
```

### Resolution

A reference of a contract `<contract>`, a claim template (a map), resolves
to the output of `<contract>` its binding gives (`Inputs.Bindings`, by
parameter path): what the provider bound to it publishes for it
([claims](#claims)), as an output, below. A reference `<name>` resolves to:

1. external: `connections.<name>`, when it is a map;
2. otherwise, a named claim: `claims.<name>` of the context, its contract
   `<contract>`, when the reference takes claims (`provision`); it resolves
   through its binding, as a claim template does;
3. otherwise, output: the output named `<name>` of `<contract>` that
   another instance of the namespace publishes (`Inputs.Published`), an
   instance's own named after the instance by default (`Inputs.Name`)
   ([provides](02_service_definition.md#provides)).

An output and a claim are [dependencies](#dependencies). Otherwise the
reference fails ([Connection errors](#connection-errors)). A connection is
never computed from a name: what an instance is reached at, its provider
publishes.

External: the entry of `connections`, as the context's layers give it.

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
- `secretRef`: `{name}`, the Secret of the instance's namespace its
  credentials are in, kept as `{name}`, its other keys dropped; or
  `{remoteKey: <key>}`, its credentials in the platform's secret store,
  the connection's `secretRef` then `{name}` of the Secret the compiler
  reads them into ([credentials in the platform's secret
  store](#credentials-in-the-platforms-secret-store)). Without it, the
  connection has no `secretRef`.
- Any other key: a field of the contract, not an `x-okdp-secret` one.

Output: `Inputs.Published`, what the other instances of the namespace
publish, by release, each an `Info`, its [info
file](#the-info-file) (`okdp.info.yaml`; set by `CompileAll`). The
output whose `name` is `<name>` and `contract` `<contract>`: its `values`,
copied and read as values are (every number a `float64`, whatever decoded
the file), and its `secretRef`, kept as `{name}`. The compiling release is
never its own provider. Two instances that publish one fail; an output of
that name of another contract is no match. `Inputs.Published` is the
namespace's only: an instance of another namespace is reached by a
claim, or a connection. `nil`, the other instances are not known: a name
resolves to a connection only. `CompileAll` compiles each instance after
those whose outputs it references
([Compiling instances together](#compiling-instances-together)).

Each is then [completed](#completion) against the contract, then
checked against what the reference requires
([what a reference requires](#what-a-reference-requires)). The result,
`secretRef` and `okdp` included, may not contain `{{` in a key or a string,
whatever it comes from: the context's layers above level 0 refuse it
already ([Levels](#levels)), and a
connection of level 0, the platform's, a
contract's default or an output's values are checked here, as the
connection reaches the charts' values through the mapping.

### What the mapping receives

The parameter, replaced by a map: the contract fields, `secretRef` when
there is one, and `okdp`, where the connection comes from: `ref`, the
reference, a claim's ID; `contract`; `source`, `external`, `output` or
`claim`; `instance`, the instance it comes from, the release that
publishes the output or the claim's provider (the
[dependency's](#dependencies)), absent for an external connection; and
`namespace`, the provider's, for a claim bound to an instance of another
namespace. With
`examples/02_advanced/input/db/contracts/database.schema.json`:

```yaml
# database: {} (claim, what demo-db publishes for it)
database:
  host: demo-db-db.demo.svc          # demo-db's
  dbName: demo_webapp_database  # demo-db's
  engine: postgresql                 # default
  port: 5432                         # default
  driver: org.postgresql.Driver      # x-okdp-derived from engine
  secretRef: {name: demo.webapp.database-credentials}
  okdp: {ref: demo.webapp.database, contract: database, source: claim, instance: demo-db}

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

With `examples/03_full/input/db/contracts/s3.schema.json`, an output
another instance of the namespace, `demo-store`, publishes:

```yaml
# Inputs.Published["demo-store"], its okdp.info.yaml
provides:
  - name: store-a
    contract: s3
    values: {provider: minio, endpoint: https://a.example.org, bucket: a, region: us-east-1, pathStyle: true}
    secretRef: {name: store-a-s3-credentials}

# storage: store-a (output)
storage:
  provider: minio
  endpoint: https://a.example.org
  bucket: a
  region: us-east-1
  pathStyle: true
  secretRef: {name: store-a-s3-credentials}
  okdp: {ref: store-a, contract: s3, source: output, instance: demo-store}
```

No secret field is ever inlined: a mapping reads the credentials from the
Secret `secretRef.name`. How the mapping uses `values`:
[Context](02_service_definition.md#context).

### Credentials in the platform's secret store

A connection of any level, the platform's, a project's or an instance's
own, may keep its credentials in the platform's secret store rather than
in a Secret of each namespace: `secretRef: {remoteKey: <key>}`, the key
of the store, `global.okdp.secrets.store` ([credentials across
namespaces](#credentials-across-namespaces): a SecretStore of each
namespace, or a ClusterSecretStore), under which each secret field of
the contract (`x-okdp-secret`) is a property of its name. The platform's
keys are under `platform/`, the segment the store gives every namespace
to read ([what the store must enforce](#what-the-store-must-enforce)). An
S3 store that OKDP neither deploys nor manages, for instance:

```yaml
# examples/03_full/input/db/platform.yaml
global:
  okdp:
    secrets: {store: okdp-secrets}
connections:
  datalake:
    contract: s3
    endpoint: https://s3.eu-west-1.amazonaws.com
    region: eu-west-1
    bucket: okdp-datalake
    secretRef: {remoteKey: platform/datalake}  # its properties accessKey and secretKey
```

Each instance whose reference resolves to such a connection gets, in its
objects, after the mapping's, the `ExternalSecret` a claimer gets of a
claim across namespaces ([credentials across
namespaces](#credentials-across-namespaces)), named after the Secret it
writes in the instance's namespace, `<release>.<connection>-credentials`:
the release and the connection's name, DNS-1123 labels, joined by a dot,
which neither holds, so that no other connection's Secret takes the name,
nor a claim's (`<release>-<secret>` across namespaces; within one, the
provider's `<id>-<contract>-credentials`, the ID `<namespace>.<...>`, and
a tree's release, `<namespace>-<instance>`, is never its namespace). The
ExternalSecret and, through its target's template, the Secret carry the
labels and `okdp.io/credentials-<contract>: "true"`. The connection
resolves with `secretRef: {name: <that Secret>}`, which the mapping reads
as any other's, and which meets a requirement of credentials (`"require":
{"required": ["secretRef"]}`); the instance's references to one connection
share its Secret, one ExternalSecret:

```yaml
# demo/demo-workspace: storage: datalake, catalogs[2].storage: datalake
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata: {name: demo-workspace.datalake-credentials, namespace: demo, labels: {..., okdp.io/credentials-s3: "true"}}
spec:
  refreshInterval: 1h                              # secrets.refreshInterval
  secretStoreRef: {name: okdp-secrets, kind: ClusterSecretStore}  # secrets.provider: SecretStore
  target:
    name: demo-workspace.datalake-credentials
    creationPolicy: Owner
    template: {metadata: {labels: {..., okdp.io/credentials-s3: "true"}}}
  data:
    - {secretKey: accessKey, remoteRef: {key: platform/datalake, property: accessKey}}
    - {secretKey: secretKey, remoteRef: {key: platform/datalake, property: secretKey}}
```

A key is a path of the store, segments separated by `/`, none empty, `.`
or `..`: a key reaches past its first segment, the one a store's policy
scopes, in no way. The platform's keys are under `platform/`, outside
`<namespace>/<id>`, where the credentials of the claims cross
namespaces; `platform` is no project's namespace ([inputs and
outputs](#inputs-and-outputs)). A connection of level 0 names any key: the
platform's values are reviewed with the platform. A connection above
level 0, a project's (`project.yaml`) or an instance's own
(`connections.yaml`), names a key under its namespace, `<namespace>/`,
and no other: the namespace's own segment, where its PushSecrets push
the credentials of the claims it provides, and where an administrator of
the store puts what it may read; the platform's keys are read through
the platform's connections, and another namespace's never, whatever the
store's policy allows ([what the store must
enforce](#what-the-store-must-enforce)):

```yaml
# projects/demo/instances/app/connections.yaml, level 2, the namespace demo
connections:
  own:
    contract: s3
    bucket: own
    secretRef: {remoteKey: demo/own}             # read: the instance's namespace
  stolen:
    contract: s3
    bucket: ledger
    secretRef: {remoteKey: finance/finance.ledger.database}  # refused: another namespace's claim
```

A `remoteKey` with a `name`, not a non-empty string or holding `{{`, a
platform without a store, a connection's name that is not a DNS-1123
label, a contract without secret field, a key that is no path, or one
outside the namespace above level 0 refuse the reference, after the
fields of the connection ([connection errors](#connection-errors);
`testdata/context/2/remote-key-of-another-namespace.yaml`,
`testdata/context/1/remote-key-of-the-platform.yaml`,
`testdata/cli/secrets.txtar`).

### What a reference requires

A package may need more of a connection than its contract says: one
engine of those the contract allows, a version, credentials.
`x-okdp-connection-ref`'s `require` is a JSON Schema the connection
meets, whatever it comes from: external, output or claim.

```json
"database": {"type": ["string", "object"], "default": {},
  "x-okdp-connection-ref": {"contract": "database-server", "provision": true,
    "require": {"properties": {"engine": {"const": "postgresql"}}, "required": ["secretRef"]}}}
```

- It checks the connection [completed](#completion) and valid against its
  contract ([checks](#checks)), what the mapping receives: the contract
  fields and `secretRef`, not `okdp`. `required: [secretRef]`: the
  connection has credentials.
- It names fields the connection may have: the names under its top-level
  `properties` and `required` are fields of the contract, or `secretRef`;
  never a secret field, which is in the credentials Secret, never in the
  connection.
- It is compiled as a contract is ([checks](#checks): formats, no `$ref`
  to a file), in the draft of the parameter schema's `$schema` unless it
  names its own, with the parameter schema, at each compile, whether the
  parameters set the reference or not.
- What the provider publishes is what it checks: a version to compare is
  a number the contract declares (`major`, `"minimum": 15`); JSON Schema
  compares numbers, not version strings.
- It checks, it never selects: a claim is bound first, to the provider of
  its class or to the one it names ([claims](#claims)), then the
  connection that provider publishes is checked. One that does not meet it
  refuses the instance; the claim names a class whose provider meets it,
  or the default changes: no binding moves on its own.

Every violation is reported, sorted by path, as the contract's:

```
database (claim "demo.app.database" of data/data-postgresql): the connection does not meet the package's requirement (x-okdp-connection-ref.require); data/data-postgresql is the provider of the default class standard of database-server connections (global.okdp.provisioning.database-server.default): name a class whose provider meets it (class), or change the default:
- at '/engine': value must be 'postgresql'
```

A rule on the connection a reference resolves to belongs there rather
than in the mapping's `errors`: the compiler says which reference, which
connection, where it comes from, and which provider to change.

### Connection errors

`<path>`: the parameter path, written as in [protect](#protect) errors
(`backends[1].database`, a key that holds a dot in brackets:
`databases["eu.main"]`); `<name>`: the reference.

Every reference that does not resolve, or is not a string, is reported, at
once, one line each, in parameter path order: the keys sorted at each
level, the items of a list in order (`a.x` before `a-b`, unlike the string
order of the [dependencies](#dependencies)), as the violations of the
parameter schema are. A reference gives one error, the first in this
order: its type, its contract (unknown, unreadable; another one, for an
external connection or a named claim; a named claim of a reference that
takes none; an output of several instances, or none, for a name that is no
connection), the fields its requirement names, the fields of an external
connection or an output in alphabetical order, an external connection's
`secretRef.remoteKey`, the `required` fields:
every field not set, one line each, in alphabetical order; then the
contract's types for an external connection or an output: every violation,
one line each, sorted by path ([Checks](#checks)); then the requirement's,
every violation, one line each, sorted by path ([what a reference
requires](#what-a-reference-requires)); last, `{{` in the connection, the
first one found by path (keys sorted, items in order). The instances of
`Inputs.Published` are visited in release order. The same parameters give
the same report from one compile to the next:

```
main.db: connection "shared-db": field "user" is not part of the database contract
replicas[1]: connection "store" has contract "s3", "database" is expected
replicas[2] (connection "pg"): the fields violate the database contract:
- at '/engine': value must be one of 'postgresql', 'mysql'
- at '/port': got string, want integer
replicas[3]: "Demo_DB" is not a connection, nor an output of an instance of the namespace
```

An output is named as a connection is, `output "<name>" of <instance>`
for `connection "<name>"`: `<path>: output "<name>" of <instance>: field
"<key>" is not part of the <contract> contract`, `<path> (output "<name>"
of <instance>): field "<key>" is required by the <contract> contract`. With
`Inputs.Published`, a name that is neither a connection nor an output
says so, and names the instances that publish an output of that name of
another contract, in release order:

```
storage: "demo-pg" is not a connection, nor an output of an instance of the namespace (demo-pg publishes it with contract "database", "s3" is expected)
```

| Case | Error | `testdata/` |
|---|---|---|
| neither a string nor `null`, nor a claim of a reference that takes them | `<path>: a connection reference must be a string, got <value>` | |
| a claim template with another key than `class`, `provider` and `request` | `<path>: claim: unknown key "<key>": class, provider and request only` | |
| a claim's `class` or `provider` not a string | `<path>: claim: <key> must be a string, got <value>` | |
| a claim's `request` not a map | `<path>: claim: request must be an object, got <value>` | |
| a `provider` that is not `<name>` or `<namespace>/<name>` ([providers](#providers)) | `<path>: claim: provider "<provider>": <instance>, <release>, <namespace>/<instance> or <namespace>/<release>` | |
| a named claim of another contract | `<path>: claim "<name>" has contract "<c>", "<contract>" is expected` | |
| a named claim, the reference taking none (no `provision`) | `<path>: "<name>" is a claim: the reference takes none (x-okdp-connection-ref's provision)` | |
| a claim with no binding | `<path>: claim (<contract>): no provider is bound to it: the claims bind when the instances compile together (CompileAll)` | |
| a binding of another contract (built by hand, through the library) | `<path>: claim (<contract>): bound as a <binding's contract> claim` | |
| a binding without its output | `<path>: claim "<id>" (<contract>): <namespace>/<release> publishes no output for it` | |
| no `<contract>.schema.json`, or one that cannot be read, is not JSON, not an object or not a valid JSON Schema | `<path>: the <contract> contract cannot be read: <contract>.schema.json: <cause>` | |
| external, another contract | `<path>: connection "<name>" has contract "<c>", "<contract>" is expected` | `context/2/wrong-contract.yaml` |
| output, of several instances | `<path>: "<name>" names <contract> outputs of several instances of the namespace: <instance>, <instance>`, in release order | |
| external or output, not a contract field | `<path>: connection "<name>": field "<key>" is not part of the <contract> contract` | `context/2/unknown-field.yaml` |
| external or output, secret field | `<path>: connection "<name>": "<key>" is a secret field, put it in the credentials Secret` | `context/2/secret-in-connection.yaml` |
| external, a `secretRef` with both `name` and `remoteKey` | `<path>: connection "<name>": secretRef: name or remoteKey, not both` | `context/2/remote-key-and-name.yaml` |
| external, a `remoteKey` that is not a string, or empty | `<path>: connection "<name>": secretRef.remoteKey: the key of the credentials in the platform's secret store, a non-empty string, got <value>` | |
| external, `{{` in a `remoteKey` (of level 0: above, the layer fails first) | `<path> (connection "<name>"): field "secretRef.remoteKey": the value contains "{{"` | |
| external, a `remoteKey` without `global.okdp.secrets.store` | `<path>: connection "<name>": secretRef.remoteKey: its credentials are in the platform's secret store, global.okdp.secrets.store, which is not set` | `context/2/remote-key-without-store.yaml` |
| external, a `remoteKey` of a connection whose name is not a DNS-1123 label | `<path>: connection "<name>": secretRef.remoteKey: the connection's name is not a DNS-1123 label: its credentials Secret is named after it, <release>.<name>-credentials` | |
| external, a `remoteKey` of a contract without `x-okdp-secret` field | `<path>: connection "<name>": secretRef.remoteKey: the <contract> contract has no secret field (x-okdp-secret): there are no credentials to read` | |
| external, a `remoteKey` that is no path of the store: an empty, `.` or `..` segment | `<path>: connection "<name>": secretRef.remoteKey "<key>": a path of the platform's secret store, segments separated by "/", none empty, "." or ".."` | `context/2/remote-key-not-a-path.yaml` |
| external, above level 0, a `remoteKey` outside the instance's namespace, `<namespace>/` ([credentials in the platform's secret store](#credentials-in-the-platforms-secret-store)) | `<path>: connection "<name>": secretRef.remoteKey "<key>": a key of the platform's secret store a project may not read: a connection above level 0 (level <level>) names a key under "<namespace>/", its namespace; the platform's keys are read through the platform's connections (level 0)` | `context/2/remote-key-of-another-namespace.yaml` |
| output, values that values cannot hold (a NaN) | `<path>: output "<name>" of <instance>: json: <cause>` | |
| neither a connection nor an output | `<path>: "<name>" is not a connection`; with `Inputs.Published`, `<path>: "<name>" is not a connection, nor an output of an instance of the namespace`, then ` (<instance> publishes it with contract "<c>", <instance> with contract "<c>", "<contract>" is expected)` when instances publish an output of that name of another contract | |
| required field not set | `<path> (connection "<name>"): field "<key>" is required by the <contract> contract`, one line per field | |
| external or output, a value the contract's schema refuses | `<path> (connection "<name>"): the fields violate the <contract> contract:` then `- at '/<field>': <violation>`, one line per violation | `context/2/connection-types.yaml` |
| a connection that does not meet the reference's requirement | `<path> (connection "<name>"): the connection does not meet the package's requirement (x-okdp-connection-ref.require):` then `- at '/<field>': <violation>`, one line per violation; a claim of the default class, `; <namespace>/<release> is the provider of the default class <class> of <contract> connections (global.okdp.provisioning.<contract>.default): name a class whose provider meets it (class), or change the default` before the colon | `context/2/no-secret-ref.yaml` |
| a requirement that names a field not of the contract | `<path>: x-okdp-connection-ref: require: field "<key>" is not part of the <contract> contract` | |
| a requirement that names a secret field | `<path>: x-okdp-connection-ref: require: "<key>" is a secret field, in the credentials Secret, never in the connection: require secretRef` | |
| a requirement that is not an object or not a valid JSON Schema, whether the parameters set the reference or not, every one | `config.schema.json: <place>: x-okdp-connection-ref: require: a JSON Schema, an object, got <value>`; `... require: <cause>`. `<place>`: the reference's place in the schema, `properties.catalogs.items.properties.database` | |
| a fixed request that is not an object, whether the parameters set the reference or not, every one | `config.schema.json: <place>: x-okdp-connection-ref: request: an object, got <value>` | |
| `{{` in the connection | `<path> (connection "<name>"): field "<field>": the value contains "{{"`; `... the key contains "{{"` for a key. `<field>`: a path in the connection (`host`, `secretRef.name`, `tags[1]`) | |

An external connection without `secretRef` resolves without one: a
reference whose mapping reads credentials requires them, `"require":
{"required": ["secretRef"]}`, as the example webapp's do
(`testdata/context/2/no-secret-ref.yaml`,
[what a reference requires](#what-a-reference-requires)).

## Contracts

A contract: the JSON Schema of a kind of connection (`database`, `s3`,
`hive`...), shared by the instances that provide it and the ones that
consume it.

### Location

`Inputs.Contracts`: a file system with `<contract>.schema.json` at its root;
the name of the contract is the name of the file. A tree's are the
`contracts/` of its repositories ([repository
layout](../01_usage.md#repository-layout)). A contract is read by each
reference and each `provides` item that names it: a compile that reads
none needs no contracts, an empty file system. A file that is missing,
cannot be read, is not JSON, not an object or not a valid JSON Schema, or
whose claim schema is not:
`the <contract> contract cannot be read: <contract>.schema.json: <cause>`,
`<cause>` `file does not exist` for a contract no repository has,
`x-okdp-claim: <cause>` for its claim schema.

### Format

A JSON Schema with OKDP keywords, of the two sides of a claim: what a
provider publishes, its `properties`, the connection; what its
credentials Secret holds, the `x-okdp-secret` fields; and what a claim
may request of the provider, `x-okdp-claim`, as a PersistentVolumeClaim
asks (`resources.requests`) and its volume answers (`capacity`). The
values of a connection or an output are checked against the schema
([Checks](#checks)); besides, the compiler reads:

| Keyword | On | Meaning |
|---|---|---|
| `properties` | the contract | its fields: the only keys a connection or an output may set |
| `required` | the contract | the fields that must be set after [completion](#completion): missing, `null`, `""`, `0`, an empty list or map fails; `false` is a value |
| `default` | a field | its value when not set |
| `x-ui-condition: {field, value}` | a field with a `default` | the default applies only when `field` prints as `value` |
| `x-okdp-derived: {from, map}` | a field | its value when not set: `map[<value of from>]`; none when that value is not a key of `map` |
| `x-okdp-secret: true` | a field | a credential: refused in a connection and in an output, it belongs to the credentials Secret (`secretRef`) |
| `x-okdp-contract` | the contract | the name in the `required` error message |
| `x-okdp-claim` | the contract | the claim schema: a JSON Schema of what a claim may request (`request`), read as a values schema, in the contract's draft unless it names its own: its defaults, those of its `properties` as a parameter schema's, applied to every claim's request; a contract without one takes no request ([claims](#claims)) |

`x-okdp-internal`, the naming convention of a former version, is refused:
`the <contract> contract cannot be read: <contract>.schema.json:
x-okdp-internal: naming conventions are gone, a provider publishes its
connections (provides)`. What an instance is reached at, its provider
publishes, an output a reference names
([Resolution](#resolution)).

The other `x-*` keywords (`x-okdp-external`, `x-okdp-required`,
`x-okdp-endpoint-from`, `x-okdp-icon`, `x-okdp-category`,
`x-ui-placeholder`, `x-ui-widget`) are not read, nor checked: an external
connection is accepted for any contract, `x-okdp-external` or not.

`examples/02_advanced/input/db/contracts/database.schema.json`, trimmed:

```json
{
  "x-okdp-contract": "database",
  "properties": {
    "engine":   {"type": "string", "enum": ["postgresql", "mysql"], "default": "postgresql"},
    "driver":   {"type": "string", "x-okdp-derived": {"from": "engine",
                 "map": {"postgresql": "org.postgresql.Driver", "mysql": "com.mysql.cj.jdbc.Driver"}}},
    "host":     {"type": "string"},
    "port":     {"type": "integer", "default": 5432},
    "dbName":   {"type": "string"},
    "size":     {"type": "string"},
    "password": {"type": "string", "x-okdp-secret": true}
  },
  "required": ["host", "dbName"],
  "x-okdp-claim": {
    "properties": {"size": {"type": "string", "pattern": "^[0-9]+(Mi|Gi)$", "default": "1Gi"}},
    "additionalProperties": false
  }
}
```

A claim requests a size, 1Gi when it asks none; its provider publishes
the size granted, `size` among the connection's fields.

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
(`driver` from the default `engine`).

### Checks

- Consumed, external: the `contract` of the entry, every other key a
  non-secret field, completed, then valid against the schema
  ([Connections](#connections)).
- Consumed, output or claim: the output an instance publishes, every key
  of its `values` a non-secret field, completed, then valid against the
  schema, as an external connection: checked when it was provided, it is
  checked again against the contract as the consumer reads it.
- Provided (`provides`): the contract exists, every key of `values` a
  non-secret field, completed, then valid against the schema
  ([The info file](#the-info-file)).

A connection consumed is then checked against what its reference
requires, `secretRef` included
([what a reference requires](#what-a-reference-requires)).

Valid against the schema: the completed values (no `secretRef`, no `okdp`),
validated as a chart's values against its `values.schema.json`
([Schema check](#schema-check): draft, formats, `$ref`), every keyword but
`x-*` applying (`type`, `enum`, `pattern`, `minimum`, `items`,
`additionalProperties`...). The secret fields, never in the values, are
left out of the top-level `required`. Every violation is reported with its
path in the values, sorted by path, as those of the parameters:

```
<where>: the fields violate the <contract> contract:
- at '/port': got string, want integer
```

`<where>`: `<path> (connection "<name>")` for a connection,
`<path> (output "<name>" of <instance>)` for an output consumed,
`provides "<name>" (<contract>)` for an output provided.

## Context values

`Inputs.Context`: the context's values layers, each a `ContextLayer`, its
`Values` at its `Level`; a tree's `platform.yaml` at level 0, its
project's `project.yaml` at 1, its own `connections.yaml` at 2. Among them
`global`, `global.okdp`, the settings of the platform (ingress,
certificate issuers, OIDC, proxy, the platform's charts...),
`connections`, which only the context gives, an instance's own included,
and `claims`, the named claims of a project, which only the context above
level 0 gives ([claims](#claims)). Level 0 is the platform's:
`examples/02_advanced/input/db/platform.yaml`,
`platform/platform-values.yaml` in a platform repository; level 1,
typically a project's, and each level above adds to the lower ones
([Levels](#levels)). The context's layers are merged by level over the
package's [defaults](#defaults), before the instance's, `Inputs.Values`
([parameters](#parameters)); a later layer of a level may change or delete
(`null`) a value of the previous ones of that level, as
`testdata/context/0/missing-platform-value.yaml` does.

```yaml
global:
  okdp:
    ingress: {suffix: okdp.example.org, className: nginx}
    tls: {clusterIssuer: okdp-ca, caBundleSecret: certs-bundle}
    oidc: {issuerUri: https://sso.okdp.example.org/realms/okdp}
```

`global` is the context's: it comes from the package's
[defaults](#defaults) and the context's layers, each level adding to the
lower ones ([Levels](#levels)); `global.okdp` decides the instance's host,
where its OAuth clients register and the chart they are rendered with, and
every chart receives `global` ([merge precedence](#merge-precedence)).
`global.okdp` holds the platform's settings, level 0's,
[Requirements](#requirements) to [OAuth client
settings](#oauth-client-settings) and the keys the mappings read: a
project adds its classes to it, `global.okdp.provisioning`, and nothing
else ([Levels](#levels)); its own settings go beside `okdp`,
`global.project`. An instance layer that sets `global`
fails before the parameter schema is checked, naming the layer by its
position in `Inputs.Values`, from 1 (a tree's `config.yaml` the first),
and the first key of `global` it may not set, in key order; `global` set
to a non-map, `null` included, fails as a whole
(`testdata/instance-sets-global.yaml`):

```
values layer 1: global.okdp is set by the context (Inputs.Context)
values layer 2: global is set by the context (Inputs.Context)
```

The exception: a key the parameter schema declares under `global`
(`properties.global.properties.<key>`), `okdp` excepted, which an instance
layer may set, over the context's value; a package without
`config.schema.json` declares none. A package opens a global value of
its charts to instances this way:

```json
"global": {"type": "object", "properties": {"imageRegistry": {"type": "string"}}}
```

A context layer is a values layer: it may set more than `global`, for
every instance it is given to, under the instance's layers. The useful
case is `connections`: a connection every instance may reference by name
([Resolution](#resolution)); a layer of a higher level may not give a
connection of the same name ([Levels](#levels)).

```yaml
# examples/02_advanced/input/db/platform.yaml
connections:
  platform-db:
    contract: database
    host: db.okdp.example.org
    dbName: shared
    secretRef: {name: platform-db-credentials}
```

The example's instance claims a database, `database: {}`, and references
the platform's connection, `audit: platform-db`. Parameters and `upstream`
values are the package's: a context file every service shares keeps to
`global` and `connections`.

`connections` is the context's only: an instance's own connections are a
context layer of a level of their own ([Levels](#levels)). An instance
layer that sets `connections`, to any value, fails before the parameter
schema is checked, after the check of `global`
(`testdata/instance-sets-connections.yaml`); one that sets `claims`, after
it: the named claims are a project's, a layer of level 1 or above, never
the platform's ([claims](#claims)):

```
values layer 1: connections are given by the context (Inputs.Context): an instance's own in a context layer of its level
values layer 1: claims are declared by the context (Inputs.Context): a project's, of level 1 or above
context layer 1 (level 0): claims: a project's, of level 1 or above
```

### Levels

`ContextLayer.Level`: 0, the platform's;
1, typically a project's, the namespace of its instances: what it adds to
the platform values for each of them, its settings under `global`, beside
`okdp` (`global.project`, which the mapping reads and the compiler does
not; a key of `global` its charts read, `storageClass`), its connections,
which its instances reference by name as the platform's, its named
claims (`claims`), which they reference by name too ([claims](#claims)),
and its classes beside the platform's (`global.okdp.provisioning`,
[classes](#classes)). More levels may
follow (a team's...); their numbers may leave gaps (0, 10, 20). An
instance's own connections are a layer of a level of its own, above its
project's (2 over a project at 1), given to its compile only: the level
rule below applies to them, so an instance adds connections beside its
project's and the platform's, and changes none of theirs.

The layers are merged by level, lowest first, whatever their order in
`Inputs.Context`; a level's layers in their order (`helm -f`), a later
one changing or deleting (`null`) a value of an earlier one of the same
level. A tree gives one layer per level: `platform.yaml`, the project's
`project.yaml`, the instance's `connections.yaml`.

A level adds values, it changes none of a lower level's. A layer fails,
before the parameter schema is checked, at the first lower level (lowest
first), then the first path (keys sorted), at which it sets a value that
level's layers set, merged: a key whose value is not a map, to any value,
the same one and `null` included; a parent of one, to anything but a map;
or a connection, `connections.<name>`, a named claim, `claims.<name>`, a
class, `global.okdp.provisioning.<contract>.classes.<name>`, or a default
class, `global.okdp.provisioning.<contract>.default`, taken whole: a layer
may not give a field the lower level's connection leaves out, a `port` to
its contract's default
(`testdata/context/1/redefines-platform-connection.yaml`), nor change the
platform's default class
(`testdata/context/1/redefines-platform-class.yaml`): a project that
wants another provider adds a class and names it. The
layer is named by its position in `Inputs.Context`, from 1, and its
level:

```
context layer 2 (level 1): global.okdp.ingress.suffix is set at level 0 (Inputs.Context): a level adds values, it does not change a lower level's
context layer 2 (level 1): connections.platform-db is set at level 0 (Inputs.Context): a level adds values, it does not change a lower level's
context layer 2 (level 1): global.okdp.provisioning.database.default is set at level 0 (Inputs.Context): a level adds values, it does not change a lower level's
```

Maps merge: a level adds keys beside a lower one's (`global.project`
beside `global.okdp`). The package's [defaults](#defaults) are not
a level: a layer of any level overrides a default under `global`, as level
0's does.

`global.okdp` is the platform's, level 0's: a layer above level 0 sets
`provisioning` only, its classes and the default class of a contract the
platform leaves without one ([classes](#classes)), and no other key of
it, one the compiler reads (`charts`, `oidc`, `secrets`, `policy`...) or
one it does not, whether the platform sets it or not; nor `global.okdp`
itself to anything but a map. A project cannot name the chart its OAuth
clients are rendered with, where they register, the secret store its
credentials go through, nor make itself privileged; its own settings,
which its packages' mappings read, go under `global` beside `okdp`
(`global.project`). The layer fails at the first such key, in key order,
after the check against the lower levels
(`testdata/context/1/sets-platform-charts.yaml`, `sets-platform-oidc.yaml`,
`sets-platform-policy.yaml`, `sets-platform-secrets.yaml`,
`sets-platform-ingress.yaml`, `sets-platform-tls.yaml`,
`sets-platform-proxy.yaml`, `sets-unknown-platform-key.yaml`):

```
context layer 2 (level 1): global.okdp.charts: a platform setting, level 0 only (Inputs.Context): a project sets global.okdp.provisioning, nothing else of global.okdp
```

Only level 0 is reviewed with the platform: above it, as in an
instance layer, no key or string of a layer may contain `{{`
([Layers](#layers)), the first one found by path reported,
`context layer 3 (level 1): connections.team-db.host: the value contains "{{"`
(`testdata/context/1/template-code.yaml`).
The layers are checked in merge order, each normalized ([values
files](#values-files)), then against the lower levels, then for the
platform's settings, then for `{{`.

The instance's layers come last: an instance sets no key of `global` but
those its parameter schema declares, over the context's values, no
`connections` and no `claims` ([Context values](#context-values)).

```yaml
# examples/03_full/input/db/projects/demo/project.yaml, level 1,
# after platform.yaml at level 0
global:
  storageClass: fast-ssd
  project: {name: demo, costCenter: cc-4217, nodePool: analytics}
connections:
  demo-warehouse:
    contract: database
    engine: mysql
    host: warehouse.demo.example.org
    port: 3306
    dbName: sales
    secretRef: {name: demo-warehouse-credentials}
```

Over it, the instance's own connections, its `connections.yaml` in
`examples/03_full/input/db/projects/demo/instances/workspace/`, at
level 2 ([Connections](#connections)), add `scratch`. Given after
`platform.yaml` and `project.yaml`, a file of level 2 that gives
`demo-warehouse` again, even a field of it, fails:

```
context layer 3 (level 2): connections.demo-warehouse is set at level 1 (Inputs.Context): a level adds values, it does not change a lower level's
```

### Requirements

`require` of `okdp.yaml`: dotted paths under `global.okdp`
([require](02_service_definition.md#require)). A required key must be set
and not empty (missing, `null`, `false`, `""`, `0`, an empty list or map
fails). The entries are checked in order, once the mapping's context is
built: after the
[parameter schema](#schema-check), the platform schema ([settings the
compiler reads](#fields-the-compiler-reads)) and the
[connections](#connections), before the [platform
charts](#platform-charts) and the mapping
(`testdata/context/0/missing-platform-value.yaml`). The first missing key
fails:

```
global.okdp.ingress.className is required. It is a context value: the platform's or a project's (Inputs.Context)
```

### Fields the compiler reads

The settings of `global.okdp` the compiler reads are described by the
platform schema the compiler embeds,
[`internal/okdp/platform.schema.json`](../../internal/okdp/platform.schema.json):
their types, their defaults and, in its `description`s, what each means.
It is the platform authors' reference; an editor can check a platform
values file against it. Every key of `global.okdp`, described there or
not, is the platform's, set at level 0: a project's layer sets
`provisioning` only ([Levels](#levels)).

| `global.okdp` | Used for |
|---|---|
| `tls.clusterIssuer` | the cert-manager ClusterIssuer of the services' certificates, whichever cert-manager runs it (OKDP's self-signed CA, an ACME or a corporate one): `platform.ingressAnnotations` (`cert-manager.io/cluster-issuer`), `""` when unset |
| `ingress.annotations` | no default: annotations of every service's ingress, strings, those the cluster's ingress controller reads (Traefik's middlewares, HAProxy's timeouts, ingress-nginx's), merged over `cert-manager.io/cluster-issuer` in `platform.ingressAnnotations`: a platform whose controller is not the one a package tunes its ingress for adds its own equivalents once |
| `tls.caBundleSecret` | no default: the Secret, in each namespace, of the platform's CA bundle, `ca.crt` (PEM) and `bundle.p12` (PKCS#12), as a trust-manager Bundle writes one, whichever tool fills it; the packages mount it to trust the platform's certificates (unset: they mount none, the certificates being a public CA's), and it is the default of `oidc.dcr.caSecret` ([OAuth client settings](#oauth-client-settings)) |
| `charts.oidcDcr` | the upstream chart of the [OAuth client charts](#oauth-client-charts) ([Platform charts](#platform-charts)) |
| `oidc.enabled`, `oidc.clientProvisioning` | `platform.oidc`, the Secret names (`platform.oidcClientSecret`, `oidcClientSecret`), whether the [OAuth client charts](#oauth-client-charts) are compiled |
| `oidc.scope` | `platform.oidc.scope`; the scope a client chart registers when its client sets none |
| `oidc.dcr` | `platform.oidc.dcr`, the values of the OAuth client charts ([OAuth client settings](#oauth-client-settings)) |
| `oidc.issuerUri` | the OAuth client charts' wait for the provider (`waitForProvider.discoveryUrl`) |
| `proxy.httpProxy`, `proxy.httpsProxy`, `proxy.noProxy` | `platform.proxyEnv` |
| `provisioning.<contract>` | the classes of the claims of the contract, each a provider and what it fixes of the request, and the default class, of the claims that name neither a class nor a provider ([classes](#classes)): the platform's route every namespace to their providers, a project's reach a provider of another namespace with its consent only ([providers](#providers)) |
| `secrets.store` | the name of the secret store the credentials of claims across namespaces go through, under `<provider namespace>/<id>` ([credentials across namespaces](#credentials-across-namespaces)), and where a connection may keep its own, the platform's under `platform/` ([credentials in the platform's secret store](#credentials-in-the-platforms-secret-store)) |
| `secrets.provider`, `secrets.serviceAccount` | set, the store is a SecretStore in each project's namespace, `provider` its spec verbatim (External Secrets' `spec.provider`), authenticating with the ServiceAccount `serviceAccount` of the namespace, which the [targets](#targets) write (`Result.SecretStore`), and which the PushSecrets and ExternalSecrets reference (`kind: SecretStore`); unset, a ClusterSecretStore of the platform's, one identity for every namespace |
| `secrets.refreshInterval` | `1h` by default: how often the PushSecrets push the credentials of the claims across namespaces to the store, and the ExternalSecrets read them, and those of the connections kept in it |
| `policy.privileged` | the namespaces of the platform's own projects (cert-manager, ingress-nginx, an operator...), whose instances install cluster-scoped objects, write other namespaces and set any upstream value; the other projects' objects stay in their namespace, none cluster-scoped ([Objects section](#objects-section)), and their upstream values are held to the [upstream policy](#upstream-policy); `Result.Privileged`, which the [targets](#targets) read to give their releases the engine's own identity, the other projects' one confined to their namespace |
| `policy.upstream.deny` | key names the upstream values of the other projects' instances set nowhere, beside the compiler's ([upstream policy](#upstream-policy)) |
| `policy.upstream.registries` | the registries the images of the other projects' upstream values come from, each a host, its port and a path prefix optional; unset, the images are not checked ([upstream policy](#upstream-policy)) |

Every compile checks `global` against the schema, in every mode, before
the [connections](#connections) and the [requirements](#requirements), and
applies its defaults. A setting of another type is refused, and so is a
map on the way to one (`global`, `global.okdp`, `oidc`, `proxy`...) that
is not a map, rather than read as not set: `oidc: false` would leave OIDC
enabled, `proxy: <url>` give no proxy environment. A former setting is
refused, renamed: `certificateIssuers`, whose `selfSigned.name` is
`tls.clusterIssuer` (`- at '/global/okdp/certificateIssuers': false
schema`). A `null` is read as in a chart's values ([Layers and
nulls](#layers-and-nulls)): over a default, it deletes the setting
(`oidc.enabled: null` turns OIDC off, `platform.oidc.enabled` absent;
`oidc.clientProvisioning: null` leaves no mode); where there is no
default, it is refused (`ingress: null`). An empty string is a value
(`oidc.scope: ""`). The other keys of `global.okdp` are not checked, but
for those of `charts`. Every violation is reported at once, sorted by
path, in Helm's words, as those of the [parameters](#schema-check):

```
context values (Inputs.Context) don't meet the specifications of the schema of global.okdp:
- at '/global/okdp/oidc': validation failed
  - at '/global/okdp/oidc/clientProvisioning': value must be one of 'existing', 'dcr'
  - at '/global/okdp/oidc/enabled': got string, want boolean
- at '/global/okdp/proxy': got string, want object
```

The mapping sees `global.okdp` as written and `platform` as derived, with
the defaults ([Context](02_service_definition.md#context)). `global`
also reaches every upstream chart ([Merge precedence](#merge-precedence)).

### Platform charts

`global.okdp.charts`: the upstream charts that render the charts the
compiler adds itself, each an upstream chart, checked as a dependency of
`Chart.yaml` is ([dependencies](02_service_definition.md#dependencies)):
`repository`, `chart` (the chart's name), `version`.

| Key | Renders |
|---|---|
| `oidcDcr` | the [OAuth client charts](#oauth-client-charts), in `dcr` mode: an oidc-dcr chart that takes the values of oidc-dcr 0.5.0, which the embedded template writes; oidc-dcr 0.5.0 of `oci://quay.io/adaltas` when unset |

Their form is checked with the other [settings the compiler
reads](#fields-the-compiler-reads), against the platform schema: `charts`
a map of these keys, another refused (a misspelled `oidcDcr` rather than
leave the default in place); each chart a map, its `repository`, `chart`
and `version` strings (its other keys are ignored). Then, after the
[connections](#connections) and the [requirements](#requirements), before
the mapping, each is checked as a source, `oidcDcr` when set, in every
mode: a repository URL (`https://` or `oci://`; `http://`, the chart
fetched in clear, is refused), the name of a chart, a pinned version. The
first violation is reported, in this order
(`testdata/context/0/http-chart-repository.yaml`):

```
global.okdp.charts.oidcDcr.repository "http://charts.example.org/mirror": http://, the chart fetched in clear: the URL of a chart repository (https://... or oci://...)
global.okdp.charts.oidcDcr.repository "quay.io": the URL of a chart repository (https://... or oci://...)
global.okdp.charts.oidcDcr.version "1.x": a pinned chart version (1.2.3), not a range
```

The [chart cache](#chart-cache) of a check holds each when its chart is
compiled: the oidc-dcr chart in `dcr` mode with a client. Another
repository with the same chart and version (a mirror) has its own
directory there.

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

| Key | Client chart value |
|---|---|
| `registrationUrl` | `registrationUrl`: the registration endpoint; required in `dcr` mode, with a client |
| `authMethod` | none: `anonymous`, required in `dcr` mode, with a client; OKDP registers anonymously |
| `image` | `image` (`registry`, `repository`, `tag`, `pullPolicy`, `pullSecrets`), merged over the chart's |
| `caSecret` | `tls.certificate`: the Secret holding the CA bundle the Jobs trust; a client's `caSecret` wins. Unset, the platform's, `global.okdp.tls.caBundleSecret`; `""`, or both unset, none: `tls.certificate: ""`, the chart's default, the Job mounting no CA bundle and trusting the CAs of its image (a provider whose certificate a public CA signs) |
| `ttlSeconds` | `ttlSeconds`: how long a finished Job, and its logs, is kept |
| `backoffLimit` | `backoffLimit`: the Job's retries |
| `waitTtlSeconds` | `waitForProvider.ttlSeconds`: the longest wait for the provider's discovery document (the chart's, 600, when unset); the Jobs wait only when `oidc.issuerUri` is set |

Their types and defaults are the platform schema's, applied and checked
with the other [settings the compiler reads](#fields-the-compiler-reads),
in every mode (`existing`, OIDC disabled, no client): the three numbers
whole and `>= 0` (`0` is a value), a quoted number (`"600"`) a string:

```
- at '/global/okdp/oidc/dcr/ttlSeconds': got string, want integer
```

`image`: the camelCase keys of oidc-dcr >= 0.5.0 (`pullPolicy`,
`pullSecrets`); those of an earlier version, `pull_policy` and
`pull_secrets`, are refused (`- at
'/global/okdp/oidc/dcr/image/pull_policy': false schema`). Its other keys
are passed as written. The other keys of `oidc.dcr` are kept in
`platform.oidc.dcr` and not used. `registrationUrl` and `authMethod` are
left as written (the schema gives them no type), and checked only in `dcr`
mode with a client.

In `dcr` mode ([OAuth client charts](#oauth-client-charts)), when the
mapping declares at least one OAuth client (after `$if`, once the clients
are checked), `authMethod` must be `anonymous` (unset, it is reported as
`""`), then `registrationUrl` a non-empty string
(`testdata/context/0/dcr-without-registration-url.yaml`):

```
global.okdp.oidc.dcr.authMethod "bearer" is not supported: OKDP registers its OAuth clients anonymously
global.okdp.oidc.dcr.registrationUrl is required (clientProvisioning: dcr): the endpoint the oidc-dcr Jobs register the OAuth clients at
```

## Instance values

What an instance sets: its parameters and values for the upstream charts.
Its own connections are not instance values: a context layer of a level of
their own ([Levels](#levels)).

### Layers

`Inputs.Values`: the instance's layers in order, the last one winning
(Helm's `-f` order), after the whole context's; a tree's, the instance's
`config.yaml`.

An instance layer sets no `global` but the keys the parameter schema
declares under it, and no `connections` ([Context values](#context-values)).
Merge, nulls and schema: [Parameters](#parameters).

No key or string of an instance layer, at any depth, may contain `{{`,
whatever key it is under: parameters, `upstream`. A
parameter reaches the charts' values through the mapping, and some
upstream charts pass their values to `tpl`, where template code could call
`lookup` and read the cluster's Secrets. Each layer is checked in order,
normalized as it is merged, before the [schema check](#schema-check); the
first `{{` in path order (keys sorted, items in order) fails, the layer
numbered from 1 and the path written as in [protect](#protect) errors, a
list item `[<i>]` (`testdata/template-code.yaml`):

```
values layer 1: database: the value contains "{{"
values layer 2: upstream.app.podAnnotations["example.org/{{ x }}"]: the key contains "{{"
```

A layer is normalized before it is checked: a Go value an in-memory layer
gives that values cannot hold has failed then, with its layer and path
([values files](#values-files)). The context's layers above level 0 are
checked the same way, before the instance's ([Levels](#levels)). Level 0's,
the platform's, and the package's defaults are not checked: they are
reviewed with the platform and the package
([security boundaries](../00_architecture.md#security-boundaries)). A
connection resolved from them is ([Resolution](#resolution)).

### Upstream values

Values given to an upstream chart as they are, over the values the mapping
computed: `upstream.<key>` of the parameters, for a dependency that
accepts them, by `upstream` of `okdp.yaml`
([upstream](02_service_definition.md#upstream)). Upstream values are keyed
by the chart key, not by its chart
([dependencies](02_service_definition.md#dependencies)). Values for a
chart that accepts none (an [OAuth client chart](#oauth-client-charts)
included), or for a name that is not a chart, are
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
  layer or a context layer above level 0 gives are refused before, with
  the layer ([Layers](#layers), [Levels](#levels)); this check covers
  those of the package's defaults and of the platform's layers, level 0;
- [protect](#protect): `global` and the protected paths are refused;
- [upstream policy](#upstream-policy): for a project that is not
  privileged, the keys of a pod's privileges, identity, containers or
  objects are refused, and the images come from the platform's
  registries;
- [append](#append): the listed lists are appended to.

### protect

`protect` of the key in `upstream` of `okdp.yaml`
(`upstream.<key>.protect`): dotted paths the platform sets; `global` is
always protected. The upstream values fail when they set a protected path
(to any value, `null` included), or one of its parents to anything but a
map (`null` included). The keys beside a protected path are free:

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
it fail. Whatever `protect` names, the [upstream
policy](#upstream-policy) applies too: a package does not open what the
platform denies.

### Upstream policy

The platform's rules over the upstream values of an instance of a
project that is not privileged (`global.okdp.policy.privileged`, [fields
the compiler reads](#fields-the-compiler-reads)), whatever its package's
`upstream` and `protect`, after `protect`: the package's own values, its
defaults and values file, are not checked, being reviewed with it. The
upstream values are `upstream.<key>` of the instance's merged values, its
context's layers included. A privileged project's are not checked: the
platform's own projects set any upstream value.

A key named, at any depth, in a map or in a list's items, one of a pod's
privileges, security context, identity, containers or objects, is
refused, whatever its value: `privileged`, `hostPath`, `hostNetwork`,
`hostPID`, `hostIPC`, `hostPort`, `allowPrivilegeEscalation`,
`capabilities`, `seccompProfile`, `sysctls`, `securityContext`,
`podSecurityContext`, `containerSecurityContext`, `serviceAccountName`,
`serviceAccount`, `automountServiceAccountToken`, `extraContainers`,
`sidecars`, `initContainers`, `extraInitContainers`, `extraObjects`,
`extraDeploy`, `extraManifests`, and those of
`global.okdp.policy.upstream.deny`; so are `runAsUser: 0`, root, and
`runAsNonRoot: false`. The scheduling keys, `nodeSelector`,
`tolerations` and `affinity`, stay free. A string that holds a `:` and
reads as YAML values, a map or a list, each of its documents, is checked
as values too: some charts render a string value as YAML (`tpl`,
Bitnami's `tplvalues.render`), a list of volumes written as text.

With `global.okdp.policy.upstream.registries`, every image of the
upstream values comes from one of them: an `image` string, and the
reference a `registry` and a `repository` string make, either set by the
instance, the other the computed one (OKDP's defaults and the mapping's),
`<registry>/<repository>`, the repository alone without registry; an
`image` map's `name` is its repository where it has none (Zero to
JupyterHub's). The reference starts with an entry, then ends, goes on
with `/` or, after an entry with a path, with `:` or `@` (a tag, a
digest): `mirror.example.org/okdp` admits
`mirror.example.org/okdp/app:1.0`, not `mirror.example.org/okdp-x/app`
nor `mirror.example.org:5000/okdp/app`. An `image`, a `registry` or a
`repository` set to `null` leaves the chart's default, which the compile
does not read: it is refused. Unset, the images are not checked; `[]`,
none may be set. The chart's defaults are not checked: a package whose
chart pulls from elsewhere sets the platform's registry in its values.

The first refused, in path order (a map's images, then its keys
sorted, a list's items in order), fails, the path and the key named (`testdata/denied-upstream.yaml`,
`testdata/denied-upstream-text.yaml`,
`testdata/context/0/unlisted-registry.yaml`):

```
app: upstream.app.podSecurityContext: podSecurityContext is denied to the instance's upstream values (a pod's privileges, identity, containers or objects): the platform's projects set it (global.okdp.policy.privileged), demo is not one of them
app: upstream.app.extraVolumes, read as YAML: [0].hostPath: hostPath is denied to the instance's upstream values (a pod's privileges, identity, containers or objects): the platform's projects set it (global.okdp.policy.privileged), demo is not one of them
app: upstream.app.image.repository: image docker.io/evil/app is not from a registry of global.okdp.policy.upstream.registries (registry.example.org, mirror.example.org/okdp): the platform's projects pull from any (global.okdp.policy.privileged), demo is not one of them
```

A key the lists do not name, a chart's own spelling of a privilege
(`rbac.create`, a chart's extra RBAC rules), is the platform's to add to
`global.okdp.policy.upstream.deny`; and a privilege a chart grants from
keys of other names is out of the compiler's reach: Pod Security
Admission and the engine's identity, confined to the namespace
([targets](#targets)), are the second line.

### append

`append` of the key in `upstream` of `okdp.yaml`
(`upstream.<key>.append`): dotted paths of lists. A list the upstream values
give at such a path is appended to the list the values have there
(OKDP's defaults, computed values) instead of replacing it. The value
given must be a list; a `null` is refused too:
`upstream.app.env must be a list`.

The chart's own default list is not among them: Helm replaces it with the
final list, as it replaces a default list with that of a `-f` file. A
package that keeps its items restates them in its defaults or values file
of the key; with a chart cache, a compile warns of an appended list that
lacks some of them ([checks of the final values](#checks-of-the-final-values)).

With the example (`append: [env]`), the computed `OIDC_*` and `DB_*` items
come first, then `TZ` and `LOG_LEVEL`, of `upstream.app` in the instance's
`config.yaml` (`app` of the example's `instance.values.yaml`,
[Example](#example)).

### Upstream errors

`<at>`: `upstream.<key>.<path>`.

| Case | Error |
|---|---|
| a template (in values an instance layer or a context layer above level 0 gives, the layer fails before: [Layers](#layers), [Levels](#levels)) | `<key>: <at>: the value contains "{{"`, `<key>: <at>: the key contains "{{"`; `<key>: <at>: cannot be checked for "{{": <type> is not a values type` |
| a protected path | `<key>: <at>: <protected> is set by the platform and cannot be changed` |
| a denied key, of a project that is not privileged ([upstream policy](#upstream-policy)) | `<key>: <at>: <name> is denied to the instance's upstream values (a pod's privileges, identity, containers or objects): the platform's projects set it (global.okdp.policy.privileged), <namespace> is not one of them`; `(global.okdp.policy.upstream.deny)` for the platform's; `runAsUser 0, root, is denied...`, `runAsNonRoot false is denied...`; in a string read as YAML, `<at>, read as YAML: <path>: ...` |
| an image of none of the platform's registries | `<key>: <at>: image <reference> is not from a registry of global.okdp.policy.upstream.registries (<registries>): the platform's projects pull from any (global.okdp.policy.privileged), <namespace> is not one of them`; `image null, the chart's default,` for a `null` |
| an appended path, not a list | `<key>: <at> must be a list` |

## Merge precedence

The final values of a compiled upstream chart (`Result.Values[<key>]`),
each layer over the previous ones:

| | Layer | From |
|---|---|---|
| 1 | OKDP's defaults | the file `defaults.<key>` of `okdp.yaml` |
| 2 | computed values | `<key>` of the values file, `values.cel.yaml` or `values.yaml`; `{}` when the file does not write the key ([rendered dependencies](02_service_definition.md#rendered-dependencies)) |
| 3 | upstream values | `upstream.<key>` of the parameters ([Upstream values](#upstream-values)) |

The chart of an OAuth client has layer 2 only (the values the compiler
computes for it: [OAuth client charts](#oauth-client-charts)).

Then `global` is replaced by the `global` of the parameters (the
context's, [Context values](#context-values)), under the `global` of layer
2, and left out when empty: Helm gives every chart one. The `global` of
OKDP's defaults is not kept; layer 3 may not set it (protected). Every
chart thus gets the context's `global`, the platform values among it.

The layers are merged as Helm merges `-f` files: maps merge key by key,
lists and scalars replace (except at [append](#append) paths), a `null`
replaces too and stays `null`.

The chart's own defaults, the `values.yaml` of the key's source (the
upstream chart of the key, [Chart index](#chart-index)), are no layer:
Helm coalesces the final values over them when it renders the chart, as
it coalesces `-f` files ([Equivalence with Helm](#equivalence-with-helm)).
The final values hold what OKDP decides of the chart's values, and nothing
of its defaults: new defaults, in another version of the chart, change no
final value. A check reads them ([checks of the final
values](#checks-of-the-final-values)).

### Nulls

A `null` of any layer stays `null` in `Result.Values`; Helm gives it its
meaning when it renders: over a default, the key is deleted; with no
default, it is kept.

```yaml
# upstream: {app: {podAnnotations: null, newKey: null}}
podAnnotations: null   # the chart's default {}: deleted by Helm
newKey: null           # no default: kept by Helm
```

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
`oci://<repository>/<chart> --version <version>`, or the chart unpacked in
the chart cache.
`testdata/helm/values` holds what Helm computes on each case
(`TestHelmValues`), with Helm 4.3.0 and the versions the deployers embed
([GitOps](../01_usage.md#gitops)): Helm 4.2.4, 4.2.1 and 4.2.0 render as
Helm 4.3.0. Helm 3.19.4 and 3.19.2 coalesce some nulls differently (a
`null` default the values leave unset is kept, a key below the top level
that `values.yaml` and the values both set to `null` is deleted:
`helm3.json`), and render the final values as they render the layers.

### Checks of the final values

With a [chart cache](#chart-cache) (`Inputs.Charts`, `okdp-compile --check
--charts`), the final values of each compiled chart are checked against
its upstream chart; without one, no chart is read and nothing below is
checked. The final values are the same either way.

Each compiled chart whose source has a `values.schema.json` in the
chart cache is checked on its final values coalesced over
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

A schema that does not compile, or holds a number outside the range of a
float64 (`1e400`), fails with its path in the cache, prefixed with the
chart key (`app: charts.example.org/app-1.8.0/app/values.schema.json: ...`).

A list the instance appended to ([append](#append)) where the chart's
defaults hold a list some item of which the final list lacks is a warning
(`Result.Warnings`), one per path, in key order: Helm replaces the
chart's list with the final one, and the instance's items do not extend
it. Items compare by value, in any position; an empty default list, a
path the instance does not append to, or a package that restates the
items gives none:

```
upstream.app.env: appended to the package's list, not to the default list of app-1.8.0, which Helm replaces: the final list lacks 1 of its 2 items; restate them in the package's values of app (its defaults or values file) to keep them
```

## Outputs

`compiler.Compile` returns a `*compiler.Result`:

| Field | Type | Content |
|---|---|---|
| `Values` | `map[string]map[string]any` | chart key -> its final values ([Merge precedence](#merge-precedence)) |
| `Charts` | `map[string]compiler.ChartRef` | chart key -> the upstream chart that renders its values ([Chart index](#chart-index)) |
| `Objects` | `[]map[string]any` | OKDP's own objects, whole manifests: the [credentials Secrets](#credentials-secrets), then the [objects section](#objects-section), then the PushSecrets and ExternalSecrets of the [credentials across namespaces](#credentials-across-namespaces) and the ExternalSecrets of the [credentials in the platform's secret store](#credentials-in-the-platforms-secret-store), in parameter path order; written to [the objects file](#the-objects-file); nil when none |
| `Info` | `compiler.Info` | what the instance publishes: its URL, its usage and the connections it provides ([The info file](#the-info-file)) |
| `Dependencies` | `[]compiler.Dependency` | the instances the parameters reference, by an output or a claim, for a deployer that orders the releases ([Dependencies](#dependencies)); in no file. The binary prints each on stderr, `depends on <instance> (<contract> <output>, <field>)` |
| `Release`, `Namespace` | `string` | `Inputs.Release` and `Inputs.Namespace`, those the values are compiled for, which render them |
| `Service`, `Version` | `string` | the `name` and `version` of `Chart.yaml`, those of the [umbrella chart](#umbrella-chart) |
| `Chart` | `compiler.ChartMetadata` | the package's `Chart.yaml`, as read, its dependencies as written: the metadata of the [umbrella chart](#umbrella-chart) |
| `Warnings` | `[]string` | what the compile warns of without failing, the package's (`Definition.Warnings`): a `values.yaml` that holds expressions ([values.yaml](02_service_definition.md#valuesyaml)); nil when none. The binary prints each on stderr, `warning: <message>` |

`Values` and `Charts` have one entry per compiled chart: every dependency
of `Chart.yaml`, its computed values `{}` when the values file does not
write its key; a dependency the values file leaves out (its key left out
by `$if`, or null) is in neither, and is not to be rendered
([rendered dependencies](02_service_definition.md#rendered-dependencies)),
and in `dcr` mode the chart of each OAuth client. Numbers are float64, as
in Helm values.
`compiler.Write(res)` gives the files the binary writes from them, path in
the output directory -> content ([command line](../01_usage.md#command-line)):
the [umbrella chart](#umbrella-chart), [the info
file](#the-info-file) and [the objects file](#the-objects-file);
`examples/02_advanced/output/` holds those of the example
([Example](#example)).

The keys of a compile are the keys of the `Chart.yaml` dependencies, DNS
labels ([chart keys](02_service_definition.md#chart-keys)), and the compiler's
(the OAuth client charts). `Write` names the umbrella's dependency
aliases after them. It takes a `Result` as `Compile` returns it: its keys,
charts, release and namespace are not checked again.

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
| a dependency of `Chart.yaml` | its `repository`, its `name` as `Chart`, its `version` ([dependencies](02_service_definition.md#dependencies)) |
| the chart of an OAuth client | `global.okdp.charts.oidcDcr`, oidc-dcr 0.5.0 of `oci://quay.io/adaltas` by default ([Platform charts](#platform-charts)) |

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
```

Everything else is keyed by the key
([dependencies](02_service_definition.md#dependencies)). The umbrella's
`Chart.yaml` lists them as its dependencies ([umbrella
chart](#umbrella-chart)); rendering with it:
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
chart is added and the [chart cache](#chart-cache) of a check needs no
oidc-dcr chart; the clients are checked all the same.

| Client | Chart key | Job, ServiceAccount, Role | Secret, `dcr` | Secret, `existing` |
|---|---|---|---|---|
| main (no `name`) | `oidc-dcr` | `<release>-oidc-dcr` | `<release>-<namespace>-dcr` | `creds-<release>-oauth2` |
| `name: n` | `n-oidc-dcr` | `<release>-n-oidc-dcr` | `<release>-<namespace>-n-dcr` | `creds-<release>-oauth2-n` |

The objects' name, `<release>-<chart key>` cut to 63 characters without a
trailing `-` (as `okdpFullname`), is set as `fullnameOverride`,
`security.serviceAccount` and `security.role`; a client name has at most
54 characters, its chart key a DNS-1123 label. In `dcr`
mode a client's `secret` replaces the Secret name, which
[`oidcClientSecret`](04_functions.md#oidcclientsecret) does not
follow, and two clients may not write one Secret
([compile errors](02_service_definition.md#compile-errors)). The
Secret is the release's, the default names above and a `secret` given
alike: its name starts with `<release>-`, checked in every mode, so
that the Job, whose Role writes it, cannot overwrite another Secret of
the namespace, another instance's credentials
(`testdata/cli/oidc.txtar`).

A client chart is no dependency of `Chart.yaml`, and its key is reserved
([oidcClients](02_service_definition.md#oidcclients)): it takes no OKDP defaults
and no instance upstream values ([merge precedence](#merge-precedence)).

The computed values are not Go code: `internal/okdp/oidc-dcr.cel.yaml`, a
template in the mapping language embedded in the compiler, computes them
for each client from the client (its keys, checked),
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
[oidcClients](02_service_definition.md#oidcclients). Helm merges these
maps over the chart's defaults: its default `mapping.keyMapping` entries
stay, so the Secret also holds `registration_client_uri` and
`registration_access_token` of the registration response (oidc-dcr
0.5.0). The example's final values in `dcr` mode ([Example](#example)),
with the platform's settings above (its `platform.yaml`), trimmed:

```yaml
# app
env:
  - name: OIDC_CLIENT_ID
    valueFrom: {secretKeyRef: {key: client_id, name: demo-webapp-demo-dcr}}   # platform.oidcClientSecret
  ...
# oidc-dcr
backoffLimit: 6                        # default of the settings
commonLabels: {app.kubernetes.io/instance: demo-webapp}
fullnameOverride: demo-webapp-oidc-dcr
global: {okdp: ...}
mapping:
  keyMapping:                          # over the chart's default entries
    client_id: .client_id
    client_secret: .client_secret
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
tls: {certificate: certs-bundle, insecure: false}   # global.okdp.tls.caBundleSecret
ttlSeconds: 300                        # global.okdp.oidc.dcr.ttlSeconds
waitForProvider:
  discoveryUrl: https://sso.okdp.example.org/realms/okdp/.well-known/openid-configuration
```

### The info file

`okdp.info.yaml` (`compiler.InfoFile`): `Info`, what the
instance publishes, built from the mapping's `instance` and `provides`
sections ([Sections](02_service_definition.md#sections)): what a control
plane or a console reads of each instance in the deployments repository,
and the outputs the other instances of the namespace reference by name
([Resolution](#resolution), `Inputs.Published`). `compiler.Write` (`-o`)
writes it when the instance publishes something, a URL, a usage or a
provided connection, beside the [umbrella chart](#umbrella-chart), which
gives the service's name and version; Helm ignores it. An instance that
publishes nothing has no info file. It is indented by 2 spaces, its
keys in this order, an empty one (`""`, an empty list or map) not
written:

| Key | Value |
|---|---|
| `url` | `instance.url`, trimmed; absent when unset |
| `usage` | `instance.usage`, trimmed, with a final newline; absent when unset or blank |
| `provides` | the provided connections, a YAML list, in the order of `provides`; absent when none |

One item of `provides` per `provides` item, its keys in this order, an
empty one not written:

| Key | Value |
|---|---|
| `name` | `name` of the item; `<release>` by default |
| `contract` | `contract` of the item |
| `values` | `values` of the item, [completed](#completion) against the contract, keys sorted; absent when it has none |
| `secretRef` | `{name}` of `secretRef`, its `name` a non-empty string (`secretRef: {}` is not set); else `{name: <name>-<contract>-credentials}` when the item has a `secret`; else absent |

```go
type Info struct {
	URL      string   `json:"url,omitempty" yaml:"url,omitempty"`           // what the console opens, trimmed; "" when none
	Usage    string   `json:"usage,omitempty" yaml:"usage,omitempty"`       // how to use the instance, trimmed, with one final newline; "" when none
	Provides []Output `json:"provides,omitempty" yaml:"provides,omitempty"` // the connections it provides, in the order of provides; nil when none
}

type Output struct {
	Name      string         `json:"name,omitempty" yaml:"name,omitempty"`           // the item's name; the instance's when it names none (Inputs.Name)
	Contract  string         `json:"contract,omitempty" yaml:"contract,omitempty"`   // the contract it provides
	Values    map[string]any `json:"values,omitempty" yaml:"values,omitempty"`       // its values, completed against the contract
	SecretRef *SecretRef     `json:"secretRef,omitempty" yaml:"secretRef,omitempty"` // the Secret of its credentials; nil when none
}

type SecretRef struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
}
```

Every item is checked for its shape first, with the mapping's other
sections, every violation at once
([provides](02_service_definition.md#provides)). Then each is checked
against its contract, in the order of `provides`: the fields of its
`values` in alphabetical order. The first error is
reported, as for an expression of the mapping: `provides` is the package's
to fix, not the instance's, and the error is the same from one compile to
the next; the required fields that item lacks, and the schema's violations
of that item, all at once, one line each ([Checks](#checks)). Once every
item passes, two items of one contract and name fail, one line per name.
`<out>`: `provides "<name>" (<contract>)`; `<i>`: the item's position.

| Case | Error |
|---|---|
| an item that is not a map, without a `contract`; its `values`, `secretRef` or `secret` set, not a map; a `secretRef` that is set without a `name`, or whose `name` is not a non-empty string; the `generate` of `secret` not a list, a key of it of another type; its `stringData` not a map, a value not a string (with `generate`, nor a number or a boolean) | `the mapping's sections don't meet the specifications of mapping.schema.json:` then `- at '/provides/<i>/values': got array, want object`, `- at '/provides/<i>/secretRef': missing property 'name'`, one line per violation ([provides](02_service_definition.md#provides)) |
| no `<contract>.schema.json`, or one that cannot be read, is not JSON, not an object or not a valid JSON Schema | `<out>: the <contract> contract cannot be read: <contract>.schema.json: <cause>` |
| not a contract field | `<out>: field "<key>" is not part of the <contract> contract` |
| a secret field | `<out>: "<key>" is a secret field, put it in the credentials Secret` |
| required field not set | `<out>: field "<key>" is required by the <contract> contract`, one line per field |
| a value the contract's schema refuses | `<out>: the fields violate the <contract> contract:` then `- at '/<field>': <violation>`, one line per violation |
| same contract and name twice | `two <contract> outputs are named "<name>"` |

An output named after the instance, the default (`Inputs.Name`, the
release when it is empty), is what other instances reach the instance by,
by its name; a `name` is for several outputs of one
contract: the name other instances reference each by
([Resolution](#resolution)).

### Credentials Secrets

The Secret of a `provides` item with a `secret`: named after the item's
`secretRef` (`<name>-<contract>-credentials` by default), labelled
`okdp.io/credentials-<contract>: "true"`, as are the claimers' copies of
those across namespaces ([credentials across
namespaces](#credentials-across-namespaces)) and the Secrets of the
connections whose credentials are in the platform's secret store
([credentials in the platform's secret
store](#credentials-in-the-platforms-secret-store)). They are first in
`Objects`, in `provides` order.

| `secret` | Objects |
|---|---|
| `{stringData: {...}}` | with `Inputs.AllowPlainSecrets` only, a `v1` `Secret`, `type: Opaque`, `stringData` as given (strings); labels: the labels and the credentials label. Without it, error: `<service>: output "<name>": secret: a plain value (stringData): credentials are generated (generate) or read from the platform's secret store; Inputs.AllowPlainSecrets allows it` |
| `{generate: [<keys>], stringData: {...}}` | per key, a `generators.external-secrets.io/v1alpha1` `Password` named `<secret>-<key>`; then the `external-secrets.io/v1` `ExternalSecret` `<secret>` writing the Secret: the generated keys and the static `stringData` ones (`stringData` optional; strings, numbers or booleans, written as text) |
| neither, in a non-empty `secret` | error: `<service>: output "<name>": secret needs stringData or generate` (`<service>`: `name` of `Chart.yaml`) |

Credentials are never written in clear: the objects file is in the
deployments repository, which every reader of the repository reads, and
in the release's manifests. They are generated in the cluster
(`generate`), or the output points at a Secret another object writes
(`secretRef`: an operator's, an upstream chart's, an ExternalSecret
reading the platform's secret store). `Inputs.AllowPlainSecrets` accepts
a plain Secret, for a caller that knows its repository private, and the
[objects section](#objects-section)'s alike
(`testdata/cli/plain-secrets.txtar`).

A generated Secret is what `okdpGeneratedSecret` renders
([`okdpGeneratedSecret`](04_functions.md#okdpgeneratedsecret): key
options, generator names), the credentials label on the `ExternalSecret`
and on the Secret it writes, not on the `Password` generators. The random
values are generated in the cluster, once (`refreshInterval: "0"`): every
render of the release is the same. For these items:

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
    secret: {stringData: {accessKey: ak, secretKey: sk}}  # AllowPlainSecrets
```

`Objects`, with `Inputs.AllowPlainSecrets`:
`Password/store-a-s3-credentials-accesskey`,
`Password/store-a-s3-credentials-secretkey`,
`ExternalSecret/store-a-s3-credentials`, `Secret/store-b-s3-credentials`;
without it, store-b fails the compile.

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

The platform's policy over `Objects` whole, every source (the credentials
Secrets, the objects section, the generated Secrets, the PushSecrets and
ExternalSecrets), once assembled: an instance of a project that is not
privileged (`global.okdp.policy.privileged`, [fields the compiler
reads](#fields-the-compiler-reads)) installs no cluster-scoped object and
writes no other namespace than its own. An object whose
`metadata.namespace` is set (not `null`, not `""`) to another namespace
than `Inputs.Namespace` is refused, and so is one of a cluster-scoped
kind, by its API group and kind: Kubernetes' own (`Namespace`, `Node`,
`PersistentVolume`, `StorageClass`, `CSIDriver`, `CSINode`,
`VolumeAttachment`, `VolumeAttributesClass`, `ClusterRole`,
`ClusterRoleBinding`, `CustomResourceDefinition`,
`ValidatingWebhookConfiguration`, `MutatingWebhookConfiguration`,
`ValidatingAdmissionPolicy`, `ValidatingAdmissionPolicyBinding`,
`MutatingAdmissionPolicy`, `MutatingAdmissionPolicyBinding`,
`PriorityClass`, `IngressClass`, `IPAddress`, `ServiceCIDR`,
`RuntimeClass`, `APIService`, `CertificateSigningRequest`,
`ClusterTrustBundle`, `FlowSchema`, `PriorityLevelConfiguration`,
`PodSecurityPolicy`, `DeviceClass`, `ResourceSlice`,
`VolumeSnapshotClass`, `VolumeSnapshotContent`, `GatewayClass`) and the
operators' (`ClusterSecretStore`, `ClusterExternalSecret`,
`ClusterPushSecret`, `ClusterGenerator`, `ClusterIssuer`,
trust-manager's `Bundle`, Kyverno's `ClusterPolicy` and
`ClusterCleanupPolicy`, Gatekeeper's `ConstraintTemplate`, KEDA's
`ClusterTriggerAuthentication`, `CiliumClusterwideNetworkPolicy`,
Calico's `GlobalNetworkPolicy`, OpenShift's `SecurityContextConstraints`
and `ClusterResourceQuota`, `ClusterWorkflowTemplate`). An object that
holds `items` (a `List`, which Helm installs item by item) has each item
checked alike. A kind the compiler does not know, without a namespace, is
assumed namespaced: the engine's identity, confined to the namespace, is
the second line ([targets](#targets)). The first refused object, in the
order of `Objects`, fails the compile, named `<kind>/<name>`
(`testdata/cli/policy.txtar`); a privileged project's instance is not
checked:

```
web: objects: ClusterRole/demo-web-reader: a cluster-scoped kind (rbac.authorization.k8s.io/v1): the platform's projects install cluster-scoped objects (global.okdp.policy.privileged), demo is not one of them
web: objects: ExternalSecret/demo-web-ledger: namespace finance, not the instance's (demo): the platform's projects write other namespaces (global.okdp.policy.privileged), demo is not one of them
```

Whatever the project, no Secret (`v1`) of `Objects`, nor of the items of
a `List`, holds values in clear, a non-empty `data` or `stringData`,
unless the caller allows it (`Inputs.AllowPlainSecrets`): credentials are
generated in the cluster
([`okdpGeneratedSecret`](04_functions.md#okdpgeneratedsecret), whose
objects are a `Password` per key and an `ExternalSecret`) or read from
the platform's secret store (an `ExternalSecret`,
[credentials in the platform's secret
store](#credentials-in-the-platforms-secret-store)), never written to the
deployments repository. A Secret with only `metadata` and `type` (a
ServiceAccount token's, which Kubernetes fills) passes. The first
refused, in the order of `Objects`, fails, named `Secret/<name>`, `data`
before `stringData` (`testdata/cli/plain-secrets.txtar`):

```
web: objects: Secret/demo-web-token: a plain value (stringData): credentials are generated (okdpGeneratedSecret) or read from the platform's secret store (an ExternalSecret); Inputs.AllowPlainSecrets allows it
```

### The objects file

`okdp.objects.yaml` (`compiler.ObjectsFile`): `Objects`, which
`compiler.Write` (`-o`) writes beside the [umbrella chart](#umbrella-chart)
when there is at least one, else neither it nor its template: a YAML
stream, one document per object, in the order of `Objects`, `---` between
them, indented by 2 spaces, the keys of each map sorted. Its first line
marks it generated, as the umbrella's values:
`# Code generated by okdp-compile. DO NOT EDIT.`

The umbrella renders it, in the release of the instance:
`templates/okdp.yaml` (`compiler.ObjectsTemplate`), the same in every
output, includes it as written:

```
{{- /* OKDP's own objects: okdp.objects.yaml, as okdp-compile wrote it, not templated. */ -}}
{{ .Files.Get "okdp.objects.yaml" }}
```

Helm does not template the file: the `{{` of an ExternalSecret's template,
or of a string of the objects section, reaches the cluster as written, and
so do its nulls and numbers; each document is one manifest of the release,
installed with those of the upstream charts, sorted by kind
(`testdata/helm/umbrella`; [OKDP's own
objects](../03_okdp_objects.md#how-it-reaches-the-cluster)). The file is
also the manifests as they are: a deployer may apply it without the
umbrella.

An object Helm keeps, annotated `helm.sh/resource-policy: keep` (trimmed,
in any case, as Helm reads it), outlives its release: Helm leaves it
when the release no longer renders it or is uninstalled, and so does
Flux's helm-controller. Argo CD renders the chart with `helm template`
and reads no Helm annotation: it would prune the object, and delete it
with its Application. Whatever wrote it (the mapping's `objects`, a
generated Secret's, the compiler's), each such object of `Objects`
carries Argo CD's equivalents, in its annotation
`argocd.argoproj.io/sync-options`, after the options it holds,
comma-separated: `Prune=false`, not pruned when it leaves the
Application's manifests, and `Delete=false`, not deleted with the
Application; each unless the object sets an option of its name already
(`Prune=confirm` stays):

```yaml
# a claim's database, as the provider's mapping writes it, and in Objects
metadata:
  annotations:
    helm.sh/resource-policy: keep
    argocd.argoproj.io/sync-options: Prune=false,Delete=false
```

The objects of the upstream charts are the charts': one a chart keeps,
Argo CD prunes, unless the chart annotates it so itself.

### Dependencies

`Dependencies` (`compiler.Dependency`): one item per reference to an
instance, an output or a claim ([Resolution](#resolution)), sorted by
`field` (string order), the parameter path as the
[connection errors](#connection-errors) write it, its `instance` a
release name: the one that publishes the output, the claim's provider.
`output`: the output the reference resolved to, by name, the reference
or the claim's ID.
Two references to one instance give two items; external and empty
references give none. `nil` when there is none. Every field of an item
is set: an empty reference gives no item, a reference without its
contract fails.

```go
type Dependency struct {
	Instance  string `json:"instance,omitempty" yaml:"instance,omitempty"`   // the referenced instance, or the one that publishes the referenced output (its release name)
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"` // the namespace of Instance when it is not the instance's: a claim bound to a provider of another namespace
	Contract  string `json:"contract,omitempty" yaml:"contract,omitempty"`   // the contract it must provide
	Field     string `json:"field,omitempty" yaml:"field,omitempty"`         // the parameter that references it, e.g. backends[0].database
	Output    string `json:"output,omitempty" yaml:"output,omitempty"`       // the output the reference resolved to, by name, a claim's ID
}
```

A dependency holds when its `instance` publishes an output named `output`
of `contract`, as resolution found it; the [deployment
graph](#the-deployment-graph) checks it again, and orders the releases
with the list, read from `Result.Dependencies`: each release waits for the
instances it depends on, Flux `dependsOn` and Argo CD sync waves
([GitOps](../01_usage.md#gitops)). No file holds it: `okdp-compile -o`
prints each item on stderr once the files are written, after the warnings,
`depends on <instance> (<contract> <output>, <field>)`, `depends on
demo-pg (database-server pg-hive, db)`, `<namespace>/<instance>` for one
of another namespace, and `--check` none.

### Umbrella chart

`compiler.Write` (`okdp-compile -o <dir>`): the instance as one chart,
for one Helm release per instance, under the release name the values were
compiled for. Two files, [the objects file](#the-objects-file) and its
template when the instance has objects, and `okdp.info.yaml`
([the info file](#the-info-file)) when the instance publishes
something, which Helm ignores, by path in the directory:

- `Chart.yaml`: the package's (`Result.Chart`), every field kept, in
  Helm's order (`apiVersion`, `name`, `version`, `kubeVersion`,
  `description`, `type`, `keywords`, `home`, `sources`, `dependencies`,
  `maintainers`, `icon`, `appVersion`, `deprecated`, `annotations`), its
  `dependencies` replaced by one per key of `Values`, in this order:
  1. the rendered dependencies of the package, every one the values file
     does not leave out
     ([rendered dependencies](02_service_definition.md#rendered-dependencies)),
     in `Chart.yaml` order, each as written (`name`, `alias` when written,
     `repository`, `version`);
  2. the OAuth client charts, by key: the
     [chart index](#chart-index) entry of the key (`name`, `repository`,
     `version`), and `alias: <key>` when the key is not the chart's name;
- `instance.values.yaml` (`compiler.UmbrellaValues`): `Values`, each key's
  final values under the key, each with its own `global`; none at the top.
  Its first line marks it generated:
  `# Code generated by
  okdp-compile. DO NOT EDIT.` (the line Go, GitHub, linters and editors
  recognize);
- `okdp.objects.yaml` and `templates/okdp.yaml`, OKDP's own objects and
  the template that renders them: [the objects file](#the-objects-file).

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
| a key below the top level, null in the chart's `values.yaml`, that the values set to null | kept, null | deleted |
| a null default nested in a map of the chart's `values.yaml`, the values leaving it unset | kept, null | dropped |

Both are a null against an absent key, which a template reads the same but
for `hasKey`, `toYaml` and `range`.

### Example

`examples/02_advanced/output/projects/demo/instances/demo-webapp/`, what
`-o <dir>` writes for the example: release `demo-webapp`, namespace
`demo`, the instance's `config.yaml` (`replicas: 2`,
`database: {}`, a claim, `audit: platform-db`, a memory limit and two `env`
entries in `upstream.app`), over the context
`examples/02_advanced/input/db/platform.yaml`, level 0 (`dcr` mode;
`platform-db`, the connection the instance's `audit` references): the
OAuth client's chart, `oidc-dcr` ([its values](#oauth-client-charts)), a
dependency beside `app`, its Job a pre-install hook that runs
before the release's other resources. The `existing` mode has no example
output: no OAuth client chart, and the client's Secret is
`creds-demo-webapp-oauth2` ([OAuth client charts](#oauth-client-charts)).

`Chart.yaml`:

```yaml
apiVersion: v2
name: webapp
version: 1.0.0
dependencies:
  - name: app
    repository: https://charts.example.org
    version: 1.8.0
  - name: oidc-dcr
    repository: oci://quay.io/adaltas
    version: 0.5.0
```

`instance.values.yaml`, annotated: the values of `app` and those of the
OAuth client's chart:

```yaml
# Code generated by okdp-compile. DO NOT EDIT.
app:
  env:
    - name: OIDC_ISSUER                     # computed
      value: https://sso.okdp.example.org/realms/okdp
    - name: OIDC_CLIENT_ID                  # platform.oidcClientSecret, dcr mode
      valueFrom:
        secretKeyRef:
          key: client_id
          name: demo-webapp-demo-dcr
    - name: OIDC_CLIENT_SECRET
      valueFrom:
        secretKeyRef:
          key: client_secret
          name: demo-webapp-demo-dcr
    - name: DB_URL                          # the claimed database, resolved
      value: jdbc:postgresql://demo-db-db.demo.svc:5432/demo_webapp_database
    - name: DB_PASSWORD
      valueFrom:
        secretKeyRef:
          key: password
          name: demo.webapp.database-credentials
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
oidc-dcr:
  # ... computed by the template (OAuth client charts)
```

`okdp.info.yaml` ([the info file](#the-info-file)): its URL,
its usage and its one output, under the release name, without
credentials:

```yaml
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

No objects file: the instance has no objects, no credentials Secret and no
`objects` section. Its dependency, `Result.Dependencies`
([dependencies](#dependencies)), which the compile prints on stderr:

```text
depends on demo-db (database demo.webapp.database, database)
```

`examples/03_full/` compiles the workspace service, a package written to use
every part of the mapping language, with four values layers:
`platform.yaml` (level 0: `global.okdp` in `dcr` mode, the oidc-dcr
chart from a mirror, a proxy, two connections), `project.yaml` (level 1:
the project's settings under `global`, two connections of its own, a
named claim),
`connections.yaml` (level 2: the instance's own connection) and
`config.yaml` (the instance's values: two claims, a claim template and
the project's named claim, an
output of the namespace and connections of the platform, the project
and the instance itself, in a list of catalogs and under a nested key;
upstream values). Release
`demo-workspace`, namespace `demo`:
`output/projects/demo/instances/demo-workspace/` is what `-o <dir>`
writes. Its `Result.Dependencies`, which the compile prints on stderr,
lists `demo-db`, the provider of its claims, twice, for
`catalogs[1].database` and `database`, and
`demo-alerts` for `notifications.webhook`; the chart `backup`, left out by
`$if`, is no dependency, and its chart is not in the chart cache of the
check. Its
`okdp.info.yaml` lists three outputs, one with its credentials, whose
Secret is generated: its objects file holds the `Password` generators and
the `ExternalSecret`, then the `objects` section.

## Compiling instances together

`CompileAll(ctx, instances)` compiles instances together, each a
`compiler.Instance`: its `Definition`; its `Inputs`, whose `Name`,
`Published`, `Claims` and `Bindings` it sets (the instance's name, what
the other instances of the namespace publish, the claims bound to it, the
providers its claims are bound to); and its `Name`, which a provider
names it by, as by its release, and which names its outputs and its
claims, its release when empty ([providers](#providers)). Two instances
of a namespace of one release, or of one name, are refused, each:
`release "<release>" is given twice in namespace "<namespace>"`,
`instance name "<name>" is given twice in namespace "<namespace>"`. It
gives one `compiler.Outcome` per instance, in their order: its `Result`,
or, `nil`, why it is refused (`Err`), and its claims, bound (`Claims`).
First each [claim](#claims) is bound to its provider, then the instances
are compiled, of every namespace (`Inputs.Namespace`) at once, each
published to its namespace.

The references between the instances are of two kinds:

- to an output: the instance needs what its provider publishes, and is
  compiled after it;
- a claim: the provider it is bound to publishes the claim's output, and
  the instance is compiled after it, of the same namespace or not; the
  provider depends on the claim only, known before anything is compiled.

Which outputs an instance references does not depend on what is
published ([Resolution](#resolution)): its first compile knows every
output it lacks. In the order of the instances:

1. Each instance is compiled with what the instances compiled so far
   publish. A success is final, and publishes its `Info`; a compile that
   lacks outputs no instance has published, of its namespace or a claim's
   of its provider, waits for them; any other refusal is final.
2. An instance that waits is compiled again once every output it waits
   for is published: final, whatever it gives.
3. Those still waiting, for an output nobody publishes or in a cycle, are
   compiled again, everything published, for their message.
4. Two instances of a namespace that publish an output of the same
   contract and name are refused, each: a reference to it would not know
   which. The
   instances whose references resolved through it, or through an output
   of an instance refused since, or whose claim's provider is refused, of
   any namespace, are compiled again: each gets the error a compile after
   every provider gives. A provider refused gives its claims no output.

An instance is compiled once when it comes after the instances whose
outputs it references, twice when it comes before: the outcomes are the
same whatever the order. A refusal for an output nobody publishes names
the instances of the namespace that are refused, and the refused
providers of its claims of another namespace, one of them maybe its
provider. Whether the instances deploy together, each reference to an
instance that exists and provides it, is the
[deployment graph](#the-deployment-graph)'s to check.

| Case | Error |
|---|---|
| a release given twice in a namespace, each time | `release "<release>" is given twice in namespace "<namespace>"` |
| an output another instance of the namespace publishes too | `provides "<name>" (<contract>): <instance> publishes an output of that contract and name too, a reference to it would not know which`; `<instance>, <instance> publish`, several |
| a reference to an output nobody publishes, instances of the namespace refused | its [connection error](#connection-errors), then ` (<instance> does not compile either: what it publishes is unknown)`, or ` (<instance>, <instance> do not compile either: what they publish is unknown)`, in release order, a provider of another namespace `<namespace>/<release>` |

## The deployment graph

`NewGraph(results)` builds the deployment graph of compiled instances,
the `Result` of every instance of their namespaces, a refused one
missing: a release (`GraphNode`, its namespace and release) waits for
the releases its references point to, of its namespace, or of another for
a claim bound across namespaces ([Dependencies](#dependencies)), each
once. It returns the graph only
when every dependency holds and no release waits for itself; else every
problem at once, each a `*GraphError` at its release, by namespace then
release:

| Case | Error, after `<namespace>/<release>: ` |
|---|---|
| a release given twice in a namespace | `release "<release>" is given twice in namespace "<namespace>"` |
| a dependency on a release no result of the namespace has, or on itself | `<field>: it references "<instance>", which is not another instance of namespace "<namespace>"`; of another namespace, `"<namespace>/<instance>", which is not an instance of namespace "<namespace>"` |
| a reference to an output its release does not publish | `<field>: it references the <contract> output "<output>" of "<instance>", which "<instance>" does not publish` |
| releases that wait for each other: from each release, a shortest cycle through it, when it is the cycle's smallest | `dependency cycle: <release> -> <release> -> <release>: the releases would wait for each other` |

A cycle of references never compiles
([Compiling instances together](#compiling-instances-together)): the
graph refuses one in results compiled otherwise. A release that waits for
a cycle is not reported: the cycle is.

The graph gives:

- `DependsOn(node)`: the releases it waits for, by namespace then
  release: Flux's `dependsOn`;
- `Wave(node)`: 0 for a release that waits for none, else one more than
  the highest wave of those it waits for: an Argo CD sync wave. The
  releases of a wave deploy together, after those of the lower waves.

## Targets

A target writes compiled instances for a deployer (`compiler.Target`):
`Files(results, graph)`, given the results of every instance of their
namespaces and their [deployment graph](#the-deployment-graph), gives
the files, path -> content, every one under `projects/`, each instance's
umbrella chart, the files of `Write`, in its directory:

```text
projects/
  kustomization.yaml          flux: the directories of the namespaces
  <namespace>/
    kustomization.yaml        flux: the instances' directories, and project
    project/                  flux, argocd: the project's own manifests
      namespace.yaml          the Namespace, with its Pod Security labels
      secret-store.yaml       the platform's secret store in it
      service-account.yaml    flux: the identity of its releases
      kustomization.yaml      flux: these files
      appproject.yaml         argocd: the AppProject of its Applications
    instances/<release>/      every target: the umbrella chart (Write)
      helmrelease.yaml        flux: the HelmRelease
      kustomization.yaml      flux: the HelmRelease and its values ConfigMap
      application.yaml        argocd: the Application
```

| Target | Options (its fields) | Files besides the umbrella charts |
|---|---|---|
| `compiler.Helm` | none | none: each instance installs with Helm, as one release, its dependencies built first ([rendering with Helm](../01_usage.md#rendering-with-helm)), after those it waits for |
| `compiler.Flux` | `Source`, the GitRepository of the tree, `<namespace>/<name>` (`flux-system/okdp-gitops`); `PathPrefix`, the tree's path in its repository (`""`, its root); `Keep`, namespaces the root lists without a result, projects whose directories the output keeps as they are (`okdp-compile --partial`) | per instance, `helmrelease.yaml` and `kustomization.yaml`; per namespace, `project/` ([the project's manifests](#the-projects-manifests)) and `kustomization.yaml`; `projects/kustomization.yaml`, the root's |
| `compiler.ArgoCD` | `RepoURL`, the repository of the tree, required; `Revision` (`HEAD`); `PathPrefix` | per instance, `application.yaml`; per namespace, `project/` |

The files of the flux and argocd targets are marked generated on their
first line (`# Code generated by okdp-compile. DO NOT EDIT.`). They are
all an engine needs of the compiled tree: okdp-gitops commits the files
of a target as they are, under its output directory (`compiled/`), and
writes none of its own.

The HelmRelease (`flux`): in `okdp-releases`, named after the release,
labelled `okdp.io/project: <namespace>` and `okdp.io/instance:
<release>`; `releaseName` the release, `targetNamespace` and
`storageNamespace` the namespace; `serviceAccountName:
project-<namespace>`, the project's identity, unless the project is one
of the platform's own (`Result.Privileged`), whose releases keep
helm-controller's; the chart
`./<PathPrefix>/projects/<namespace>/instances/<release>` of the
GitRepository `Source`, `reconcileStrategy: Revision`: rebuilt at every
revision, its version, the package's, not changing when a dependency's
does; `dependsOn`, the releases it waits for (`Graph.DependsOn`), absent
when none; its values from the ConfigMap `values-<release>`, key
`instance.values.yaml`, user values; a 15 minutes timeout, the namespace
not created at install (the project's manifests hold it), retried on
failure every 2 minutes, an upgrade rolled back after 3 retries, drift
detection enabled. Its `kustomization.yaml`, in `okdp-releases`: the
HelmRelease, and the ConfigMap generated from `instance.values.yaml`,
without a name suffix, labelled `reconcile.fluxcd.io/watch: Enabled`, a
change of values reconciling the HelmRelease. A namespace's
`kustomization.yaml`: the directories of its instances,
`instances/<release>`, and `project`, sorted. The root's,
`projects/kustomization.yaml`: the directories of the namespaces, those
of `Keep` among them, sorted, `resources: []` when there is none,
written even without instance, so that a tree emptied syncs, its
releases pruned. Neither sets a `namespace`, which kustomize would set
on every resource of every directory under it, moving a project's
manifests out of its namespace: each manifest carries its own.

The Application (`argocd`): in `argocd`, named after the release,
labelled as the HelmRelease; its sync wave,
`argocd.argoproj.io/sync-wave: "<wave>"` (`Graph.Wave`), server-side
diff, the resources finalizer; `project: project-<namespace>`, the
project's AppProject; its source the chart
`<PathPrefix>/projects/<namespace>/instances/<release>` of `RepoURL` at
`Revision`, released as the release with `instance.values.yaml`; destined
to the namespace of the cluster; the CA bundles of webhooks and
conversion webhooks ignored; synced automatically, pruned and
self-healed, server-side, retried forever with a backoff from 10 seconds
to 5 minutes. Pruning is Argo CD's: an
object the render no longer holds is deleted, and so is every object of
an Application deleted, its finalizer cascading, where Helm and Flux
keep those annotated `helm.sh/resource-policy: keep`; Argo CD keeps them
alike through their sync options, `Prune=false` and `Delete=false`, which
the compiler adds to the objects it writes ([the objects
file](#the-objects-file)).

### The project's manifests

Both deployer targets write, for every project that has an instance, its
own manifests in `projects/<namespace>/project/`
(`compiler.ProjectManifestsDir`), each labelled `okdp.io/project:
<namespace>`. They are the project's boundary: its instances are
installed with an identity confined to its namespace, unless the project
is one of the platform's own (`global.okdp.policy.privileged`,
`Result.Privileged`: cert-manager, ingress-nginx, an operator), whose
instances install cluster-scoped objects and write other namespaces with
the engine's own identity.

| File | Content |
|---|---|
| `namespace.yaml` | the Namespace, with the [Pod Security Admission](https://kubernetes.io/docs/concepts/security/pod-security-admission/) labels `pod-security.kubernetes.io/enforce: baseline`, `audit: restricted` and `warn: restricted`; `enforce: privileged` for one of the platform's own projects. Kept by both engines when its project goes, its volumes with it, until an administrator deletes it: labelled `kustomize.toolkit.fluxcd.io/prune: disabled` (Flux) and annotated `argocd.argoproj.io/sync-options: Delete=false` (Argo CD), whatever the target |
| `secret-store.yaml` | when the platform names the provider of its secret store (`global.okdp.secrets.provider`, `Result.SecretStore`): the ServiceAccount `global.okdp.secrets.serviceAccount` (`okdp-secrets`), its token not mounted (External Secrets requests one), and the `SecretStore` named `global.okdp.secrets.store` (`external-secrets.io/v1`), its `spec.provider` as the platform writes it, both in the namespace |
| `service-account.yaml` | `flux`, a project that is not one of the platform's own: the ServiceAccount `project-<namespace>` in `okdp-releases`, which helm-controller impersonates for the project's releases (`serviceAccountName`), the Role `okdp-project` in the namespace, every verb on every resource of every API group (the boundary is the namespace, not the kinds), and its RoleBinding: a cluster-scoped object, or one of another namespace, is refused by the API server |
| `kustomization.yaml` | `flux`: these files, without a `namespace`: each carries its own |
| `appproject.yaml` | `argocd`: the AppProject `project-<namespace>` in `argocd`, `sourceRepos` the `RepoURL`, its destination the namespace of `https://kubernetes.default.svc` and `clusterResourceWhitelist: []`, no cluster-scoped resource; for one of the platform's own projects, every namespace (`"*"`) and every cluster-scoped resource (`{group: "*", kind: "*"}`); a `description` |

One tree, two engines: a caller may write the files of both deployer
targets into one tree (okdp-gitops does), each engine reading its own.
Every file both write is byte for byte the same, the umbrella charts and,
in `project/`, `namespace.yaml` and `secret-store.yaml` (a caller
refuses a path the two write differently); only `appproject.yaml` is
Argo CD's, and `kustomization.yaml` and `service-account.yaml` Flux's.

The platform's entry point applies them before the instances: Flux
through the root Kustomization, which lists each project's directory,
whose `kustomization.yaml` lists `project` with the instances (Flux
applies a Kustomization's Namespaces first, and a HelmRelease whose
ServiceAccount or Role is not there yet fails its install and retries);
Argo CD with an ApplicationSet whose git generator finds the directories
`<PathPrefix>/projects/*/project`, one Application each under the entry
point's own AppProject, which admits Namespaces, AppProjects in `argocd`
and the secret store, as the Applications of the instances are under the
project's. Its source is a directory, `directory: {include:
'{namespace.yaml,secret-store.yaml,appproject.yaml}'}`: in a tree that
holds the Flux files too, Argo CD would otherwise build the directory
with its `kustomization.yaml`, which lists no AppProject and Flux's
identity (an instance's Application is a Helm source, which its
directory's Flux files do not change). The Namespace must exist before
an instance syncs: an Application whose namespace does not exist yet
fails its sync and retries. Network isolation is not the compiler's: no
NetworkPolicy is written, the platform's own applies.

The HelmReleases share `okdp-releases`, the Applications `argocd`: their
names, the releases', must be unique across the namespaces. What a
deployer sets up once is not generated:

- Flux: the GitRepository `Source` of the repository, the namespace
  `okdp-releases`, and a Kustomization of the path
  `./<PathPrefix>/projects`, which reads the root's `kustomization.yaml`,
  then each namespace's;
- Argo CD: an Application that applies the Applications,
  `<PathPrefix>/projects`, `directory: {recurse: true, include:
  '*/instances/*/application.yaml'}`, which needs no file of the target's
  but the Applications (an ApplicationSet's git generator finds the
  namespaces alike, the directories `<PathPrefix>/projects/*`); the
  ApplicationSet of the projects' manifests, over
  `<PathPrefix>/projects/*/project`, a directory source of its three files,
  and the AppProject it is under; and
  the health of an Application in `argocd-cm`
  (`resource.customizations.health.argoproj.io_Application`, its status's
  health): Argo CD assesses none by default, and a sync wave then does
  not wait for the previous one.

A sync wave orders the Applications one parent Application syncs: those
of a namespace must be applied by one. It orders their creation and the
changes to them, not a later change of an instance's chart or values,
which its own Application syncs at once. Flux's `dependsOn` holds at
every reconcile.

| Case | Error |
|---|---|
| a `Source` that is not `<namespace>/<name>`, two DNS-1123 labels | `Flux.Source "<source>": the GitRepository of the tree, <namespace>/<name>` |
| a `PathPrefix` that is absolute, `.`, or has an empty, `.` or `..` segment | `Flux.PathPrefix "<prefix>": the path of the tree in the repository, relative, without an empty, . or .. segment`; `ArgoCD.PathPrefix` alike |
| no `RepoURL` | `ArgoCD.RepoURL is required` |
| a `RepoURL` or `Revision` with a quote, a backslash or a newline | `ArgoCD.RepoURL "<url>": holds a quote, a backslash or a newline`; `ArgoCD.Revision` alike |
| one release name in two namespaces | `release "<release>" is in namespaces "<a>" and "<b>": the HelmReleases of okdp-releases need distinct names` (flux); `the Applications of argocd need distinct names` (argocd) |

## Claims

A provider's package provisions connections of the contracts its
`provisions` lists ([provisions](02_service_definition.md#provisions)): one
per claim bound to an instance, `Inputs.Claims`, each a `compiler.Claim`:

```go
type Claim struct {
	ID        string `json:"id"`             // the connection's name, DNS-1123 labels joined by dots: <namespace>.<name>, or <namespace>.<instance>.<field>
	Contract  string `json:"contract"`       // a contract the package provisions
	Namespace string `json:"namespace"`      // the namespace of the instances that claim it
	Name      string         `json:"name,omitempty"`    // a named claim's name, claims.<name> of the context, the connection of every reference to it in Namespace; "" for a claim template's
	Request   map[string]any `json:"request,omitempty"` // what the claim requests, as the contract's claim schema (x-okdp-claim) allows, its defaults applied
}
```

The mapping reads them as `claims`, its `request` `{}` when the claim has
none, and publishes, for each, an output of its contract named its ID,
what it grants of the request among its values
([provides](02_service_definition.md#provides)). The claims are checked
before the context is built, every problem at once, `<i>` the claim's
position; the outputs after the mapping, every claim without its output at
once:

| Case | Error |
|---|---|
| a claim of a contract the package does not provision | `Inputs.Claims[<i>] (<id>): the package provisions no <contract> connection (okdp.yaml's provisions)` |
| an ID that is not DNS-1123 labels joined by dots, 63 characters at most | `Inputs.Claims[<i>] (<id>): the ID is a claim ID, DNS-1123 labels joined by dots, 63 characters at most` |
| a `Namespace` that is not a DNS-1123 label | `Inputs.Claims[<i>] (<id>): the namespace is a DNS-1123 label` |
| an ID that does not start with its `Namespace` and a dot | `Inputs.Claims[<i>] (<id>): the ID starts with the claimers' namespace, "<namespace>."` |
| an ID given twice | `Inputs.Claims[<i>] (<id>): claimed twice` |
| no output of the claim's contract named its ID | `claim "<id>" (<contract>): provides publishes no <contract> output of that name` |

The words, and what each is in Kubernetes' claims of storage:

| Term | Here | Kubernetes |
|---|---|---|
| claim | a connection a consumer asks of a provider, of a contract | a PersistentVolumeClaim |
| request | what a claim asks (`request`), as the contract's claim schema allows (`x-okdp-claim`) | `resources.requests`, `accessModes` |
| claim template | `{}` in place of a connection's name: a claim of the reference's own, `<namespace>.<instance>.<field>` | a StatefulSet's `volumeClaimTemplates`, a claim per replica |
| named claim | `claims.<name>` of a project, declared once, referenced by name by any instance of the namespace | a PersistentVolumeClaim of the namespace, referenced by name from pods |
| provider | the instance a claim is bound to, which provisions it: its class's, or the one it names (`provider`) | the volume's provisioner, as deployed; a claim's `volumeName`, for the one it names |
| class | `global.okdp.provisioning.<contract>.classes.<name>` of the context: a provider and what it fixes of the request, which a claim names (`class`) | a StorageClass: a provisioner and its parameters |
| default class | `global.okdp.provisioning.<contract>.default`: the class of the claims that name neither a class nor a provider | the default StorageClass |
| provisioner | the package that provisions the contract (`provisions`) | a CSI driver |
| binding | the claim and its provider (`Inputs.Bindings`, `Outcome.Claims`) | the PersistentVolumeClaim's `claimRef` |
| connection | the output the provider publishes for the claim, named its ID | the PersistentVolume |
| credentials Secret | the output's `secretRef`, copied to the claimer's namespace across namespaces | the connection Secret |
| retained | the data the provider keeps when the claim is gone, its mapping's choice | `persistentVolumeReclaimPolicy: Retain` |

Two divergences, on purpose. No selection by matching: a claim is bound to
the provider of its class or to the one it names, never to one chosen for
what it publishes (a `selector`); what a reference requires checks the
connection, it never selects one. No persisted binding: the binding is
computed from the tree at each compile, never stored, so a changed
default class, or a class's provider, moves every claim of it, each to a
new, empty connection, the data left at the former one: the binding lines
of `okdp-compile --repository` show the move before it is deployed.

A consumer claims a connection with a reference that takes claims
(`x-okdp-connection-ref`'s `provision: true`, [declaring a
reference](#declaring-a-reference)): a claim template, a map in place of
a name, or the name of a named claim of its project:

| Claim | Means |
|---|---|
| `{}` | a connection of the reference's own, of the default class |
| `{class: <name>}` | of that class ([classes](#classes)) |
| `{provider: <name>}`, `{provider: <namespace>/<name>}` | of that provider's instance, by its name or its release ([providers](#providers)), whatever the classes; not with `class` |
| `{request: {...}}`, with any | what it requests of its provider ([requests](#requests)) |
| `<name>`, `claims.<name>` of the context | the named claim's connection, one for every reference to it in the namespace |

A default `{}` in the parameter schema claims when the instance says
nothing; a name is a connection's, a named claim's or an output's,
resolved in that order ([resolution](#resolution)).

A named claim is declared once, in its project's context
(`project.yaml`, level 1, or a level above, never level 0, the
platform's, nor an instance's values: [levels](#levels)), each a name of
the namespace, a DNS-1123 label, and no connection's:

```yaml
# examples/04_claims/input/db/projects/demo/project.yaml
claims:
  analytics:
    contract: database-server      # required, the contract of its connection
    class: large                   # optional: the default class of the contract otherwise
    request: {extensions: [postgis]}  # optional: what it requests of its provider
```

It is bound and given to its provider whether a reference names it or not,
as a namespace holds a PersistentVolumeClaim no pod mounts; each reference
to it is a [dependency](#dependencies). A level adds named claims, it
changes none of a lower level's (`claims.<name>`, taken whole). Through
the library, the context of each instance of the namespace declares its
named claims, alike: a name declared differently in two refuses every
instance of the namespace. A package requires what it needs of a named
claim's connection with `require` ([what a reference
requires](#what-a-reference-requires)): the claim is the namespace's, not
the reference's. A claim resolves through its binding, `Inputs.Bindings`,
by parameter path, each a `compiler.Binding`: the provider's instance, the
claim's contract and ID, and the output the provider publishes for it, as
an output reference ([resolution](#resolution)); its provider is a
[dependency](#dependencies), its `namespace` set when it is another
namespace's:

```go
type Binding struct {
	Namespace, Release string  // the provider's instance
	Contract           string  // the claim's contract: the one the output is of
	ID                 string  // the claim's ID: the name of the output it publishes for it
	Output             *Output // that output, nil when it is not known yet (the provider not compiled)
}
```

`CompileAll` binds each claim to a provider, before compiling: among the
instances whose package provisions its contract, of any namespace, the one
the claim names (`provider`, `<name>` of its namespace or
`<namespace>/<name>`, an instance's name or its release:
[providers](#providers)), else the provider of the class it names
(`class`), else of the default class of the contract, of the classes of
the claimer's context ([classes](#classes)); a named claim with the
context of the first instance, in release order, that declares it. A
provider of another namespace serves the claim only through a class of
the platform's, or with its consent, the claimer's namespace among its
claimers ([providers](#providers)). A claim that names neither a class
nor a provider, without a default class, is refused, and so is one
whose provider names two instances, or one of another namespace without
its consent. Its ID is
`<namespace>.<name>` for a named claim, else
`<namespace>.<instance>.<field>`, each part a DNS-1123 label (lowercased,
every run of other characters than letters and digits a `-`, the field
normalized to one), the parts joined by dots: `demo.app.db`,
`demo.app.catalogs-1-database`, `demo.analytics`. No part holds a
dot, so that two sources never give one ID (`<namespace>.<name>` has one
dot, a claim template's two): the claims of instance `a-b` and parameter
`c`, and of instance `a` and parameter `b-c`, are `demo.a-b.c` and
`demo.a.b-c`. The instance is its name (`Instance.Name`), unique in its
namespace, not its release: the project is the namespace already. The
ID is a Kubernetes name, a DNS subdomain: an object name, a Secret name,
a label value and a store key take it as it is; what takes a label only,
a Service name or a host segment, and a SQL identifier, replace its dots in the provider's mapping (`c.id.replace('.',
'_').replace('-', '_')`), which the provider chooses. Two parameters of
one instance whose names normalize to one label (`dbs["eu.main"]` and
`dbs.eu-main`) give one ID: the instance is refused (`<field>: claim: it
gives the same ID (<id>) as <first field>: rename one of the parameters`).
Longer than 63 characters, the ID is cut to 54, and ended with `-` and 8
hexadecimal digits of its SHA-256. The provider gets each claim once, a
named claim's for all the references to it (`Inputs.Claims`, sorted by
ID); the claimer its binding (`Inputs.Bindings`) and `Outcome.Claims`, by
parameter path, each a `compiler.Bound`:

```go
type Bound struct {
	Field              string // the reference's parameter path
	Contract           string // its contract
	ID                 string // the claim's ID: the name of the output its provider publishes for it
	Namespace, Release string // the provider's instance
	Class              string // the class whose provider it is (global.okdp.provisioning.<contract>.classes); "" when the claim names the provider
	Named              bool   // the claim names its class or its provider; else the class is the default one of the claimer's context (global.okdp.provisioning.<contract>.default)
}
```

A binding moves only when the claim's class or provider, the class's
provider or the default class changes: a
second instance that provisions the contract moves none, nor does what
the reference requires, checked against the connection the provider
bound publishes
([what a reference requires](#what-a-reference-requires)). Naming the
provider by its name or by its release is one binding; an instance
added whose name or release is the provider's name refuses its claims,
it moves none ([providers](#providers)). Another
provider gives its claimers a new connection, the former one's data
where it was. `okdp-compile
--repository` prints each binding. A claim template that does not bind
refuses its instance, whose other claims go to no provider; a named claim
that does not bind, every instance whose context declares it, the error
under `claims.<name>` for `<field>`; every problem at once:

| Case | Error |
|---|---|
| the claim names neither a class nor a provider, the context sets no default class | `<field>: claim (<contract>): it names no class, and the context sets no default one, global.okdp.provisioning.<contract>.default (Inputs.Context): the classes are <class>, <class>`; `... (Inputs.Context): no class of <contract> connections is defined (global.okdp.provisioning.<contract>.classes)` when none |
| a class that is none | `<field>: claim (<contract>): class "<class>" is not a class of <contract> connections (global.okdp.provisioning.<contract>.classes): <class>, <class>`; `... classes): none is defined` when none |
| a default class that is none | `<field>: claim (<contract>): the default class "<class>" (global.okdp.provisioning.<contract>.default) is not a class of <contract> connections (global.okdp.provisioning.<contract>.classes): <class>, <class>` |
| a class's provider not of the form | `<field>: claim (<contract>): class "<class>" (global.okdp.provisioning.<contract>.classes.<class>): provider "<provider>": <instance>, <release>, <namespace>/<instance> or <namespace>/<release>`; `the default class "<class>" (...)` |
| a class's provider that names no instance, or one that does not provision the contract | `<field>: claim (<contract>): class "<class>": <namespace>/<name> is no instance that provisions <contract> connections`; `the default class "<class>": ...` |
| the provider named does not | `<field>: claim (<contract>): <namespace>/<name> is no instance that provisions <contract> connections` |
| a provider, named or a class's, that names two instances of its namespace, the release of one and the name of the other ([providers](#providers)) | `<field>: claim (<contract>): <namespace>/<name> names more than one instance, <release> (its release) and <release> (its name): the claim would not know which`, in release order; `class "<class>": ...`, `the default class "<class>": ...` before `<namespace>/<name>` for a class's |
| both a class and a provider | `<field>: claim: class or provider, not both` |
| the instance itself | `<field>: claim (<contract>): the instance provisions it itself: a claim is of another instance` |
| a provider of another namespace, named or a class's of a level above 0, that does not list the claimer's namespace among its claimers, nor `*` ([providers](#providers)) | `<field>: claim "<id>" (<contract>): of <namespace>/<release>, another namespace: <claimer namespace> is not one of its claimers (claimers of instance <namespace>/<release>: <claimer>, <claimer>): the provider lists the namespaces that may claim of it (claimers of its instance.yaml, else of its package's provisions), or the platform routes them through a class of platform.yaml`; `none` when it lists none, `, its package's` after the list when it is its package's; `of <namespace>/<release>, the provider of class "<class>"`, `the provider of the default class "<class>"`, for a class's |
| two claim templates of the instance with one ID | `<field>: claim: it gives the same ID (<id>) as <first field>: rename one of the parameters` |
| a named claim declared differently in the contexts of two instances of the namespace, every instance of it | `claims.<name>: declared differently in the context of <release> and <release>`, the first in release order and the first that differs from it |

An instance whose claims fail to bind reports every one, the claims of its
context first, in name order, then its references in parameter path order
([paths](#paths)): the same errors in the same order from one compile to
the next, whatever order the checks ran in.

A named claim's declaration fails the compile of each instance whose
context holds it, every problem at once, in name order:

| Case | Error |
|---|---|
| `claims` not a map | `claims: the named claims, by name, got <value>` |
| a name that is not a DNS-1123 label | `claims.<name>: claim: its name is a DNS-1123 label` |
| a claim that is not a map | `claims.<name>: a claim, an object, got <value>` |
| another key than `contract`, `class`, `provider` and `request` | `claims.<name>: claim: unknown key "<key>": contract, class, provider and request only` |
| a `request` that is not a map | `claims.<name>: claim: request must be an object, got <value>` |
| a value that is not a string | `claims.<name>: claim: <key> must be a string, got <value>` |
| no `contract` | `claims.<name>: claim: contract is required, the contract of the connection it claims` |
| a `contract` that is no contract name | `claims.<name>: claim: contract "<contract>" is not a contract name` |
| a `provider` not of the form | `claims.<name>: claim: provider "<provider>": <instance>, <release>, <namespace>/<instance> or <namespace>/<release>` |
| a connection's name | `claims.<name>: a connection of that name too (connections.<name>)` |
| in an instance's values | `values layer <n>: claims are declared by the context (Inputs.Context): a project's, of level 1 or above` |
| at level 0 | `context layer <n> (level 0): claims: a project's, of level 1 or above`; a tree's `platform.yaml`: `<repository>/platform.yaml: claims: a project's (project.yaml)` |

### Providers

A provider, a claim's (`provider`), a named claim's or a class's, names
an instance: `<namespace>/<name>`, or `<name>` alone, of the claimer's
namespace, a class's too, whichever level defines the class. The name is
the instance's name or its release, a DNS-1123 label of 53 characters at
most. In a tree (`LoadRepository`), the instance's name is its
directory's, `projects/<project>/instances/<instance>/`, and its release
`<project>-<instance>`: `data/postgresql` and `data/data-postgresql`
both name the instance `postgresql` of the project `data`. Through the
library, the name is `Instance.Name`, the release when it is empty: a
caller that sets the release only names its instances by their release.

The provider is the instance of that namespace whose name or release it
is, among the instances compiled together, whatever they provision; it
must provision the claim's contract. A name that is the release of one
instance and the name of another (the instance `a`, release `demo-a`, and
the instance `demo-a`, release `demo-demo-a`, for `demo/demo-a`) would
name both (the name of two is refused, above): each claim that names it,
or names a class whose provider it is, is refused ([the
errors](#claims)), and the instances it names are compiled. The claim
names the one it means otherwise (`demo/a`, `demo/demo-demo-a`), or one
of them is renamed. The messages, the bindings (`Bound`, `Binding`,
`okdp-compile --repository`) and the dependencies name the provider by
its release, whatever named it.

A provider serves the claims of its own namespace, and those of another
namespace with its consent. The platform's classes
(`global.okdp.provisioning` of level 0, `platform.yaml`, which no level
above changes) route every namespace to their providers, the platform
consenting for them. A claim that names its provider, or a class of a
level above 0 (a project's), binds a provider of another namespace only
when the provider lists the claimer's namespace among its claimers, or
`*`, any namespace; it is refused otherwise ([the errors](#claims)):

```yaml
# projects/data/instances/postgresql/instance.yaml: demo's instances
# may claim of it, by naming it or through a class of demo's
service: postgresql
version: 1.0.0
claimers: [demo]

# catalog/postgresql/1.0.0/okdp.yaml: the default of its instances, any
# namespace
provisions:
  - {contract: database-server, claimers: ['*']}
```

The claimers are the instance's, `Inputs.Claimers`: through the library,
the caller's; in a tree (`LoadRepository`), the `claimers` of its
`instance.yaml`. Absent (nil), they are its package's, the `claimers` of
its `provisions` entry of the claim's contract
([provisions](02_service_definition.md#provisions)), none by default: an
instance's list replaces its package's, an empty one (`claimers: []`)
lists none. `Instance.Claimers(<contract>)` gives the list that applies.
A project thus reaches no provider of another project, nor the
credentials it provisions, by naming it: the provider's owner, or its
package's author, decides who claims of it, and the platform who claims
of its classes. Each entry is a namespace name, a DNS-1123 label, or `*`,
once:

| Case | Error |
|---|---|
| an entry that is neither a namespace name nor `*` | `Inputs.Claimers[<i>]: "<claimer>" is not a namespace name, nor "*"`; a tree's: `<repository>/projects/<project>/instances/<instance>/instance.yaml: claimers[<i>]: ...` |
| an entry listed twice | `Inputs.Claimers: <claimer> is listed twice`; a tree's: `<repository>/.../instance.yaml: claimers: <claimer> is listed twice` |

### Classes

A claim names a class of its contract, not a provider: as a
PersistentVolumeClaim names a StorageClass, never the driver behind it.
The classes are the context's, `global.okdp.provisioning.<contract>`, per
contract a default class and the classes by name, each the provider of
its claims and, optionally, what it fixes of their request
([requests](#requests)):

```yaml
# platform.yaml (level 0): the platform's classes, and the default
global:
  okdp:
    provisioning:
      database-server:
        default: standard
        classes:
          standard: {provider: data/postgresql}
          fast: {provider: data/pg-nvme}

# projects/demo/project.yaml (level 1): a project adds classes of its
# own, of a provider of another namespace with its consent (data/postgresql
# lists demo among its claimers, providers)
global:
  okdp:
    provisioning:
      database-server:
        classes:
          large: {provider: data/postgresql, request: {size: 50Gi}}
```

A class's `provider` is required, `<name>` of the claimer's namespace or
`<namespace>/<name>`, an instance's name or its release
([providers](#providers)); its name a DNS-1123 label. A level adds
classes, and the default of a contract the lower levels leave without
one; it changes neither a class nor a default of a lower level, each
taken whole ([levels](#levels)): the default class is the platform's, as
the default StorageClass is the cluster's, or a project's for a contract
the platform leaves without one. A project that wants another provider
adds a class and names it, its claimers naming no instance; the
provider, of another namespace, serves the class's claims with its
consent only, its claimers listing the project, when a class of the
platform's needs none ([providers](#providers)). A claim may still name
its provider (`provider`), as a PersistentVolumeClaim names a volume
(`volumeName`), never with a class, with the same consent.

A key the class fixes is the class's: a claim whose request sets it, a
template's (the package's or the instance's) or a named claim's
declaration, is refused, every such key, in key order:

| Case | Error |
|---|---|
| a request that sets a key of the class | `<field>: claim: request.<key>: set by the class <class> (global.okdp.provisioning.<contract>.classes.<class>): remove it, or name another class` |

`okdp-compile --repository` prints a binding with its class:
`<field>: claim <id> (<contract>) bound to <namespace>/<release>, class
<class>` for a class named, `, the default class <class>` for the default
one, nothing after the instance for a provider named.

### Requests

A claim requests what the contract's claim schema allows
(`x-okdp-claim`, [format](#format)): what its class fixes
([classes](#classes)), then a claim template's `request`, merged
(`helm -f`) over what the package fixes of every claim of the reference
(`x-okdp-connection-ref`'s `request`), the instance's winning; or a named
claim's, its declaration's, which no package changes, as a pod does not
change a claim it mounts. The claim schema's defaults are then applied,
as a parameter schema's ([defaults](#defaults)), and the request is
checked against it when the claim binds, before any compile, every
violation at once, in the claimer's errors (a named claim's, under
`claims.<name>`, in those of every instance whose context declares it).
The provider's mapping reads it, `claims[i].request`, and answers in what
it publishes:

```yaml
# examples/04_claims/input/db/catalog/postgresql/1.0.0/okdp.yaml, trimmed
provides:
  - $for: c in claims
    $each:
      contract: database-server
      name: ${{ c.id }}
      values:
        host: ${{ release.name }}-rw.${{ release.namespace }}.svc
        dbName: ${{ c.id.replace('.', '_').replace('-', '_') }}
        size: ${{ c.request.size }}
        extensions: ${{ c.request.extensions }}
```

The request is no part of the claim's identity: its ID does not change
with it, nor its binding. What a provider does with a changed request
(grow, ignore) is its own.

| Case | Error |
|---|---|
| a request the claim schema refuses | `<field>: claim (<contract>): the request does not meet the contract's claim schema (x-okdp-claim):` then `- at '/<key>': <violation>`, one line per violation |
| a request of a contract without claim schema | `<field>: claim (<contract>): request: the contract declares no claim schema (x-okdp-claim): a claim requests nothing of it`; an empty one is none |
| a request that is not a map | `<field>: claim: request must be an object, got <value>` |
| a request values cannot hold (`Inputs.Claims`, through the library) | `Inputs.Claims[<i>] (<id>): request: json: <cause>` |

### Credentials across namespaces

A claim bound to a provider of another namespace, its output with a
`secretRef` and its contract with secret fields, has its credentials in
the provider's namespace. They cross through the platform's secret store,
`global.okdp.secrets.store`, under the key `<provider namespace>/<id>`:
the namespace of the provider that pushes them, then the claim's ID,
which starts with the claimers' namespace (`data/demo.app.db`). A
path-based store scopes a writer by the first segment, its own namespace,
and a reader by the first label of the second, its own claims, whichever
namespace provides them ([what the store must
enforce](#what-the-store-must-enforce)); the platform's keys are under
`platform/`, a namespace no project may have. The store is External
Secrets': the SecretStore named `secrets.store` of each project's
namespace, when the platform gives its provider (`secrets.provider`,
[fields the compiler reads](#fields-the-compiler-reads): `Result.SecretStore`,
which the [targets](#targets) write), else the ClusterSecretStore of that
name. The compiler writes both ends, in each instance's objects, after
the mapping's:

- the provider: a `PushSecret` named after the claim's ID, in its
  namespace, that pushes the Secret of the output, each secret field of
  the contract a property of the key, every `secrets.refreshInterval`
  (`1h`); `deletionPolicy: Delete`: the engine prunes the PushSecret with
  the claim, and External Secrets deletes the key with the PushSecret;
- each claimer: an `ExternalSecret` that reads them into a Secret of its
  own, `<release>-<secret>`, the output's Secret after its release, so that
  the claimers of a named claim have one each, every
  `secrets.refreshInterval`; the connection's `secretRef` names it. The
  ExternalSecret and, through its target's template, the Secret carry the
  labels and `okdp.io/credentials-<contract>: "true"`, as the provider's
  credentials Secret does.

```yaml
# data/data-pg, for the claim demo.app.db
apiVersion: external-secrets.io/v1alpha1
kind: PushSecret
metadata: {name: demo.app.db, namespace: data, labels: {...}}
spec:
  refreshInterval: 1h
  updatePolicy: Replace
  deletionPolicy: Delete
  secretStoreRefs: [{name: okdp-secrets, kind: SecretStore}]  # ClusterSecretStore without secrets.provider
  selector: {secret: {name: demo.app.db-credentials}}
  data:
    - match: {secretKey: password, remoteRef: {remoteKey: data/demo.app.db, property: password}}
---
# demo/demo-app, its claim db
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata: {name: demo-app-demo.app.db-credentials, namespace: demo, labels: {..., okdp.io/credentials-<contract>: "true"}}
spec:
  refreshInterval: 1h
  secretStoreRef: {name: okdp-secrets, kind: SecretStore}
  target:
    name: demo-app-demo.app.db-credentials
    creationPolicy: Owner
    template: {metadata: {labels: {..., okdp.io/credentials-<contract>: "true"}}}
  data:
    - {secretKey: password, remoteRef: {key: data/demo.app.db, property: password}}
```

Within a namespace, the Secret is already there: neither is written.
Without the store, such a claim is refused, on both sides:

| Case | Error |
|---|---|
| the provider | `claim "<id>" (<contract>): claimed of namespace <namespace>: its credentials go through the platform's secret store, global.okdp.secrets.store, which is not set` |
| the claimer | `<field>: claim "<id>" (<contract>): of <namespace>/<release>, another namespace: its credentials go through the platform's secret store, global.okdp.secrets.store, which is not set` |

The key lives with the claim. A claim that moves to a provider of another
namespace (its class, or the class's provider, changed) gets a new key
under the new provider's namespace, pushed once the new provider's
release is deployed, and the former key is deleted with the former
PushSecret: between the two, the claimer's ExternalSecret reads a key
that is gone and keeps the Secret it last wrote, retrying at every
refresh until the new key is there, then writes the new credentials. The
Secret of a claim that moves back under the same provider is the same
Secret, the key written again. To rotate the credentials of a claim,
delete the provider's credentials Secret (`<id>-credentials` in the
example, which its ExternalSecret and `Password` generator regenerate,
[credentials Secrets](#credentials-secrets)): the PushSecret pushes the
new values at its next refresh, the claimers read them at theirs; to
hurry either, `kubectl annotate pushsecret <id> force-sync=$(date +%s)`
in the provider's namespace, `kubectl annotate externalsecret
<release>-<secret> force-sync=$(date +%s)` in the claimer's. The
provider's package decides when the connection itself follows (a
database role whose password is its Secret's, read at each reconcile,
follows at once).

The credentials of a connection kept in the store, `secretRef:
{remoteKey: <key>}`, are read alike, by an ExternalSecret of each
instance that references it, under the key the connection names, the
platform's under `platform/` ([credentials in the platform's secret
store](#credentials-in-the-platforms-secret-store)).

#### What the store must enforce

The compiler writes the keys; the store keeps them apart, and the compiler
cannot check that it does. A `PushSecret` writes under any key its
author names, an `ExternalSecret` reads any key its author names, and a
package's [`objects`](02_service_definition.md#objects) may write either,
reviewed with the package; a connection of a project or an instance
names the key its instances read (`secretRef.remoteKey`), which the
compiler keeps under the namespace's own segment ([credentials in the
platform's secret store](#credentials-in-the-platforms-secret-store)),
but the compiler sees no object the engine applies beside its own: what
separates the namespaces' credentials is the store's own authorisation,
which the key layout lets it express. Every key is `<segment>/<rest>`:
`<namespace>/<id>`, the credentials a namespace pushes, under its own
name, the ID starting with the namespace that reads them;
`platform/<key>`, the platform's connections'. A path-based store
(OpenBao, Vault, a cloud secret manager) with one policy, applied to the
identity of each namespace:

- writes, creates and deletes under its own namespace, `<namespace>/*`:
  its PushSecrets push there, and delete there when a claim is gone;
- reads the claims of its own, `+/<namespace>.*`: the second segment
  starts with its name and a dot, whichever namespace pushed it, and no
  other namespace's (`demo.*` is not `demo-x.*`: a namespace is a DNS-1123
  label, without dot);
- reads the platform's keys, `platform/*`.

With `secrets.provider`, the store is the SecretStore of each
namespace, authenticating with the ServiceAccount `secrets.serviceAccount`
(`okdp-secrets`) of that namespace: the identity is the namespace's, and
one templated policy scopes all of them. With OpenBao (or Vault), a KV v2
mount `okdp` (`secrets.provider`: `vault: {server, path: okdp, version:
v2, auth: {kubernetes: {mountPath: kubernetes, role: okdp,
serviceAccountRef: {name: okdp-secrets}}}}`,
`examples/04_claims/input/db/platform.yaml`), one Kubernetes auth role
`okdp` bound to the ServiceAccount `okdp-secrets` of any namespace
(`bound_service_account_names=okdp-secrets`,
`bound_service_account_namespaces=*`, `token_policies=okdp`), and the
policy `okdp`, templated on the namespace of the ServiceAccount the token
was issued to (`auth_kubernetes_XXXX`: the accessor of the Kubernetes
auth mount, `bao auth list -format=json`). External Secrets pushes
through `okdp/data/<key>` and reads it before pushing, marks and deletes
through `okdp/metadata/<key>`, and reads `okdp/data/<key>`:

```hcl
# okdp: the policy of okdp-secrets of each namespace, templated on it.
# Its own keys, the credentials of the claims it provides: pushed, read
# back to compare, deleted when a claim goes (PushSecret).
path "okdp/data/{{identity.entity.aliases.auth_kubernetes_XXXX.metadata.service_account_namespace}}/*" {
  capabilities = ["create", "read", "update", "delete"]
}
path "okdp/metadata/{{identity.entity.aliases.auth_kubernetes_XXXX.metadata.service_account_namespace}}/*" {
  capabilities = ["create", "read", "update", "delete"]
}
# The credentials of its own claims, whichever namespace provides them
# (ExternalSecret).
path "okdp/data/+/{{identity.entity.aliases.auth_kubernetes_XXXX.metadata.service_account_namespace}}.*" {
  capabilities = ["read"]
}
# The platform's keys, the credentials of its connections.
path "okdp/data/platform/*" {
  capabilities = ["read"]
}
```

The platform writes `platform/<key>` as the operator of the store, and
nothing of the compiler's needs more of it: a project named `platform`
would own the platform's keys, and is refused ([inputs and
outputs](#inputs-and-outputs)). A cloud secret manager scopes its
identities alike, on the path of the key (AWS: a resource policy on
`okdp/<namespace>/*`, with the mount as the prefix of every key).

Without `secrets.provider`, the store is a ClusterSecretStore, one
identity for every namespace (its `conditions` admit the namespaces of
the tree, and no other): the store cannot tell a namespace from another,
every namespace it admits reads every key, and the tree is one trust
domain, its namespaces trusting one another with the credentials of the
claims that cross; the compiler still keeps a project's connections
under its own segment. A backend without paths, External Secrets'
Kubernetes provider among them
(a key is a Secret's name, which holds no `/`), cannot hold the keys of
the claims across namespaces: it is not supported for them, only for the
connections of a platform that names its keys as Secret names, one
trust domain.
