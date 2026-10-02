# OKDP's own objects

Besides the upstream charts, a compile deploys objects of OKDP's own: the
descriptor ConfigMap, the credentials Secrets of the connections an instance
provides, and the mapping's `objects` section. This note explains why they
reach the cluster through a chart of the platform, okdp-objects, what reads
the descriptor, and what removing either would cost. It records the
trade-offs; it changes nothing. The formats are specified in
[the okdp chart](specs/01_inputs_outputs.md#the-okdp-chart),
[descriptor ConfigMap](specs/01_inputs_outputs.md#descriptor-configmap) and
[credentials Secrets](specs/01_inputs_outputs.md#credentials-secrets).

## The okdp-objects chart

### What it does

okdp-objects is a generic chart of the platform: its template writes each
item of `.Values.objects` as a manifest. Every compile adds the chart key
`okdp`, rendered by the chart the platform names in
`global.okdp.charts.objects`
([platform charts](specs/01_inputs_outputs.md#platform-charts)), with
`Result.Objects` as its values:

- the descriptor ConfigMap, `<release>-okdp`;
- the credentials Secrets of the connections the instance provides: an
  ExternalSecret and its External Secrets `Password` generators, or a plain
  Secret;
- the mapping's [`objects`](specs/02_service_definition.md#objects)
  section.

The `objects` section carries real weight: on 2026-10-12, 15 of the 24
packages of the `okdp-compiler` branches of platform-charts and
sandbox-dependencies use it. Most of it is Secrets
([`okdpGeneratedSecret`](specs/02_service_definition.md#okdpgeneratedsecret)
11 times, plain Secrets 14 times); the rest is ServiceAccounts, Roles, Jobs,
ConfigMaps, SecretStores, ValidatingAdmissionPolicies, and even Deployments
and Services.

### Why it exists

OKDP does not modify an upstream chart
([values for unmodified upstream charts](00_architecture.md#values-for-unmodified-upstream-charts)):
what an upstream chart cannot produce needs a vehicle. The okdp-objects
chart makes these objects one more Helm release beside the upstream ones:
the same release name and namespace, the same deployer (an Argo CD source
per key of the index, or a subchart of the instance's umbrella), pruned
when they change or the instance is removed ([GitOps](01_usage.md#gitops)).

### Removing the chart

The objects cannot go: consumers find a provider's credentials through the
Secret its `secretRef` names, and a password generated at compile time
would change at every render, which GitOps tools do at every sync
([stateless, hermetic](00_architecture.md#stateless-hermetic-concurrent)):
External Secrets generates them in the cluster. Only the vehicle can
change.

**Plain manifests, applied by the deployer.** The compiler writes the
objects as a multi-document file; Flux applies it with a Kustomization,
Argo CD with a directory source.

- Pros: no chart of the platform to publish, version and hold in the chart
  cache: `global.okdp.charts.objects` and the cache's `okdp-objects-<version>/`
  go, which fits packages that need nothing pushed to a registry; the output
  is simpler.
- Cons: a second kind of source per instance. The deployer, and the control
  plane server, which generates one HelmRelease per chart key, need a
  second path. No Helm hooks. The objects must be ordered against the charts
  that read them (a Secret before its pods) with `dependsOn` or sync waves.
  Their `app.kubernetes.io/managed-by: Helm` label no longer holds.

**An upstream chart's `extraObjects` or `extraDeploy` value.**

- Pros: no release of its own.
- Cons: not every chart has such a value, and its name differs from one
  chart to the next; the objects live and die with that chart (a chart the
  mapping leaves out with `$if` takes them along); the descriptor hangs on
  an arbitrary chart. It does not generalize.

Conclusion: if the chart is the burden, plain manifests are the
alternative, and the cost moves to the deployer and the control plane.

## The descriptor ConfigMap

### What it holds

`<release>-okdp`, built from the mapping's `instance` and `provides`
sections: `service`, `version`, `url`, `usage`, and `outputs.yaml`, the
connections the instance provides, with their `secretRef`. Its labels:
`okdp.io/instance`, `okdp.io/service`, and `okdp.io/provides-<contract>`
per contract provided.

### What reads it

Not the compiler. An internal reference resolves through the naming
convention of its contract (`x-okdp-internal`,
[contracts](specs/01_inputs_outputs.md#contracts)), and the check that a
provider publishes what its consumers compute runs at compile time.

The control plane server reads it from the cluster
(`DescriptorRepository`):

- `List` and `Watch` feed the list of instances (URL, usage, version) and
  its live updates;
- `ConnectionService.ListInternal` and `ListSelectable` feed the internal
  connections a deployment form offers: what the deployed instances
  provide.

### Removing the descriptor

Possible: everything it holds is known at compile time.

- Pros: one ConfigMap less per instance; the control plane no longer lists
  and watches ConfigMaps in the project namespaces; the compile-time checks
  are unchanged.
- Cons:
  - the cluster no longer says what is actually deployed: the descriptor
    appears once the sync has applied it, the compile is what git holds.
    The console's deployed view and its live updates would come from the
    GitOps engine's status;
  - the control plane would need the compile result of each instance: kept
    somewhere, or recompiled to list, pulling the charts;
  - instances deployed without the control plane (the CLI and plain GitOps)
    are no longer discoverable by any tool in the cluster;
  - it is a contract shared with other readers ("shared no-kubocd contract"
    in the control plane server): they break too.

Removing the descriptor alone does not remove the okdp-objects chart: the
credentials Secrets and the `objects` section still need it, or a vehicle
from [removing the chart](#removing-the-chart).
