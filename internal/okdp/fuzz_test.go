package okdp

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// layer: a values layer read from b as a values file is, or false when
// it is not one.
func layer(b []byte) (M, bool) {
	v, err := chart.ParseValues(b)
	return v, err == nil
}

// errorString: err's message, "" for none.
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// FuzzParseAccess: whatever a project's access holds, ParseAccess gives
// the same answer twice, and either an error and no group, or only groups
// IsGroup accepts with a role of Roles; a map every entry of which is such
// a group and a role is read back whole, and an absent access gives the
// project's default group, an editor. Seeds: the cases of TestParseAccess.
func FuzzParseAccess(f *testing.F) {
	for _, s := range []string{
		"", "access: null", "access: {}", "access: {okdp-project-demo: admin, analysts: viewer, data.eng_team@corp:x: editor}",
		"access: [okdp-project-demo]", "access: {/okdp/demo: viewer}", "access: {demo team: viewer}", "access: {'a,b': viewer}",
		"access: {analysts: owner}", "access: {analysts: true}", "access: {a b: viewer, analysts: null}", "access: demo",
	} {
		f.Add([]byte(s), "demo")
	}
	f.Add([]byte("access: {analysts: viewer}"), "")
	f.Add([]byte("access: {analysts: viewer}"), "d/m\x00")
	f.Fuzz(func(t *testing.T, b []byte, namespace string) {
		doc, ok := layer(b)
		if !ok {
			return
		}
		access := doc[AccessKey]
		got, err := ParseAccess(access, namespace)
		again, err2 := ParseAccess(access, namespace)
		if errorString(err) != errorString(err2) || !reflect.DeepEqual(got, again) {
			t.Fatalf("%q: not the same twice: %v %v / %v %v", b, got, err, again, err2)
		}
		if err != nil {
			if got != nil {
				t.Fatalf("%q: groups %v with an error %v", b, got, err)
			}
			return
		}
		if access == nil {
			if want := map[string]Role{DefaultGroup(namespace): RoleEditor}; !reflect.DeepEqual(got, want) {
				t.Fatalf("%q: absent access gives %v, want %v", b, got, want)
			}
			return
		}
		m, isMap := access.(M)
		if !isMap || len(got) != len(m) {
			t.Fatalf("%q: read as %v", b, got)
		}
		for group, role := range got {
			if !IsGroup(group) || !slices.Contains(Roles(), role) || m[group] != string(role) {
				t.Fatalf("%q: group %q role %q of %v", b, group, role, m)
			}
		}
	})
}

// FuzzClaimID: a claim's ID, whatever its parts: the same twice, at most
// 63 characters, only lowercase letters, digits, "-" and "."; and when every
// part has a letter or a digit, the parts normalised (lowercase, every run
// of other characters one "-", trimmed) and joined by dots when they fit in
// 63 characters, else cut and ended with a hash: <namespace>.<name> for a
// named claim, <namespace>.<instance>.<field> for a template, so that the
// dots tell the kind and two sources never give one ID. Seeds: TestClaimID's.
func FuzzClaimID(f *testing.F) {
	for _, s := range [][4]string{
		{"demo", "web", "database", ""}, {"demo", "", "", "reports"}, {"Demo", "My_App", "a.b[0].c", ""},
		{"demo", "web", "x", "y"}, {"ns", strings.Repeat("a", 80), "database", ""}, {strings.Repeat("n", 54), "web", "db", ""},
		{strings.Repeat("n", 60), "web", "db", ""}, {"", "", "", ""}, {"-", "--", "..", ""}, {"demo", "web", "", ""},
	} {
		f.Add(s[0], s[1], s[2], s[3])
	}
	norm := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			} else if !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
		return strings.Trim(b.String(), "-")
	}
	f.Fuzz(func(t *testing.T, namespace, instance, field, name string) {
		args := []string{namespace, instance, field, name}
		id := claimID(namespace, instance, field, name)
		if again := claimID(namespace, instance, field, name); again != id {
			t.Fatalf("%q: %q then %q", args, id, again)
		}
		if len(id) > 63 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789-.") != "" {
			t.Fatalf("%q: ID %q", args, id)
		}
		parts := []string{norm(namespace), norm(instance), norm(field)}
		if name != "" {
			parts = []string{norm(namespace), norm(name)}
		}
		if slices.Contains(parts, "") {
			return
		}
		if joined := strings.Join(parts, "."); len(joined) <= 63 && id != joined {
			t.Fatalf("%q: ID %q, want %q", args, id, joined)
		}
		if !strings.HasPrefix(id, parts[0]+".") && len(parts[0]) < 54 {
			t.Fatalf("%q: ID %q does not start with its namespace", args, id)
		}
	})
}

