# Writing a package

How to package an upstream application for OKDP: a directory of
parameters, OKDP's defaults and a service definition, compiled by
okdp-compiler (the `okdp-compile` of its
[command line](01_usage.md#command-line), or a host that embeds it). The
steps follow a `webapp` service, of which the example of this
repository is a smaller version
(`examples/02_advanced/input/catalog/webapp/1.0.0/`, which calls a
function of the example's rule set, `examples/02_advanced/rules`;
`examples/00_passthrough/input/catalog/echo/1.2.0/` is the smallest
package, a chart and its values,
`examples/01_minimal/input/catalog/hello/1.0.0/` the smallest with a
mapping, `examples/04_claims/input/catalog/` a provider and its claimers,
each commented); the recipes and pitfalls come from the services packaged
in `OKDP/platform-charts` and `OKDP/sandbox-charts`. A package that serves
a host, signs its users in or adds workloads of its own does so with the
security rule set: okdp-compiler-security's [writing a
package](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/02_writing_package.md),
whose examples use every part of the mapping language. What each file may
contain is specified in [the service
definition](specs/02_service_definition.md), its [control
flow](specs/03_control_flow.md) and [functions](specs/04_functions.md),
and [inputs and outputs](specs/01_inputs_outputs.md), the command line in
[usage](01_usage.md), the internals in [architecture](00_architecture.md).

## What a package is

A package is a directory of a git repository, laid out as a Helm chart
over its upstream charts, not published as one. It carries the
parameters an instance sets (`config.schema.json`, their defaults
included), OKDP's defaults for each upstream chart
(`<key>.defaults.yaml`), and the service definition. `Chart.yaml` names
the service and the upstream charts it is made of, its dependencies, each
by its name, repository and pinned version; `okdp.yaml` says what the
service needs of the platform, which defaults each chart takes and what an
instance may override, and holds the mapping's sections that are not chart
values: `vars`, `errors`, OKDP's own objects, the instance's URL and
usage, the provided connections, and the sections a rule set adds;
`values.cel.yaml`, the values file, says how the values of each chart
follow from the parameters, the platform values (`global.okdp`) and the
connections.
A compile turns the values of one instance into the final values of
every chart, OKDP's own objects and what the instance publishes
(`okdp.info.yaml`); a deployer then renders each upstream chart from its
repository with its values, as the
[chart index](specs/01_inputs_outputs.md#chart-index) names it. Nothing of
the package is published as a chart.

```
<name>/
├── Chart.yaml              name, version, the upstream charts (dependencies), Helm's other metadata
├── okdp.yaml               required platform values, roles, dependsOn, OKDP's defaults, upstream values, protect, append; vars, errors, OKDP's own objects, URL and usage, provided connections, a rule set's sections
├── values.cel.yaml         the computed values of each chart (values.yaml when none is computed)
├── config.schema.json      the parameters: types, defaults, rules, x-ui-* hints, x-okdp-connection-ref
├── <key>.defaults.yaml     OKDP's defaults of each chart: plain YAML
├── ci/platform.yaml        the platform values of the test cases (global.okdp): a case's Settings values
├── ci/<case>-values.yaml   test instances, without global: a case's Instance values
└── README.md               parameters, platform values read, charts, known gaps
```

A package is a version directory of a catalog,
`catalog/<service>/<version>/`, which its `Chart.yaml` names ([the
catalog](specs/01_inputs_outputs.md#the-catalog)); `okdp-compile
--catalog` reads it, `-f` the input documents of the instances that use
it ([input documents](01_usage.md#input-documents)). The compiler reads no
upstream chart: checking the final values against the upstream charts'
defaults and schemas, from a chart cache, is a host's
([chart cache](specs/01_inputs_outputs.md#chart-cache)).

## Integrating a chart

Start from `Chart.yaml`, the chart as a dependency, and a plain
`values.yaml`: the chart's values the platform sets, under the
dependency's key, as a chart's `values.yaml` holds them
(`examples/00_passthrough/input/catalog/echo/1.2.0/`). Every
dependency is rendered, with its chart's defaults when the file does not
write its key: `Chart.yaml` alone is a package
([rendered dependencies](specs/02_service_definition.md#rendered-dependencies)).
Add `okdp.yaml` when the service needs more: platform values, OKDP's
defaults, instance upstream values, `vars` and OKDP's sections (URL and
usage, connections, objects, a rule set's sections); and `config.schema.json`
when an instance has parameters. Once a value needs an expression or a
directive (`${{ }}`, `$if`), rename the file `values.cel.yaml`: the
compiler reads both alike, but a `values.yaml` that holds them, which
Helm would not read as the same values, compiles with a warning
([values.yaml](specs/02_service_definition.md#valuesyaml)):

```text
warning: values.yaml holds expressions or directives (${{ }}, $-keys): name it values.cel.yaml
```

A package has one values file: both fail. `values.yaml` is read as the
mapping is, not as Helm reads it: YAML 1.2 (`yes` and `on` are text), and
an alias (`*name`) refused; write such values out in full.

## Step by step

### 1. Choose the upstream charts

Each upstream chart the service is made of is a dependency of
`Chart.yaml`: the chart's name, its chart repository (`https://`) or OCI
registry path (`oci://`), and a pinned version. The package holds
no copy of it: a deployer renders the chart from its repository, over its
defaults. The compiler reads none of it; a host's check may read its
`values.yaml` and `values.schema.json` from a chart cache, filled with
`helm pull --untar`, each chart unpacked under its repository's
directory, the URL without its scheme
([chart cache](specs/01_inputs_outputs.md#chart-cache)).

```yaml
# Chart.yaml (airflow)
dependencies:
  - name: airflow             # the chart; its key too, without an alias
    repository: https://airflow.apache.org
    version: 1.22.0
```

```bash
helm pull airflow --repo https://airflow.apache.org --version 1.22.0 --untar -d "<chart cache>/airflow.apache.org/airflow-1.22.0"
```

- The key, the `alias` when given, else the chart's `name`, names the
  chart's values everywhere (the values file, `defaults` and `upstream` of
  `okdp.yaml`, the instance's `upstream.<key>`, its values in
  `values.yaml`). It is a DNS label:
  lowercase letters, digits and `-`, 63 characters at most
  ([chart keys](specs/02_service_definition.md#chart-keys)).
- `Chart.yaml` is read strictly, as Helm defines it: a field it does not
  define fails the load rather than being ignored. Helm's `condition`,
  `tags`, `enabled` and `import-values` fail too: the values file decides
  what is rendered
  ([dependencies](specs/02_service_definition.md#dependencies)).
- A check that needs a chart the cache lacks fails with the `helm pull
  --untar` command that fetches it.
- A chart rendered twice is two dependencies of one chart, each with its
  alias ([recipe](#render-a-chart-twice)).
- The chart is rendered as published. A bundled subchart the package does
  not use is disabled by values, in OKDP's defaults, and protected (superset
  disables the bundled `postgresql` and `redis` and runs a Valkey of its
  own). Part of a chart alone (trust-manager's CRDs, in cert-manager) cannot
  be rendered: render the chart with its switches, or leave the part to
  another package. CRDs upgraded with the release (spark-operator) are a
  deployer setting: `upgrade.crds: CreateReplace` on the Flux HelmRelease;
  Argo CD applies `crds/` on every sync.

Flux (`helm install`) and Argo CD (`helm template`) must render the same
objects. An upstream feature built on `lookup`, a random function, `genCA` or
`.Capabilities` breaks that: keep it disabled and protect its switch
(step 4).

### 2. Name the package

```yaml
# Chart.yaml
apiVersion: v2
name: webapp
version: 2.3.0-1.0.0      # <upstream>-<okdp semver>
dependencies:             # step 1
  - {name: app, repository: https://charts.example.org, version: 1.8.0}
  - {name: metrics, repository: oci://registry.example.org/charts, version: 0.4.2}
```

```yaml
# okdp.yaml
roles: [webapp]           # what the service provides in the catalog
dependsOn:                # the platform services it needs
  - database-server
```

`apiVersion: v2`, `name` and `version` are required, and `version` is
SemVer: Helm requires it of the chart, and the console compares versions.
Its upstream half stays SemVer (`480.0.0-1.0.0` for Trino 480);
release-please owns the OKDP half. Helm's other fields (`description`,
`maintainers`, `annotations`...) are Helm's, not read. The mapping sees
`name` and `version` as `service` (`service.name`, `service.version`); the
labels of OKDP's objects take `app.kubernetes.io/name` and
`okdp.io/service` from the name. `roles` and `dependsOn`, in `okdp.yaml`,
are the console's: the compiler does not read them
([Chart.yaml](specs/02_service_definition.md#chartyaml),
[okdp.yaml](specs/02_service_definition.md#okdpyaml)).

### 3. Design the parameters

Keep the surface small: sizing, switches, connection references, and the few
settings that mean something on the platform. Any other value of an upstream
chart is reached through `upstream.<chart>`, without a parameter.

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": false,
  "required": ["host"],
  "properties": {
    "global": {"type": "object", "description": "Platform values: the context's layers, the platform's level then higher ones adding to it, never an instance's."},
    "replicas": {"type": "integer", "minimum": 1, "default": 1, "title": "Replicas", "x-ui-group": "Sizing"},
    "host": {"type": "string", "minLength": 1, "pattern": "^[a-z0-9.-]+$", "title": "Host"},
    "database": {
      "type": "string",
      "default": "",
      "x-okdp-connection-ref": {"contract": "database"},
      "not": {"pattern": "\\{\\{|\\}\\}|[\\r\\n]"}
    },
    "metrics": {
      "type": "object",
      "properties": {
        "enabled": {"type": "boolean", "default": false},
        "interval": {"type": "string", "default": "30s"}
      }
    },
    "upstream": {
      "type": "object",
      "default": {},
      "additionalProperties": false,
      "properties": {"app": {"type": "object", "allOf": [{"$ref": "#/definitions/noTemplate"}]}}
    }
  },
  "definitions": {
    "noTemplate": {
      "not": {"type": "string", "pattern": "\\{\\{"},
      "propertyNames": {"not": {"pattern": "\\{\\{"}},
      "additionalProperties": {"$ref": "#/definitions/noTemplate"},
      "items": {"$ref": "#/definitions/noTemplate"}
    }
  }
}
```

- The schema is optional, but the compiler takes the parameters' defaults from
  it, finds the connection references through it, and checks the values
  against it before the mapping runs
  ([schema check](specs/01_inputs_outputs.md#schema-check)): a rule
  on a parameter belongs here, not in the mapping's `errors`. Declare
  `$schema`. With `additionalProperties: false`, declare `global` and
  `upstream` too.
- A `default` gives a parameter its value when no layer sets it, as a
  chart's `values.yaml` does: on a property reached from the root through
  `properties` only, never under `allOf`, `items`, `additionalProperties`,
  `definitions`... (one there is not read), nor on both an object and a
  property under it (the object's wins). `metrics` above gets its defaults
  from its properties' ([defaults](specs/01_inputs_outputs.md#defaults)).
- `global` is the platform's: an instance that sets it fails. A key the
  schema declares under it (`"global": {"properties": {"imageRegistry":
  ...}}`), never `okdp`, is one an instance may set, and every chart
  receives it
  ([context values](specs/01_inputs_outputs.md#context-values)). A
  schema that declares `global.okdp`, or gives it a default (`"global":
  {"default": {"okdp": ...}}`), is refused: the platform's settings come
  from the context alone.
- `connections` is not a parameter: the context gives the connections, an
  instance's own at a level of its own (`connections` of its Instance
  document), and the schema does not declare it; a declaration has no effect
  ([reserved keys](specs/01_inputs_outputs.md#reserved-keys)).
- A string pasted into a configuration file, a script or a template refuses
  `{{`, `}}` and line breaks (the `not` pattern above, as in trino, airflow,
  superset and polaris). The compiler refuses `{{` in every instance value
  anyway ([layers](specs/01_inputs_outputs.md#layers)); the pattern makes
  the console refuse it before committing, and covers the rest.
- `x-okdp-connection-ref` makes a string a [connection
  reference](specs/01_inputs_outputs.md#connections), on the items of a
  list too (trino's `hiveCatalogs[].metastore`), on a property reached
  from the root through `properties` and `items` only: one under `allOf`,
  `$defs`, `additionalProperties`... is refused, no compile would read
  it. Give it `"type": "string"`: the compiler refuses any other value
  but `null`, after the schema check. Its `require`, a JSON Schema, is
  what the service needs of the connection beyond its contract:
  `{"properties": {"engine": {"const": "postgresql"}}, "required":
  ["secretRef"]}` for a service that speaks PostgreSQL only and reads a
  password. Write it there rather than as an
  `errors` rule of the mapping: it is checked whatever the connection
  comes from, an external connection, an output or a claim, and the error
  names the reference, the connection and its provider ([what a reference
  requires](specs/01_inputs_outputs.md#what-a-reference-requires)).
- `upstream` declares one object per chart key that accepts instance values:
  the inline values of any other chart would be ignored without a word. The
  compiler refuses `{{` in them, as in every instance value (upstream
  charts pass some values to `tpl`); `noTemplate` makes the console refuse
  it before committing.
- `title`, `description` and the `x-ui-*` hints (`x-ui-group`, `x-ui-order`,
  `x-ui-advanced`, `x-ui-condition`...) build the console form.
- Without `config.schema.json`, the package has the empty schema, `{}`: no
  parameter has a default, any value passes the check (any parameter,
  `upstream.<key>` of any key), and an instance sets no key
  of `global` ([package files](specs/01_inputs_outputs.md#package-files)).

### 4. Write okdp.yaml

```yaml
require:                                 # read by the mapping in every case
  - storageClass.data
defaults:
  app: app.defaults.yaml                 # OKDP's defaults, plain YAML; metrics has none
upstream:
  app:                                   # the instance's upstream.app; metrics: none
    protect: [fullnameOverride, service.port, auth]
    append: [env]
```

- `okdp.yaml` is optional, and read strictly: a key it does not define, a
  misspelt `require` among them, fails the load rather than being ignored;
  so does a key of `defaults` or `upstream` that no dependency has
  ([okdp.yaml](specs/02_service_definition.md#okdpyaml)). The mapping's
  sections follow its package keys (step 5).
- `require` lists the `global.okdp` keys the mapping reads in every case: the
  compile fails, naming the key, when one is missing or empty. A value needed
  in one mode only is checked by a rule of the mapping's `errors`
  ([recipe](#require-a-platform-value-in-one-mode)).
- `defaults` names, by key, the file of OKDP's defaults of a chart: what
  depends on no parameter, platform value or connection:
  images, probes, switches, static configuration. The mapping's values go
  over it, the instance's over both
  ([merge precedence](specs/01_inputs_outputs.md#merge-precedence)).
- `upstream` lets an instance set any value of a chart, under
  `upstream.<key>` of its values: `true` for every dependency, or by key,
  `true`, `false` or `{protect, append}`
  ([upstream](specs/02_service_definition.md#upstream)). Leave out
  a chart an instance has no reason to set (the webapp's `metrics`).
  Whatever it accepts, a host that applies the upstream policy of the
  security set denies a project's instances the keys of a pod's
  privileges, identity, containers and objects (`hostPath`,
  `serviceAccount`, `sidecars`, `extraObjects`...) and may hold their
  images to the platform's registries ([upstream
  values](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/02_writing_package.md#upstream-values)):
  a parameter is the way to offer one of them, the package reviewed with
  it.
- `protect` refuses instance values on a path, under it, or a non-map on the
  way to it ([protect](specs/01_inputs_outputs.md#protect)).
  Protect what the platform relies on:
  - names other objects or contracts address: `fullnameOverride`, Service
    names and ports (what `provides` publishes is built from them);
  - authentication: the sign-in settings, auth types, access control;
  - the exposure: the switches, hosts and certificates the service is
    reached by (the host is published in `okdp.info.yaml`);
  - the wiring of Secrets: `existingSecret`, `*SecretName`, any key that
    would put a credential in the values;
  - the switches of features kept off for determinism (`admissionController`,
    `serviceMonitor`), and the RBAC and ServiceAccounts another service owns.

  Protect the narrowest path: `service.port` leaves `service.annotations`
  open, `auth` refuses everything under it. A key containing dots is written
  in brackets, a family of keys with a final glob
  ([recipe](#keys-containing-dots)).
- `append` lists the lists the mapping fills that an instance should extend
  (`env`, `extraVolumes`, `extraVolumeMounts`, `extraArgs`): the instance's
  items come after the computed ones; any other list of the instance
  replaces the computed one. A path listed in both stays protected
  ([append](specs/01_inputs_outputs.md#append)). A templated string
  (airflow's `extraEnv`) cannot be appended to: protect it and point users at
  another list.

### 5. Write the mapping

The mapping is YAML shaped like the upstream values, with `${{ CEL }}`
[expressions](specs/02_service_definition.md#expressions) and
[directives](specs/03_control_flow.md) (`$if`, `$else`, `$for`,
`$merge`, `$spread`, `$vars`). The values of the charts are the values file,
`values.cel.yaml`, by key; the other
[sections](specs/02_service_definition.md#sections) follow the package keys
of `okdp.yaml`. As the webapp writes them (the
[evaluation order](specs/02_service_definition.md#evaluation-order)
puts `vars` and `errors` first, `objects` and a rule set's sections
last):

```yaml
# values.cel.yaml: chart key -> the values computed for it
app:
  fullnameOverride: ${{ release.name }}-app   # the name the http contract promises
  replicaCount: ${{ values.replicas }}
  persistence: {storageClass: '${{ global.okdp.storageClass.data }}'}
metrics:
  $if: ${{ values.metrics.enabled }}   # left out: the chart is not rendered
  target: http://${{ release.name }}-app.${{ release.namespace }}.svc:8080/metrics
```

```yaml
# okdp.yaml, after the package keys
vars:                      # names and hosts computed once, in order
  host: ${{ values.host }}
  db: '${{ type(values.database) == map ? values.database : {} }}'
  sessionSecret: ${{ okdpFullname('session') }}

errors:                    # a message fails the compile when its $if holds
  - $if: ${{ values.replicas > 1 && db.size() == 0 }}
    $value: "webapp: replicas > 1 needs a database, sessions are stored there"

objects:                   # OKDP's own objects, whole manifests
  - $spread: >-
      ${{ okdpGeneratedSecret({'name': sessionSecret, 'keys': [{'key': 'session-key', 'length': 48}]}) }}

instance:                  # url and usage, in okdp.info.yaml
  url: https://${{ host }}
  usage: |
    A web application. Open https://${{ host }}

provides:                  # the connections the instance publishes
  - contract: http
    values:
      url: https://${{ host }}
      internalUrl: http://${{ release.name }}-app.${{ release.namespace }}.svc:8080
```

- `vars` computes every name, host and repeated structure once; a binding
  sees the previous ones, and hides a
  [context](specs/02_service_definition.md#context) variable of the
  same name (`labels`). The values file reads the bindings too (`host`).
  A platform that applies the security set holds a project to hosts of
  its own form, and the package then builds the host from the platform's
  suffix rather than a parameter
  ([hosts](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/02_writing_package.md#hosts-ingress-and-tls)).
- The values file holds only what is computed, by key. Every dependency is
  rendered, as Helm renders a chart's dependencies: one the file does not
  write gets `{}` as its computed values, OKDP's defaults, the instance's
  upstream values and, by Helm, its chart's defaults still applied. A
  `$if` on a chart, or a null value (`backup: ~`), leaves it out: it is
  neither compiled nor rendered (the webapp's `metrics`, trino's
  `opa-kube-mgmt` and `opal`). A key a top-level `$merge` or `$for` gives
  counts only when it gives it: to leave a dependency out, write its key
  ([rendered
  dependencies](specs/02_service_definition.md#rendered-dependencies)).
  Any other value that is not a map fails the compile. OKDP's own objects
  are not values: they reach the cluster through the okdp-objects chart,
  whatever the file leaves out ([the objects
  chart](specs/01_inputs_outputs.md#the-objects-chart)).
- `errors` fails the compile with every message at once. A message is the
  `$value` of an item with its condition, `$if`; `$for` checks every
  element of a list.
  Prefix each message with the service and the parameter, and say what to set.
- `objects` are whole manifests (a `kind` and a `metadata.name` at least)
  the okdp-objects chart renders as written, after the credentials Secrets
  (a `{{` in them is not Helm's): generated Secrets, hooks, custom
  resources, a workload the upstream charts lack. A component without an
  upstream chart (cnpg-postgresql) declares no dependency, has no values
  file, and writes everything here.
- `instance.url` is what the console opens (none without a UI); `usage` is
  shown with the instance. Both are strings: write a number with `str`.

Besides CEL's own, [host functions](specs/04_functions.md#host-functions)
(`str`, `sha256`, `toJson`, `okdpFullname`, `okdpLabels`,
`okdpGeneratedSecret`...) reproduce Helm's functions and give OKDP's
names, labels and Secrets; a rule set adds its own.

### 6. Consume and provide connections

A parameter carrying `x-okdp-connection-ref` names an entry of
`connections` (external, from the context: the platform's, the
project's, or the instance's own at a level of its own), or an output
another instance of the namespace publishes, an instance's own named after
its release by default. Before the mapping runs, the compiler replaces the
name with the connection, completed against its contract ([what the
mapping receives](specs/01_inputs_outputs.md#what-the-mapping-receives)).
A reference left empty stays `""` (or `null`); a name that is not an entry
of `connections` must be an output's name.

```yaml
# the instance's Instance document: its own connections, the context at
# level 2, and its values, an external database
kind: Instance
project: demo
name: webapp
service: webapp
version: 1.0.0
connections:
  shared-db:
    contract: database
    engine: mysql
    host: db.example.org
    port: 3306
    dbName: webapp
    secretRef: {name: shared-db-credentials}   # never the password itself
values:
  database: shared-db
```

A connection whose credentials are in the platform's secret store says
so, `secretRef: {remoteKey: <key>}` (an S3 store the platform neither
deploys nor manages): the compiler reads them into a Secret of the
instance's namespace, `<release>.<connection>-credentials`, with an
ExternalSecret among the instance's objects, and the mapping reads its
`secretRef.name` as any other's ([credentials in the platform's secret
store](specs/01_inputs_outputs.md#credentials-in-the-platforms-secret-store)).

In the advanced example, `database: {}` instead claims a database of its
own, which `demo-db`, the provider of the platform's default class of
database connections, publishes under the claim's ID: host
`demo-db-db.demo.svc`, Secret `demo.webapp.database-credentials`, and
a dependency (`Result.Dependencies`), which a host orders the releases
with (Flux `dependsOn`, Argo CD sync waves, in its files; claims
below). The mapping reads both the same way, credentials
through a `secretKeyRef` on `secretRef.name`; the reference requires them,
`"require": {"required": ["secretRef"]}`
(`examples/02_advanced/input/catalog/webapp/1.0.0/config.schema.json`),
and a connection without a Secret is refused before the mapping runs.
cnpg-postgresql publishes one `database-server` output per database: an
instance of the namespace references it by that name (`database:
pg-hive`), which resolves to the output's values and `secretRef`, and
depends on the cnpg-postgresql instance; an instance of another namespace
claims it, or gets it as a connection, its credentials Secret being in the
provider's namespace
([resolution](specs/01_inputs_outputs.md#resolution)). The compile is
given what the other instances of its namespace publish
(`Inputs.Published`).
See [contracts](specs/01_inputs_outputs.md#contracts).

Provide a connection in `provides`. Its values are checked against the
contract, its schema included (a `port` is a number, not a string built by
an expression). An output is published under the instance's name, the
default `name` (`db` for the instance `name: db` of the project `demo`,
not its release `demo-db`): other instances reach the instance by it. Its
values are what the instance is reached at: build the hosts and Service
names once, in `vars`, for the values and `provides` alike. A `name` is
for several
outputs of one contract (cnpg-postgresql's `database-server` outputs,
one per database): the name the other instances of the namespace reference
each by, unique per contract in the namespace, or their references fail.
Then say where the credentials come from:

```yaml
provides:
  - contract: s3
    values:
      apiUrl: https://${{ host }}
      internalUrl: http://${{ okdpFullname('s3') }}.${{ release.namespace }}.svc:9000
    # Generated by ESO into <release>-s3-credentials, the default secretRef.
    secret:
      generate:
        - {key: accessKey, length: 20, symbols: 0}
        - {key: secretKey, length: 40, symbols: 0}
```

`secret` (`generate`, with `stringData` for the keys that are not
secret): the compiler renders the credentials Secret, generated in the
cluster (rustfs); a `stringData` alone, credentials in clear in the
deployments repository, fails unless the caller sets
`Inputs.AllowPlainSecrets`, and so does a Secret of `objects` with
`data` or `stringData` ([credentials
Secrets](specs/01_inputs_outputs.md#credentials-secrets)). `secretRef`
alone: an operator or an upstream chart creates it (cnpg-postgresql).
Neither: consumers bring their own identity (seaweedfs). An output never
points at a Secret nobody creates.

A consumer may instead claim a connection, and let the instance that
provisions its contract create it ([claims](specs/01_inputs_outputs.md#claims)):
a reference with `provision: true`, which accepts a map, `{}` by default:

```json
"database": {"type": ["string", "object"], "default": {},
             "x-okdp-connection-ref": {"contract": "database-server", "provision": true,
                                       "request": {"extensions": ["pgvector"]},
                                       "require": {"properties": {"engine": {"const": "postgresql"}}, "required": ["secretRef"]}}}
```

A contract has two sides: what a provider publishes, its `properties`
(and the `x-okdp-secret` fields of its credentials Secret), and what a
claim may request of the provider, its claim schema, `x-okdp-claim`,
with defaults and bounds (a size, the extensions of a database). The
package fixes what the service needs of every claim of the reference
(`request`: pgvector), the instance's `{request: {size: 5Gi}}` merged
over it; both are checked against the claim schema when the claim
binds, its defaults applied.

An instance then says nothing for a database of its own, of the default
class the platform or its project sets
(`global.okdp.provisioning.database-server.default`), the claim template;
`{class: large}` for one of another class, the platform's or one its
project adds, each a provider and what it fixes of the request; or
`{provider: data/postgres2}` for one of a provider it names, an
instance, `<namespace>/<instance>` (or its release,
`data/data-postgres2`); a name references an existing connection, or a
named claim its project declares (`claims: {analytics: {contract:
database-server}}` in its Project document's values), one database for
every instance that names it. Of a named claim, a package requires what
it needs with `require`, as of a connection: the claim is its project's,
and so is what it requests, its declaration's `request`, which the
package's does not change. Without a default class, a claim that names
neither a class nor a provider is refused. The mapping reads the claim's
connection as any other, checked against what the reference requires
(`require`): a provider that publishes what the service cannot use
refuses the claimer, which names a class whose provider meets it; the
compiler never picks one. A key a class fixes of the request is the
class's: a package's `request` that sets it refuses the claims of that
class.

A provider declares the contracts it provisions, `provisions`, and
creates, for each claim bound to its instance (`claims`), what the
connection needs, as the claim requests it (`c.request`, the claim
schema's defaults applied), publishing it under the claim's ID, with what
it granted. Keep what it holds
when the claim is gone: the claimer may come back, under the same ID
(`examples/04_claims/input/catalog/postgresql/1.0.0/okdp.yaml`):

```yaml
provisions: [database-server]
objects:
  - $for: c in claims
    $each:
      apiVersion: postgresql.cnpg.io/v1
      kind: Database
      metadata: {name: '${{ c.id }}', annotations: {helm.sh/resource-policy: keep}}
      spec: {name: "${{ c.id.replace('.', '_').replace('-', '_') }}", owner: '${{ c.id }}', cluster: {name: '${{ release.name }}'},
             extensions: "${{ c.request.extensions.map(e, {'name': e}) }}", databaseReclaimPolicy: retain}
provides:
  - $for: c in claims
    $each:
      contract: database-server
      name: ${{ c.id }}
      values: {host: '${{ release.name }}-rw.${{ release.namespace }}.svc', dbName: "${{ c.id.replace('.', '_').replace('-', '_') }}",
               size: '${{ c.request.size }}', extensions: '${{ c.request.extensions }}'}
      secretRef: {name: '${{ c.id }}-credentials'}
      secret: {generate: [{key: password}], stringData: {username: '${{ c.id }}'}}
```

A claim's ID is a Kubernetes name, DNS-1123 labels joined by dots
(`demo.app.db`): an object name, a Secret name or a label value takes
it as it is; a SQL name replaces its dots and dashes, as above, and a name
that takes a label only (a Service, a host segment) its dots, a choice of
the provider's.

The compiler carries the credentials of a claim of another namespace
through the platform's secret store ([credentials across
namespaces](specs/01_inputs_outputs.md#credentials-across-namespaces)).
An instance serves such a claim through a class of the platform's, or
with its consent: the namespaces its Instance document lists
(`claimers`), else those of the package's entry, `{contract:
database-server, claimers: ['*']}` for a server every project may claim
of; none by default
([providers](specs/01_inputs_outputs.md#providers)).

### 7. Compile, render and check

Each `ci/<case>-values.yaml` is an instance: its parameters, without
`global` and `connections`, which an instance may not set. A case is
compiled from input documents ([input
documents](01_usage.md#input-documents)), the catalog's directory given
as `--catalog`, its `catalog/` and `contracts/`: a `Settings` document of
the platform values (`global.okdp`), `ci/platform.yaml` and, for a case
that needs others, its own `ci/<case>-platform.yaml` after it in
`valuesFiles`; and an `Instance` document of the project `demo`, the
release `demo-<service>`, naming the package (`service`, `version`), its
`valuesFiles` the case's values, its `connections`, inline, the case's
own connections.
`scripts/compile-charts.sh` builds one per case. Write one case per
mode: internal and external connections, proxy set, each optional chart
on, upstream values with appended lists, and each mode of the settings a
rule set reads (the security set's:
[test cases](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/02_writing_package.md#test-cases)).
A case whose platform requires a rule set (`global.okdp.ruleSets`) fails
a compile not given it: `okdp-compile` takes it with `--rules`, and a
host may give the security set to every compile.

```bash
# /tmp/polaris-case/inputs.yaml, ci/platform.yaml and the case's values beside it:
#   kind: Settings
#   valuesFiles: [platform.yaml]
#   ---
#   kind: Instance
#   project: demo
#   name: polaris
#   service: polaris
#   version: <version>
#   valuesFiles: [polaris-values.yaml]
okdp-compile -f /tmp/polaris-case --catalog . --check   # compile and check, write nothing
okdp-compile -f /tmp/polaris-case --catalog . -o /tmp/out
i=/tmp/out/projects/demo/instances/demo-polaris
chart=$(mktemp -d) && cp -R catalog/polaris/<version>/. "$chart" && helm dependency build "$chart"
helm template demo-polaris-okdp ../okdp-objects-chart -n demo -f $i/okdp-objects.values.yaml
helm template demo-polaris "$chart" -n demo -f $i/values.yaml
scripts/compile-charts.sh catalog/polaris/<version>   # every case, compiled and rendered
```

- Render with the release name and namespace of the compile,
  `<project>-<name>` and the project, `demo-polaris` and `demo`: the
  upstream charts derive their names from the release.
- `helm dependency build` fetches the upstream charts of the package's
  `Chart.yaml`, `polaris-admin-principals` the chart of `polaris-admin`
  under its own alias, in a copy of the package, so that the catalog's
  directory gets no `Chart.lock` nor `charts/`; the okdp-objects chart
  renders OKDP's own objects as written.
- The library gives the final values alone, `values.yaml` and
  `okdp-objects.values.yaml`, for the catalog's chart as it is
  ([installing an instance](01_usage.md#installing-an-instance)).
- A case whose first line is `# error: <message>` must fail with that
  message (`scripts/compile-charts.sh`). Add one per `errors` entry, per
  protected path that matters, per schema rule.

[Troubleshooting](01_usage.md#troubleshooting) explains the error messages.

## Recipes

### Hosts, sign-in and workloads

The host a service is reached at, the OAuth clients its users sign in
with and the workloads a package adds beside its charts are rules of the
security set: the form of a project's hosts, the section `oidcClients`
and the hardened fragments (`okdpPodSpec`, `okdpJob`,
`okdpSignInProxy`, `okdpWebWorkload`), in okdp-compiler-security's
[writing a
package](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/02_writing_package.md).

### The platform's CA bundle

A service that calls another of the platform over TLS trusts the
platform's CA bundle: the Secret `global.okdp.tls.caBundleSecret` of its
namespace, `ca.crt` (PEM) and `bundle.p12` (PKCS#12), as a trust-manager
Bundle writes one. Mount it only when the platform sets it, never a name
of the package's own (`certs-bundle`): unset, the platform's certificates
are a public CA's, which the image trusts already.

```yaml
# values.cel.yaml
app:
  extraVolumes:
    $if: ${{ global.okdp.?tls.?caBundleSecret.orValue('') != '' }}
    $value: [{name: ca-bundle, secret: {secretName: '${{ global.okdp.tls.caBundleSecret }}'}}]
```

### Who may use the service

A realm's roles are the platform's, every project's people hold them: a
service that lets anyone with a role of the realm in runs the code of a
project (a notebook, a flow, a UI that submits jobs) for the people of
the others. The people of a project are its groups, the roles of the
`access` of its values, level 1 (`okdp-project-<namespace>`, an editor,
without one), and the compiler gives them to the mapping:

| Variable | Value |
|---|---|
| `platform.access.viewers` | the groups with any role in the project, and the platform's administrators |
| `platform.access.editors` | the groups with the editor or the admin role, and the platform's administrators |
| `platform.access.admins` | the groups with the admin role, and the platform's administrators |
| `platform.access.platformAdmins` | the platform's administrators alone (`global.okdp.access.admins`) |

Each list is sorted, once each name, the names the identity provider's
tokens give the groups (no prefix). A package that restricts who signs in
defaults its allowed groups to them, a parameter replacing the default
only to narrow or to widen on purpose:

```yaml
# values.cel.yaml
app:
  auth:
    allowedGroups: ${{ values.?allowedGroups.orValue(platform.access.editors) }}
```

A service with a role of its own maps them: viewers read, editors run,
admins administer. Never a default of the realm's roles (`data_engineer`),
which every project's people hold.

### Render a chart twice

Two renders of one upstream chart (polaris-admin's bootstrap and principals
phases) are two dependencies of `Chart.yaml` with the same chart, each
under its alias, its key:

```yaml
# Chart.yaml
dependencies:
  - name: polaris-admin
    alias: polaris-admin               # optional: the chart's name is the key
    repository: https://charts.example.org
    version: 1.0.0
  - name: polaris-admin                # the same chart
    alias: polaris-admin-principals    # the values of a second render
    repository: https://charts.example.org
    version: 1.0.0
```

```yaml
# okdp.yaml
defaults:
  polaris-admin: polaris-admin.defaults.yaml
  polaris-admin-principals: polaris-admin-principals.defaults.yaml
upstream:
  polaris-admin:                       # the instance's upstream.polaris-admin
    protect: [fullnameOverride, phase, realms]
```

```yaml
# values.cel.yaml
polaris-admin:
  realms: ${{ adminRealms }}
polaris-admin-principals:
  realms: ${{ adminRealms }}
  principals:
    polaris: {url: "${{ internalUrl }}"}
```

Both keys render with the chart, over its defaults, and a check holds both
to its `values.schema.json`, each with its own values file: the index maps
both to the same chart ([chart
index](specs/01_inputs_outputs.md#chart-index)). Everything else is keyed by the
key ([dependencies](specs/02_service_definition.md#dependencies)). Without
an alias, the second would have the key of the first, and fail the load.
The second render accepts no instance upstream values: an entry of its key
in `upstream` would read the instance's
`upstream.polaris-admin-principals`, with its own `protect` and `append`
([upstream](specs/02_service_definition.md#upstream)).

### Generated secrets

Nothing random is computed at compile time: passwords and keys come from
External Secrets Operator generators, generated once in the cluster.

```yaml
# okdp.yaml
vars:
  keep:                     # outlive the release: the database they encrypt does
    helm.sh/resource-policy: keep   # Argo CD's Prune=false,Delete=false added
objects:
  - $spread: >-
      ${{ okdpGeneratedSecret({'name': okdpFullname('internal'), 'annotations': keep,
          'keys': [{'key': 'jwt-secret', 'length': 64},
                   {'key': 'fernet-key', 'length': 32, 'transform': 'b64enc'}]}) }}
  - $spread: >-
      ${{ values.builtInAdmin ? okdpGeneratedSecret({'name': okdpFullname('admin'),
          'keys': [{'key': 'password', 'length': 24}]}) : [] }}
```

Helm and Flux keep what `helm.sh/resource-policy: keep` marks; the
compiler adds Argo CD's equivalents, `argocd.argoproj.io/sync-options:
Prune=false,Delete=false`, to each such object it writes ([the objects
chart](specs/01_inputs_outputs.md#the-objects-chart)).

Point the upstream chart at the Secret (`*SecretName`, `existingSecret`) and
protect that path: the chart's own templates would draw a new value on every
render. A generated Secret is frozen once written: to change its keys, give
it a new name.

### Restart the pods when a configuration changes

```yaml
# okdp.yaml
vars:
  settings:
    APP_URL: https://${{ host }}
    METRICS_ENABLED: ${{ str(values.metrics.enabled) }}
objects:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: ${{ okdpFullname('settings') }}
      labels: ${{ okdpLabels('settings') }}
    data: ${{ settings }}
```

```yaml
# values.cel.yaml
app:
  podAnnotations:
    checksum/settings: ${{ sha256(toJson(settings)) }}
```

`toJson` sorts the keys: the checksum changes with the content only. Hash
the text the pod reads when there is one (seaweedfs `checksum/iam`, keycloak
`checksum/realm`).

### Conditional items, maps from lists

```yaml
env:
  - $if: ${{ db.size() > 0 }}                 # one item
    name: DB_DRIVER
    value: ${{ db.driver }}
  - $for: "secret in (values.smtp.enabled ? [okdpFullname('smtp')] : [])"
    $each:                                     # a group of items, on one condition
      - name: SMTP_USER
        valueFrom: {secretKeyRef: {name: "${{ secret }}", key: username}}
      - name: SMTP_PASSWORD
        valueFrom: {secretKeyRef: {name: "${{ secret }}", key: password}}
catalogs:                                      # trino: one catalog file per item
  $if: ${{ catalogs.size() > 0 }}
  $for: c in catalogs
  $key: ${{ c.name }}
  $value: ${{ c.properties + '\n' }}
```

A `$for` over a list of zero or one element binds the value the group uses
(superset, spark-history-server). A list computed in an expression is
spliced with `$spread`.

### Fragments

A fragment is a function of a rule set that returns objects, or a part
of one, from its arguments, checked against the mapping schema under
its name: a workload written once and reviewed once, rather than by
hand in each package. It is a CEL function, its result `$spread` into
`objects`. The compiler's own is `okdpGeneratedSecret`
([generated secrets](#generated-secrets)); the security set gives the
hardened pod and the workloads built on it, a Job, a web service, a
sign-in proxy
([workloads of the package](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/02_writing_package.md#workloads-of-the-package));
a platform's own set may give others
([functions](specs/04_functions.md)). A package that needs what a
fragment refuses writes its objects itself.

### Labels

OKDP's objects take OKDP's labels, their selectors OKDP's selector labels:
`labels` and `selectorLabels`, or, for one component of the instance,
`okdpLabels('cache')` and `okdpSelectorLabels('cache')` (superset's Valkey
Deployment and Service). The console finds an instance's workloads by
`app.kubernetes.io/instance`. An upstream chart that leaves it out of its
pods usually has a value for extra labels: set it (polaris-console
`podLabels`, airflow `labels`).

### Names from a claim's ID

A provider names what it creates for a claim after the claim's ID
(`<namespace>.<name>`): a database, a role, a keyspace, a topic's object.
Never `c.id.replace('-', '_').replace('.', '_')`: `a-b.c` and `a.b-c`
are both `a_b_c`, and the claim of one namespace would own the other's
(two claimers, one role). Call `okdpSqlName(c.id)` for an identifier of
`[a-z0-9_]` (`okdpSqlName(c.id, 48)` for a store with a shorter limit,
Cassandra's keyspaces), `okdpObjectName(c.id)` for a Kubernetes name:
one name per ID, cut with a hash of the ID past the limit, never a name
that starts with `system` (`okdpSqlName`). Test it with two claimers
whose IDs collide under a lossy mapping, and the compile refuses two
objects of one kind, namespace and name
([functions](specs/04_functions.md#okdpsqlname-and-okdpobjectname)).

### Database connection

```yaml
# okdp.yaml
vars:
  db: '${{ type(values.database) == map ? values.database : {} }}'
```

```yaml
# values.cel.yaml
app:
  env:
    - $if: ${{ db.size() > 0 }}
      name: DB_URL
      value: jdbc:${{ db.engine }}://${{ db.host }}:${{ str(db.port) }}/${{ db.dbName }}
    - $if: ${{ db.size() > 0 }}
      name: DB_PASSWORD
      valueFrom: {secretKeyRef: {name: "${{ db.secretRef.name }}", key: password}}
```

The same lines serve a database another instance publishes and an
external one. A required connection is checked in `errors`
(hive-metastore, polaris, superset), with what the service supports
(polaris refuses any engine other than `postgresql`).

### Proxy

`platform.proxyEnv` holds `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`, the
empty ones left out. Merge it into an environment map, or build list items
in a fixed order:

```yaml
extraEnv:                                       # a map (trino's OPAL server)
  OPAL_POLICY_REPO_MANIFEST_PATH: ${{ values.OPAL_POLICY_REPO_MANIFEST_PATH }}
  $merge: ${{ platform.proxyEnv }}
env:                                            # a list (superset)
  - $spread: >-
      ${{ ['HTTPS_PROXY', 'HTTP_PROXY', 'NO_PROXY'].filter(k, k in platform.proxyEnv)
          .map(k, {'name': k, 'value': platform.proxyEnv[k]}) }}
```

### Require a platform value in one mode

`require` in `okdp.yaml` lists the values of every case
([require](specs/02_service_definition.md#require)). A value of one mode
is checked by a rule of the mapping's `errors`, with the condition of the
mode:

```yaml
# okdp.yaml
errors:
  - $if: ${{ values.persistence && global.okdp.?storageClass.?data.orValue('') == '' }}
    $value: global.okdp.storageClass.data is required with persistence. It is a context value
```

Read an optional platform value with `.?` and `orValue`
(`global.okdp.?storageClass.?data.orValue('')`): a missing key fails the
expression otherwise.

### Configuration files and serialized values

Write a file as a block scalar with expressions in it, and serialize data
with the Helm functions rather than by hand: `toPrettyJson` (keycloak's
realm), `toYaml` (okdp-examples' realms), `quote` for a string in Python or
shell (airflow, superset). Data pasted into code is safer encoded: superset
passes user formats as `base64.encode(bytes(toJson(d3Format)))`. Template
code meant for the upstream chart's `tpl` stays literal
(`{{ .Values.configMountPath }}`); `$${{` writes a literal `${{`, and `$$`
a key starting with `$` (`$$schema: ...` is the key `$schema`).

### Keys containing dots

Annotations, Spark properties and environment maps have keys with dots. In
a `protect` or `append` path, write such a key in brackets, and a family of
keys with a final glob
([protect and append](specs/02_service_definition.md#protect-and-append)):

```yaml
# okdp.yaml
upstream:
  app:
    protect:
      - 'podAnnotations["sidecar.istio.io/*"]'   # every sidecar.istio.io/ annotation
      - 'config["spark.ui.filters"]'             # one key, dots included
```

An instance setting one of them fails:

```text
app: upstream.app.podAnnotations["sidecar.istio.io/inject"]: podAnnotations["sidecar.istio.io/*"] is set by the platform and cannot be changed
```

A rule no path expresses (a value that depends on a parameter) goes in
`errors`: the mapping sees the instance's upstream values, as
`values.upstream.<key>`.

## Pitfalls

- **`: `, ` #` and flow collections.** A conditional (`a ? b : c`) or a
  message with a colon breaks the YAML, and `{` and `,` are syntax in
  `[...]` and `{...}`: fold the expression with `>-`, or quote the whole
  scalar (`'${{ ... }}'`, `hosts: ["${{ host }}"]`)
  ([quoting](specs/02_service_definition.md#quoting)).
- **Quotes do not make a string.** `${{ expr }}` alone in a scalar keeps its
  type, quoted or not: `"${{ values.replicas }}"` is the number 1. Text
  around it makes a string (`${{ values.memoryGi }}Gi`). An environment
  value, an annotation or a ConfigMap entry needs `str(...)`.
- **Numbers are doubles.** `values.replicas * 2` has no overload, write
  `* 2.0` or `int(values.replicas) * 2`; `str()` prints 1000000 as `1e+06`
  ([numbers](specs/02_service_definition.md#numbers)).
- **YAML 1.1 and 1.2.** OKDP's defaults and the upstream charts' defaults
  are read as Helm reads values, YAML 1.1: plain `no`, `off`, `y` are
  booleans, `0440` is the octal 288; quote a string that looks like one
  (`"no"`). The mapping is read as YAML 1.2: there `no`, `off`, `y` are
  strings, write `false` and `true`
  ([YAML reading](specs/02_service_definition.md#yaml-reading)), in the
  package keys of `okdp.yaml` too (`upstream: false`).
- **Untested branches are not evaluated.** Every expression compiles when
  the mapping is loaded, in every branch (a misspelled name fails every
  compile), but one in a branch no case takes is never evaluated: a type
  error (`no such overload`) or a missing key passes until a case takes it
  ([expressions](specs/02_service_definition.md#lone-expressions-and-interpolation)):
  give every branch a `ci/` case that takes it, each mode of the
  platform's settings the mapping reads included.
- **A null deletes the default.** A key computed as `null`
  (`${{ values.?x.orValue(null) }}`) removes the upstream chart's default at
  render: leave the key out with `$if`. A parameter set to `null` by a values
  layer loses its default: read it with `values.?x.orValue(<default>)`.
- **Lists replace.** A list of the mapping replaces the list of OKDP's
  defaults, and an instance's list replaces the mapping's unless the path is in
  `append`. Keep each list wholly in OKDP's defaults or in the mapping.
- **Names longer than 63 characters.** `${{ release.name }}-something`
  overflows with long project and instance names: a release has up to 53
  characters (Helm's limit, checked by the compiler), which leaves 10 for
  `-<suffix>`. Use `okdpFullname('something')`, cut to 63 characters.
- **One object, one name.** Give each object of a kind its own name in its
  namespace (the credentials Secrets included): applied, the second would
  replace the first.
- **No credential in values.** Connections and values carry `secretRef`
  names, never passwords: values end up in Git and in readable objects. A
  secret field in a connection or in `provides` fails the compile.
- **No literal version in the mapping.** Pin the image tag in OKDP's
  defaults, next to the chart's default: a literal in the mapping is missed
  when the chart is bumped.
- **An unset connection is `""`.** `values.database.host` fails on an empty
  reference: test `type(...) == map` first, or report it in `errors`.
- **`okdp-objects` is the compiler's chart key.** A `Chart.yaml` dependency
  or a key of the values file of that name fails, in every mode.
- **A dependency is pinned.** `version` is one version, not a range
  (`^1.2`, `1.x` fail): bump it in `Chart.yaml`, then pull the new version
  into a host's chart cache. The final values do not change with it: they
  hold none of the chart's defaults.

## Checklist

- [ ] Every dependency of `Chart.yaml` has its name, repository and
  version, pinned; a bundled subchart the package does not use is disabled
  by values; no `lookup`, random or `.Capabilities` feature is on.
- [ ] `Chart.yaml` and the values file do not use the compiler's chart key
  (`okdp-objects`); a second render of a chart is a
  dependency of the same chart with its alias.
- [ ] `Chart.yaml`: `apiVersion: v2`, `name`, `version`
  `<upstream>-<okdp semver>`; `okdp.yaml`: `roles` and `dependsOn`.
- [ ] `config.schema.json` declares `$schema`, every parameter with its
  rules, `global`, and `upstream` for exactly the charts that accept
  instance values, no `connections`; strings pasted into files refuse `{{`,
  `}}` and line breaks.
- [ ] `require` lists the platform values read in every mode, `errors`
  checks the others in their mode; `protect` covers names, authentication,
  exposure, Secret wiring and the switches kept off; `append` the lists
  carrying the platform's wiring.
- [ ] The mapping writes no literal image tag, no credential, no name that
  can exceed 63 characters; every OKDP object is labelled.
- [ ] `provides` publishes what the instance is reached at, under the
  release name, and says where the credentials come from.
- [ ] A host, a sign-in or a workload of the package follows the security
  set's checklist
  ([writing a package](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/02_writing_package.md#checklist)).
- [ ] One `ci/` case per mode and per branch, one failing case per
  check; `scripts/compile-charts.sh` compiles them all and renders each
  chart with the source the index names.
- [ ] `README.md` lists the parameters, the platform values read, the charts
  and when each is compiled, and the known gaps.
