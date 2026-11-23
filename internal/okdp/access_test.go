package okdp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestParseAccess: access maps group names to a role, every problem at
// once; absent or null, no group; {} no group either.
func TestParseAccess(t *testing.T) {
	for name, tc := range map[string]struct {
		access any
		want   map[string]Role
		err    string // the error, none when empty
	}{
		"absent":       {nil, nil, ""},
		"none":         {M{}, map[string]Role{}, ""},
		"every role":   {M{"okdp-project-demo": "admin", "analysts": "viewer", "data.eng_team@corp:x": "editor"}, map[string]Role{"okdp-project-demo": RoleAdmin, "analysts": RoleViewer, "data.eng_team@corp:x": RoleEditor}, ""},
		"go types":     {map[string]string{"analysts": "viewer"}, map[string]Role{"analysts": RoleViewer}, ""},
		"not a map":    {[]any{"okdp-project-demo"}, nil, "access: the roles of groups, a map of group names to viewer, editor or admin"},
		"a path":       {M{"/okdp/demo": "viewer"}, nil, `access./okdp/demo: a group name`},
		"a space":      {M{"demo team": "viewer"}, nil, `access.demo team: a group name`},
		"a comma":      {M{"a,b": "viewer"}, nil, `a group name`},
		"too long":     {M{strings.Repeat("a", 256): "viewer"}, nil, `a group name`},
		"no role":      {M{"analysts": "owner"}, nil, `access.analysts: a role, viewer, editor or admin, got "owner"`},
		"not a string": {M{"analysts": true}, nil, `access.analysts: a role, viewer, editor or admin, got true`},
		"every error":  {M{"a b": "viewer", "analysts": nil}, nil, "access.a b: a group name"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseAccess("access", tc.access)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %v, want an error containing %q", err, tc.err)
				}
				if name == "every error" && !strings.Contains(err.Error(), "access.analysts: a role") {
					t.Errorf("not every problem: %v", err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}

// TestSettingsOf: the namespace is checked as the compile of its
// instances checks it, the compiler's reserved names and the settings'
// (global.okdp.policy.reserved), privileged or not, a *NamespaceError,
// so that SettingsOf and Compile agree; the settings come with their
// defaults.
func TestSettingsOf(t *testing.T) {
	platform := map[string]any{"global": M{"okdp": M{"policy": M{"privileged": []any{"demo"}, "reserved": []any{"demo"}}}}}
	in := inputs(platform)
	_, cerr := Compile(context.Background(), def, in)
	_, serr := SettingsOf(context.Background(), in)
	if cerr == nil || serr == nil || cerr.Error() != serr.Error() {
		t.Fatalf("Compile: %v\nSettingsOf: %v", cerr, serr)
	}
	if _, ok := errors.AsType[*NamespaceError](serr); !ok || !strings.Contains(serr.Error(), "reserved by the platform (global.okdp.policy.reserved)") {
		t.Errorf("reserved by the platform: %v", serr)
	}
	for _, ns := range []string{"kube-system", "argocd", "platform", "Not_A_Label"} {
		in := inputs()
		in.Namespace = ns
		if _, err := SettingsOf(context.Background(), in); !errors.As(err, new(*NamespaceError)) {
			t.Errorf("%s: %v", ns, err)
		}
	}
	settings, err := SettingsOf(context.Background(), inputs(map[string]any{"global": M{"okdp": M{"policy": M{"reserved": []any{"other"}}}}}))
	if err != nil || settings["secrets"] == nil {
		t.Errorf("a namespace nothing reserves: %v, %v", settings, err)
	}
	// The settings refused are no namespace problem.
	if _, err := SettingsOf(context.Background(), inputs(map[string]any{"global": M{"okdp": M{"policy": M{"privileged": "demo"}}}})); err == nil || errors.As(err, new(*NamespaceError)) {
		t.Errorf("the settings refused: %v", err)
	}
}
