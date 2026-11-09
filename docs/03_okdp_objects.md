# OKDP's own objects

Besides the upstream charts, a compile deploys objects of OKDP's own: the
credentials Secrets of the connections an instance provides, and the
mapping's `objects` section. This note explains how they reach the
cluster, in the instance's umbrella, and why so. It records the
trade-offs; it changes nothing. The formats are specified in
[the objects chart](specs/01_inputs_outputs.md#the-objects-chart) and
[credentials Secrets](specs/01_inputs_outputs.md#credentials-secrets).

What the instance publishes, its URL, its usage and the connections it
provides, is no object: it stays in git, `okdp.info.yaml`, which a
control plane or a console reads in the deployments repository
([the info file](specs/01_inputs_outputs.md#the-info-file)).

## The objects chart

### What it holds

`Result.Objects`, which `compiler.Write` (`okdp-compile -o`) writes in the
umbrella's values, the string `manifests` of the key `okdp-objects`, when
the instance has objects: a YAML stream, one document per object, in this
order:

- the credentials Secrets of the connections the instance provides: an
  ExternalSecret and its External Secrets `Password` generators, or a plain
  Secret;
- the mapping's [`objects`](specs/02_service_definition.md#objects)
  section;
- the credentials of claims across namespaces
  ([claims](specs/01_inputs_outputs.md#credentials-across-namespaces)): a
  provider's PushSecret of each claim of another namespace, a claimer's
  ExternalSecret of each of its claims of another namespace; and the
  ExternalSecret of each connection it references whose credentials are
  in the platform's secret store ([credentials in the platform's secret
  store](specs/01_inputs_outputs.md#credentials-in-the-platforms-secret-store)).
  The platform's secret store does not keep the namespaces' keys apart by
  itself: what it must enforce is in [What the store must
  enforce](specs/01_inputs_outputs.md#what-the-store-must-enforce).

An instance with neither has no `okdp-objects` chart: its umbrella is
the upstream charts alone. Unless the project is one of the platform's own
(`global.okdp.policy.privileged`), every object stays in the instance's
namespace and none is of a cluster-scoped kind: the compile refuses them
([objects section](specs/01_inputs_outputs.md#objects-section)). And no
Secret holds values in clear, unless the caller allows it
(`Inputs.AllowPlainSecrets`): the objects are in the deployments
repository. Nor do two objects of one kind, namespace and name coexist
in an instance, whatever the project: the second would replace the first.
A name derived from a claim's ID, the database or the role of a claim,
the object of a topic, is `okdpSqlName(id)` or `okdpObjectName(id)`, one
name per ID, never the ID with its dots and dashes replaced by one
character, which gives `a-b.c` and `a.b-c` one name
([functions](specs/04_functions.md#okdpsqlname-and-okdpobjectname)).

A workload the package adds, a Job, a web service with its volume, its
Ingress and its sign-in proxy, is written with the fragments the compiler
provides (`okdpWebWorkload`, `okdpJob`, `okdpSignInProxy`,
`okdpPodSpec`:
[functions](specs/04_functions.md#fragments-okdppodspec-okdpjob-okdpsigninproxy-okdpwebworkload),
`examples/05_fragments`), hardened once, not by hand in each package.

The `objects` section carries real weight: on 2026-10-12, 15 of the 24
packages of the `okdp-compiler` branches of platform-charts and
sandbox-charts use it. Most of it is Secrets
([`okdpGeneratedSecret`](specs/04_functions.md#okdpgeneratedsecret)
11 times, plain Secrets 14 times); the rest is ServiceAccounts, Roles, Jobs,
ConfigMaps, SecretStores, ValidatingAdmissionPolicies, and even Deployments
and Services.

### How it reaches the cluster

OKDP does not modify an upstream chart ([values for unmodified upstream
charts](00_architecture.md#values-for-unmodified-upstream-charts)): what
an upstream chart cannot produce needs a vehicle. The vehicle is the
`okdp-objects` chart, one more dependency of the instance's umbrella when
it has objects, pinned as the oidc-dcr chart is
(`global.okdp.charts.objects`). Its one template writes its value as it
is, `{{- .Values.manifests }}`.

- **One string.** Objects hold `{{` that is not Helm's: the template of
  an ExternalSecret (`{{ index . "password" }}`), any string of the
  `objects` section. Written in `templates/`, the manifests would be
  executed by Helm. As structured values, an integer would become a float
  and a null would be deleted against the chart's defaults. A string
  reaches the template as written: Helm renders each document as one
  manifest, its strings, nulls and numbers unchanged
  (`testdata/helm/umbrella`, [Helm fixtures](00_architecture.md#helm-fixtures)).
- **One release.** The objects are rendered with the upstream charts,
  under the release name and namespace of the compile, by the same
  deployer (one HelmRelease or Application per instance,
  [GitOps](01_usage.md#gitops)), and pruned with them: an object the next
  compile no longer writes, or the whole instance removed, is deleted with
  the release, but one Helm keeps (`helm.sh/resource-policy: keep`),
  which Flux's helm-controller leaves as Helm does, and Argo CD through
  the sync options the compiler adds to it, `Prune=false` and
  `Delete=false` ([the objects
  chart](specs/01_inputs_outputs.md#the-objects-chart)). The chart gets
  no `global`, and a check does not read it.
- **Helm's install order.** Helm installs a release's manifests sorted by
  kind, the umbrella's and its subcharts' together: Namespaces,
  ServiceAccounts, Secrets and ConfigMaps before the workloads, the kinds
  it does not know (an ExternalSecret, a `Password` generator) last, by
  kind. It does not wait between them: a pod whose Secret an ExternalSecret
  writes starts once External Secrets has written it. Argo CD applies a
  render in its own order, by sync wave then by kind. The label OKDP's
  objects carry, `app.kubernetes.io/managed-by: Helm`, holds.
- **Reach.** Every deployer must reach the chart. A site that cannot
  reach the default repository sets `global.okdp.charts.objects`, to a
  chart that renders its `manifests` value as written.
- **Under Flux.** The objects travel with the values, in the ConfigMap
  `values-<release>` of the namespace `okdp-releases`, at most 1 MiB, which
  `--target flux` enforces. With `Inputs.AllowPlainSecrets`, the data of a
  plain Secret is in that ConfigMap too.
- **Plain manifests.** The value is the objects as applied: a deployer
  that does not render the umbrella applies it as it is,
  `yq '.["okdp-objects"].manifests' instance.values.yaml | kubectl apply -f -`.

### Other vehicles

The objects cannot go: consumers find a provider's credentials through the
Secret its `secretRef` names, and a password generated at compile time
would change at every render, which GitOps tools do at every sync
([stateless, hermetic](00_architecture.md#stateless-hermetic-concurrent)):
External Secrets generates them in the cluster. Only the vehicle can
change.

**A file of the umbrella**, `okdp.objects.yaml`, that a template the
compiler writes includes with `.Files.Get`.

- Pros: no chart for a deployer to reach.
- Cons: the compiler writes a template in every output; a subchart cannot
  read the file, as `.Files` is each chart's own.

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

## The project's network policy

The NetworkPolicy of a project's namespace (`global.okdp.network.policy`)
is no object of an instance: it is the platform's decision, as the
Namespace and the roles are, and the targets write it with the project's
manifests, `projects/<namespace>/project/network-policy.yaml`, for a
project kept too ([the project's
manifests](specs/01_inputs_outputs.md#the-projects-manifests)). An
instance's own NetworkPolicy, of its `objects` section, adds to it (the
policies of a namespace are a union): it may open its pods further, to
another namespace, never close what the platform's lets in. Its ingress
is the namespace's, the platform's ingress namespaces' and the
namespaces of the claims its instances provide, which the compile binds:
a provider stays reachable by its claimers without a rule of its
package.
