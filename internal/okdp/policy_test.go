package okdp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/testrules"
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
		"an engine's kind":      {object("helm.toolkit.fluxcd.io/v2", "HelmRelease", "r", nil), "demo: objects: HelmRelease/r: an object of the deployer's kinds (helm.toolkit.fluxcd.io/v2) lets a project drive the platform's engine"},
		"the Flux Operator's":   {object("fluxcd.controlplane.io/v1", "ResourceSet", "r", nil), "demo: objects: ResourceSet/r: an object of the deployer's kinds (fluxcd.controlplane.io/v1)"},
		"a source extension's":  {object("source.extensions.fluxcd.io/v1beta1", "ArtifactGenerator", "g", nil), "demo: objects: ArtifactGenerator/g: an object of the deployer's kinds (source.extensions.fluxcd.io/v1beta1)"},
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

// TestDuplicateObjects: an instance's objects hold no two of one apiVersion
// group, kind, namespace and name, whatever their sources and their
// versions, the items of a list included, in a privileged project too; a
// name apart, a kind, a group or a Secret (left to the namespace's check
// of the Secrets) does not collide, nor does a namespace apart, a privileged
// project's.
func TestDuplicateObjects(t *testing.T) {
	object := func(apiVersion, kind, name, namespace string) string {
		ns := ""
		if namespace != "" {
			ns = ", namespace: " + namespace
		}
		return "  - {apiVersion: " + apiVersion + ", kind: " + kind + ", metadata: {name: " + name + ns + "}}\n"
	}
	for name, tc := range map[string]struct {
		objects string
		want    string // the error, none when empty
	}{
		"apart":                           {object("v1", "ConfigMap", "a", "") + object("v1", "ConfigMap", "b", "") + object("v1", "Service", "a", "") + object("example.org/v1", "ConfigMap", "a", ""), ""},
		"two":                             {object("v1", "ConfigMap", "a", "") + object("v1", "ConfigMap", "a", ""), "demo: objects: ConfigMap/a: two objects of one kind (core), namespace (demo) and name: object 1 and object 2"},
		"the namespace is the instance's": {object("v1", "ConfigMap", "a", "") + object("v1", "ConfigMap", "a", "demo"), "ConfigMap/a: two objects"},
		"versions":                        {object("kafka.strimzi.io/v1beta2", "KafkaTopic", "t", "") + object("kafka.strimzi.io/v1", "KafkaTopic", "t", ""), "KafkaTopic/t: two objects of one kind (kafka.strimzi.io)"},
		"a list's item":                   {object("v1", "ConfigMap", "a", "") + "  - {apiVersion: v1, kind: List, metadata: {name: l}, items: [{apiVersion: v1, kind: ConfigMap, metadata: {name: a}}]}\n", "demo: objects: ConfigMap/a: two objects of one kind (core), namespace (demo) and name: object 1 and object 3"},
		"a generated Secret":              {"  - $spread: \"${{ okdpGeneratedSecret({'name': 's', 'keys': [{'key': 'k'}]}) }}\"\n  - $spread: \"${{ okdpGeneratedSecret({'name': 's', 'keys': [{'key': 'k'}]}) }}\"\n", "Password/s-k: two objects of one kind (generators.external-secrets.io)"},
		"a Secret":                        {object("v1", "Secret", "s", "") + object("v1", "Secret", "s", ""), ""},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Okdp = []byte("objects:\n" + tc.objects)
			for _, privileged := range []bool{false, true} {
				in := inputs()
				if privileged {
					in = inputs(map[string]any{"global": M{"okdp": M{"policy": M{"privileged": []any{"demo"}}}}})
				}
				_, err := Compile(context.Background(), &d, in)
				if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
					t.Errorf("privileged %v: got %v, want %q", privileged, err, tc.want)
				}
			}
		})
	}
}

