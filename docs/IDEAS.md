# Ideas

What the compiler could do, and what it would bring. Nothing here is
decided or implemented.

## A descriptor ConfigMap per instance

Each compile would add one object to the instance's
[objects](03_okdp_objects.md), a ConfigMap `<release>-okdp` (cut as
`okdpFullname('okdp')`) that describes the instance in its namespace, with
what a compile writes to git about it:

- `service` and `version`: the package, the `name` and `version` of its
  `Chart.yaml`;
- `url` and `usage`: where to open the instance and what it is for, its
  `instance` section;
- `outputs.yaml`: the connections the instance provides, its `provides`
  section as the compiler checked it against the contracts, each with its
  `secretRef` (`okdp.info.yaml`);
- the labels of OKDP's objects (`okdp.io/instance`, `okdp.io/service`),
  and `okdp.io/provides-<contract>` per contract it provides.

It would be one more document of `okdp.objects.yaml`, rendered in the
instance's release by its umbrella, created and deleted with it.

### Advantages

- **The cluster says what runs.** The ConfigMap exists once the release is
  applied and goes when it is uninstalled: a reader lists the instances
  deployed, not those git holds or a sync has yet to apply, without reading
  the status of the GitOps engine.
- **One way to find every instance**, whoever deployed it: a console, a
  pipeline running `okdp-compile` then `helm upgrade`, plain Flux or Argo CD
  objects. A tool lists a namespace's instances by label, with no access to
  the deployments repository and no knowledge of its layout.
- **Live updates.** A console watches ConfigMaps by label and sees an
  instance appear, change version or go, in one watch, beside the workloads
  of the release (`app.kubernetes.io/instance`), instead of following the
  repository's commits and the engine's syncs.
- **Outputs for a deployment form.** What the deployed instances of a
  namespace provide, by contract (a label selector on
  `okdp.io/provides-<contract>`), with their fields and `secretRef`: the
  outputs a reference may name, of instances that run.
- **Documentation beside the workloads.** `kubectl get configmap -l
  okdp.io/instance` and the `usage` text tell an operator what an instance
  is, its version and how to use it, from the cluster alone.
- **Access control of the cluster.** A reader needs `get`, `list` and
  `watch` on the ConfigMaps of the namespaces it shows, a role per project,
  instead of credentials to the git repository.
- **Small.** One ConfigMap per instance, computed by the compile like the
  other objects: no controller, no custom resource, no new component.

### What it would take

- The compiler: the ConfigMap as a template in the mapping language,
  evaluated in the compile's session, from the checked `instance` and
  `provides` sections; one more object of `Result.Objects`.
- Its readers: a label selector and a watch on ConfigMaps; the format of
  `outputs.yaml` becomes a contract between the compiler and them.

## Helm's NOTES.txt in place of the instance's usage

