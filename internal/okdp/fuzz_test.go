package okdp

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
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

// FuzzParseAccess: whatever access holds, ParseAccess gives the same
// answer twice, and either an error and no group, or only groups IsGroup
// accepts with a role of roles; a map every entry of which is such a group
// and a role is read back whole, and an absent access gives no group.
// Seeds: the cases of TestParseAccess.
func FuzzParseAccess(f *testing.F) {
	for _, s := range []string{
		"", "access: null", "access: {}", "access: {okdp-project-demo: admin, analysts: viewer, data.eng_team@corp:x: editor}",
		"access: [okdp-project-demo]", "access: {/okdp/demo: viewer}", "access: {demo team: viewer}", "access: {'a,b': viewer}",
		"access: {analysts: owner}", "access: {analysts: true}", "access: {a b: viewer, analysts: null}", "access: demo",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		doc, ok := layer(b)
		if !ok {
			return
		}
		access := doc["access"]
		got, err := ParseAccess("access", access)
		again, err2 := ParseAccess("access", access)
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
			if got != nil {
				t.Fatalf("%q: absent access gives %v", b, got)
			}
			return
		}
		m, isMap := access.(M)
		if !isMap || len(got) != len(m) {
			t.Fatalf("%q: read as %v", b, got)
		}
		for group, role := range got {
			if !IsGroup(group) || !slices.Contains(roles, role) || m[group] != string(role) {
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
		parts := []string{instance, field}
		if name != "" {
			parts = []string{name}
		}
		id := claimID(namespace, parts...)
		if again := claimID(namespace, parts...); again != id {
			t.Fatalf("%q: %q then %q", args, id, again)
		}
		if len(id) > 63 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789-.") != "" {
			t.Fatalf("%q: ID %q", args, id)
		}
		parts = []string{norm(namespace), norm(instance), norm(field)}
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

// FuzzValuesLayers: values layers, whatever they hold: the same answer
// twice, the layers unchanged and not shared with what is returned (a
// copy); and when accepted, one layer each, and the settings
// {global: {okdp: ...}} alone, global.okdp of the layers merged in order.
// Seeds: the cases of TestValuesLayers.
func FuzzValuesLayers(f *testing.F) {
	for _, s := range []string{
		"", "replicas: 2", "global: {storageClass: instance}", "global: {okdp: {cluster: {domain: x}}}", "global: {proxy: p}", "global: null",
		"connections: {}", "connections: null", "claims: {reports: {contract: database}}", "access: {a: viewer}", "upstream: {app: {tags: ['{{ .Release.Name }}']}}",
		"global: {okdp: null}", "global: {okdp: 1}",
	} {
		f.Add([]byte("global: {okdp: {proxy: {noProxy: .svc}}, storageClass: fast}\n"), []byte(s))
	}
	f.Fuzz(func(t *testing.T, b0, b1 []byte) {
		l0, ok := layer(b0)
		l1, ok1 := layer(b1)
		if !ok || !ok1 {
			return
		}
		before := value.Copy(M{"0": l0, "1": l1})
		in := Inputs{Rules: Mandatory(), Values: []map[string]any{l0, l1}}
		ls, err := valuesLayers(in)
		ls2, err2 := valuesLayers(in)
		got, settings, settings2 := ls.layers, ls.platform, ls2.platform
		if errorString(err) != errorString(err2) || !reflect.DeepEqual(got, ls2.layers) || !reflect.DeepEqual(settings, settings2) || !reflect.DeepEqual(ls.written, settings) {
			t.Fatalf("%q %q: not the same twice: %v / %v", b0, b1, err, err2)
		}
		for _, l := range got {
			l["touched"] = true
		}
		if !reflect.DeepEqual(before, M{"0": l0, "1": l1}) {
			t.Fatalf("%q %q: the layers changed, or are shared with the result", b0, b1)
		}
		if err != nil {
			return
		}
		if len(got) != 2 {
			t.Fatalf("%q %q: %d layers", b0, b1, len(got))
		}
		want := M{}
		for _, l := range []M{l0, l1} {
			if okdp, set := value.AsMap(l["global"])["okdp"]; set {
				want = value.Merge(want, M{"global": M{"okdp": okdp}})
			}
		}
		if !reflect.DeepEqual(settings2, want) {
			t.Fatalf("%q %q: settings %v, want %v", b0, b1, settings2, want)
		}
	})
}
