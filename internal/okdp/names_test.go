package okdp

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
)

var (
	sqlIdentifier = regexp.MustCompile(`^[a-z0-9_]+$`)
	dnsLabel      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// claimIDs: every claim ID (chart.IsClaimID) of the alphabet a, b, 1, "-"
// and ".", up to n characters; and count random ones of up to four labels of twelve
// characters, to make the names past the length asked for.
func claimIDs(n, count int) []string {
	var out []string
	var walk func(s string)
	walk = func(s string) {
		if s != "" && chart.IsClaimID(s) {
			out = append(out, s)
		}
		if len(s) == n {
			return
		}
		for _, c := range "ab1-." {
			walk(s + string(c))
		}
	}
	walk("")
	rnd := rand.New(rand.NewPCG(1, 2))
	for len(out) < n+count {
		var b strings.Builder
		for i := rnd.IntN(4) + 1; i > 0; i-- {
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			label := make([]byte, rnd.IntN(12)+1)
			for j := range label {
				label[j] = "ab1-"[rnd.IntN(4)]
			}
			if label[0] == '-' || label[len(label)-1] == '-' {
				label[0], label[len(label)-1] = 'a', 'b'
			}
			b.Write(label)
		}
		if chart.IsClaimID(b.String()) {
			out = append(out, b.String())
		}
	}
	return out
}

// TestNamesInjective: no two distinct claim IDs give one name, whatever the
// length asked for, short ones that make most of the names hashed included;
// a name is what it should be (an identifier of [a-z0-9_], a DNS-1123
// label), at most the length asked for, deterministic.
func TestNamesInjective(t *testing.T) {
	ids := claimIDs(7, 20000)
	// The audit's pair, and IDs of the form of the system keyspaces.
	ids = append(ids, "a-b.c", "a.b-c", "system.x", "c.system.x", "system", "systemx.y", "system-x.y", "system.x-y")
	long := strings.Repeat("abcd-", 10) + "e"
	ids = append(ids, long+".x", long+".y", long+"-x", strings.Repeat("a", 63), strings.Repeat("a", 31)+"."+strings.Repeat("a", 31), strings.Repeat("a", 61)+"-b")
	for _, limit := range []int64{24, 30, 48, 63, 100, 253} {
		seen := map[string]map[string]string{"sql": {}, "object": {}}
		for _, id := range ids {
			sql, err := sqlName(id, limit)
			if err != nil {
				t.Fatal(err)
			}
			object, err := objectName(id, limit)
			if err != nil {
				t.Fatal(err)
			}
			if !sqlIdentifier.MatchString(sql) || len(sql) > int(limit) || strings.HasPrefix(sql, "system") {
				t.Errorf("limit %d: okdpSqlName(%q) = %q", limit, id, sql)
			}
			if !dnsLabel.MatchString(object) || len(object) > int(limit) || len(object) > 63 && limit <= 63 {
				t.Errorf("limit %d: okdpObjectName(%q) = %q", limit, id, object)
			}
			if again, _ := sqlName(id, limit); again != sql {
				t.Errorf("limit %d: okdpSqlName(%q) is not deterministic", limit, id)
			}
			for kind, name := range map[string]string{"sql": sql, "object": object} {
				if other, ok := seen[kind][name]; ok && other != id {
					t.Errorf("limit %d: %s name %q for %q and %q", limit, kind, name, other, id)
				}
				seen[kind][name] = id
			}
		}
	}
	// The pairs of the audit, by hand.
	for _, tc := range []struct{ id, sql, object string }{
		{"a-b.c", "a__b_c", "a--b-c"},
		{"a.b-c", "a_b__c", "a-b--c"},
		{"team.analytics", "team_analytics", "team-analytics"},
		{"system.x", "c___system_x", "system-x"},
		{"c.system.x", "c_system_x", "c-system-x"},
	} {
		if got, _ := sqlName(tc.id, 63); got != tc.sql {
			t.Errorf("okdpSqlName(%q) = %q, want %q", tc.id, got, tc.sql)
		}
		if got, _ := objectName(tc.id, 63); got != tc.object {
			t.Errorf("okdpObjectName(%q) = %q, want %q", tc.id, got, tc.object)
		}
	}
}

// TestNamesLong: a name that does not fit is its start, a separator and 16
// digits of the ID's SHA-256, as long as asked for; one that fits is not
// cut, and is shorter than that.
func TestNamesLong(t *testing.T) {
	id := strings.Repeat("a", 30) + "-" + strings.Repeat("b", 30)
	for fn, tc := range map[string]struct {
		f    func(string, int64) (string, error)
		want string
	}{
		"sql":    {sqlName, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa__bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		"object": {objectName, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa--bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	} {
		short, err := tc.f(id, 253)
		if err != nil || short != tc.want {
			t.Errorf("%s: got %q (%v)", fn, short, err)
		}
		cut, err := tc.f(id, 48)
		if err != nil || len(cut) != 48 || cut[:31] != tc.want[:31] || cut[31] != tc.want[2+30] && cut[31] != '_' && cut[31] != '-' {
			t.Errorf("%s: got %q (%v)", fn, cut, err)
		}
		if exact, _ := tc.f("a-b.c", 24); len(exact) != 6 {
			t.Errorf("%s: a short name is cut: %q", fn, exact)
		}
		// Shorter than limit it stays, of limit characters it is cut.
		if edge, _ := tc.f(strings.Repeat("a", 23), 24); edge != strings.Repeat("a", 23) {
			t.Errorf("%s: a name shorter than limit is cut: %q", fn, edge)
		}
		if edge, _ := tc.f(strings.Repeat("a", 24), 24); len(edge) != 24 || edge == strings.Repeat("a", 24) {
			t.Errorf("%s: a name of limit characters is not cut: %q", fn, edge)
		}
	}
}

// TestNameFunctions: okdpSqlName and okdpObjectName called from CEL, with
// and without a length; an ID or a length out of the form fails.
func TestNameFunctions(t *testing.T) {
	s := newSession(M{})
	for expr, want := range map[string]any{
		"okdpSqlName('a-b.c')":                    "a__b_c",
		"okdpObjectName('a-b.c')":                 "a--b-c",
		"okdpSqlName('system.x', 48)":             "c___system_x",
		"okdpSqlName(id)":                         "team_db",
		"claims.map(c, okdpObjectName(c.id, 30))": []any{"team-db"},
	} {
		got, err := eval(s, expr, M{"id": "team.db", "claims": []any{M{"id": "team.db"}}})
		if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: got %v (%v), want %v", expr, got, err, want)
		}
	}
	for expr, want := range map[string]string{
		"okdpSqlName('A.b')":         `okdpSqlName: "A.b" is not a claim's ID`,
		"okdpSqlName('a..b')":        `is not a claim's ID`,
		"okdpSqlName('-a.b')":        `is not a claim's ID`,
		"okdpObjectName('a.b-')":     `is not a claim's ID`,
		"okdpObjectName('')":         `is not a claim's ID`,
		"okdpSqlName('a.b', 10)":     "okdpSqlName: the length 10 is out of 24 to 253",
		"okdpObjectName('a.b', 254)": "okdpObjectName: the length 254 is out of 24 to 253",
		"okdpSqlName(1)":             "found no matching overload",
		"okdpSqlName(n)":             "no such overload",
	} {
		if _, err := eval(s, expr, M{"n": 1}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error containing %q", expr, err, want)
		}
	}
}
