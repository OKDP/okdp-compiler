package okdp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// TestPrivileged: Result.Privileged follows global.okdp.policy.privileged,
// the platform's list of its own projects' namespaces.
func TestPrivileged(t *testing.T) {
	for _, tc := range []struct {
		name       string
		policy     any
		privileged bool
	}{
		{"unset", nil, false},
		{"other namespace", M{"privileged": []any{"cert-manager"}}, false},
		{"listed", M{"privileged": []any{"cert-manager", "demo"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := inputs()
			if tc.policy != nil {
				in = inputs(map[string]any{"global": M{"okdp": M{"policy": tc.policy}}})
			}
			res, err := Compile(context.Background(), def, in)
			if err != nil {
				t.Fatal(err)
			}
			if res.Privileged != tc.privileged {
				t.Errorf("Privileged = %v, want %v", res.Privileged, tc.privileged)
			}
		})
	}
}

// TestObjectsPolicy: the objects of an instance of a project that is not
// privileged stay in its namespace (metadata.namespace unset, null, "" or
// the release's) and none is of a cluster-scoped kind, whatever its
// source, the objects section or a generated Secret's, the items of a list
// included; a kind the compiler does not know, without a namespace, passes.
// A privileged project's instance is not checked.
func TestObjectsPolicy(t *testing.T) {
	object := func(apiVersion, kind, name string, namespace any) string {
		ns := ""
		if namespace != nil {
			ns = fmt.Sprintf(", namespace: %v", namespace)
		}
		return fmt.Sprintf("  - {apiVersion: %s, kind: %s, metadata: {name: %s%s}}\n", apiVersion, kind, name, ns)
	}
	for name, tc := range map[string]struct {
		objects string
		want    string // the error, none when empty
	}{
		"in the namespace":      {object("v1", "ConfigMap", "a", "demo"), ""},
		"no namespace":          {object("v1", "ConfigMap", "a", nil) + object("example.org/v1", "Widget", "w", nil), ""},
		"an empty namespace":    {object("v1", "ConfigMap", "a", `""`) + object("v1", "ConfigMap", "b", "~"), ""},
		"another namespace":     {object("v1", "ConfigMap", "a", "demo") + object("v1", "Secret", "token", "kube-system"), "demo: objects: Secret/token: namespace kube-system, not the instance's (demo): the platform's projects write other namespaces (global.okdp.policy.privileged), demo is not one of them"},
		"a cluster-scoped kind": {object("rbac.authorization.k8s.io/v1", "ClusterRoleBinding", "demo-admin", nil), "demo: objects: ClusterRoleBinding/demo-admin: a cluster-scoped kind (rbac.authorization.k8s.io/v1): the platform's projects install cluster-scoped objects (global.okdp.policy.privileged), demo is not one of them"},
		"a core kind":           {object("v1", "Namespace", "other", nil), "demo: objects: Namespace/other: a cluster-scoped kind (v1)"},
		"an operator's kind":    {object("external-secrets.io/v1", "ClusterSecretStore", "s", nil), "demo: objects: ClusterSecretStore/s: a cluster-scoped kind (external-secrets.io/v1)"},
		"a generated Secret":    {"  - $spread: \"${{ okdpGeneratedSecret({'name': 's', 'namespace': 'other', 'keys': [{'key': 'k'}]}) }}\"\n", "demo: objects: Password/s-k: namespace other, not the instance's (demo)"},
		"a list":                {"  - {apiVersion: v1, kind: List, metadata: {name: l}, items: [{apiVersion: v1, kind: ConfigMap, metadata: {name: a}}]}\n", ""},
		"a list's item":         {"  - {apiVersion: v1, kind: List, metadata: {name: l}, items: [{apiVersion: v1, kind: ConfigMap, metadata: {name: a}}, {apiVersion: rbac.authorization.k8s.io/v1, kind: ClusterRole, metadata: {name: r}}]}\n", "demo: objects: ClusterRole/r: a cluster-scoped kind (rbac.authorization.k8s.io/v1)"},
		"a list's namespace":    {"  - {apiVersion: v1, kind: ConfigMapList, metadata: {name: l}, items: [{apiVersion: v1, kind: ConfigMap, metadata: {name: a, namespace: other}}]}\n", "demo: objects: ConfigMap/a: namespace other, not the instance's (demo)"},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Okdp = []byte("objects:\n" + tc.objects)
			_, err := Compile(context.Background(), &d, inputs())
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
			// A privileged project: not checked.
			res, err := Compile(context.Background(), &d, inputs(map[string]any{"global": M{"okdp": M{"policy": M{"privileged": []any{"demo"}}}}}))
			if err != nil || !res.Privileged || len(res.Objects) == 0 {
				t.Errorf("privileged: %v %+v", err, res)
			}
		})
	}
}

