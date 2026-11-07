package okdp

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/value"
)

// fragmentScope: what the fragments see of an instance: the release demo-app
// of the project demo, the platform's OIDC, the project's groups, the
// platform's CA bundle.
func fragmentScope(oidc bool) M {
	return M{
		"release": M{"name": "demo-app", "namespace": "demo"},
		"service": M{"name": "app", "version": "1.0.0"},
		"platform": M{
			"oidc":               M{"enabled": oidc, "issuerUri": "https://sso.example.org/realms/okdp", "scope": "openid profile email groups"},
			"oidcClientSecret":   "demo-app-oauth2",
			"ingressAnnotations": M{"cert-manager.io/cluster-issuer": "ca"},
			"access":             M{"viewers": []any{"okdp-project-demo", "ops"}, "editors": []any{"okdp-project-demo", "ops"}, "admins": []any{"ops"}},
		},
		"global": M{"okdp": M{"ingress": M{"className": "nginx"}, "tls": M{"caBundleSecret": "certs"}}},
	}
}

// container: a container of a fragment's arguments.
func container(name string) M {
	return M{"name": name, "image": "registry.example.org/app@sha256:0", "ports": []any{M{"name": "http", "containerPort": 8080.0}}}
}

// hardened: the pod spec's containers are all restricted: no privilege
// escalation, no capability, non-root, the runtime's seccomp profile, a
// read-only root filesystem unless writable names the container; the pod
// non-root with the same profile, no token.
func hardened(t *testing.T, spec M, writable ...string) {
	t.Helper()
	for _, key := range []string{"initContainers", "containers"} {
		for _, c := range value.AsList(spec[key]) {
			c := value.AsMap(c)
			sc := value.AsMap(c["securityContext"])
			want := !slices.Contains(writable, value.Str(c["name"]))
			if sc["allowPrivilegeEscalation"] != false || value.Dig(sc, "capabilities.drop") == nil || sc["runAsNonRoot"] != true ||
				value.Dig(sc, "seccompProfile.type") != "RuntimeDefault" || sc["readOnlyRootFilesystem"] != want || sc["privileged"] != nil {
				t.Errorf("%s %v: securityContext %#v", key, c["name"], sc)
			}
			if drop := value.AsList(value.Dig(sc, "capabilities.drop")); len(drop) != 1 || drop[0] != "ALL" {
				t.Errorf("%s %v: capabilities %#v", key, c["name"], drop)
			}
		}
	}
	if spec["securityContext"] == nil || value.Dig(spec, "securityContext.runAsNonRoot") != true || value.Dig(spec, "securityContext.seccompProfile.type") != "RuntimeDefault" {
		t.Errorf("pod securityContext: %#v", spec["securityContext"])
	}
}

