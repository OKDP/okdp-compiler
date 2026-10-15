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
  `secretRef` (`okdp.instance.yaml`);
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
- **Internal connections for a deployment form.** What the deployed
  instances of a namespace provide, by contract (a label selector on
  `okdp.io/provides-<contract>`), with their fields and `secretRef`: the
  values an internal reference resolves to, for instances that run.
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
`okdp.instance.yaml`, beside the umbrella, where a console reads it from
the deployments repository; Helm ignores the file
([instance](specs/02_service_definition.md#instance),
[the instance file](specs/01_inputs_outputs.md#the-instance-file)). Helm
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
`okdp.instance.yaml`. `instance.url` is unchanged: a CEL string of the
`instance` section, written to `okdp.instance.yaml`, a field a console
opens rather than text it shows. The instance file keeps `url` and
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
  instance file.

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
  ([values for unmodified upstream charts](00_architecture.md#values-for-unmodified-upstream-charts)).
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
  usage shows no diff of it, where `okdp.instance.yaml` is committed and
  diffed with the values.

### A middle way

Keep `usage` in the `instance` section, evaluated and checked by the
compile, and have Helm keep it too: the umbrella's `templates/NOTES.txt`,
the same in every output, written when the instance has a usage, prints
it as written, as `templates/okdp.yaml` includes the objects:

```
{{- (.Files.Get "okdp.instance.yaml" | fromYaml).usage -}}
```

Helm templates no part of the usage (a `{{` in it is printed), and a
template the compiler writes cannot fail an install. `helm install` and
`helm get notes` show the usage under Flux and plain Helm; Argo CD and the
console keep `okdp.instance.yaml`. It is an addition, not a replacement:
the repository stays what a console reads, or the
[descriptor ConfigMap](#a-descriptor-configmap-per-instance), which puts
the usage in the cluster under Argo CD too, readable with a role on
ConfigMaps; the notes serve the operators who use the Helm CLI.
