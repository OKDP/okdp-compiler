package okdp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestNoObjectsPolicy: with the mandatory set alone, no hook refuses an
// object: an object of a cluster-scoped kind, of another namespace, of an
// engine's kind, a Secret in clear and a Fake generator's values all
// compile, as written. The policy over them is the security set's.
func TestNoObjectsPolicy(t *testing.T) {
	d := *def
	d.Okdp = []byte(`objects:
  - {apiVersion: rbac.authorization.k8s.io/v1, kind: ClusterRoleBinding, metadata: {name: demo-admin}}
  - {apiVersion: v1, kind: ConfigMap, metadata: {name: a, namespace: kube-system}}
  - {apiVersion: helm.toolkit.fluxcd.io/v2, kind: HelmRelease, metadata: {name: r}}
  - {apiVersion: v1, kind: Service, metadata: {name: s}, spec: {externalIPs: [10.0.0.1]}}
  - {apiVersion: v1, kind: Secret, metadata: {name: s}, stringData: {password: p}}
  - {apiVersion: generators.external-secrets.io/v1alpha1, kind: Fake, metadata: {name: f}, spec: {data: {user: u}}}
`)
	res, err := Compile(context.Background(), &d, inputs())
	if err != nil || len(res.Objects) != 6 {
		t.Fatalf("%v %+v", err, res)
	}
	if err := CheckObjects(context.Background(), Mandatory(), "demo", "demo", nil, false, res.Objects); err != nil {
		t.Errorf("CheckObjects: %v", err)
	}
}

// TestDuplicateObjects: an instance's objects hold no two of one apiVersion
// group, kind, namespace and name, whatever their sources and their
// versions, the items of a list included; a name apart, a kind, a group
// or a Secret (left to the namespace's check of the Secrets) does not
// collide, nor does a namespace apart.
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
			_, err := Compile(context.Background(), &d, inputs())
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// TestDuplicateObjectsNamespace: the same name in two namespaces is two
// objects.
func TestDuplicateObjectsNamespace(t *testing.T) {
	d := *def
	d.Okdp = []byte("objects:\n  - {apiVersion: v1, kind: ConfigMap, metadata: {name: a}}\n  - {apiVersion: v1, kind: ConfigMap, metadata: {name: a, namespace: other}}\n")
	in := inputs()
	if _, err := Compile(context.Background(), &d, in); err != nil {
		t.Error(err)
	}
}

// TestSettingsOf: the namespace is checked as the compile of its
// instances checks it, a *NamespaceError, so that SettingsOf and Compile
// agree; the settings come with their defaults; settings refused are no
// namespace problem.
func TestSettingsOf(t *testing.T) {
	in := inputs()
	in.Namespace = "kube-system"
	_, cerr := Compile(context.Background(), def, in)
	_, serr := SettingsOf(context.Background(), in)
	if cerr == nil || serr == nil || cerr.Error() != serr.Error() {
		t.Fatalf("Compile: %v\nSettingsOf: %v", cerr, serr)
	}
	for _, ns := range []string{"kube-system", "platform", "Not_A_Label"} {
		in := inputs()
		in.Namespace = ns
		if _, err := SettingsOf(context.Background(), in); !errors.As(err, new(*NamespaceError)) {
			t.Errorf("%s: %v", ns, err)
		}
	}
	settings, err := SettingsOf(context.Background(), inputs())
	if err != nil || settings["secrets"] == nil {
		t.Errorf("a namespace nothing reserves: %v, %v", settings, err)
	}
	if _, err := SettingsOf(context.Background(), inputs(map[string]any{"global": M{"okdp": M{"proxy": "none"}}})); err == nil || errors.As(err, new(*NamespaceError)) {
		t.Errorf("the settings refused: %v", err)
	}
}