// TestPodSpec: okdpPodSpec, the containers and volumes of a pod, hardened;
// what the arguments pass as they are; a security context, a host
// namespace, a hostPath volume refused.
func TestPodSpec(t *testing.T) {
	s := newSession(fragmentScope(true))
	got, err := eval(s, "okdpPodSpec(x)", M{"x": M{
		"containers":     []any{container("app"), M{"name": "tools", "image": "i", "readOnlyRootFilesystem": false, "runAsUser": 1000.0, "runAsGroup": 1000.0}},
		"initContainers": []any{container("init")},
		"volumes":        []any{M{"name": "tmp", "emptyDir": M{}}},
		"nodeSelector":   M{"disk": "ssd"},
		"restartPolicy":  "Always",
		"runAsUser":      501.0,
		"fsGroup":        501.0,
	}})
	if err != nil {
		t.Fatal(err)
	}
	spec := value.AsMap(got)
	hardened(t, spec, "tools")
	if spec["automountServiceAccountToken"] != false || spec["serviceAccountName"] != nil {
		t.Errorf("token: %#v", spec)
	}
	if value.Dig(spec, "securityContext.runAsUser") != 501.0 || value.Dig(spec, "securityContext.fsGroup") != 501.0 || value.Dig(spec, "securityContext.runAsGroup") != nil {
		t.Errorf("pod securityContext: %#v", spec["securityContext"])
	}
	tools := value.AsMap(value.AsList(spec["containers"])[1])
	if value.Dig(tools, "securityContext.runAsUser") != 1000.0 || value.Dig(tools, "securityContext.runAsGroup") != 1000.0 || tools["readOnlyRootFilesystem"] != nil || tools["runAsUser"] != nil {
		t.Errorf("container: %#v", tools)
	}
	if value.Dig(spec, "nodeSelector.disk") != "ssd" || spec["restartPolicy"] != "Always" || len(value.AsList(spec["volumes"])) != 1 {
		t.Errorf("spec: %#v", spec)
	}
	// The token and the account on request.
	got, err = eval(s, "okdpPodSpec(x)", M{"x": M{"containers": []any{container("app")}, "serviceAccountName": "sa", "automountServiceAccountToken": true}})
	if spec := value.AsMap(got); err != nil || spec["automountServiceAccountToken"] != true || spec["serviceAccountName"] != "sa" || spec["volumes"] != nil || spec["initContainers"] != nil {
		t.Errorf("token asked: %v %#v", err, got)
	}
	for name, tc := range map[string]struct {
		args M
		want string
	}{
		"a security context":   {M{"containers": []any{M{"name": "a", "image": "i", "securityContext": M{"privileged": true}}}}, "additional properties 'securityContext' not allowed"},
		"the pod's":            {M{"containers": []any{container("a")}, "securityContext": M{}}, "additional properties 'securityContext' not allowed"},
		"the host's network":   {M{"containers": []any{container("a")}, "hostNetwork": true}, "additional properties 'hostNetwork' not allowed"},
		"a hostPath":           {M{"containers": []any{container("a")}, "volumes": []any{M{"name": "h", "hostPath": M{"path": "/"}}}}, "/okdpPodSpec/volumes/0"},
		"no container":         {M{"containers": []any{}}, "/okdpPodSpec/containers"},
		"no image":             {M{"containers": []any{M{"name": "a"}}}, "missing property 'image'"},
		"root":                 {M{"containers": []any{M{"name": "a", "image": "i", "runAsUser": 0.0}}}, "/okdpPodSpec/containers/0/runAsUser"},
		"a pod's root":         {M{"containers": []any{container("a")}, "runAsUser": 0.0}, "/okdpPodSpec/runAsUser"},
		"an unknown container": {M{"containers": []any{M{"name": "a", "image": "i", "privileged": true}}}, "additional properties 'privileged' not allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eval(s, "okdpPodSpec(x)", M{"x": tc.args}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestJob: okdpJob, a Job on the hardened pod; a hook of Helm and Argo CD
// when asked; its labels OKDP's.
func TestJob(t *testing.T) {
	s := newSession(fragmentScope(true))
	got, err := eval(s, "okdpJob(x)", M{"x": M{
		"name": "demo-app-claims", "component": "claims",
		"pod":    M{"containers": []any{M{"name": "run", "image": "i", "command": []any{"sh", "-c", "true"}}}, "serviceAccountName": "claims"},
		"hook":   M{"helm": "post-install,post-upgrade", "weight": 5.0, "argocd": "PostSync"},
		"labels": M{"team": "data"}, "annotations": M{"a": "b"}, "backoffLimit": 0.0, "activeDeadlineSeconds": 600.0,
	}})
	if err != nil {
		t.Fatal(err)
	}
	jobs := value.AsList(got)
	if len(jobs) != 1 {
		t.Fatalf("got %#v", got)
	}
	job := value.AsMap(jobs[0])
	if job["kind"] != "Job" || value.Dig(job, "metadata.name") != "demo-app-claims" {
		t.Fatalf("job: %#v", job)
	}
	ann := value.AsMap(value.Dig(job, "metadata.annotations"))
	if ann["helm.sh/hook"] != "post-install,post-upgrade" || ann["helm.sh/hook-weight"] != "5" || ann["helm.sh/hook-delete-policy"] != "before-hook-creation" ||
		ann["argocd.argoproj.io/hook"] != "PostSync" || ann["argocd.argoproj.io/hook-delete-policy"] != "BeforeHookCreation" || ann["a"] != "b" {
		t.Errorf("annotations: %#v", ann)
	}
	lbl := value.AsMap(value.Dig(job, "metadata.labels"))
	if lbl["app.kubernetes.io/instance"] != "demo-app" || lbl["app.kubernetes.io/component"] != "claims" || lbl["team"] != "data" || lbl["okdp.io/service"] != "app" {
		t.Errorf("labels: %#v", lbl)
	}
	spec := value.AsMap(value.Dig(job, "spec"))
	if spec["backoffLimit"] != 0.0 || spec["ttlSecondsAfterFinished"] != 300.0 || spec["activeDeadlineSeconds"] != 600.0 {
		t.Errorf("spec: %#v", spec)
	}
	pod := value.AsMap(value.Dig(spec, "template.spec"))
	hardened(t, pod)
	if pod["restartPolicy"] != "Never" || pod["serviceAccountName"] != "claims" || pod["automountServiceAccountToken"] != false {
		t.Errorf("pod: %#v", pod)
	}
	// The defaults: no hook, one retry, the pod's own restart policy.
	got, err = eval(s, "okdpJob(x)", M{"x": M{"name": "j", "pod": M{"containers": []any{container("run")}, "restartPolicy": "OnFailure"}}})
	if err != nil {
		t.Fatal(err)
	}
	job = value.AsMap(value.AsList(got)[0])
	if value.Dig(job, "metadata.annotations") != nil || value.Dig(job, "spec.backoffLimit") != 1.0 || value.Dig(job, "spec.template.spec.restartPolicy") != "OnFailure" || value.Dig(job, "spec.activeDeadlineSeconds") != nil {
		t.Errorf("defaults: %#v", job)
	}
	for name, tc := range map[string]struct {
		args M
		want string
	}{
		"no name":          {M{"pod": M{"containers": []any{container("a")}}}, "missing property 'name'"},
		"no pod":           {M{"name": "j"}, "missing property 'pod'"},
		"a hook":           {M{"name": "j", "pod": M{"containers": []any{container("a")}}, "hook": M{"argocd": "Later"}}, "/okdpJob/hook/argocd"},
		"a privileged pod": {M{"name": "j", "pod": M{"containers": []any{M{"name": "a", "image": "i", "securityContext": M{}}}}}, "additional properties 'securityContext' not allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eval(s, "okdpJob(x)", M{"x": tc.args}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestSignInProxy: okdpSignInProxy, oauth2-proxy by digest with the
// platform's OIDC, letting in the project's editors by default, the groups
// claim, never the realm's roles, and never an unverified email.
func TestSignInProxy(t *testing.T) {
	s := newSession(fragmentScope(true))
	got, err := eval(s, "okdpSignInProxy(x)", M{"x": M{"host": "app-demo.example.org", "upstream": "http://127.0.0.1:8080/", "upstreamTimeout": "1h"}})
	if err != nil {
		t.Fatal(err)
	}
	proxy := value.AsMap(got)
	c := value.AsMap(proxy["container"])
	var args []string
	for _, a := range value.AsList(c["args"]) {
		args = append(args, value.Str(a))
	}
	for _, want := range []string{
		"--upstream=http://127.0.0.1:8080/", "--provider=oidc", "--oidc-issuer-url=https://sso.example.org/realms/okdp",
		"--redirect-url=https://app-demo.example.org/oauth2/callback", "--code-challenge-method=S256", "--provider-ca-file=/cacerts/ca.crt",
		"--oidc-groups-claim=groups", "--allowed-group=okdp-project-demo", "--allowed-group=ops", "--upstream-timeout=1h", "--cookie-secure=true",
		"--cookie-name=_demo-app-oauth2-proxy_oauth2",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("args: no %s in %v", want, args)
		}
	}
	for _, a := range args {
		if strings.Contains(a, "insecure") || strings.Contains(a, "allowed-role") || strings.Contains(a, "skip-auth") {
			t.Errorf("args: %s", a)
		}
	}
	if img := value.Str(c["image"]); !strings.Contains(img, "@sha256:") || !strings.HasPrefix(img, "quay.io/oauth2-proxy/oauth2-proxy:") {
		t.Errorf("image: %s", img)
	}
	secret := value.Dig(c, "env")
	if !strings.Contains(value.Str(secret), "demo-app-oauth2-proxy") || !strings.Contains(value.Str(secret), "demo-app-oauth2") {
		t.Errorf("env: %#v", secret)
	}
	if objects := value.AsList(proxy["objects"]); len(objects) != 2 || value.Dig(value.AsMap(objects[1]), "metadata.name") != "demo-app-oauth2-proxy" {
		t.Errorf("objects: %#v", proxy["objects"])
	}
	if vol := value.AsList(proxy["volumes"]); len(vol) != 1 || value.Dig(value.AsMap(vol[0]), "secret.secretName") != "certs" {
		t.Errorf("volumes: %#v", proxy["volumes"])
	}
	// Its own groups, no CA bundle, a name and a port.
	scope := fragmentScope(true)
	delete(value.AsMap(scope["global"])["okdp"].(M), "tls")
	got, err = eval(newSession(scope), "okdpSignInProxy(x)", M{"x": M{"host": "h", "upstream": "http://u", "allowedGroups": []any{"admins"}, "name": "p", "port": 4181.0, "groupsClaim": "roles"}})
	if err != nil {
		t.Fatal(err)
	}
	c = value.AsMap(value.AsMap(got)["container"])
	text := value.Str(c["args"])
	if !strings.Contains(text, "--allowed-group=admins") || strings.Contains(text, "okdp-project-demo") || strings.Contains(text, "provider-ca-file") ||
		!strings.Contains(text, "--oidc-groups-claim=roles") || !strings.Contains(text, "0.0.0.0:4181") || value.AsMap(got)["volumes"] == nil || len(value.AsList(value.AsMap(got)["volumes"])) != 0 {
		t.Errorf("args: %s", text)
	}
	for name, tc := range map[string]struct {
		oidc bool
		args M
		want string
	}{
		"no host":      {true, M{"upstream": "http://u"}, "okdpSignInProxy: host is required"},
		"no upstream":  {true, M{"host": "h"}, "okdpSignInProxy: upstream is required"},
		"no group":     {true, M{"host": "h", "upstream": "http://u", "allowedGroups": []any{}}, "okdpSignInProxy: no allowed group"},
		"OIDC off":     {false, M{"host": "h", "upstream": "http://u"}, "okdpSignInProxy: the platform's OIDC is disabled"},
		"an extra arg": {true, M{"host": "h", "upstream": "http://u", "extraArgs": []any{"--insecure-oidc-allow-unverified-email=true"}}, "additional properties 'extraArgs' not allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eval(newSession(fragmentScope(tc.oidc)), "okdpSignInProxy(x)", M{"x": tc.args}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// kinds: the kind/name of objects.
func kinds(objects any) []string {
	var out []string
	for _, o := range value.AsList(objects) {
		out = append(out, value.Str(value.AsMap(o)["kind"])+"/"+value.Str(value.Dig(value.AsMap(o), "metadata.name")))
	}
	return out
}

// TestWebWorkload: okdpWebWorkload, a Deployment on the hardened pod and a
// Service; a volume, an Ingress and the sign-in proxy in front when asked.
func TestWebWorkload(t *testing.T) {
	s := newSession(fragmentScope(true))
	// The least: a Deployment and a Service.
	got, err := eval(s, "okdpWebWorkload(x)", M{"x": M{"component": "web", "port": "http", "pod": M{"containers": []any{container("app")}}}})
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(got); !slices.Equal(k, []string{"Deployment/demo-app-web", "Service/demo-app-web"}) {
		t.Errorf("kinds: %v", k)
	}
	objects := value.AsList(got)
	dep, svc := value.AsMap(objects[0]), value.AsMap(objects[1])
	hardened(t, value.AsMap(value.Dig(dep, "spec.template.spec")))
	if value.Dig(dep, "spec.strategy.type") != "RollingUpdate" || value.Dig(dep, "spec.replicas") != 1.0 {
		t.Errorf("deployment: %#v", dep)
	}
	if sel := value.AsMap(value.Dig(dep, "spec.selector.matchLabels")); sel["app.kubernetes.io/component"] != "web" || sel["app.kubernetes.io/instance"] != "demo-app" {
		t.Errorf("selector: %#v", sel)
	}
	if value.Dig(svc, "spec.ports") == nil || value.Str(value.Dig(svc, "spec.ports")) != "[map[name:http port:80 targetPort:http]]" {
		t.Errorf("service: %#v", svc)
	}
	// The full: a volume, an Ingress, the proxy in front.
	got, err = eval(s, "okdpWebWorkload(x)", M{"x": M{
		"component": "web", "name": "hop-web", "port": 8080.0, "replicas": 2.0, "servicePort": 8080.0,
		"pod":     M{"containers": []any{container("app")}, "runAsUser": 501.0, "fsGroup": 501.0, "volumes": []any{M{"name": "tmp", "emptyDir": M{}}}},
		"storage": M{"size": "10Gi", "storageClassName": "fast"},
		"ingress": M{"host": "web-demo.example.org", "annotations": M{"nginx.ingress.kubernetes.io/proxy-body-size": "0"}},
		"signIn":  M{"allowedGroups": []any{"ops"}},
		"labels":  M{"team": "data"}, "podAnnotations": M{"checksum": "x"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"PersistentVolumeClaim/hop-web-data", "Deployment/hop-web", "Service/hop-web", "Ingress/hop-web", "NetworkPolicy/hop-web", "Password/demo-app-web-oauth2-proxy-cookie-secret", "ExternalSecret/demo-app-web-oauth2-proxy"}
	if k := kinds(got); !slices.Equal(k, want) {
		t.Fatalf("kinds: %v, want %v", k, want)
	}
	objects = value.AsList(got)
	pvc, dep, svc, ing, np := value.AsMap(objects[0]), value.AsMap(objects[1]), value.AsMap(objects[2]), value.AsMap(objects[3]), value.AsMap(objects[4])
	if value.Dig(pvc, "spec.storageClassName") != "fast" || value.Dig(pvc, "spec.resources.requests.storage") != "10Gi" || value.Str(value.Dig(pvc, "metadata.annotations")) != "map[helm.sh/resource-policy:keep]" {
		t.Errorf("pvc: %#v", pvc)
	}
	pod := value.AsMap(value.Dig(dep, "spec.template.spec"))
	hardened(t, pod)
	containers := value.AsList(pod["containers"])
	if len(containers) != 2 || value.AsMap(containers[1])["name"] != "oauth2-proxy" || value.Dig(dep, "spec.strategy.type") != "Recreate" || value.Dig(dep, "spec.replicas") != 2.0 ||
		value.Dig(dep, "spec.template.metadata.annotations.checksum") != "x" || value.Dig(dep, "metadata.labels.team") != "data" {
		t.Errorf("deployment: %#v", dep)
	}
	if vols := value.Str(pod["volumes"]); !strings.Contains(vols, "claimName:hop-web-data") || !strings.Contains(vols, "name:tmp") || !strings.Contains(vols, "secretName:certs") {
		t.Errorf("volumes: %s", vols)
	}
	if !strings.Contains(value.Str(value.AsMap(containers[1])["args"]), "--allowed-group=ops") || !strings.Contains(value.Str(value.AsMap(containers[1])["args"]), "--upstream=http://127.0.0.1:8080/") ||
		!strings.Contains(value.Str(value.AsMap(containers[1])["args"]), "--redirect-url=https://web-demo.example.org/oauth2/callback") {
		t.Errorf("proxy: %#v", containers[1])
	}
	if value.Str(value.Dig(svc, "spec.ports")) != "[map[name:http port:8080 targetPort:proxy]]" {
		t.Errorf("service: %#v", svc)
	}
	if ann := value.AsMap(value.Dig(ing, "metadata.annotations")); ann["cert-manager.io/cluster-issuer"] != "ca" || ann["nginx.ingress.kubernetes.io/proxy-body-size"] != "0" ||
		value.Dig(ing, "spec.ingressClassName") != "nginx" || value.Str(value.Dig(ing, "spec.tls")) != "[map[hosts:[web-demo.example.org] secretName:hop-web-tls]]" {
		t.Errorf("ingress: %#v", ing)
	}
	if value.Str(value.Dig(np, "spec.ingress")) != "[map[ports:[map[port:proxy protocol:TCP]]]]" {
		t.Errorf("network policy: %#v", np)
	}
	for name, tc := range map[string]struct {
		args M
		want string
	}{
		"signIn without ingress": {M{"component": "w", "port": 8080.0, "pod": M{"containers": []any{container("a")}}, "signIn": M{}}, "okdpWebWorkload: signIn needs an ingress"},
		"signIn, a named port":   {M{"component": "w", "port": "http", "pod": M{"containers": []any{container("a")}}, "ingress": M{"host": "h"}, "signIn": M{}}, "okdpWebWorkload: signIn needs the application's port as a number"},
		"no component":           {M{"port": 1.0, "pod": M{"containers": []any{container("a")}}}, "missing property 'component'"},
		"no port":                {M{"component": "w", "pod": M{"containers": []any{container("a")}}}, "missing property 'port'"},
		"no host":                {M{"component": "w", "port": 1.0, "pod": M{"containers": []any{container("a")}}, "ingress": M{}}, "missing property 'host'"},
		"a hostPath":             {M{"component": "w", "port": 1.0, "pod": M{"containers": []any{container("a")}, "volumes": []any{M{"name": "h", "hostPath": M{"path": "/"}}}}}, "/okdpWebWorkload/pod/volumes/0"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eval(s, "okdpWebWorkload(x)", M{"x": tc.args}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestFragmentsCompile: a package that writes its workloads with the
// fragments compiles under the platform's policy for a project that is not
// privileged: its objects stay in the namespace, its hosts are the
// project's, no Secret in clear; a host of another project's is refused.
func TestFragmentsCompile(t *testing.T) {
	d := *def
	d.Okdp = []byte(`
objects:
  - $spread: >-
      ${{ okdpWebWorkload({'component': 'web', 'port': 8080, 'pod': {'containers': [{'name': 'app', 'image': 'registry.example.org/app@sha256:0'}]},
          'ingress': {'host': 'app-demo.example.org'}, 'signIn': {}}) }}
  - $spread: >-
      ${{ okdpJob({'name': okdpFullname('init'), 'pod': {'containers': [{'name': 'init', 'image': 'registry.example.org/app@sha256:0'}]},
          'hook': {'helm': 'post-install'}}) }}
`)
	global := M{"okdp": M{"ingress": M{"suffix": "example.org"}, "oidc": M{"issuerUri": "https://sso.example.org/realms/okdp"}}}
	res, err := Compile(context.Background(), &d, inputs(map[string]any{"global": global}))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range res.Objects {
		got = append(got, value.Str(o["kind"])+"/"+value.Str(value.Dig(o, "metadata.name")))
	}
	if want := "Deployment/demo-app-web Service/demo-app-web Ingress/demo-app-web NetworkPolicy/demo-app-web Password/demo-app-web-oauth2-proxy-cookie-secret ExternalSecret/demo-app-web-oauth2-proxy Job/demo-app-init"; strings.Join(got, " ") != want {
		t.Errorf("objects: %s, want %s", strings.Join(got, " "), want)
	}
	// Another project's host.
	d.Okdp = []byte(`
objects:
  - $spread: >-
      ${{ okdpWebWorkload({'component': 'web', 'port': 8080, 'pod': {'containers': [{'name': 'app', 'image': 'i'}]},
          'ingress': {'host': 'keycloak.example.org'}}) }}
`)
	if _, err := Compile(context.Background(), &d, inputs(map[string]any{"global": global})); err == nil || !strings.Contains(err.Error(), `Ingress/demo-app-web: spec.rules[0].host: host "keycloak.example.org" is not served by demo`) {
		t.Errorf("a host of another project: got %v", err)
	}
}
