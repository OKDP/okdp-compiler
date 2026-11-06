package okdp

import (
	"context"
	"strings"
	"testing"
)

// TestReleaseNamedLikeAPlatformObject: a release that is the name of an
// object the platform writes in every namespace (the Role and the
// NetworkPolicy okdp-project, okdp-control-plane, the RoleBindings
// okdp-access-*, the secret store's SecretStore and ServiceAccount, the
// DCR token's Secret), or the prefix, up to a "-", of one, is refused: the
// objects of a package are named after the release, <release>, <release>-x,
// and would take the platform's place in the namespace (the project okdp's
// instance project, release okdp-project).
func TestReleaseNamedLikeAPlatformObject(t *testing.T) {
	secrets := M{"global": M{"okdp": M{"secrets": M{"store": "vault-store", "serviceAccount": "vault-sa"}, "oidc": M{"dcr": M{"tokenSecret": "dcr-creds"}}}}}
	for _, tc := range []struct {
		release string
		layers  []map[string]any
		want    string // the error, "" for none
	}{
		{"okdp-project", nil, `Inputs.Release "okdp-project": the name of an object the platform writes in every namespace (okdp-project)`},
		{"okdp-control-plane", nil, "okdp-control-plane"},
		{"okdp-access-editor", nil, "okdp-access-editor"},
		{"okdp-secrets", nil, "okdp-secrets"},
		{"okdp-dcr-token", nil, "okdp-dcr-token"},
		// A prefix up to a "-": <release>-admin is the platform's.
		{"okdp-access", nil, `Inputs.Release "okdp-access": the name of an object the platform writes in every namespace (okdp-access-admin)`},
		{"okdp-control", nil, "okdp-control-plane"},
		{"okdp", nil, "okdp-access-admin"},
		// The platform's own names for them.
		{"vault-store", []map[string]any{secrets}, "vault-store"},
		{"vault-sa", []map[string]any{secrets}, "vault-sa"},
		{"dcr-creds", []map[string]any{secrets}, "dcr-creds"},
		{"vault", []map[string]any{secrets}, "vault-sa"},
		// Not one.
		{"demo-app", nil, ""},
		{"okdp-system-app", nil, ""},
		{"okdp-projects", nil, ""},
		{"okdp-secrets-x", nil, ""},
		{"vault-store", nil, ""},
	} {
		t.Run(tc.release, func(t *testing.T) {
			in := inputs(tc.layers...)
			in.Release = tc.release
			_, err := Compile(context.Background(), def, in)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
			if tc.want == "" {
				return
			}
			// A privileged project's alike: the platform's own objects are
			// theirs too.
			priv := inputs(append(tc.layers, map[string]any{"global": M{"okdp": M{"policy": M{"privileged": []any{"demo"}}}}})...)
			priv.Release = tc.release
			if _, err := Compile(context.Background(), def, priv); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("privileged: got %v", err)
			}
		})
	}
}
