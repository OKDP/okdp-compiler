# OKDP's own objects

Besides the upstream charts, a compile deploys objects of OKDP's own: the
credentials Secrets of the connections an instance provides, and the
mapping's `objects` section. This note explains how they reach the
cluster, in the instance's umbrella, and why so. It records the
trade-offs; it changes nothing. The formats are specified in
[the objects file](specs/01_inputs_outputs.md#the-objects-file) and
[credentials Secrets](specs/01_inputs_outputs.md#credentials-secrets).

What the instance publishes, its URL, its usage and the connections it
provides, is no object: it stays in git, `okdp.instance.yaml`, which a
control plane or a console reads in the deployments repository
([the instance file](specs/01_inputs_outputs.md#the-instance-file)).

## The objects file

### What it holds

`Result.Objects`, which `compiler.Write` (`okdp-compile -o`) writes to
`okdp.objects.yaml`, beside the umbrella's `Chart.yaml` and values, when
the instance has objects: a YAML stream, one document per object, in this
order:

- the credentials Secrets of the connections the instance provides: an
  ExternalSecret and its External Secrets `Password` generators, or a plain
  Secret;
- the mapping's [`objects`](specs/02_service_definition.md#objects)
  section.

An instance with neither has no objects file: its umbrella is the
upstream charts alone.

The `objects` section carries real weight: on 2026-10-12, 15 of the 24
packages of the `okdp-compiler` branches of platform-charts and
sandbox-dependencies use it. Most of it is Secrets
([`okdpGeneratedSecret`](specs/04_functions.md#okdpgeneratedsecret)
11 times, plain Secrets 14 times); the rest is ServiceAccounts, Roles, Jobs,
ConfigMaps, SecretStores, ValidatingAdmissionPolicies, and even Deployments
and Services.

### How it reaches the cluster

OKDP does not modify an upstream chart
([values for unmodified upstream charts](00_architecture.md#values-for-unmodified-upstream-charts)):
what an upstream chart cannot produce needs a vehicle. The vehicle is the
instance's umbrella, the chart the compiler writes: its template
`templates/okdp.yaml`, the same in every output, includes the file as
written, `{{ .Files.Get "okdp.objects.yaml" }}`.

- **Not templates.** Objects hold `{{` that is not Helm's: the template of
  an ExternalSecret (`{{ index . "password" }}`), any string of the
  `objects` section. Written in `templates/`, the manifests would be
  executed by Helm. `.Files.Get` returns the file as written: Helm renders
  each document as one manifest, its strings, nulls and numbers unchanged
  (`testdata/helm/umbrella`, [Helm fixtures](00_architecture.md#helm-fixtures)).
- **One release.** The objects are rendered with the upstream charts,
  under the release name and namespace of the compile, by the same
  deployer (one HelmRelease or Application per instance,
  [GitOps](01_usage.md#gitops)), and pruned with them: an object the next
  compile no longer writes, or the whole instance removed, is deleted with
  the release. Nothing is published, and the chart cache of a check holds
  nothing for them.
- **Helm's install order.** Helm installs a release's manifests sorted by
  kind, the umbrella's and its subcharts' together: Namespaces,
  ServiceAccounts, Secrets and ConfigMaps before the workloads, the kinds
  it does not know (an ExternalSecret, a `Password` generator) last, by
  kind. It does not wait between them: a pod whose Secret an ExternalSecret
  writes starts once External Secrets has written it. Argo CD applies a
  render in its own order, by sync wave then by kind. The label OKDP's
  objects carry, `app.kubernetes.io/managed-by: Helm`, holds.
- **Plain manifests.** The file is the objects as applied, its first line a
  comment: a deployer that does not render the umbrella applies it as it
  is (`kubectl apply -f`, a Flux Kustomization, an Argo CD directory
  source).

### Other vehicles

The objects cannot go: consumers find a provider's credentials through the
Secret its `secretRef` names, and a password generated at compile time
would change at every render, which GitOps tools do at every sync
([stateless, hermetic](00_architecture.md#stateless-hermetic-concurrent)):
External Secrets generates them in the cluster. Only the vehicle can
change.

**The file applied by the deployer**, not by the umbrella: Flux applies it
with a Kustomization, Argo CD with a directory source.

- Pros: no Helm for the objects.
- Cons: a second source per instance: the deployer, and whatever
  generates its objects, need a second path. No Helm hooks. The objects
  must be ordered against the charts that read them (a Secret before its
  pods) with `dependsOn` or sync waves, and are pruned by that source,
  not the release. Their `app.kubernetes.io/managed-by: Helm` label no
  longer holds.

**An upstream chart's `extraObjects` or `extraDeploy` value.**

- Pros: no template of the umbrella.
- Cons: not every chart has such a value, and its name differs from one
  chart to the next; the objects live and die with that chart (a chart the
  mapping leaves out with `$if` takes them along). It does not generalize.
