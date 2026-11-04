package okdp

import (
	"context"
	"errors"
	"maps"
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
			got, err := ParseAccess(tc.access, "demo")
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

// TestAccessCompile: Result.Access is the access of the project's file,
// level 1, its layers merged, with the platform's group prefix; refused,
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
	want := Access{Groups: map[string]Role{"analysts": RoleViewer, "okdp-project-demo": RoleAdmin}, GroupPrefix: "oidc:"}
	if !reflect.DeepEqual(res.Access, want) {
		t.Errorf("Access = %+v, want %+v", res.Access, want)
	}
	for chart, v := range res.Values {
		if _, set := v["access"]; set {
			t.Errorf("%s: the values hold access", chart)
		}
	}
	res, err = Compile(context.Background(), def, inputs())
	if err != nil || !reflect.DeepEqual(res.Access, Access{Groups: map[string]Role{"okdp-project-demo": RoleEditor}}) {
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

// TestProjectOf: a project's settings of its context alone, as a compile
// of its instances gives them; an access refused is an *AccessError, the
// rest given, no group a role; the platform's settings refused, an error.
func TestProjectOf(t *testing.T) {
	platform := map[string]any{"global": M{"okdp": M{"access": M{"groupPrefix": "oidc:"}, "policy": M{"privileged": []any{"demo"}}, "secrets": M{"store": "platform", "provider": M{"fake": M{}}}}}}
	in := inputs(platform)
	res, err := Compile(context.Background(), def, in)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ProjectOf(in)
	if err != nil {
		t.Fatal(err)
	}
	want := Project{Namespace: "demo", Privileged: res.Privileged, SecretStore: res.SecretStore, Access: res.Access}
	if !reflect.DeepEqual(p, want) || !p.Privileged || p.SecretStore == nil {
		t.Errorf("ProjectOf = %+v, want %+v", p, want)
	}
	in.Context = append(in.Context, ContextLayer{Level: 1, Values: map[string]any{"access": M{"a b": "viewer"}}})
	p, err = ProjectOf(in)
	if ae, ok := errors.AsType[*AccessError](err); !ok || !strings.Contains(ae.Error(), "access.a b: a group name") {
		t.Errorf("an access refused: %v", err)
	}
	if !p.Privileged || p.SecretStore == nil || len(p.Access.Groups) != 0 || p.Access.GroupPrefix != "oidc:" {
		t.Errorf("an access refused: %+v", p)
	}
	in = inputs(map[string]any{"global": M{"okdp": M{"policy": M{"privileged": "demo"}}}})
	if _, err := ProjectOf(in); err == nil || errors.As(err, new(*AccessError)) {
		t.Errorf("the platform's settings refused: %v", err)
	}
}

// TestPlatformAdmins and TestProjectAccess: the accessors of a program
// that mirrors the platform's authority.
func TestPlatformAdmins(t *testing.T) {
	got, err := PlatformAdmins(map[string]any{"global": M{"okdp": M{"access": M{"admins": []any{"platform_admin", "ops"}}}}})
	if err != nil || !reflect.DeepEqual(got, []string{"platform_admin", "ops"}) {
		t.Errorf("got %v, %v", got, err)
	}
	if got, err := PlatformAdmins(nil); err != nil || got != nil {
		t.Errorf("none: %v, %v", got, err)
	}
	if _, err := PlatformAdmins(map[string]any{"global": M{"okdp": M{"access": M{"admins": "ops"}}}}); err == nil {
		t.Error("not a list: no error")
	}
}

func TestProjectAccess(t *testing.T) {
	got, err := ProjectAccess([]byte("description: the demo\naccess:\n  analysts: viewer\n  okdp-project-demo: admin\n"), "demo")
	if want := map[string]Role{"analysts": RoleViewer, "okdp-project-demo": RoleAdmin}; err != nil || !maps.Equal(got, want) {
		t.Errorf("got %v, %v, want %v", got, err, want)
	}
	if got, err := ProjectAccess(nil, "demo"); err != nil || !maps.Equal(got, map[string]Role{"okdp-project-demo": RoleEditor}) {
		t.Errorf("no file: %v, %v", got, err)
	}
	if _, err := ProjectAccess([]byte("access: {a: owner}\n"), "demo"); err == nil {
		t.Error("a role refused: no error")
	}
	if _, err := ProjectAccess(nil, "kube-system"); err == nil {
		t.Error("a reserved project: no error")
	}
}

// TestControlPlaneSetting: global.okdp.controlPlane.serviceAccount, the
// control plane's identity, Result.ControlPlane and ProjectOf's; absent,
// none; its namespace and name required, a DNS label and subdomain; the
// platform's only.
func TestControlPlaneSetting(t *testing.T) {
	sa := func(v any) map[string]any {
		return map[string]any{"global": M{"okdp": M{"controlPlane": M{"serviceAccount": v}}}}
	}
	in := inputs(sa(M{"namespace": "okdp-system", "name": "okdp-control-plane-server"}))
	res, err := Compile(context.Background(), def, in)
	want := &ServiceAccount{Namespace: "okdp-system", Name: "okdp-control-plane-server"}
	if err != nil || !reflect.DeepEqual(res.ControlPlane, want) {
		t.Fatalf("ControlPlane = %+v, %v", res, err)
	}
	if p, err := ProjectOf(in); err != nil || !reflect.DeepEqual(p.ControlPlane, want) {
		t.Errorf("ProjectOf: %+v, %v", p, err)
	}
	if res, err := Compile(context.Background(), def, inputs()); err != nil || res.ControlPlane != nil {
		t.Errorf("absent: %+v, %v", res, err)
	}
	for name, tc := range map[string]struct {
		in   Inputs
		want string
	}{
		"no name":     {inputs(sa(M{"namespace": "okdp-system"})), "missing property 'name'"},
		"not a label": {inputs(sa(M{"namespace": "okdp.system", "name": "s"})), "/serviceAccount/namespace': 'okdp.system' does not match pattern"},
		"another key": {inputs(sa(M{"namespace": "okdp-system", "name": "s", "token": "t"})), "additional properties 'token' not allowed"},
		"a project's": {func() Inputs {
			in := inputs()
			in.Context = append(in.Context, ContextLayer{Level: 1, Values: sa(M{"namespace": "demo", "name": "s"})})
			return in
		}(), "global.okdp.controlPlane: a platform setting, level 0 only"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Compile(context.Background(), def, tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
