package okdp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestParseAccess: a project's access maps group names to a role, every
// problem at once; absent or null, the default group an editor; {} no
// group.
func TestParseAccess(t *testing.T) {
	for name, tc := range map[string]struct {
		access any
		want   map[string]Role
		err    string // the error, none when empty
	}{
		"absent":       {nil, map[string]Role{"okdp-project-demo": RoleEditor}, ""},
		"none":         {M{}, map[string]Role{}, ""},
		"every role":   {M{"okdp-project-demo": "admin", "analysts": "viewer", "data.eng_team@corp:x": "editor"}, map[string]Role{"okdp-project-demo": RoleAdmin, "analysts": RoleViewer, "data.eng_team@corp:x": RoleEditor}, ""},
		"not a map":    {[]any{"okdp-project-demo"}, nil, "access: the roles of the project's groups, a map of group names to viewer, editor or admin"},
		"a path":       {M{"/okdp/demo": "viewer"}, nil, `access./okdp/demo: a group name`},
		"a space":      {M{"demo team": "viewer"}, nil, `access.demo team: a group name`},
		"a comma":      {M{"a,b": "viewer"}, nil, `a group name`},
		"too long":     {M{strings.Repeat("a", 256): "viewer"}, nil, `a group name`},
		"no role":      {M{"analysts": "owner"}, nil, `access.analysts: a role, viewer, editor or admin, got "owner"`},
		"not a string": {M{"analysts": true}, nil, `access.analysts: a role, viewer, editor or admin, got true`},
		"every error":  {M{"a b": "viewer", "analysts": nil}, nil, "access.a b: a group name"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseAccess(nil, tc.access, "demo")
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

// TestAccessCompile: Result.Groups is the access of the project's file,
// level 1, its layers merged; refused,
// it refuses the instance; access at another level of the context, or in
// the instance's values, is refused; no instance receives it, and a
// project sets nothing of global.okdp.access.
func TestAccessCompile(t *testing.T) {
	platform := map[string]any{"global": M{"okdp": M{"access": M{"admins": []any{"platform_admin"}, "groupPrefix": "oidc:"}}}}
	in := inputs(platform)
	in.Context = append(in.Context, ContextLayer{Level: 1, Values: map[string]any{"access": M{"analysts": "viewer", "okdp-project-demo": "viewer"}}}, ContextLayer{Level: 1, Values: map[string]any{"access": M{"okdp-project-demo": "admin"}}})
	res, err := Compile(context.Background(), def, in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Role{"analysts": RoleViewer, "okdp-project-demo": RoleAdmin}
	if !reflect.DeepEqual(res.Groups, want) {
		t.Errorf("Access = %+v, want %+v", res.Groups, want)
	}
	for chart, v := range res.Values {
		if _, set := v["access"]; set {
			t.Errorf("%s: the values hold access", chart)
		}
	}
	res, err = Compile(context.Background(), def, inputs())
	if err != nil || !reflect.DeepEqual(res.Groups, map[string]Role{"okdp-project-demo": RoleEditor}) {
		t.Errorf("the default: %+v, %v", res, err)
	}
	for name, tc := range map[string]struct {
		in   func() Inputs
		want string
	}{
		"refused": {func() Inputs {
			in := inputs()
			in.Context = append(in.Context, ContextLayer{Level: 1, Values: map[string]any{"access": M{"analysts": "owner"}}})
			return in
		}, "the project's file (level 1): access.analysts: a role"},
		"the platform's": {func() Inputs {
			in := inputs()
			in.Context = append(in.Context, ContextLayer{Values: map[string]any{"access": M{"analysts": "viewer"}}})
			return in
		}, "access: the roles of a project's groups, its file's, of level 1 only"},
		"an instance's level": {func() Inputs {
			in := inputs()
			in.Context = append(in.Context, ContextLayer{Level: 2, Values: map[string]any{"access": M{}}})
			return in
		}, "(level 2): access: the roles of a project's groups"},
		"the values": {func() Inputs {
			in := inputs()
			in.Values = []map[string]any{{"access": M{"analysts": "admin"}}}
			return in
		}, "values layer 1: access is the project's, its file's (the context at level 1)"},
		"global.okdp.access of a project": {func() Inputs {
			in := inputs()
			in.Context = append(in.Context, ContextLayer{Level: 1, Values: map[string]any{"global": M{"okdp": M{"access": M{"admins": []any{"demo"}}}}}})
			return in
		}, "global.okdp.access: a platform setting, level 0 only"},
		"a group of the platform's": {func() Inputs {
			return inputs(map[string]any{"global": M{"okdp": M{"access": M{"admins": []any{"/admins"}}}}})
		}, "at '/global/okdp/access/admins/0': '/admins' does not match pattern"},
		"another key": {func() Inputs {
			return inputs(map[string]any{"global": M{"okdp": M{"access": M{"admin": []any{"admins"}}}}})
		}, "at '/global/okdp/access': additional properties 'admin' not allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(context.Background(), def, tc.in())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestProjectOfNamespace: the namespace of a project is checked as the
// compile of its instances checks it, the compiler's reserved names and
// the platform's (global.okdp.policy.reserved), privileged or not, a
// *NamespaceError, so that PlatformOf and Compile agree on which
// projects exist; CheckProject is that check alone.
func TestProjectOfNamespace(t *testing.T) {
	platform := map[string]any{"global": M{"okdp": M{"policy": M{"privileged": []any{"demo"}, "reserved": []any{"demo"}}}}}
	in := inputs(platform)
	_, cerr := Compile(context.Background(), def, in)
	_, perr := PlatformOf(in)
	if cerr == nil || perr == nil || cerr.Error() != perr.Error() {
		t.Fatalf("Compile: %v\nPlatformOf: %v", cerr, perr)
	}
	for _, err := range []error{perr, CheckProject(in)} {
		if _, ok := errors.AsType[*NamespaceError](err); !ok || !strings.Contains(err.Error(), "reserved by the platform (global.okdp.policy.reserved)") {
			t.Errorf("reserved by the platform: %v", err)
		}
	}
	for _, ns := range []string{"kube-system", "argocd", "platform", "Not_A_Label"} {
		in := inputs(nil)
		in.Namespace = ns
		if _, err := PlatformOf(in); !errors.As(err, new(*NamespaceError)) {
			t.Errorf("%s: %v", ns, err)
		}
	}
	if err := CheckProject(inputs(map[string]any{"global": M{"okdp": M{"policy": M{"reserved": []any{"other"}}}}})); err != nil {
		t.Errorf("a namespace nothing reserves: %v", err)
	}
	// The platform's settings refused are no namespace problem.
	if err := CheckProject(inputs(map[string]any{"global": M{"okdp": M{"policy": M{"privileged": "demo"}}}})); err == nil || errors.As(err, new(*NamespaceError)) {
		t.Errorf("the platform's settings refused: %v", err)
	}
}

// TestPlatformAdmins and TestProjectAccess: the accessors of a program
// that mirrors the platform's authority.
func TestPlatformAdmins(t *testing.T) {
	got, err := PlatformAdmins(nil, map[string]any{"global": M{"okdp": M{"access": M{"admins": []any{"platform_admin", "ops"}}}}})
	if err != nil || !reflect.DeepEqual(got, []string{"platform_admin", "ops"}) {
		t.Errorf("got %v, %v", got, err)
	}
	if got, err := PlatformAdmins(nil, nil); err != nil || got != nil {
		t.Errorf("none: %v, %v", got, err)
	}
	if _, err := PlatformAdmins(nil, map[string]any{"global": M{"okdp": M{"access": M{"admins": "ops"}}}}); err == nil {
		t.Error("not a list: no error")
	}
}
