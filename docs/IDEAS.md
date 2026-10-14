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