// FuzzUpstreamCheck: the platform's check of an instance's upstream values,
// whatever they hold: none for a privileged project; the same answer twice;
// and for any other, a key of the denied ones refused wherever it is added:
// beside the values, nested in them, or in a string a chart renders as YAML.
// Seeds: the upstream values of TestUpstreamPolicy's cases and the denied
// fixtures.
func FuzzUpstreamCheck(f *testing.F) {
	for _, s := range []string{
		"", "{}", "image: {repository: reg.example.org/okdp/app, tag: v1}", "image: docker.io/library/nginx", "registry: evil.example.org\nrepository: app",
		"securityContext: {runAsUser: 0}", "podSecurityContext: {runAsNonRoot: false}", "extraContainers: [{name: x}]",
		"initImage: evil.example.org/x", "config: \"hostPath: /x\\n\"", "a: {b: [{c: {privileged: true}}]}",
		"tpl: \"---\\nsidecars: []\\n---\\nimage: x\"", "image: null", "image: {name: reg.example.org/okdp/x}",
	} {
		f.Add([]byte(s))
	}
	okdp := M{"policy": M{"upstream": M{"deny": []any{"extraVolumes"}, "registries": []any{"reg.example.org/okdp"}}}}
	f.Fuzz(func(t *testing.T, b []byte) {
		user, ok := layer(b)
		if !ok {
			return
		}
		at := func(p string) string { return "upstream." + p }
		if upstreamCheck(okdp, "demo", true) != nil {
			t.Fatal("a check for a privileged project")
		}
		check := upstreamCheck(okdp, "demo", false)
		got, again := check(user, nil, at), check(user, nil, at)
		if errorString(got) != errorString(again) {
			t.Fatalf("%q: %v then %v", b, got, again)
		}
		for name, with := range map[string]func(M){
			"beside":    func(m M) { m["hostPath"] = "/x" },
			"deny list": func(m M) { m["extraVolumes"] = []any{} },
			"nested":    func(m M) { m["wrap"] = M{"list": []any{M{"sidecars": []any{}}}} },
			"text":      func(m M) { m["rendered"] = "name: x\nextraContainers: []\n" },
		} {
			m := value.AsMap(value.Copy(user))
			with(m)
			if err := check(m, nil, at); err == nil {
				t.Fatalf("%q: %s: %#v accepted", b, name, m)
			}
		}
	})
}

// FuzzContextLayers: the context's layers, whatever the platform, a
// project and an instance write: the same answer twice, the inputs
// unchanged, and not shared with what is returned (a copy); and when they
// are accepted, a layer above level 0 sets nothing of global.okdp but
// provisioning and holds no template code, and the platform's settings are
// {global: {okdp: ...}} alone. Seeds: the layers of TestContextLevels and
// the context fixtures.
func FuzzContextLayers(f *testing.F) {
	platform := "global: {labels: {okdp.io/tier: gold}, okdp: {registry: null, provisioning: {server: {default: standard, classes: {standard: {provider: data/pg}}}}}}\nconnections: {shared-db: {contract: database, host: db.example.org}}\n"
	f.Add([]byte(platform), []byte(""), []byte(""))
	f.Add([]byte(platform), []byte("global: {storageClass: fast, project: {name: demo}}\nconnections: {team-db: {contract: database, host: a}}\n"), []byte("connections: {mine: {contract: database, host: b}}\n"))
	f.Add([]byte(platform), []byte("global: {okdp: {ingress: {suffix: other.example.org}}}\n"), []byte(""))
	f.Add([]byte(platform), []byte("global: {okdp: {charts: {oidcDcr: {repository: 'oci://x/y', chart: c, version: 1.0.0}}}}\n"), []byte(""))
	f.Add([]byte(platform), []byte("global: {okdp: {provisioning: {s3: {classes: {fast: {provider: data/minio}}}}}}\n"), []byte("global: {okdp: {proxy: {noProxy: .svc}}}\n"))
	f.Add([]byte(platform), []byte("claims: {reports: {contract: database}}\naccess: {analysts: viewer}\n"), []byte("claims: {reports: {provider: data/pg}}\n"))
	f.Add([]byte(platform), []byte("connections: {t: {host: '{{ .Release.Name }}'}}\n"), []byte(""))
	f.Add([]byte(""), []byte("global: {okdp: null}\n"), []byte("global: {okdp: 1}\n"))
	f.Fuzz(func(t *testing.T, b0, b1, b2 []byte) {
		var layers, before []ContextLayer
		for level, b := range [][]byte{b0, b1, b2} {
			v, ok := layer(b)
			if !ok {
				return
			}
			layers = append(layers, ContextLayer{Level: uint(level), Values: v})
			before = append(before, ContextLayer{Level: uint(level), Values: value.AsMap(value.Copy(v))})
		}
		out, platform, err := contextLayers(layers)
		out2, platform2, err2 := contextLayers(layers)
		if errorString(err) != errorString(err2) || !reflect.DeepEqual(out, out2) || !reflect.DeepEqual(platform, platform2) {
			t.Fatalf("%q: not the same twice: %v / %v", [][]byte{b0, b1, b2}, err, err2)
		}
		for _, l := range out {
			l["touched"] = true
		}
		for k := range platform {
			platform[k] = nil
		}
		if !reflect.DeepEqual(layers, before) {
			t.Fatalf("%q: the layers changed, or are shared with the result", [][]byte{b0, b1, b2})
		}
		if err != nil {
			return
		}
		for _, l := range layers {
			if l.Level == 0 {
				continue
			}
			if okdp, set := value.AsMap(l.Values["global"])["okdp"]; set {
				m, isMap := okdp.(M)
				if !isMap {
					t.Fatalf("%q: accepted global.okdp %#v at level %d", [][]byte{b0, b1, b2}, okdp, l.Level)
				}
				for k := range m {
					if k != "provisioning" {
						t.Fatalf("%q: accepted global.okdp.%s at level %d", [][]byte{b0, b1, b2}, k, l.Level)
					}
				}
			}
			if err := chart.NoTemplate(l.Values, func(p string) string { return p }); err != nil {
				t.Fatalf("%q: accepted template code at level %d: %v", [][]byte{b0, b1, b2}, l.Level, err)
			}
		}
		if g := platform2["global"]; len(platform2) > 1 || g != nil && len(value.AsMap(g)) > 1 {
			t.Fatalf("%q: platform settings %v", [][]byte{b0, b1, b2}, platform2)
		}
	})
}

