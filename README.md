# okdp-compiler

Compiles an OKDP service instance into the values of its upstream charts
with a YAML + CEL mapping.

Two ways to use it:

- **as a Go library** (`github.com/okdp/okdp-compiler/compiler`): no binary to call,
  everything is read through `fs.FS` and in-memory values;
- **as a binary** (`okdp-compile`), for CI and/or scripts.

## Library

```go
import "github.com/okdp/okdp-compiler/compiler"

def, err := compiler.LoadDefinition(serviceFS) // service.yaml, mapping.cel.yaml
res, err := compiler.Compile(def, compiler.Inputs{
	Chart:     chartFS,     // wrapper chart: Chart.yaml, values.yaml, values.schema.json, vendor/, vendor-values/
	Instance:  instanceFS,  // optional: the instance's upstream values files (upstream.<chart>.yaml)
	Contracts: contractsFS, // e.g. contracts.FS of github.com/okdp/okdp-lib-chart/contracts
	Values:    []map[string]any{instanceValues}, // layers, in order (helm -f); compiler.ParseValues reads one
	Release:   "demo-trino",
	Namespace: "demo",
})
// res.Values:   chart -> final values of the upstream chart (helm template vendor/<res.Charts[chart]> -f them)
// res.Charts:   chart -> the vendored chart that renders them: the chart itself, its
//               service.yaml alias (chart:), oidc-dcr for the OAuth clients (oidcClients)
// res.Computed: chart -> what the mapping computed, before the merges
// res.Objects:  OKDP's own objects: the descriptor ConfigMap, the credentials
//               Secrets of the provided connections, the mapping's objects section
// res.Dependencies: instances the connection references point to (Flux dependsOn, Argo sync waves)
```

`Compile` keeps no state between calls and is safe for concurrent use. Any
`fs.FS` works: `os.DirFS`, `embed.FS`, a git tree, `fstest.MapFS` (see
`compiler/compile_test.go`). `compiler.Compare` checks a result against a
`helm template` output of the wrapper chart.

What Helm did, the compiler does the same way, checked against `helm
template` (`compiler/testdata/helm`):

- every values file is read as Helm reads it (`sigs.k8s.io/yaml`, YAML 1.1:
  `yes`, `no`, `on`, `off` are booleans; every document of a file), and the
  layers are merged and coalesced as Helm does: a `null` deletes a default,
  and stays where there is no default. The mapping's scalars and keys are
  read the same way (YAML 1.1), except the names of `let` and `$let`
  bindings;
- the merged parameters are checked against the wrapper's
  `values.schema.json` before the mapping runs, and the final values of each
  chart against `vendor/<chart>/values.schema.json` when it has one: every
  violation is reported with its path, as Helm reports it;
- `res.Values[chart]`, rendered by Helm, gives what Helm gives with the base
  values, the computed values and the instance's upstream values as `-f`
  files: a `null` that deletes a default stays `null`, the chart's `null`
  defaults are left out.

When `service.yaml` declares a chart named `okdp` (`compiler.ObjectsChart`,
the okdp-objects chart, values `objects: []`), the compiler appends
`res.Objects` to the objects the mapping computes for it
(`output.okdp.objects`, kept first) and compiles it even when the mapping
leaves it out: the descriptor and the generated Secrets reach the cluster
with the upstream charts.

A chart of `service.yaml` is a key, and by default the vendored chart of
the same name (`vendor/<key>`). It may name another vendored chart, its
alias, for a chart rendered more than once:

```yaml
charts:
  polaris-admin: {base: vendor-values/polaris-admin-bootstrap.yaml}
  polaris-admin-principals:            # the values of a second render
    chart: polaris-admin               # vendor/polaris-admin: defaults, schema, render
    base: vendor-values/polaris-admin-principals.yaml
```

The chart is read from `vendor/<chart>` (its defaults and
`values.schema.json`) and rendered with it, so `vendor.yaml` lists the
chart once; everything else stays keyed by the key: the mapping's output,
`res.Values`, `upstream.<key>.yaml` and the inline `upstream.<key>` values,
the values ConfigMap `compiler.Compare` looks for. `res.Charts` maps every
compiled chart to its vendored chart, and so does the CLI's index
(`okdp.charts.yaml`, below): a renderer runs `helm template
vendor/<res.Charts[key]> -f <values of key>`. A missing `vendor/<chart>` is
reported as such.

## Mapping