// TestUpstreamPolicy: the upstream values of an instance of a project that
// is not privileged set no denied key, at any depth, in a list or in a
// string read as YAML, nor runAsUser 0 nor runAsNonRoot false, nor a key
// of the platform's deny list; the scheduling keys stay free. With the
// platform's registries, every image comes from one of them, a registry
// and a repository making one, over the computed ones. A privileged
// project's are not checked.
func TestUpstreamPolicy(t *testing.T) {
	registries := M{"registries": []any{"registry.example.org", "mirror.example.org/okdp"}}
	for name, tc := range map[string]struct {
		policy M // global.okdp.policy.upstream
		user   M // upstream.app
		want   string
	}{
		"free keys":           {nil, M{"nodeSelector": M{"a": "b"}, "tolerations": []any{M{"key": "k"}}, "affinity": M{}, "pod": M{"runAsUser": 1000.0, "runAsNonRoot": true}, "image": "docker.io/x:1"}, ""},
		"a denied key":        {nil, M{"securityContext": M{}}, "app: upstream.app.securityContext: securityContext is denied to the instance's upstream values (a pod's privileges, identity, containers or objects): the platform's projects set it (global.okdp.policy.privileged), demo is not one of them"},
		"in a list":           {nil, M{"volumes": []any{M{"name": "h", "hostPath": M{"path": "/"}}}}, "app: upstream.app.volumes[0].hostPath: hostPath is denied"},
		"runAsUser 0":         {nil, M{"pod": M{"runAsUser": 0.0}}, "app: upstream.app.pod.runAsUser: runAsUser 0, root, is denied to the instance's upstream values: the platform's projects set it (global.okdp.policy.privileged), demo is not one of them"},
		"runAsNonRoot false":  {nil, M{"pod": M{"runAsNonRoot": false}}, "app: upstream.app.pod.runAsNonRoot: runAsNonRoot false is denied"},
		"the platform's deny": {M{"deny": []any{"rbac"}}, M{"rbac": M{"create": true}}, "app: upstream.app.rbac: rbac is denied to the instance's upstream values (global.okdp.policy.upstream.deny)"},
		"YAML text":           {nil, M{"extraVolumes": "- name: h\n  hostPath: {path: /}\n"}, "app: upstream.app.extraVolumes, read as YAML: [0].hostPath: hostPath is denied"},
		"YAML documents":      {nil, M{"extra": "a: b\n---\nkind: X\nprivileged: true\n"}, "app: upstream.app.extra, read as YAML: privileged: privileged is denied"},
		"JSON text":           {nil, M{"config": M{"json": `{"hostNetwork": true}`}}, "app: upstream.app.config.json, read as YAML: hostNetwork: hostNetwork is denied"},
		"plain text":          {nil, M{"url": "https://x.example.org:8443/a", "note": "a: b", "list": "- a: 1"}, ""},
		"an image":            {registries, M{"image": "registry.example.org/app:1", "side": M{"image": "mirror.example.org/okdp/tool@sha256:0"}}, ""},
		"an image of another": {registries, M{"side": M{"image": "docker.io/x"}}, "app: upstream.app.side.image: image docker.io/x is not from a registry of global.okdp.policy.upstream.registries (registry.example.org, mirror.example.org/okdp): the platform's projects pull from any (global.okdp.policy.privileged), demo is not one of them"},
		"past a path":         {registries, M{"side": M{"image": "mirror.example.org/okdp-evil/x"}}, "app: upstream.app.side.image: image mirror.example.org/okdp-evil/x is not"},
		"another port":        {registries, M{"side": M{"image": "registry.example.org:5000/x"}}, "app: upstream.app.side.image: image registry.example.org:5000/x is not"},
		"a registry":          {registries, M{"image": M{"registry": "mirror.example.org", "repository": "okdp/app"}}, ""},
		"the computed one":    {registries, M{"image": M{"repository": "okdp/other"}}, ""},
		"another repository":  {registries, M{"image": M{"registry": "mirror.example.org", "repository": "other/app"}}, "app: upstream.app.image.registry: image mirror.example.org/other/app is not"},
		"another registry":    {registries, M{"image": M{"registry": "docker.io"}}, "app: upstream.app.image.registry: image docker.io/okdp/app is not"},
		"a null registry":     {registries, M{"image": M{"registry": nil}}, "app: upstream.app.image.registry: image okdp/app is not"},
		"an image null":       {registries, M{"image": nil}, "app: upstream.app.image: image null, the chart's default, is not"},
		"an image's name":     {registries, M{"singleuser": M{"image": M{"name": "docker.io/jupyter/x", "tag": "1"}}}, "app: upstream.app.singleuser.image.name: image docker.io/jupyter/x is not"},
		"no image's registry": {registries, M{"registry": M{"replicas": 1.0}}, ""},
		"no registry":         {M{"registries": []any{}}, M{"image": "registry.example.org/x"}, "app: upstream.app.image: image registry.example.org/x is not from a registry of global.okdp.policy.upstream.registries (none)"},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Package.Upstream = chart.Upstream{All: true}
			d.Values = []byte("app:\n  image: {registry: registry.example.org, repository: okdp/app}\n")
			global := M{"okdp": M{"policy": M{"upstream": tc.policy}}}
			if tc.policy == nil {
				global = M{}
			}
			_, err := Compile(context.Background(), &d, inputs(map[string]any{"global": global, "upstream": M{"app": tc.user}}))
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
			// A privileged project: not checked.
			privileged := M{"okdp": M{"policy": M{"upstream": tc.policy, "privileged": []any{"demo"}}}}
			if _, err := Compile(context.Background(), &d, inputs(map[string]any{"global": privileged, "upstream": M{"app": tc.user}})); err != nil {
				t.Errorf("privileged: %v", err)
			}
		})
	}
}

