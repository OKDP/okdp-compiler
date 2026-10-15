# Writing a package

How to package an upstream application for OKDP: a directory of parameters,
OKDP's defaults and a service definition, compiled by `okdp-compile`. The steps
follow a `webapp` service, of which the example of this repository is a
smaller version (`examples/02_advanced/input/db/catalog/webapp/1.0.0/`;
`examples/00_passthrough/input/db/catalog/echo/1.2.0/` is the smallest
package, a chart and its values,
`examples/01_minimal/input/db/catalog/hello/1.0.0/` the smallest with a
mapping, `examples/03_full/input/db/catalog/workspace/3.2.0-1.4.0/` one
that uses every part of the mapping language, each commented); the
recipes and pitfalls come from the services packaged in
`OKDP/platform-charts` and `OKDP/sandbox-dependencies`. What each file may contain is specified in
[the service definition](specs/02_service_definition.md), its
[control flow](specs/03_control_flow.md) and [functions](specs/04_functions.md),
and [inputs and outputs](specs/01_inputs_outputs.md), the command line
in [usage](01_usage.md), the internals in [architecture](00_architecture.md).

## What a package is

A package is a directory of a git repository, laid out as a Helm umbrella
chart, not published as one. It carries the
parameters an instance sets (`config.schema.json`, their defaults
included), OKDP's defaults for each upstream chart
(`<key>.defaults.yaml`), and the service definition. `Chart.yaml` names
the service and the upstream charts it is made of, its dependencies, each
by its name, repository and pinned version; `okdp.yaml` says what the
service needs of the platform, which defaults each chart takes and what an
instance may override, and holds the mapping's sections that are not chart
values: `vars`, `errors`, the OAuth clients, OKDP's own objects, the
instance's URL and usage, and the provided connections; `values.cel.yaml`,
the values file, says how the values of each chart follow from the
parameters, the platform values (`global.okdp`) and the connections.
`okdp-compile` turns the values of one instance into the final values of
every chart, OKDP's own objects and what the instance publishes
(`okdp.instance.yaml`); a deployer then renders each upstream chart from its
repository with its values, as the
[chart index](specs/01_inputs_outputs.md#chart-index) names it. Nothing of
the package is published as a chart.

```
<name>/
├── Chart.yaml              name, version, the upstream charts (dependencies), Helm's other metadata
├── okdp.yaml               required platform values, roles, dependsOn, OKDP's defaults, upstream values, protect, append; vars, errors, OAuth clients, OKDP's own objects, URL and usage, provided connections
├── values.cel.yaml         the computed values of each chart (values.yaml when none is computed)
├── config.schema.json      the parameters: types, defaults, rules, x-ui-* hints, x-okdp-connection-ref
├── <key>.defaults.yaml     OKDP's defaults of each chart: plain YAML
├── ci/platform.yaml        the platform values of the test cases (global.okdp), given with --context
├── ci/<case>-values.yaml   test instances, without global
└── README.md               parameters, platform values read, charts, known gaps
```

`--package` names the package directory (`okdp-compile --package
examples/02_advanced/input/db/catalog/webapp/1.0.0`); `--check --charts`
checks a compile against the upstream charts' defaults and schemas, from
a chart cache ([`--check`](01_usage.md#--check)). In a repository laid out as the
examples, a package is a version directory of the catalog,
`catalog/<service>/<version>/`, which its `Chart.yaml` names
([repository layout](01_usage.md#repository-layout)).

## Integrating a chart

Start from `Chart.yaml`, the chart as a dependency, and a plain
`values.yaml`: the chart's values the platform sets, under the
dependency's key, as an umbrella chart's `values.yaml` holds them
(`examples/00_passthrough/input/db/catalog/echo/1.2.0/`). Every
dependency is rendered, with its chart's defaults when the file does not
write its key: `Chart.yaml` alone is a package
([rendered dependencies](specs/02_service_definition.md#rendered-dependencies)).
Add `okdp.yaml` when the service needs more: platform values, OKDP's
defaults, instance upstream values, `vars` and OKDP's sections (URL and
usage, connections, OAuth clients, objects); and `config.schema.json`
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
defaults. A compile reads none of it; a check (`okdp-compile --check
--charts`) reads its `values.yaml` and `values.schema.json` from a chart
cache, filled with `helm pull --untar`, each chart unpacked under its
repository's directory, the URL without its scheme
([chart cache](specs/01_inputs_outputs.md#chart-cache)).

```yaml
# Chart.yaml (airflow)
dependencies:
  - name: airflow             # the chart; its key too, without an alias
    repository: https://airflow.apache.org
    version: 1.22.0
```

```bash
helm pull airflow --repo https://airflow.apache.org --version 1.22.0 --untar -d "$OKDP_CHARTS/airflow.apache.org/airflow-1.22.0"
```

- The key, the `alias` when given, else the chart's `name`, names the
  chart's values everywhere (the values file, `defaults` and `upstream` of
  `okdp.yaml`, the instance's `upstream.<key>`, the umbrella's dependency
  alias and its values in `instance.values.yaml`). It is a DNS label:
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
- `oidc-dcr` is not a dependency of the package: the compiler adds one
  oidc-dcr chart per OAuth client in `dcr` mode, and the platform names the
  chart that renders them (`global.okdp.charts`:
  [platform charts](specs/01_inputs_outputs.md#platform-charts)).

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
  - ingress
```

`apiVersion: v2`, `name` and `version` are required, and `version` is
SemVer: the umbrella chart (`-o`) takes it as its version, and the console
compares versions. Its upstream half stays SemVer (`480.0.0-1.0.0` for
Trino 480); release-please owns the OKDP half. Helm's other fields
(`description`, `maintainers`, `annotations`...) are written back to the
umbrella's `Chart.yaml`, not read. The mapping sees `name` and `version`
as `service` (`service.name`, `service.version`); the labels of OKDP's
objects take `app.kubernetes.io/name` and `okdp.io/service` from the name.
`roles` and `dependsOn`, in
`okdp.yaml`, are the console's: the compiler does not read them
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
  "properties": {
    "global": {"type": "object", "description": "Platform values: the context's layers (--context), the platform's level then higher ones adding to it, never an instance's."},
    "replicas": {"type": "integer", "minimum": 1, "default": 1, "title": "Replicas", "x-ui-group": "Sizing"},
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
  ([context values](specs/01_inputs_outputs.md#context-values)).
- `connections` is not a parameter: the context gives the connections, an
  instance's own in a context file of its level, and the schema does not
  declare it; a declaration has no effect
  ([reserved keys](specs/01_inputs_outputs.md#reserved-keys)).
- A string pasted into a configuration file, a script or a template refuses
  `{{`, `}}` and line breaks (the `not` pattern above, as in trino, airflow,
  superset and polaris). The compiler refuses `{{` in every instance value
  anyway ([layers](specs/01_inputs_outputs.md#layers)); the pattern makes
  the console refuse it before committing, and covers the rest.
- `x-okdp-connection-ref` makes a string a
  [connection reference](specs/01_inputs_outputs.md#connections),
  on the items of a list too (trino's `hiveCatalogs[].metastore`). Give it
  `"type": "string"`: the compiler refuses any other value but `null`, after
  the schema check.
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
  - ingress.suffix
  - ingress.className
  - certificateIssuers.selfSigned.name
defaults:
  app: app.defaults.yaml                 # OKDP's defaults, plain YAML; metrics has none
upstream:
  app:                                   # the instance's upstream.app; metrics: none
    protect: [fullnameOverride, ingress.enabled, ingress.hosts, ingress.tls]
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
- `protect` refuses instance values on a path, under it, or a non-map on the
  way to it ([protect](specs/01_inputs_outputs.md#protect)).
  Protect what the platform relies on:
  - names other objects or contracts address: `fullnameOverride`, Service
    names and ports (what `provides` publishes is built from them);
  - authentication: OIDC settings, auth types, access control;
  - the exposure: `ingress.enabled`, `className`, `hosts`, `tls` (the host is
    registered with the identity provider and published in
    `okdp.instance.yaml`);
  - the wiring of Secrets: `existingSecret`, `*SecretName`, any key that
    would put a credential in the values;
  - the switches of features kept off for determinism (`admissionController`,
    `serviceMonitor`), and the RBAC and ServiceAccounts another service owns.

  Protect the narrowest path: `ingress.hosts` leaves `ingress.annotations`
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
puts `vars` and `errors` first, `objects` and `oidcClients` last):

```yaml
# values.cel.yaml: chart key -> the values computed for it
app:
  fullnameOverride: ${{ release.name }}-app   # the name the http contract promises
  replicaCount: ${{ values.replicas }}
metrics:
  $if: ${{ values.metrics.enabled }}   # left out: the chart is not rendered
  target: http://${{ release.name }}-app.${{ release.namespace }}.svc:8080/metrics
```

```yaml
# okdp.yaml, after the package keys
vars:                      # names and hosts computed once, in order
  host: ${{ release.name }}.${{ global.okdp.ingress.suffix }}
  db: '${{ type(values.database) == map ? values.database : {} }}'
  sessionSecret: ${{ okdpFullname('session') }}

errors:                    # a message fails the compile when its $if holds
  - $if: ${{ values.replicas > 1 && db.size() == 0 }}
    $value: "webapp: replicas > 1 needs a database, sessions are stored there"

oidcClients:               # the OAuth clients: the platform's, or registered by DCR
  - redirectUris: ['https://${{ host }}/oauth2/callback']
    grantTypes: [authorization_code, refresh_token]

objects:                   # OKDP's own objects, whole manifests
  - $spread: >-
      ${{ okdpGeneratedSecret({'name': sessionSecret, 'keys': [{'key': 'session-key', 'length': 48}]}) }}

instance:                  # url and usage, in okdp.instance.yaml
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
- The values file holds only what is computed, by key. Every dependency
  is rendered, as Helm renders a chart's dependencies: one the file does
  not write gets `{}` as its computed values, OKDP's defaults, the
  instance's upstream values and, by Helm, its chart's defaults still
  applied. A `$if` on a
  chart, or a null value (`backup: ~`), leaves it out: it is neither
  compiled nor rendered (the webapp's `metrics`, trino's `opa-kube-mgmt`
  and `opal`). A key a top-level `$merge` or `$for` gives counts only when
  it gives it: to leave a dependency out, write its key
  ([rendered dependencies](specs/02_service_definition.md#rendered-dependencies)).
  Any other value that is not a map fails the compile. OKDP's own objects
  are not values: they reach the cluster in the umbrella, whatever the
  file leaves out
  ([the objects file](specs/01_inputs_outputs.md#the-objects-file)).
- `errors` fails the compile with every message at once. A message is the
  `$value` of an item with its condition, `$if`; `$for` checks every
  element of a list.
  Prefix each message with the service and the parameter, and say what to set.
- `oidcClients` declares the service's OAuth clients; the mapping reads each
  from its Secret, `platform.oidcClientSecret` for the main client
  ([recipe](#oauth-clients)).
- `objects` are whole manifests (a `kind` and a `metadata.name` at least) the
  umbrella renders as written, after the credentials Secrets (a `{{` in
  them is not Helm's): generated Secrets, hooks, custom
  resources, a workload the upstream charts lack. A component without an
  upstream chart (cnpg-postgresql) declares no dependency, has no values
  file, and writes everything here.
- `instance.url` is what the console opens (none without a UI); `usage` is
  shown with the instance. Both are strings: write a number with `str`.

Besides CEL's own, [host functions](specs/04_functions.md#host-functions)
(`str`, `sha256`, `toJson`, `okdpFullname`, `okdpLabels`,
`okdpGeneratedSecret`, `oidcClientSecret`...) reproduce Helm's functions
and give OKDP's names, labels and Secrets.

### 6. Consume and provide connections

A parameter carrying `x-okdp-connection-ref` names either an entry of
`connections` (external, from a context file: the platform's, the
project's, or the instance's own at a level of its own) or another instance
of the namespace (internal). Before the mapping runs, the compiler replaces
the name with the connection, completed against its contract
([what the mapping receives](specs/01_inputs_outputs.md#what-the-mapping-receives)).
A reference left empty stays `""` (or `null`); a name that is not an entry
of `connections` must be a release name.

```yaml
# instance values (-f): an external database
database: shared-db
```

```yaml
# the instance's own connections, a context file of its level (--context 2:<file>)
connections:
  shared-db:
    contract: database
    engine: mysql
    host: db.example.org
    port: 3306
    dbName: webapp
    secretRef: {name: shared-db-credentials}   # never the password itself
```

With the example's `database` contract, `database: demo-db` instead names an
instance of the namespace: host `demo-db-db.demo.svc`, Secret
`demo-db-database-credentials`, and a dependency (`Result.Dependencies`),
which [okdp-gitops](https://github.com/OKDP/okdp-gitops) orders the
releases with (Flux `dependsOn`, Argo CD sync waves,
[GitOps](01_usage.md#gitops)). The mapping reads both the same way,
credentials through a `secretKeyRef` on `secretRef.name`; it fails with a
clear message when it needs the Secret and the connection has none (the
second `errors` entry of
`examples/02_advanced/input/db/catalog/webapp/1.0.0/okdp.yaml`). `s3` and
`database-server` have no internal convention: they are always external (cnpg-postgresql publishes one
`database-server` connection per database, which consumers declare as
connection files). See [contracts](specs/01_inputs_outputs.md#contracts).

Provide a connection in `provides`. Its values are checked against the
contract, its schema included (a `port` is a number, not a string built by
an expression). Of a contract with an internal convention, the output is
published under the release name, the default `name`: internal references
reach the instance by it, and another name fails. Its values must then
also be what an internal reference to this instance resolves to: build the
hosts and Service names from the contract's convention (trino's host is
`trino-<namespace>.<suffix>`, hive-metastore's Service
`<release>-hive-metastore`), once, in `vars`, for the ingress and `provides`
alike. A `name` is for a contract without a convention (cnpg-postgresql's
`database-server` outputs, one per database). Then say where the
credentials come from:

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

`secret` (`generate` or `stringData`): the compiler renders the credentials
Secret (rustfs). `secretRef` alone: an operator or an upstream chart creates
it (cnpg-postgresql). Neither: consumers bring their own identity
(seaweedfs). An output never points at a Secret nobody creates.

### 7. Compile, render and check

Each `ci/<case>-values.yaml` is an instance: its parameters, without
`global` and `connections`, which an instance may not set. The
platform values (`global.okdp`) are given apart,
with `--context`: `ci/platform.yaml` for every case, then, for a case that
needs others, its own `ci/<case>-platform.yaml`, and its own connections
at level 2 (`--context 2:ci/<case>-connections.yaml`, what an instance's
`connections.yaml` is in the
[repository layout](01_usage.md#repository-layout)). Write one case per
mode: OIDC `existing`, `dcr` and off, internal and external connections,
proxy set, each optional chart on, upstream values with appended lists. The
`dcr` case's platform file gives `oidc.clientProvisioning: dcr`,
`oidc.dcr.registrationUrl` and `authMethod: anonymous` (polaris's
`ci/dcr-platform.yaml`). The examples of this repository compile in `dcr`
mode only; the `existing` mode is tested by the unit tests
(`internal/okdp/oidc_test.go`), with no example output.

```bash
export OKDP_CONTRACTS=~/okdp/contracts     # the contract schemas
export OKDP_CHARTS=~/.cache/okdp/charts   # the chart cache of --check, filled with helm pull --untar
polaris() { okdp-compile --package charts/services/polaris \
  --context charts/services/polaris/ci/platform.yaml \
  --context charts/services/polaris/ci/dcr-platform.yaml \
  -f charts/services/polaris/ci/dcr-values.yaml \
  --release demo-polaris --namespace demo "$@"; }
polaris --check                  # against the upstream charts' defaults and schemas
polaris -o /tmp/demo-polaris
yq '.dependencies' /tmp/demo-polaris/Chart.yaml   # the source of each key
chart=$(mktemp -d) && cp -R /tmp/demo-polaris/. "$chart" && helm dependency build "$chart"
helm template demo-polaris "$chart" -n demo -f /tmp/demo-polaris/instance.values.yaml
scripts/compile-charts.sh charts/services/polaris   # every case, compiled and rendered
```

- Render with the release name and namespace of the compile: the upstream
  charts derive their names from the release.
- Render the umbrella as a deployment installs it: its
  dependencies are the sources the index names for the keys, the clients'
  charts the platform's oidc-dcr chart, `polaris-admin-principals` the
  source of `polaris-admin` under its own alias; `helm dependency build`
  fetches them, in a copy of the directory, since the next compile into the
  directory refuses `Chart.lock` and `charts/`
  ([rendering with Helm](01_usage.md#rendering-with-helm)).
- `-o` writes the final values, the input of `helm template`, under their
  keys in `instance.values.yaml`, and OKDP's own objects in
  `okdp.objects.yaml`, which the umbrella renders as written.
- A case whose first line is `# error: <message>` must fail with that message
  (`scripts/compile-charts.sh`, and `testdata/` here: `protected-upstream.yaml`
  starts with `# error: upstream.app.ingress.hosts: ingress.hosts is set by
  the platform`; `testdata/context/0/` holds the platform files that must
  fail). Add one per `errors` entry, per protected path that matters, per
  schema rule.

[Troubleshooting](01_usage.md#troubleshooting) explains the error messages.

## Recipes

### Ingress and TLS

The host follows the platform suffix; the class and the certificate issuer
are platform values (`require` them, `protect` the four ingress paths).

```yaml
# values.cel.yaml
app:
  ingress:
    enabled: true
    className: ${{ global.okdp.ingress.className }}
    annotations: ${{ platform.ingressAnnotations }}   # cert-manager.io/cluster-issuer
    hosts:
      - host: ${{ host }}
        paths: [{path: /, pathType: Prefix}]
    tls:
      - hosts: ["${{ host }}"]
        secretName: ${{ release.name }}-tls
```

To add annotations, write them in a map next to
`$merge: ${{ platform.ingressAnnotations }}` (airflow, superset).

### OAuth clients

A service that signs users in declares its OAuth clients in `oidcClients`,
and reads each from its Secret: `client_id`, and `client_secret` unless the
client is public. The main client has no name; any other is named
(polaris's `console`):

```yaml
# okdp.yaml
vars:
  consoleHost: ${{ release.name }}-console.${{ global.okdp.ingress.suffix }}
oidcClients:
  - redirectUris: ['https://${{ host }}/oauth2/callback']   # the main client: no name
    grantTypes: [authorization_code, refresh_token]          # required
    scope: ${{ platform.oidc.scope }} offline_access         # what the application asks for
  - name: console                                            # a named client
    public: true                                             # a browser application: no secret
    redirectUris: ['https://${{ consoleHost }}/auth/callback']
    grantTypes: [authorization_code]
```

```yaml
# values.cel.yaml
app:
  env:
    - name: OIDC_CLIENT_ID
      valueFrom: {secretKeyRef: {name: "${{ platform.oidcClientSecret }}", key: client_id}}
    - name: OIDC_CLIENT_SECRET
      valueFrom: {secretKeyRef: {name: "${{ platform.oidcClientSecret }}", key: client_secret}}
    - name: CONSOLE_CLIENT_ID
      valueFrom: {secretKeyRef: {name: "${{ oidcClientSecret('console') }}", key: client_id}}
```

The same lines serve both provisioning modes of the platform
(`global.okdp.oidc.clientProvisioning`); only the Secret's name changes, and
`platform.oidcClientSecret` (the main client) and `oidcClientSecret('<name>')`
return it, in any section, `vars` included:

- `existing`, the default: the platform registers the clients and creates
  their Secrets beforehand. The compiler checks the section and adds
  nothing.
- `dcr`: the compiler adds one chart per client, `oidc-dcr` and
  `console-oidc-dcr`, both rendered by the platform's oidc-dcr chart, whose
  Job registers the client and writes its Secret.

Write the redirect URIs and the scope the application actually sends
(airflow and spark-history-server pass their login scope, `offline_access`
included); `request` adds or overrides registration fields (trino's
`logo_uri`). The compiler computes the rest of the registration and the
values of each oidc-dcr chart from the client and the platform's
[OAuth client settings](specs/01_inputs_outputs.md#oauth-client-settings).
The mapping writes nothing else for the clients: no chart entry
([pitfalls](#pitfalls)), no `$if` on the mode, no Job, ServiceAccount or
Secret name. The keys of a client and their checks:
[oidcClients](specs/02_service_definition.md#oidcclients); the names of
the charts, objects and Secrets in each mode, and the keys of the Secret:
[OAuth client charts](specs/01_inputs_outputs.md#oauth-client-charts).

With OIDC off (`global.okdp.oidc.enabled: false`) no chart is added and no
Secret exists: read the Secrets under `platform.oidc.enabled`
([conditional items](#conditional-items-maps-from-lists)) when the service
supports it.

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

Both keys render with the chart, over its defaults, and a check holds
both to its `values.schema.json`, each with its own values file: the index maps both to the same
chart ([rendering with Helm](01_usage.md#rendering-with-helm)). Everything
else is keyed by the key
([dependencies](specs/02_service_definition.md#dependencies)).
Without an alias, the second would have the key of the first, and fail
the load. The second render accepts no instance upstream values: an entry
of its key in `upstream` would read the instance's
`upstream.polaris-admin-principals`, with its own `protect` and `append`
([upstream](specs/02_service_definition.md#upstream)).

### Generated secrets

Nothing random is computed at compile time: passwords and keys come from
External Secrets Operator generators, generated once in the cluster.

```yaml
# okdp.yaml
vars:
  keep:                     # outlive the release: the database they encrypt does
    helm.sh/resource-policy: keep
    argocd.argoproj.io/sync-options: Delete=false
objects:
  - $spread: >-
      ${{ okdpGeneratedSecret({'name': okdpFullname('internal'), 'annotations': keep,
          'keys': [{'key': 'jwt-secret', 'length': 64},
                   {'key': 'fernet-key', 'length': 32, 'transform': 'b64enc'}]}) }}
  - $spread: >-
      ${{ platform.oidc.enabled ? [] : okdpGeneratedSecret({'name': okdpFullname('admin'),
          'keys': [{'key': 'password', 'length': 24}]}) }}
```

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
  - $for: "secret in (platform.oidc.enabled ? [platform.oidcClientSecret] : [])"
    $each:                                     # a group of items, on one condition
      - name: OIDC_CLIENT_ID
        valueFrom: {secretKeyRef: {name: "${{ secret }}", key: client_id}}
      - name: OIDC_CLIENT_SECRET
        valueFrom: {secretKeyRef: {name: "${{ secret }}", key: client_secret}}
catalogs:                                      # trino: one catalog file per item
  $if: ${{ catalogs.size() > 0 }}
  $for: c in catalogs
  $key: ${{ c.name }}
  $value: ${{ c.properties + '\n' }}
```

A `$for` over a list of zero or one element binds the value the group uses
(superset, spark-history-server). A list computed in an expression is
spliced with `$spread`.

### Labels

OKDP's objects take OKDP's labels, their selectors OKDP's selector labels:
`labels` and `selectorLabels`, or, for one component
of the instance, `okdpLabels('cache')` and `okdpSelectorLabels('cache')`
(superset's Valkey Deployment and Service). The console finds an instance's workloads by `app.kubernetes.io/instance`.
An upstream chart that leaves it out of its pods usually has a value for
extra labels: set it (polaris-console `podLabels`, airflow `labels`).

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

The same lines serve an internal and an external database. A required
connection is checked in `errors` (hive-metastore, polaris, superset), with
what the service supports (polaris refuses any engine other than `postgresql`).

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
  - $if: ${{ platform.oidc.enabled && platform.oidc.?issuerUri.orValue('') == '' }}
    $value: global.okdp.oidc.issuerUri is required when OIDC is enabled. It is a context value
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
      - 'ingress.annotations["nginx.ingress.kubernetes.io/auth-*"]'   # every auth-* annotation
      - 'config["spark.ui.filters"]'                                  # one key, dots included
```

An instance setting one of them fails:

```text
app: upstream.app.ingress.annotations["nginx.ingress.kubernetes.io/auth-url"]: ingress.annotations["nginx.ingress.kubernetes.io/auth-*"] is set by the platform and cannot be changed
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
  give every branch a `ci/` case that takes it, and test both OIDC modes:
  the clients' charts are compiled, and the platform's `registrationUrl` and
  `authMethod` required, in `dcr` mode only.
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
  `-<suffix>`. Use `okdpFullname('something')`, cut to 63 characters. A
  name a contract's convention promises (`<release>-app`,
  `<release>-hive-metastore`) is kept as the convention writes it.
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
- **Some chart keys are the compiler's.** `oidc-dcr` and `<name>-oidc-dcr`
  name the charts of the declared clients: a `Chart.yaml` dependency or a
  key of the values file of the same key fails, in every mode.
- **A client's `secret` is not followed.** It renames the Secret the Job
  writes, in `dcr` mode only; `oidcClientSecret(name)` still returns the
  mode's name, and in `existing` mode the platform's Secret keeps its own.
  Reference the name you set yourself:
  `platform.oidc.dcr.enabled ? mySecret : oidcClientSecret('n')`.
- **A dependency is pinned.** `version` is one version, not a range
  (`^1.2`, `1.x` fail): bump it in `Chart.yaml`, then pull the new version
  into the chart cache of the checks. The final values do not change with
  it: they hold none of the chart's defaults.

## Checklist

- [ ] Every dependency of `Chart.yaml` has its name, repository and
  version, pinned; a bundled subchart the package does not use is disabled
  by values; no `lookup`, random or `.Capabilities` feature is on.
- [ ] `Chart.yaml` and the values file use no chart key of the compiler
  (`oidc-dcr`, `<name>-oidc-dcr`); a second render of a chart is a
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
  can exceed 63 characters outside a contract's convention; every OKDP
  object is labelled.
- [ ] `provides` follows the contract's convention, under the release
  name, and says where the credentials come from.
- [ ] The OAuth clients are declared in `oidcClients` and read from
  `platform.oidcClientSecret` and `oidcClientSecret('<name>')`.
- [ ] One `ci/` case per mode and per branch, OIDC `existing` and `dcr`
  included, one failing case per check; `scripts/compile-charts.sh` compiles
  them all and renders each chart with the source the index names.
- [ ] `README.md` lists the parameters, the platform values read, the charts
  and when each is compiled (the clients' charts: `dcr` mode), and the known
  gaps.