`mapping.cel.yaml` is YAML shaped like the upstream values, with `${{ CEL }}`
expressions and `$if`/`$for`/`$merge`/`$spread`/`$let` directives (see
`compiler/cel.go`). Sections: `let`, `errors`, `output` (chart -> values),
`instance` (url, usage), `provides` (the connections the instance
publishes; `secret: {generate, stringData}` or `{stringData}` renders their
credentials Secret, `secretRef` defaulting to `<name>-<contract>-credentials`),
`objects` (OKDP's own objects, whole manifests) and `oidcClients` (the
service's OAuth clients, below).

| Context | |
|---|---|
| `release` | `{name, namespace}` |
| `values` | the parameters, connection references resolved |
| `global.okdp` | the platform values, as written |
| `platform` | derived from them: `oidc` (defaults applied; `dcr`: `enabled` and the oidc-dcr settings below; `existing`), `oidcClientSecret`, `ingressAnnotations`, `proxyEnv` |
| `chart` | `{name, version, appVersion}` of `Chart.yaml` |
| `labels`, `selectorLabels` | `okdp.labels`, `okdp.selectorLabels` (a `let` of the same name wins) |

| Function | As |
|---|---|
| `str(x)`, `first(list)` | Go templates' printing, Helm's `default` |
| `sha256(x)` | `sha256sum` of `str(x)` |
| `toJson(x)`, `toPrettyJson(x)`, `toYaml(x)`, `quote(x)` | Helm's |
| `okdpFullname(suffix)` | `okdp.fullname`: `<release>-<suffix>` cut to 63 characters |
| `okdpLabels(component)`, `okdpSelectorLabels(component)` | the labels with `app.kubernetes.io/component` |
| `okdpGeneratedSecret({name, keys, ...})` | the objects of `okdp.generatedSecret`, for `$spread` in `objects` |
| `oidcClientSecret(name)` | the Secret of the OAuth client `name` of `oidcClients` (`''`: the main client, as `platform.oidcClientSecret`) |

### OAuth clients

The services that sign users in declare their OAuth clients in the
`oidcClients` section, a list (directives allowed); the platform provisions
them (`global.okdp.oidc.clientProvisioning: existing`, the default) or each
service registers its own by dynamic client registration (`dcr`):

```yaml
oidcClients:
  # The main client: no name.
  - redirectUris: ['https://${{ host }}/oauth2/callback']
    grantTypes: [authorization_code, refresh_token]   # required
    scope: ${{ platform.oidc.scope }} offline_access  # default platform.oidc.scope; openid is left out
  # Other clients are named (lowercase letters, digits, "-").
  - name: console
    public: true                                      # no secret: token_endpoint_auth_method none
    redirectUris: ['https://${{ consoleHost }}/auth/callback']
    grantTypes: [authorization_code]
```

A client takes `grantTypes` (required), `redirectUris`, `scope`, `clientName`
(default `<release>-<namespace>`, then `-<name>` for a named client),
`public`, `request` (registration fields, over the computed ones), `secret`
(the Secret the Job writes, default below) and `caSecret` (default the
platform's). Every mode checks them: maps of these keys and types,
`grantTypes` given, one client without a name at most, unique names.

In `dcr` mode the compiler adds one chart per client, compiled from
`vendor/oidc-dcr` (the chart `oci://quay.io/adaltas/oidc-dcr` >= 0.5.0,
whose Job, a pre-install/pre-upgrade hook, registers the client anonymously
at the platform's `registrationUrl` and writes its Secret); in `existing`
mode, none:

| Client | Chart (values file) | Objects (Job, ServiceAccount, Role) | Secret, `dcr` | Secret, `existing` |
|---|---|---|---|---|
| main (no name) | `oidc-dcr` | `<release>-oidc-dcr` | `<release>-<namespace>-dcr` | `creds-<release>-oauth2` |
| `name: n` | `n-oidc-dcr` | `<release>-n-oidc-dcr` | `<release>-<namespace>-n-dcr` | `creds-<release>-oauth2-n` |

The mapping reads a client's Secret with `oidcClientSecret('n')` (the main
client's is also `platform.oidcClientSecret`) in any section, `let`
included: the name depends on the mode only, so a client that sets
`secret` references that name itself. These charts belong to the compiler:
`service.yaml` does not list them (a `service.yaml` chart, or an `output`
entry, of the same key is an error), `vendor.yaml` lists `oidc-dcr` once
(a missing `vendor/oidc-dcr` is an error as soon as the mapping declares a
client, in any mode), they take no instance upstream values, and
`res.Charts` maps them to `oidc-dcr`.

Their values are not Go code: [`compiler/oidc-dcr.cel.yaml`](compiler/oidc-dcr.cel.yaml),
a template in the mapping language embedded in the compiler, computes them
for each client from the client, its names and the context. The Go part
(`compiler/oidc.go`) reads and checks the section, applies the mode, names
the charts, objects and Secrets, and adds the charts.

The settings of the oidc-dcr Jobs are platform values,
`global.okdp.oidc.dcr` (`platform.oidc.dcr` in the mapping, defaults
applied; their types are checked in every mode):

| Key | Default | |
|---|---|---|
| `registrationUrl` | (required in `dcr` mode, with a client) | the registration endpoint |
| `authMethod` | (`anonymous` required in `dcr` mode, with a client) | OKDP registers anonymously |
| `image` | the chart's | `registry`, `repository`, `tag`, `pullPolicy`, `pullSecrets`; the former `pull_policy` and `pull_secrets` are renamed (a camelCase key wins) |
| `caSecret` | `certs-bundle` | the Secret holding the CA bundle the Jobs trust (`tls.certificate`; a client's `caSecret` wins) |
| `ttlSeconds` | `30` | how long a finished Job, and its logs, is kept |
| `backoffLimit` | `6` | the Job's retries |
| `waitTtlSeconds` | the chart's, 600 | the longest wait for the provider's discovery document (`waitForProvider.ttlSeconds`); the Jobs wait when `oidc.issuerUri` is set |

## Binary

```bash
make build    # bin/okdp-compile
export OKDP_CONTRACTS=example/contracts   # or --contracts
bin/okdp-compile --chart example/webapp --service example \
  -f example/platform-values.yaml -f example/values/internal-db.yaml --out /dev/stdout
bin/okdp-compile --chart example/webapp --service example \
  -f example/platform-values.yaml -f example/values/internal-db.yaml -f testdata/protected-upstream.yaml
# error: app: upstream.app.ingress.hosts: ingress.hosts is set by the platform and cannot be changed
```

`--emit-dir <dir>` writes the final values of each chart,
`<dir>/<chart>.values.yaml`, the index of the vendored chart that renders
each, `<dir>/okdp.charts.yaml` (`res.Charts`; an excerpt, polaris in `dcr` mode):

```yaml
console-oidc-dcr: oidc-dcr
oidc-dcr: oidc-dcr
polaris: polaris
```

then `<dir>/okdp.objects.yaml` and `<dir>/okdp.dependencies.yaml`. Render
each chart with `helm template <release> vendor/<index[chart]> -f
<dir>/<chart>.values.yaml`. `--out <file>` writes the computed values, the
index (`charts`), the objects and the dependencies.

## Layout

| Path | Description |
|---|---|
| `compiler/compile.go` | public API: `LoadDefinition`, `Compile`, `Inputs`, `Result`, `ObjectsChart` |
| `compiler/context.go` | what a mapping sees: the values layers (checked against the parameter schema), the platform requirements, the platform context derived from `global.okdp`, the chart and its labels |
| `compiler/connections.go` | connection references declared in the parameter schema (`x-okdp-connection-ref`), resolved to an external connection or an instance of the namespace and completed against their contract |
| `compiler/cel.go` | the mapping engine: YAML shaped like the upstream values (read as YAML 1.1), with `${{ CEL }}` expressions and `$if`/`$for`/`$merge`/`$spread`/`$let` directives |
| `compiler/functions.go` | the host functions `sha256`, `toJson`, `toPrettyJson`, `toYaml`, `quote`, `okdpFullname` |
| `compiler/labels.go` | `okdp.labels` and `okdp.selectorLabels`: the `chart`, `labels` and `selectorLabels` of the context, `okdpLabels`, `okdpSelectorLabels` |
| `compiler/secrets.go` | `okdpGeneratedSecret` (`okdp.generatedSecret`) and the credentials Secrets of `provides[].secret` |
| `compiler/oidc.go` | the OAuth clients (`oidcClients`): the platform's oidc-dcr settings (`global.okdp.oidc.dcr`), the checks, the modes, the names of the charts, objects and Secrets (`oidcClientSecret`), the oidc-dcr chart of each client |
| `compiler/oidc-dcr.cel.yaml` | the values of the oidc-dcr chart for one client: a template in the mapping language, embedded |
| `compiler/merge.go` | final values of each upstream chart (from `vendor/<chart>`, or its alias): upstream defaults < base values < computed values < instance `upstream.<chart>.yaml` files < inline instance `upstream:` values, merged as Helm merges `-f` files |
| `compiler/schema.go` | values checked against a `values.schema.json` as Helm checks them |
| `compiler/descriptor.go` | the descriptor ConfigMap `<release>-okdp`, from the mapping's `instance` and `provides` sections, checked against the contracts |
| `compiler/values.go` | YAML/JSON read as Helm values (`ParseValues`), and Helm's merge and coalescing of values |
| `compiler/compare.go` | `Compare`: a result against a `helm template` output of the wrapper chart |
| `compiler/testdata/helm/` | what `helm template` computes on the same inputs (values, schemas, functions, labels, generated Secrets, descriptor), regenerated by `generate.sh` |
| `cmd/okdp-compile/main.go` | binary entrypoint; `--emit-dir` writes the values files and their index |
| `example/` | a complete example service, `webapp`: `service.yaml` and `mapping.cel.yaml` (the service definition, an OAuth client included), `webapp/` (its wrapper chart and vendored chart defaults, the okdp and oidc-dcr charts included), `contracts/`, `platform-values.yaml`, `values/` (instances; `dcr.yaml` registers the OAuth client by DCR) and `compiled/` (their compiled output, checked by `go test`; `go test ./compiler -update` rewrites it) |
| `testdata/` | instance values that must fail on the example, each with its expected error on the first line (`# error: ...`, `\n` for a line break): malformed connections (wrong contract, unknown or secret field, no secretRef), mapping error, protected upstream path, missing platform value, parameter schema violation, DCR without a registration URL |