// TestPolicySchema: global.okdp.policy, its keys only, a misspelt one
// refused: it would leave the projects unconstrained; the registries
// hosts, a port and a path optional, no scheme, no trailing "/".
func TestPolicySchema(t *testing.T) {
	for name, tc := range map[string]struct {
		policy any
		want   string // the violations, "" for none
	}{
		"every key": {M{"privileged": []any{"cert-manager"}, "upstream": M{"deny": []any{"rbac"}, "registries": []any{"registry.example.org", "mirror.example.org:5000/okdp/tools"}}}, ""},
		"misspelt":  {M{"privilged": []any{"x"}, "upstream": M{"registry": []any{"x"}}}, "- at '/global/okdp/policy': additional properties 'privilged' not allowed\n- at '/global/okdp/policy/upstream': additional properties 'registry' not allowed"},
		"registries": {M{"upstream": M{"deny": []any{""}, "registries": []any{"https://registry.example.org", "registry.example.org/", "docker.io/library/nginx:1"}}},
			"- at '/global/okdp/policy/upstream/deny/0': minLength: got 0, want 1\n- at '/global/okdp/policy/upstream/registries': validation failed\n" +
				`  - at '/global/okdp/policy/upstream/registries/0': 'https://registry.example.org' does not match pattern '^[^\\s/:@]+(:[0-9]+)?(/[^\\s/:@]+)*$'` + "\n" +
				`  - at '/global/okdp/policy/upstream/registries/1': 'registry.example.org/' does not match pattern '^[^\\s/:@]+(:[0-9]+)?(/[^\\s/:@]+)*$'` + "\n" +
				`  - at '/global/okdp/policy/upstream/registries/2': 'docker.io/library/nginx:1' does not match pattern '^[^\\s/:@]+(:[0-9]+)?(/[^\\s/:@]+)*$'`},
	} {
		t.Run(name, func(t *testing.T) {
			heading := "context values (Inputs.Context) don't meet the specifications of the schema of global.okdp"
			_, err := schemaValues(platformSchema, heading, M{"global": M{"okdp": M{"policy": tc.policy}}})
			if want := heading + ":\n" + tc.want; (err == nil) != (tc.want == "") || err != nil && err.Error() != want {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// TestPlainSecrets: no Secret of the objects holds values in clear, data
// or stringData, a list's items included, unless the caller allows it
// (Inputs.AllowPlainSecrets); a Secret without values, another kind with
// data, a Secret of another API group and a generated Secret pass.
func TestPlainSecrets(t *testing.T) {
	for name, tc := range map[string]struct {
		objects string
		want    string // the error, none when empty
	}{
		"stringData":      {"  - {apiVersion: v1, kind: Secret, metadata: {name: s}, stringData: {password: p}}\n", "demo: objects: Secret/s: a plain value (stringData): credentials are generated (okdpGeneratedSecret) or read from the platform's secret store (an ExternalSecret); Inputs.AllowPlainSecrets allows it"},
		"data":            {"  - {apiVersion: v1, kind: Secret, metadata: {name: s}, type: Opaque, data: {password: cA==}, stringData: {user: u}}\n", "demo: objects: Secret/s: a plain value (data)"},
		"a list's item":   {"  - {apiVersion: v1, kind: List, metadata: {name: l}, items: [{apiVersion: v1, kind: Secret, metadata: {name: s}, data: {k: dg==}}]}\n", "demo: objects: Secret/s: a plain value (data)"},
		"no values":       {"  - {apiVersion: v1, kind: Secret, metadata: {name: s, annotations: {kubernetes.io/service-account.name: sa}}, type: kubernetes.io/service-account-token, data: {}}\n", ""},
		"another kind":    {"  - {apiVersion: v1, kind: ConfigMap, metadata: {name: c}, data: {k: v}}\n", ""},
		"another group":   {"  - {apiVersion: example.org/v1, kind: Secret, metadata: {name: s}, data: {k: v}}\n", ""},
		"a generated one": {"  - $spread: \"${{ okdpGeneratedSecret({'name': 's', 'keys': [{'key': 'k'}], 'stringData': {'user': 'u'}}) }}\"\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Okdp = []byte("objects:\n" + tc.objects)
			_, err := Compile(context.Background(), &d, inputs())
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
			// Allowed: written as computed.
			in := inputs()
			in.AllowPlainSecrets = true
			if res, err := Compile(context.Background(), &d, in); err != nil || len(res.Objects) == 0 {
				t.Errorf("allowed: %v", err)
			}
		})
	}
}