Only `usage` is concerned; `url` stays as it is. Today a package says how
to use an instance in `instance.usage` of `okdp.yaml`, beside
`instance.url`: a CEL string the compile evaluates, checks and writes to
`okdp.info.yaml`, beside the umbrella, where a console reads it from
the deployments repository; Helm ignores the file
([instance](specs/02_service_definition.md#instance),
[the info file](specs/01_inputs_outputs.md#the-info-file)). Helm
has its own convention for that text: a chart's `templates/NOTES.txt`,
rendered at each install and upgrade with the release's values, printed by
`helm install` and `helm upgrade`, and stored in the release record, one
copy per revision (`helm get notes <release> [--revision <n>]`,
`helm status`).

The package would carry a `NOTES.txt` beside `okdp.yaml`, a Go template,
and the compile would copy it as the umbrella's `templates/NOTES.txt`. It
sees what the umbrella's templates see: `.Release`, `.Chart` and the
compiled values by chart key, not the parameters nor the `vars` of the
mapping. The webapp's:

```
A web application. Open https://{{ (index .Values.app.ingress.hosts 0).host }}
```

`instance.usage` would go, and with it the `usage` key of
`okdp.info.yaml`. `instance.url` is unchanged: a CEL string of the
`instance` section, written to `okdp.info.yaml`, a field a console
opens rather than text it shows. The info file keeps `url` and
`provides`, and an instance that has neither has no file.

### Advantages

- **Helm's convention.** Chart authors know it, upstream charts write one,
  and an operator finds it with `helm status`, with no OKDP tool: a
  pipeline that runs `helm upgrade --install` prints it at the end of the
  deployment.
- **What was deployed.** Rendered with the values Helm applied, stored
  with the release, one per revision (a rollback brings back its notes),
  gone with `helm uninstall`: what runs, not what git holds.
- **The umbrella prints something.** Helm renders a subchart's
  `NOTES.txt` only with `--render-subchart-notes`: under the umbrella, the
  upstream charts' notes are not shown, and the umbrella has none.
- **Helm's functions at install time.** Conditions and loops over the
  values, and `lookup`, the cluster as it is when Helm renders (an object
  of another release; not one of this release at its first install), which
  a compile, hermetic, does not see.
- **Less of OKDP.** `usage` out of the mapping, its schema and the
  info file.

### Drawbacks

- **Argo CD has none.** It renders the umbrella with `helm template`,
  which leaves `NOTES.txt` out, and keeps no release: the notes are
  neither printed nor stored, and the instance's usage would be nowhere.
  The compiler targets Flux and Argo CD alike ([GitOps](01_usage.md#gitops));
  so is any deployer that renders the manifests and applies them itself.
- **A failed release for a sentence.** A mistake in `NOTES.txt`, a value
  path a chart bump moved (`nil pointer evaluating interface {}.ingress`),
  fails the whole install or upgrade, in the cluster, not the compile nor
  `--check` in CI: the compiler embeds no Helm to render it. The `instance`
  section is checked by the compile, its expressions on every branch,
  before anything is committed.
- **A second template language.** The text in Go templates, the values in
  CEL: an author learns both, and the notes re-derive from the upstream
  chart's values what `vars` already computed (the host, the catalog
  names), tied to that chart's layout, or the compile writes them into the
  values for the notes. Helm would run OKDP template code, which it does
  not today: the umbrella's only template includes the objects as written
  ([values for unmodified upstream
  charts](00_architecture.md#values-for-unmodified-upstream-charts)).
- **Read through the release Secrets.** A console reads the notes from
  `sh.helm.release.v1.<release>.v<n>`, a Secret, base64 twice around a
  gzip, that holds the values and the rendered manifests too, the latest
  revision found by listing them (or through the Helm SDK, its storage
  driver and, with Flux, `spec.storageNamespace`). Its role needs `get`
  and `list` on the Secrets of each namespace it shows, the credentials
  Secrets of every instance among them: Kubernetes RBAC selects no Secret
  by label. A role on ConfigMaps, or read access to the repository, is far
  less.
- **Only once deployed, and not in git.** Nothing before Helm installs
  the instance: a console that lists the instances of the repository has
  no usage for one not yet synced, and a pull request that changes the
  usage shows no diff of it, where `okdp.info.yaml` is committed and
  diffed with the values.

### A middle way

Keep `usage` in the `instance` section, evaluated and checked by the
compile, and have Helm keep it too: the umbrella's `templates/NOTES.txt`,
the same in every output, written when the instance has a usage, prints
it as written, as `templates/okdp.yaml` includes the objects:

```
{{- (.Files.Get "okdp.info.yaml" | fromYaml).usage -}}
```

Helm templates no part of the usage (a `{{` in it is printed), and a
template the compiler writes cannot fail an install. `helm install` and
`helm get notes` show the usage under Flux and plain Helm; Argo CD and the
console keep `okdp.info.yaml`. It is an addition, not a replacement:
the repository stays what a console reads, or the
[descriptor ConfigMap](#a-descriptor-configmap-per-instance), which puts
the usage in the cluster under Argo CD too, readable with a role on
ConfigMaps; the notes serve the operators who use the Helm CLI.

## servicebinding.io credentials Secrets

The [Service Binding for
Kubernetes](https://servicebinding.io/spec/core/1.1.0/) specification
gives a Secret that describes a connection a shape standard tooling
reads: a `type` entry, the abstract classification of the binding
(`postgresql`, `mysql`), a `provider` entry, who provides it, and the
Secret's type `servicebinding.io/<type>`; well-known entries (`host`,
`port`, `uri`, `username`, `password`, `certificates`, `private-key`);
entry names in lowercase (`[a-z0-9\-\.]`). A `ServiceBinding` mounts
such a Secret into a workload, its entries as files under
`$SERVICE_BINDING_ROOT/<name>`, from which binding libraries (Spring
Cloud Bindings, Quarkus) configure a client.

Today a [credentials
Secret](specs/01_inputs_outputs.md#credentials-secrets) is `Opaque`,
labelled `okdp.io/credentials-<contract>`, and holds the contract's
secret fields (`x-okdp-secret`) only: the connection's other fields
(`host`, `port`, `dbName`) are its values, which the claimer's mapping
writes into its chart's values. The idea comes in two sizes:

- **The shape alone.** The provider's credentials Secret and the
  claimer's copy [across
  namespaces](specs/01_inputs_outputs.md#credentials-across-namespaces)
  get two more entries, `type` (the contract's name) and `provider` (the
  provider's package), and the Secret type: a generated Secret through
  the `type` argument `okdpGeneratedSecret` already has, the claimer's
  `ExternalSecret` through its target's template (`mergePolicy: Merge`,
  so that the pulled keys stay). The `PushSecret` and the store key are
  unchanged: `type` and `provider` are known at compile time and never
  cross the store.
- **A binding Secret.** The Secret holds the whole connection, its values
  written in as literals beside the credentials, under the well-known
  entry names, with a `type` the libraries recognise.

### Advantages

- **Standard tooling**, with a binding Secret: a workload outside OKDP's
  packages mounts the Secret with a `ServiceBinding`, or reads it with a
  binding library, and connects with no knowledge of OKDP.
- **Credentials by type.** `kubectl get secrets --field-selector
  type=servicebinding.io/s3` lists them, as the
  `okdp.io/credentials-<contract>` label already does.

### Drawbacks

- **The shape alone is of no use.** With the credentials only, a Secret
  is conformant, but no library configures a client from a username and
  a password without the host: a `ServiceBinding` mounts half a
  connection.
- **A binding Secret changes the model.** The connection twice, in the
  claimer's values and in the Secret; the contracts' field names in
  lowercase (`database`, `access-key`), every mapping reading
  `values.s3['access-key']`, or a table from fields to entry names; a
  `type` that may depend on a value (`database-server` is `postgresql`
  or `mysql` by its `engine`).
- **One type per Secret.** A provider whose operator requires its own
  Secret type (`kubernetes.io/basic-auth`) keeps it. The specification
  allows it (the Secret type is a SHOULD), and the shape then differs
  from one provider to another.
- **A second binding mechanism.** OKDP binds at compile time: a claim
  comes from an instance of a package, whose mapping gets the connection
  and its `secretRef` and wires them into its chart. servicebinding.io
  binds at run time, for workloads that read `$SERVICE_BINDING_ROOT`,
  which OKDP does not deploy, through a `ServiceBinding` controller in
  the cluster or workloads that mount the Secret themselves.

### Conclusion

Not now, even with nothing deployed. The shape alone costs two functions
and the fixtures and gives nothing the label does not; a binding Secret
is a model change for consumers OKDP does not have, every claimer being
a mapping the compile already gives the connection.

It becomes worth it once workloads outside OKDP's packages (a project's
own applications, deployed by its own CI) are to use claimed
connections. Then the binding Secret, decided before anything is
deployed, as two of its parts get expensive later: a Secret's type is
immutable (an upgrade fails with `field is immutable` unless the
deployer replaces the Secret), and a renamed contract field breaks every
mapping. A generated Secret's `ExternalSecret` is no easier: at
`refreshInterval: "0"`, a changed template either draws new passwords
the database does not have or never reaches the existing Secret, by the
version of External Secrets. Limited to a Secret of the claimer's, which
the compiler writes with the connection's values in hand (today the copy
across namespaces; one more for a claim of its own namespace), the
provider's Secret unchanged.
