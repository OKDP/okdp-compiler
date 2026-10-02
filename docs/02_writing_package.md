# Writing a package

How to package an upstream application for OKDP: a directory of parameters,
base values and a service definition, compiled by `okdp-compile`. The steps
follow a `webapp` service, of which the example of this repository is a
smaller version (`example/input/package/`); the
recipes and pitfalls come from the services packaged in
`OKDP/platform-charts` and `OKDP/sandbox-dependencies`. What each file may contain is specified in
[the service definition](specs/02_service_definition.md) and
[inputs and outputs](specs/01_inputs_outputs.md), the command line
in [usage](01_usage.md), the internals in [architecture](00_architecture.md).

## What a package is

A package is a directory of a git repository, not a chart. It carries the
parameters an instance sets (`values.yaml`, `values.schema.json`), OKDP's
default values for each upstream chart (`base/`), and the service
definition. `service.yaml` names the service, the upstream charts it is
made of, each by its source (repository, chart, pinned version), and what
an instance may override; `mapping.cel.yaml` says how the values of each
chart follow from the parameters, the platform values (`global.okdp`) and
the connections, and declares the service's OAuth clients. `okdp-compile`
turns the values of one instance into the final values of every chart and
OKDP's own objects; a deployer then renders each upstream chart from its
repository with its values, as the
[chart index](specs/01_inputs_outputs.md#chart-index) names it. Nothing of
the package is published as a chart.

```
<name>/
├── service.yaml            name, version, appVersion, roles, dependsOn; the charts and their sources, base values, required platform values, protect, append
├── mapping.cel.yaml        computed values, OAuth clients, OKDP's own objects, descriptor, provided connections
├── values.yaml             the parameters, with their defaults
├── values.schema.json      their schema: types, rules, x-ui-* hints, x-okdp-connection-ref
├── base/<key>.yaml         base values of each chart: plain YAML
├── ci/<case>-values.yaml   test instances, each with its global.okdp block
└── README.md               parameters, platform values read, charts, known gaps
```

`--package` names the package directory, `--charts` the chart cache the
compiler reads the upstream charts' defaults and schemas from
(`okdp-compile --package example/input/package --charts example/input/charts`).

## Step by step

### 1. Choose the upstream charts

Each upstream chart the service is made of is a chart key of `service.yaml`
with its source: the chart repository (`https://`) or OCI registry path
(`oci://`), the chart's name in it, and a pinned version. The package holds
no copy of it: a deployer renders the chart from its repository, and the
compiler reads its `values.yaml` and `values.schema.json` from the chart
cache, filled with `helm pull`
([chart cache](specs/01_inputs_outputs.md#chart-cache)).

```yaml
# service.yaml (airflow)
charts:
  airflow:                    # the chart key: output.airflow of the mapping
    source: {repository: https://airflow.apache.org, chart: airflow, version: 1.22.0}
```

```bash
helm pull airflow --repo https://airflow.apache.org --version 1.22.0 -d "$OKDP_CHARTS"
```

- The key names the chart's values everywhere (`output`, the instance's
  `upstream.<key>`, the emitted `<key>.values.yaml`); it need not be the
  chart's name.
- A compile that needs a chart the cache lacks fails with the `helm pull`
  command that fetches it.
- A chart rendered twice is two keys of one source
  ([recipe](#render-a-chart-twice)).
- The chart is rendered as published. A bundled subchart the package does
  not use is disabled by values, in the base values, and protected (superset
  disables the bundled `postgresql` and `redis` and runs a Valkey of its
  own). Part of a chart alone (trust-manager's CRDs, in cert-manager) cannot
  be rendered: render the chart with its switches, or leave the part to
  another package. CRDs upgraded with the release (spark-operator) are a
  deployer setting: `upgrade.crds: CreateReplace` on the Flux HelmRelease;
  Argo CD applies `crds/` on every sync.
- `okdp` and `oidc-dcr` are not charts of the package: the compiler adds
  them, the okdp chart of OKDP's own objects in every compile, one oidc-dcr
  chart per OAuth client in `dcr` mode, and the platform names the charts
  that render them (`global.okdp.charts`:
  [platform charts](specs/01_inputs_outputs.md#platform-charts)).

Flux (`helm install`) and Argo CD (`helm template`) must render the same
objects. An upstream feature built on `lookup`, a random function, `genCA` or
`.Capabilities` breaks that: keep it disabled and protect its switch
(step 4).

### 2. Name the package

```yaml
# service.yaml
name: webapp
version: 2.3.0-1.0.0      # <upstream>-<okdp semver>
appVersion: "2.3"
roles: [webapp]           # what the service provides in the catalog
dependsOn:                # the platform services it needs
  - database-server
  - ingress
```

`name` and `version` are required. The upstream half of `version` stays
SemVer (`480.0.0-1.0.0` for Trino 480): the console compares versions;
release-please owns the OKDP half. `service.yaml` is read as YAML 1.2, the
versions as written: `appVersion: 1.10` is `"1.10"`. The mapping sees them
as `service` (`service.appVersion`); the labels of OKDP's objects take
`app.kubernetes.io/name`, `okdp.io/service` and `app.kubernetes.io/version`
from them, the descriptor its `service` and `version`. `roles` and
`dependsOn` are the console's: the compiler does not read them
([service](specs/02_service_definition.md#service)).

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
    source: {repository: https://charts.example.org, chart: app, version: 1.8.0}
    base: base/app.yaml                  # OKDP's defaults, plain YAML
    upstream: true                       # upstream.app.yaml, then inline upstream.app
    protect: [fullnameOverride, ingress.enabled, ingress.hosts, ingress.tls]
    append: [env]
  metrics:                               # no base values, no instance values
    source: {repository: oci://registry.example.org/charts, chart: metrics, version: 0.4.2}
```

- `require` lists the `global.okdp` keys the mapping reads in every case: the
  compile fails, naming the key, when one is missing or empty. A value needed
  in one mode only is checked in `errors`
  ([recipe](#require-a-platform-value-in-one-mode)).
- `source` names the upstream chart that renders the key's values; two keys
  may name one ([recipe](#render-a-chart-twice),
  [sources](specs/02_service_definition.md#sources)). `okdp` and the charts
  of the OAuth clients (`oidc-dcr`, `<name>-oidc-dcr`) are not listed: the
  compiler adds them ([OAuth clients](#oauth-clients)).
- `base` holds what depends on no parameter, platform value or connection:
  images, probes, switches, static configuration. The mapping's values go
  over it, the instance's over both
  ([merge precedence](specs/01_inputs_outputs.md#merge-precedence)).
- `upstream` lets an instance set any value of the chart, from its files
  and its inline `upstream.<key>` values
  ([upstream](specs/02_service_definition.md#upstream)). Leave it out
  for a chart an instance has no reason to set (the webapp's `metrics`).
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

### 5. Write mapping.cel.yaml

The mapping is YAML shaped like the upstream values, with `${{ CEL }}`
[expressions](specs/02_service_definition.md#expressions) and
[directives](specs/02_service_definition.md#directives) (`$if`, `$for`,
`$merge`, `$spread`, `$let`). Its [sections](specs/02_service_definition.md#sections),
as the webapp writes them (the
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
      tag: ${{ service.appVersion }}
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
  it is neither compiled nor rendered (the webapp's `metrics`, trino's
  `opa-kube-mgmt` and `opal`). `okdp` is not a key of `output`: the
  compiler adds the okdp chart to every compile, so the descriptor always
  reaches the cluster
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
  no chart and writes everything here.
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
`example/input/package/mapping.cel.yaml`). `s3` and `database-server` have no internal
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
(the platform values, `charts.objects` included), then connections and
parameters. Write one case per mode: OIDC `existing`, `dcr` and off,
internal and external connections, proxy set, each optional chart on,
upstream values with appended lists. The `dcr` case gives the platform's `oidc.dcr.registrationUrl` and
`authMethod: anonymous` (polaris's `ci/dcr-values.yaml`).

```bash
export OKDP_CONTRACTS=~/okdp/contracts     # the contract schemas
export OKDP_CHARTS=~/.cache/okdp/charts   # the chart cache, filled with helm pull
okdp-compile --package charts/services/polaris \
  -f charts/services/polaris/ci/dcr-values.yaml \
  --release demo-polaris --namespace demo --emit-dir /tmp/demo-polaris
yq '.polaris' /tmp/demo-polaris/okdp.charts.yaml   # {repository, chart, version}
helm template demo-polaris oci://quay.io/adaltas/oidc-dcr --version 0.5.0 -n demo \
  -f /tmp/demo-polaris/console-oidc-dcr.values.yaml
scripts/compile-charts.sh charts/services/polaris   # every case, compiled and rendered
```

- Render with the release name and namespace of the compile: the upstream
  charts derive their names from the release.
- Render each chart with the source the index `<emit-dir>/okdp.charts.yaml`
  names for its key: `helm template <release> <chart> --repo <repository>
  --version <version>`, `oci://<repository>/<chart>` for a registry, or the
  `<chart>-<version>.tgz` `helm pull` wrote in the chart cache. The clients'
  charts render with the platform's oidc-dcr chart, `polaris-admin-principals`
  with the source of `polaris-admin`
  ([rendering with Helm](01_usage.md#rendering-with-helm));
  `scripts/compile-charts.sh` reads the index.
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
phases) are two chart keys of `service.yaml` with the same source:

```yaml
# service.yaml
charts:
  polaris-admin:
    source: &admin {repository: https://charts.example.org, chart: polaris-admin, version: 1.0.0}
    base: base/polaris-admin-bootstrap.yaml
    upstream: true                     # upstream.polaris-admin.yaml, then inline upstream.polaris-admin
    protect: [fullnameOverride, phase, realms]
  polaris-admin-principals:            # the values of a second render
    source: *admin                     # the same chart
    base: base/polaris-admin-principals.yaml
    upstream: {from: polaris-admin}    # the instance values of polaris-admin, its protect
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
```

Both keys take the chart's defaults and `values.schema.json` and render
with it, each with its own values file: the index maps both to the same
source ([rendering with Helm](01_usage.md#rendering-with-helm)). Everything
else is keyed by the key ([sources](specs/02_service_definition.md#sources)).
`upstream: {from: polaris-admin}` gives the second render the instance's
upstream values of the first, its files and its inline values, with the
first key's `protect` and `append`: an instance sets both renders at once,
refused or appended the same way, and the mapping sees the values once, as
`values.upstream.polaris-admin`
([upstream](specs/02_service_definition.md#upstream)).

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

Annotations, Spark properties and environment maps have keys with dots. In
a `protect` or `append` path, write such a key in brackets, and a family of
keys with a final glob
([protect and append](specs/02_service_definition.md#protect-and-append)):

```yaml
charts:
  app:
    protect:
      - 'ingress.annotations["nginx.ingress.kubernetes.io/auth-*"]'   # every auth-* annotation
      - 'config["spark.ui.filters"]'                                  # one key, dots included
```

The path covers the instance's files and inline values alike:

```text
app: upstream.app.yaml: ingress.annotations["nginx.ingress.kubernetes.io/auth-url"]: ingress.annotations["nginx.ingress.kubernetes.io/auth-*"] is set by the platform and cannot be changed
```

A rule no path expresses (a value that depends on a parameter) goes in
`errors`: the mapping sees the instance's files and inline values merged,
as `values.upstream.<key>`.

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
- **YAML 1.1.** The mapping, the base values and the upstream charts'
  defaults are read as Helm reads values: plain `no`, `off`, `y` are booleans, `0440` is
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
  `service.appVersion`, or pin it in the base values next to the chart's
  default: a literal in the mapping is missed when `service.yaml` is bumped.
- **An unset connection is `""`.** `values.database.host` fails on an empty
  reference: test `type(...) == map` first, or report it in `errors`.
- **Some chart keys are the compiler's.** `okdp` names the chart of OKDP's
  own objects, `oidc-dcr` and `<name>-oidc-dcr` the charts of the declared
  clients: a `service.yaml` chart or an `output` entry of the same key fails
  the compile, in every mode.
- **A client's `secret` is not followed.** It renames the Secret the Job
  writes, in `dcr` mode only; `oidcClientSecret(name)` still returns the
  mode's name, and in `existing` mode the platform's Secret keeps its own.
  Reference the name you set yourself:
  `platform.oidc.dcr.enabled ? mySecret : oidcClientSecret('n')`.
- **A source is pinned.** `version` is one version, not a range (`^1.2`,
  `1.x` fail): bump it in `service.yaml`, then pull the new version into the
  chart cache.

## Checklist

- [ ] Every chart of `service.yaml` has its `source`, version pinned; a
  bundled subchart the package does not use is disabled by values; no
  `lookup`, random or `.Capabilities` feature is on.
- [ ] `service.yaml` and `output` use no chart key of the compiler (`okdp`,
  `oidc-dcr`, `<name>-oidc-dcr`); a second render of a chart is a key with
  the same source.
- [ ] `service.yaml`: `name`, `version` `<upstream>-<okdp semver>`,
  `appVersion`, `roles` and `dependsOn`.
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
  them all and renders each chart with the source the index names.
- [ ] `README.md` lists the parameters, the platform values read, the charts
  and when each is compiled (the clients' charts: `dcr` mode), and the known
  gaps.
