# Writing a package

How to package an upstream application for OKDP: a wrapper chart and its
service definition, compiled by `okdp-compile`. The steps follow the `webapp`
example of this repository (`example/`); the recipes and pitfalls come from
the services packaged in `OKDP/platform-charts` and
`OKDP/sandbox-dependencies`. What each file may contain is specified in
[the service definition](specs/02_service_definition.md) and
[inputs and outputs](specs/01_inputs_outputs.md), the command line
in [usage](01_usage.md), the internals in [architecture](00_architecture.md).

## What a package is

A package is a wrapper chart without templates, plus a service definition.
The wrapper chart carries the parameters an instance sets (`values.yaml`,
`values.schema.json`), the upstream charts the service is made of
(`vendor/<chart>/`) and OKDP's default values for each of them
(`vendor-values/`). The service definition says how the values of each
upstream chart follow from the parameters, the platform values
(`global.okdp`) and the connections: `service.yaml` lists the charts and what
an instance may override, `mapping.cel.yaml` computes the rest and declares
the service's OAuth clients. `okdp-compile` turns the values of one instance
into the final values of every chart and OKDP's own objects; Helm then
renders each chart from the vendored chart the
[chart index](specs/01_inputs_outputs.md#chart-index) names
(`vendor/<chart>/`), as a standard chart.

A package is laid out as follows (`charts/<category>/<name>/` in
platform-charts, `packages/<category>/<name>/` in sandbox-dependencies):

```
<name>/
├── Chart.yaml              version <upstream>-<okdp semver>, appVersion, okdp.io/* annotations
├── values.yaml             the parameters, with their defaults
├── values.schema.json      their schema: types, rules, x-ui-* hints, x-okdp-connection-ref
├── service.yaml            charts and their aliases, base values, required platform values, protect, append
├── mapping.cel.yaml        computed values, OAuth clients, OKDP's own objects, descriptor, provided connections
├── vendor.yaml             the upstream charts, each once, pinned: the lock
├── vendor/<chart>/         their unpacked copy (downloaded, not committed)
├── vendor-values/<chart>.yaml  base values of each chart: plain YAML
├── ci/<case>-values.yaml   test instances, each with its global.okdp block
├── okdp-guard-allow.yaml   reviewed exceptions of the chart guard (when needed)
├── .helmignore             the upstream documentation of vendor/
└── README.md               parameters, platform values read, charts, known gaps
```

`--chart` and `--service` are separate flags: a package keeps `service.yaml`
and `mapping.cel.yaml` in its chart directory (`--chart X --service X`), the
example one level up (`--chart example/webapp --service example`).

## Step by step

### 1. Vendor the upstream charts

Each upstream chart is pinned in `vendor.yaml` and unpacked into
`vendor/<name>/` by `scripts/vendor-charts.sh <dir>`, a copy of the canonical
script of `OKDP/platform-charts` (copies stay byte-identical; its header
documents every key). `vendor/` is not committed: `vendor.yaml` is the lock,
and `--check` fails when `vendor/` differs from it.

```yaml
# vendor.yaml (airflow)
charts:
  - name: airflow             # vendor/airflow, the chart key of service.yaml
    repository: https://airflow.apache.org
    version: 1.22.0
    drop: [charts/postgresql] # bundled subchart the package disables: not vendored
  - name: oidc-dcr            # the OAuth clients of oidcClients: not a chart of service.yaml
    repository: oci://quay.io/adaltas
    version: 0.5.0
  - name: okdp                # OKDP's own objects
    repository: file://../../lib/okdp-objects
    version: 1.0.0
    chart: okdp-objects       # the chart's name in its repository
```

- `name` is the directory under `vendor/`, and by default the chart's key in
  `service.yaml` and in the mapping's `output`. The compiler reads
  `vendor/<name>/values.yaml` and `values.schema.json`; Helm renders the rest.
- `drop` removes what the package never renders (superset drops the bundled
  `postgresql` and `redis` and runs a Valkey of its own); `keep` makes a
  reduced copy (trust-manager's CRDs alone, in cert-manager);
  `crdsAsTemplates` upgrades the CRDs with the release (spark-operator).
- A chart rendered twice is vendored once: `service.yaml` gives each render
  a key, and the second names the vendored chart with `chart:` (polaris:
  `polaris-admin`, and `polaris-admin-principals` compiled from it;
  [recipe](#render-a-chart-twice)).
- Always vendor `okdp`, the okdp-objects chart: it renders the descriptor
  ConfigMap and the mapping's objects. Vendor `oidc-dcr` (>= 0.5.0) once as
  soon as the mapping declares an OAuth client, whatever the number of
  clients and the provisioning mode: the compile fails without it
  ([OAuth clients](#oauth-clients)).

Flux (`helm install`) and Argo CD (`helm template`) must render the same
objects. An upstream feature built on `lookup`, a random function, `genCA` or
`.Capabilities` breaks that: keep it disabled, protect its switch (step 4),
and record the reviewed exception in `okdp-guard-allow.yaml`.

### 2. Write Chart.yaml

```yaml
apiVersion: v2
name: webapp
type: application
version: 2.3.0-1.0.0      # <upstream>-<okdp semver>
appVersion: "2.3"         # quoted: read as Helm reads it, 1.10 would be "1.1"
annotations:
  okdp.io/roles: webapp   # what the service provides in the catalog
  okdp.io/depends-on: |   # the platform services it needs: a string, read as YAML
    - database-server
    - ingress
```

The upstream half of `version` stays SemVer (`480.0.0-1.0.0` for Trino 480):
the console compares versions; release-please owns the OKDP half. The mapping
sees `Chart.yaml` as `chart` (`chart.appVersion`), and the labels of OKDP's
objects take `helm.sh/chart` and `app.kubernetes.io/version` from it.

### 3. Design the parameters

Keep the surface small: sizing, switches, connection references, and the few
settings that mean something on the platform. Any other value of an upstream
chart is reached through `upstream.<chart>`, without a parameter.

```yaml
# values.yaml
replicas: 1
database: ""              # a connection reference (database contract)
metrics:
  enabled: false
  interval: 30s
global:
  okdp: {}                # platform values: the first values layer, never set per instance
connections: {}           # external connections, one values layer per connection file
upstream: {}              # values of the upstream charts, by chart
```

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "global": {"type": "object"},
    "connections": {"type": "object"},
    "replicas": {"type": "integer", "minimum": 1, "title": "Replicas", "x-ui-group": "Sizing"},
    "database": {
      "type": "string",
      "x-okdp-connection-ref": {"contract": "database"},
      "not": {"pattern": "\\{\\{|\\}\\}|[\\r\\n]"}
    },
    "metrics": {
      "type": "object",
      "properties": {"enabled": {"type": "boolean"}, "interval": {"type": "string"}}
    },
    "upstream": {
      "type": "object",
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

- The schema is required: the compiler finds the connection references
  through it, and checks the values against it before the mapping runs
  ([schema check](specs/01_inputs_outputs.md#schema-check)): a rule
  on a parameter belongs here, not in the mapping's `errors`. Declare
  `$schema`. With `additionalProperties: false`, declare `global`,
  `connections` and `upstream` too.
- A string pasted into a configuration file, a script or a template refuses
  `{{`, `}}` and line breaks (the `not` pattern above, as in trino, airflow,
  superset and polaris).
- `x-okdp-connection-ref` makes a string a
  [connection reference](specs/01_inputs_outputs.md#connections),
  on the items of a list too (trino's `hiveCatalogs[].metastore`).
- `upstream` declares one object per chart key that accepts instance values:
  the inline values of any other chart would be ignored without a word. The
  compiler refuses `{{` in them (upstream charts pass some values to `tpl`);
  `noTemplate` makes the console refuse it before committing.
- `title`, `description` and the `x-ui-*` hints (`x-ui-group`, `x-ui-order`,
  `x-ui-advanced`, `x-ui-condition`...) build the console form.

### 4. Write service.yaml

```yaml
require:                                 # read by the mapping in every case
  - ingress.suffix
  - ingress.className
  - certificateIssuers.selfSigned.name
charts:
  app:
    base: vendor-values/app.yaml         # OKDP's defaults, plain YAML
    upstream: true                       # upstream.app.yaml, then inline upstream.app
    protect: [fullnameOverride, ingress.enabled, ingress.hosts, ingress.tls]
    append: [env]
  metrics: {}                            # no base values, no instance values
  okdp: {}                               # OKDP's own objects: always declared
```

- `require` lists the `global.okdp` keys the mapping reads in every case: the
  compile fails, naming the key, when one is missing or empty. A value needed
  in one mode only is checked in `errors`
  ([recipe](#require-a-platform-value-in-one-mode)).
- A key compiles from `vendor/<key>`; `chart` names another vendored chart,
  for a chart rendered more than once ([recipe](#render-a-chart-twice),
  [aliases](specs/02_service_definition.md#aliases)). The charts of the
  OAuth clients (`oidc-dcr`, `<name>-oidc-dcr`) are not listed: the
  compiler adds them ([OAuth clients](#oauth-clients)).
- `base` holds what depends on no parameter, platform value or connection:
  images, probes, switches, static configuration. The mapping's values go
  over it, the instance's over both
  ([merge precedence](specs/01_inputs_outputs.md#merge-precedence)).
- `upstream` lets an instance set any value of the chart, from its files
  and its inline `upstream.<key>` values
  ([upstream](specs/02_service_definition.md#upstream)). Leave it out
  for a chart the platform owns entirely (`okdp`).
- `protect` refuses instance values on a path, under it, or a non-map on the
  way to it ([protect](specs/01_inputs_outputs.md#protect)).
  Protect what the platform relies on:
  - names other objects or contracts address: `fullnameOverride`, Service
    names and ports (what `provides` publishes is built from them);
  - authentication: OIDC settings, auth types, access control;
  - the exposure: `ingress.enabled`, `className`, `hosts`, `tls` (the host is
    registered with the identity provider and published in the descriptor);
  - the wiring of Secrets: `existingSecret`, `*SecretName`, any key that
    would put a credential in the values;
  - the switches of features kept off for determinism (`admissionController`,
    `serviceMonitor`), and the RBAC and ServiceAccounts another service owns.

  Protect the narrowest path: `ingress.hosts` leaves `ingress.annotations`
  open, `auth` refuses everything under it. Paths are split on dots: a key
  containing dots cannot be named ([recipe](#keys-containing-dots)).
- `append` lists the lists the mapping fills that an instance should extend
  (`env`, `extraVolumes`, `extraVolumeMounts`, `extraArgs`): the instance's
  items come after the computed ones; any other list of the instance
  replaces the computed one. A path listed in both stays protected
  ([append](specs/01_inputs_outputs.md#append)). A templated string
  (airflow's `extraEnv`) cannot be appended to: protect it and point users at
  another list.

### 5. Write mapping.cel.yaml

The mapping is YAML shaped like the upstream values, with `${{ CEL }}`
[expressions](specs/02_service_definition.md#expressions) and
[directives](specs/02_service_definition.md#directives) (`$if`, `$for`,
`$merge`, `$spread`, `$let`). Its [sections](specs/02_service_definition.md#sections),
as the example writes them (the
[evaluation order](specs/02_service_definition.md#evaluation-order)
puts `objects` and `oidcClients` last):

```yaml
let:                       # names and hosts computed once, in order
  host: ${{ release.name }}.${{ global.okdp.ingress.suffix }}
  db: '${{ type(values.database) == map ? values.database : {} }}'
  sessionSecret: ${{ okdpFullname('session') }}

errors:                    # each expression returns a list of messages
  - '${{ values.replicas > 1 && db.size() == 0 ? ["webapp: replicas > 1 needs a database, sessions are stored there"] : [] }}'

output:                    # chart -> the values computed for it
  app:
    fullnameOverride: ${{ release.name }}-app   # the name the http contract promises
    replicaCount: ${{ values.replicas }}
    image:
      tag: ${{ chart.appVersion }}
  metrics:
    $if: ${{ values.metrics.enabled }}   # left out: the chart is not rendered
    target: http://${{ release.name }}-app.${{ release.namespace }}.svc:8080/metrics

oidcClients:               # the OAuth clients: the platform's, or registered by DCR
  - redirectUris: ['https://${{ host }}/oauth2/callback']
    grantTypes: [authorization_code, refresh_token]

objects:                   # OKDP's own objects, whole manifests
  - $spread: >-
      ${{ okdpGeneratedSecret({'name': sessionSecret, 'keys': [{'key': 'session-key', 'length': 48}]}) }}

instance:                  # the descriptor ConfigMap <release>-okdp
  url: https://${{ host }}
  usage: |
    A web application. Open https://${{ host }}

provides:                  # the connections the instance publishes
  - contract: http
    values:
      url: https://${{ host }}
      internalUrl: http://${{ release.name }}-app.${{ release.namespace }}.svc:8080
```

- `let` computes every name, host and repeated structure once; a binding
  sees the previous ones, and hides a
  [context](specs/02_service_definition.md#context) variable of the
  same name (`labels`).
- `output` holds only what is computed. A `$if` on a chart leaves it out:
  it is neither compiled nor rendered (the example's `metrics`, trino's
  `opa-kube-mgmt` and `opal`). The `okdp` chart is compiled even when the
  mapping leaves it out: the descriptor always reaches the cluster
  ([the okdp chart](specs/01_inputs_outputs.md#the-okdp-chart)).
- `errors` fails the compile with every message at once. Prefix each with
  the service and the parameter, and say what to set.
- `oidcClients` declares the service's OAuth clients; the mapping reads each
  from its Secret, `platform.oidcClientSecret` for the main client
  ([recipe](#oauth-clients)).
- `objects` are whole manifests (a `kind` and a `metadata.name` at least) the
  okdp chart renders after the descriptor: generated Secrets, hooks, custom
  resources, a workload the upstream charts lack. A component without an
  upstream chart (the control plane server and UI, cnpg-postgresql) declares
  only `okdp` and writes everything here.
- `instance.url` is what the console opens (none without a UI); `usage` is
  shown with the instance.

Besides CEL's own, [host functions](specs/02_service_definition.md#host-functions)
(`str`, `sha256`, `toJson`, `okdpFullname`, `okdpLabels`,
`okdpGeneratedSecret`, `oidcClientSecret`...) reproduce Helm's functions
and give OKDP's names, labels and Secrets.

### 6. Consume and provide connections

A parameter carrying `x-okdp-connection-ref` names either an entry of
`connections` (external, from a connection file) or another instance of the
namespace (internal). Before the mapping runs, the compiler replaces the name
with the connection, completed against its contract
([what the mapping receives](specs/01_inputs_outputs.md#what-the-mapping-receives)).
A reference left empty stays `""`.

```yaml
# instance values: an external database
database: shared-db
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
`demo-db-database-credentials`, and a dependency (Flux `dependsOn`, Argo CD
sync waves). The mapping reads both the same way, credentials through a
`secretKeyRef` on `secretRef.name`; it fails with a clear message when it
needs the Secret and the connection has none (the second `errors` entry of
`example/mapping.cel.yaml`). `s3` and `database-server` have no internal
convention: they are always external (cnpg-postgresql publishes one
`database-server` connection per database, which consumers declare as
connection files). See [contracts](specs/01_inputs_outputs.md#contracts).

Provide a connection in `provides`. Its values are checked against the
contract; published under the release name (the default `name`), they must
also be what an internal reference to this instance resolves to: build the
hosts and Service names from the contract's convention (trino's host is
`trino-<namespace>.<suffix>`, hive-metastore's Service
`<release>-hive-metastore`), once, in `let`, for the ingress and `provides`
alike. Then say where the credentials come from:

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

Each `ci/<case>-values.yaml` is a whole instance: its `global.okdp` block
(the platform values), then connections and parameters. Write one case per
mode: OIDC `existing`, `dcr` and off, internal and external connections,
proxy set, each optional chart on, upstream values with appended lists. The
`dcr` case gives the platform's `oidc.dcr.registrationUrl` and
`authMethod: anonymous` (polaris's `ci/dcr-values.yaml`, the example's
`values/dcr.yaml`).

```bash
export OKDP_CONTRACTS=~/okdp/contracts     # the contract schemas
okdp-compile --chart charts/services/polaris --service charts/services/polaris \
  -f charts/services/polaris/ci/dcr-values.yaml \
  --release demo-polaris --namespace demo --emit-dir /tmp/demo-polaris
helm template demo-polaris charts/services/polaris/vendor/polaris -n demo \
  -f /tmp/demo-polaris/polaris.values.yaml
helm template demo-polaris charts/services/polaris/vendor/oidc-dcr -n demo \
  -f /tmp/demo-polaris/console-oidc-dcr.values.yaml
scripts/compile-charts.sh charts/services/polaris   # every case, compiled and rendered
```

- Render with the release name and namespace of the compile: the upstream
  charts derive their names from the release.
- Render each chart from the vendored chart the index
  `<emit-dir>/okdp.charts.yaml` names: `vendor/oidc-dcr` for the clients'
  charts, `vendor/polaris-admin` for `polaris-admin-principals`
  ([rendering with Helm](01_usage.md#rendering-with-helm));
  `scripts/compile-charts.sh` reads it.
- `--emit-dir` writes the final values, the input of `helm template`;
  `--out` writes what the mapping computed, the clients' charts included,
  before the merges: read it when a mapping misbehaves
  ([`--out`](01_usage.md#--out)).
- A case whose first line is `# error: <message>` must fail with that message
  (`scripts/compile-charts.sh`, and `testdata/` here: `protected-upstream.yaml`
  starts with `# error: upstream.app.ingress.hosts: ingress.hosts is set by
  the platform`). Add one per `errors` entry, per protected path that
  matters, per schema rule.

[Troubleshooting](01_usage.md#troubleshooting) explains the error messages.

## Recipes

### Ingress and TLS

The host follows the platform suffix; the class and the certificate issuer
are platform values (`require` them, `protect` the four ingress paths).

```yaml
output:
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
let:
  consoleHost: ${{ release.name }}-console.${{ global.okdp.ingress.suffix }}
oidcClients:
  - redirectUris: ['https://${{ host }}/oauth2/callback']   # the main client: no name
    grantTypes: [authorization_code, refresh_token]          # required
    scope: ${{ platform.oidc.scope }} offline_access         # what the application asks for
  - name: console                                            # a named client
    public: true                                             # a browser application: no secret
    redirectUris: ['https://${{ consoleHost }}/auth/callback']
    grantTypes: [authorization_code]
output:
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
return it, in any section, `let` included:

- `existing`, the default: the platform registers the clients and creates
  their Secrets beforehand. The compiler checks the section and adds
  nothing.
- `dcr`: the compiler adds one chart per client, `oidc-dcr` and
  `console-oidc-dcr`, both rendered from `vendor/oidc-dcr`, whose Job
  registers the client and writes its Secret.

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
phases) are two chart keys of `service.yaml`; the second names the vendored
chart it compiles from with `chart`:

```yaml
# service.yaml
charts:
  polaris-admin:                       # vendor/polaris-admin
    base: vendor-values/polaris-admin-bootstrap.yaml
    upstream: []                       # inline upstream.polaris-admin only
    protect: [fullnameOverride, phase, realms]
  polaris-admin-principals:            # the values of a second render
    chart: polaris-admin               # vendor/polaris-admin: defaults, schema, render
    base: vendor-values/polaris-admin-principals.yaml
```

```yaml
# mapping.cel.yaml
output:
  polaris-admin:
    realms: ${{ adminRealms }}
  polaris-admin-principals:
    realms: ${{ adminRealms }}
    principals:
      polaris: {url: "${{ internalUrl }}"}
    # The instance's upstream.polaris-admin, checked on the first key, applies here too.
    $merge: ${{ values.?upstream[?'polaris-admin'].orValue({}) }}
```

`vendor.yaml` lists `polaris-admin` once. Both keys take its defaults and
`values.schema.json` and render with it, each with its own values file: the
index maps `polaris-admin-principals` to `polaris-admin`
([rendering with Helm](01_usage.md#rendering-with-helm)). Everything else, the
instance's upstream values included, is keyed by the key
([aliases](specs/02_service_definition.md#aliases)). To let an instance
set both renders at once, accept its inline values on one key and merge them
into the other in the mapping, as above: the protected paths of the first
key cover both, and `upstream: []` keeps the instance files, which the
mapping does not see, out.

### Generated secrets

Nothing random is computed at compile time: passwords and keys come from
External Secrets Operator generators, generated once in the cluster.

```yaml
let:
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
let:
  settings:
    APP_URL: https://${{ host }}
    METRICS_ENABLED: ${{ str(values.metrics.enabled) }}
output:
  app:
    podAnnotations:
      checksum/settings: ${{ sha256(toJson(settings)) }}
objects:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: ${{ okdpFullname('settings') }}
      labels: ${{ okdpLabels('settings') }}
    data: ${{ settings }}
```

`toJson` sorts the keys: the checksum changes with the content only. Hash
the text the pod reads when there is one (seaweedfs `checksum/iam`, keycloak
`checksum/realm`, the control plane server `checksum/config`).

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
let:
  db: '${{ type(values.database) == map ? values.database : {} }}'
output:
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

```yaml
errors:
  - >-
    ${{ platform.oidc.enabled && platform.oidc.?issuerUri.orValue('') == ''
        ? ['global.okdp.oidc.issuerUri is required. It is a platform value: set it in platform/platform-values.yaml']
        : [] }}
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
(`{{ .Values.configMountPath }}`); `$${{` writes a literal `${{`.

### Keys containing dots

Annotations, Spark properties and environment maps have keys with dots,
which `protect` cannot name. Accept the inline upstream values only
(`upstream: []`: the mapping does not see the instance files) and refuse the
keys in `errors` (seaweedfs, ingress-nginx, spark-history-server):

```yaml
errors:
  - >-
    ${{ cel.bind(a, values.?upstream.?app.?ingress.?annotations.orValue({}),
          a.filter(k, k.startsWith('nginx.ingress.kubernetes.io/auth-')).sort()
            .map(k, 'webapp: upstream.app.ingress.annotations.' + k + ' is set by the platform and cannot be changed')) }}
```

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
- **YAML 1.1.** The mapping, the base values and the vendored defaults are
  read as Helm reads values: plain `no`, `off`, `y` are booleans, `0440` is
  the octal 288. Quote a string that looks like one (`"no"`). `service.yaml`
  alone is read as YAML 1.2: `upstream: yes` names a file called `yes`
  ([YAML reading](specs/02_service_definition.md#yaml-reading)).
- **Untested branches are not compiled.** An expression in a branch no case
  takes is never evaluated, typos included
  ([expressions](specs/02_service_definition.md#lone-expressions-and-interpolation)):
  give every branch a `ci/` case that takes it, and test both OIDC modes:
  the clients' charts are compiled, and the platform's `registrationUrl` and
  `authMethod` required, in `dcr` mode only.
- **A null deletes the default.** A key computed as `null`
  (`${{ values.?x.orValue(null) }}`) removes the upstream chart's default at
  render: leave the key out with `$if`. A parameter set to `null` by a values
  layer loses its default: read it with `values.?x.orValue(<default>)`.
- **Lists replace.** A list of the mapping replaces the list of the base
  values, and an instance's list replaces the mapping's unless the path is in
  `append`. Keep each list wholly in the base values or in the mapping.
- **Names longer than 63 characters.** `${{ release.name }}-something`
  overflows with long project and instance names: use
  `okdpFullname('something')`, cut as `okdp.fullname`. A name a contract's
  convention promises (`<release>-app`, `<release>-hive-metastore`) is kept
  as the convention writes it.
- **No credential in values.** Connections and values carry `secretRef`
  names, never passwords: values end up in Git and in readable objects. A
  secret field in a connection or in `provides` fails the compile.
- **No literal version in the mapping.** Take the image tag from
  `chart.appVersion`, or pin it in the base values next to the chart's
  default: a literal in the mapping is missed when `Chart.yaml` is bumped.
- **An unset connection is `""`.** `values.database.host` fails on an empty
  reference: test `type(...) == map` first, or report it in `errors`.
- **The clients' chart keys are the compiler's.** `oidc-dcr` and
  `<name>-oidc-dcr` name the charts of the declared clients: a
  `service.yaml` chart or an `output` entry of the same key fails the
  compile, in every mode.
- **A client's `secret` is not followed.** It renames the Secret the Job
  writes, in `dcr` mode only; `oidcClientSecret(name)` still returns the
  mode's name, and in `existing` mode the platform's Secret keeps its own.
  Reference the name you set yourself:
  `platform.oidc.dcr.enabled ? mySecret : oidcClientSecret('n')`.
- **Two `chart` keys.** In `service.yaml`, `chart` is the name of a
  vendored chart, not a path (`vendor/polaris-admin` fails); in
  `vendor.yaml`, it is the chart's name in its repository (`okdp-objects`).

## Checklist

- [ ] `vendor.yaml` pins every chart once, `okdp` included, `oidc-dcr`
  (>= 0.5.0) whenever the mapping declares an OAuth client;
  `scripts/vendor-charts.sh --check` passes; no `lookup`, random or
  `.Capabilities` feature is on, the reviewed exceptions are in
  `okdp-guard-allow.yaml`.
- [ ] `service.yaml` and `output` use no client chart key (`oidc-dcr`,
  `<name>-oidc-dcr`); a second render of a chart is a key with `chart:`.
- [ ] `Chart.yaml`: `version` `<upstream>-<okdp semver>`, `appVersion` quoted,
  `okdp.io/roles` and `okdp.io/depends-on`.
- [ ] `values.schema.json` declares `$schema`, every parameter with its
  rules, `global`, `connections`, and `upstream` for exactly the charts that
  accept instance values; strings pasted into files refuse `{{`, `}}` and
  line breaks.
- [ ] `require` lists the platform values read in every mode, `errors` the
  others; `protect` covers names, authentication, exposure, Secret wiring and
  the switches kept off; `append` the lists carrying the platform's wiring.
- [ ] The mapping writes no literal image tag, no credential, no name that
  can exceed 63 characters outside a contract's convention; every OKDP
  object is labelled.
- [ ] `provides` follows the contract's convention and says where the
  credentials come from.
- [ ] The OAuth clients are declared in `oidcClients` and read from
  `platform.oidcClientSecret` and `oidcClientSecret('<name>')`.
- [ ] One `ci/` case per mode and per branch, OIDC `existing` and `dcr`
  included, one failing case per check; `scripts/compile-charts.sh` compiles
  them all and renders each chart from the vendored chart of the index.
- [ ] `README.md` lists the parameters, the platform values read, the charts
  and when each is compiled (the clients' charts: `dcr` mode), and the known
  gaps.