// TestDuplicateObjectsNamespace: the same name in two namespaces is two
// objects, in a privileged project, which writes other namespaces.
func TestDuplicateObjectsNamespace(t *testing.T) {
	d := *def
	d.Okdp = []byte("objects:\n  - {apiVersion: v1, kind: ConfigMap, metadata: {name: a}}\n  - {apiVersion: v1, kind: ConfigMap, metadata: {name: a, namespace: other}}\n")
	in := inputs(map[string]any{"global": M{"okdp": M{"policy": M{"privileged": []any{"demo"}}}}})
	if _, err := Compile(context.Background(), &d, in); err != nil {
		t.Error(err)
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
			_, err := schemaValues(testrules.Standard().PlatformSchema(), heading, M{"global": M{"okdp": M{"policy": tc.policy}}})
			if want := heading + ":\n" + tc.want; (err == nil) != (tc.want == "") || err != nil && err.Error() != want {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// TestPlainSecrets: no Secret of the objects holds values in clear, data
// or stringData, a list's items included, no ExternalSecret's template a
// literal at a sensitive key, no Fake generator values, unless the caller
// allows it (Inputs.AllowPlainSecrets); a Secret without values, another
// kind with data, a Secret of another API group, a generated Secret, a
// template's literal at another key and a Password generator pass.
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
		"a generated one's sensitive literal": {"  - $spread: \"${{ okdpGeneratedSecret({'name': 's', 'keys': [{'key': 'k'}], 'stringData': {'adminPassword': 'p'}}) }}\"\n",
			"demo: objects: ExternalSecret/s: a plain value (spec.target.template.data.adminPassword): a literal at a sensitive key, not a template of what the store or a generator gives; credentials are generated"},
		"a template's literal": {"  - {apiVersion: external-secrets.io/v1, kind: ExternalSecret, metadata: {name: e}, spec: {target: {template: {data: {region: eu, url: 'postgres://{{ .user }}@db', password: hunter2}}}}}\n",
			"demo: objects: ExternalSecret/e: a plain value (spec.target.template.data.password)"},
		"a template's number": {"  - {apiVersion: external-secrets.io/v1, kind: ExternalSecret, metadata: {name: e}, spec: {target: {template: {data: {pin_token: 1234}}}}}\n", "ExternalSecret/e: a plain value (spec.target.template.data.pin_token)"},
		"a template":          {"  - {apiVersion: external-secrets.io/v1, kind: ExternalSecret, metadata: {name: e}, spec: {target: {template: {data: {password: '{{ .password }}', passwordKey: password, existingSecret: s, region: eu}}}}}\n", ""},
		"a cluster template": {"  - {apiVersion: external-secrets.io/v1, kind: ClusterExternalSecret, metadata: {name: c}, spec: {externalSecretSpec: {target: {template: {data: {apiKey: k}}}}}}\n",
			"ClusterExternalSecret/c: a plain value (spec.externalSecretSpec.target.template.data.apiKey)"},
		"a fake generator":         {"  - {apiVersion: generators.external-secrets.io/v1alpha1, kind: Fake, metadata: {name: f}, spec: {data: {user: u}}}\n", "demo: objects: Fake/f: a plain value (spec.data): a Fake generator gives the values it holds; credentials are generated"},
		"a fake cluster generator": {"  - {apiVersion: generators.external-secrets.io/v1alpha1, kind: ClusterGenerator, metadata: {name: f}, spec: {kind: Fake, generator: {fakeSpec: {data: {k: v}}}}}\n", "ClusterGenerator/f: a plain value (spec.generator.fakeSpec.data)"},
		"a password generator":     {"  - {apiVersion: generators.external-secrets.io/v1alpha1, kind: Password, metadata: {name: p}, spec: {length: 20}}\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Okdp = []byte("objects:\n" + tc.objects)
			layer := map[string]any{}
			if strings.Contains(name, "cluster") { // a cluster-scoped kind: a privileged project's
				layer["global"] = M{"okdp": M{"policy": M{"privileged": []any{"demo"}}}}
			}
			_, err := Compile(context.Background(), &d, inputs(layer))
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
			// Allowed: written as computed.
			in := inputs(layer)
			in.AllowPlainSecrets = true
			if res, err := Compile(context.Background(), &d, in); err != nil || len(res.Objects) == 0 {
				t.Errorf("allowed: %v", err)
			}
		})
	}
}
