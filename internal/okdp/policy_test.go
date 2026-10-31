package okdp

import (
	"context"
	"testing"
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