// FuzzValuesLayers: an instance's layers over a context, whatever they
// hold: the same answer twice, the layers unchanged; and when accepted, no
// connections, claims or access, no template code, and of global only the
// keys the parameter schema declares, never okdp: the context's layers
// first, then the instance's, one each. Seeds: the cases of
// TestValuesLayers.
func FuzzValuesLayers(f *testing.F) {
	ctx := "global: {storageClass: fast}\n"
	for _, s := range []string{
		"", "replicas: 2", "global: {storageClass: instance}", "global: {okdp: {ingress: {suffix: x}}}", "global: {proxy: p}", "global: null",
		"connections: {}", "connections: null", "claims: {reports: {contract: database}}", "access: {a: viewer}", "upstream: {app: {tags: ['{{ .Release.Name }}']}}",
		"upstream: {app: {'{{ x }}': y}}", "global: {storageClass: '{{ x }}'}",
	} {
		f.Add([]byte(ctx), []byte(s))
	}
	schema := M{"properties": M{"global": M{"properties": M{"storageClass": M{}, "okdp": M{}}}}}
	f.Fuzz(func(t *testing.T, bc, bv []byte) {
		c, ok := layer(bc)
		v, ok2 := layer(bv)
		if !ok || !ok2 {
			return
		}
		before := value.Copy(M{"c": c, "v": v})
		in := Inputs{Context: []ContextLayer{{Values: c}}, Values: []map[string]any{v}}
		got, _, err := valuesLayers(schema, in)
		got2, _, err2 := valuesLayers(schema, in)
		if errorString(err) != errorString(err2) || !reflect.DeepEqual(got, got2) {
			t.Fatalf("%q %q: not the same twice: %v / %v", bc, bv, err, err2)
		}
		if !reflect.DeepEqual(before, M{"c": c, "v": v}) {
			t.Fatalf("%q %q: the layers changed", bc, bv)
		}
		if err != nil {
			return
		}
		if len(got) != 2 {
			t.Fatalf("%q %q: %d layers", bc, bv, len(got))
		}
		for _, k := range []string{"connections", "claims", AccessKey} {
			if _, set := v[k]; set {
				t.Fatalf("%q %q: accepted %s", bc, bv, k)
			}
		}
		if g, set := v["global"]; set {
			m, isMap := g.(M)
			if !isMap {
				t.Fatalf("%q %q: accepted global %#v", bc, bv, g)
			}
			for k := range m {
				if k != "storageClass" {
					t.Fatalf("%q %q: accepted global.%s", bc, bv, k)
				}
			}
		}
		if err := chart.NoTemplate(v, func(p string) string { return p }); err != nil {
			t.Fatalf("%q %q: accepted template code: %v", bc, bv, err)
		}
	})
}
