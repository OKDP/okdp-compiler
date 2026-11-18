package okdp

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/testrules"
	"github.com/okdp/okdp-compiler/value"
)

// dcrCtx: a context whose platform registers OAuth clients anonymously (dcr
// mode), these DCR settings over the defaults (newInstance).
func dcrCtx(settings ...M) M {
	dcr := M{
		"authMethod":      "anonymous",
		"registrationUrl": "https://idp.example.org/register",
		"image":           M{"repository": "example/dcr"},
	}
	for _, s := range settings {
		dcr = value.Merge(dcr, s)
	}
	inst, err := newInstance(context.Background(), pkg, inputs(M{"global": M{"okdp": M{"oidc": M{"clientProvisioning": "dcr", "scope": "openid profile email", "dcr": dcr}}}}), defService)
	if err != nil {
		panic(err)
	}
	return M{
		"release":  M{"name": "demo-x", "namespace": "demo"},
		"platform": M{"oidc": value.Dig(inst.scope, "platform.oidc")},
	}
}

// dcrDigest: the digest of the registration Jobs' default image.
const dcrDigest = "sha256:38e21cbb15124b102b55581f74b30413633df98d291b2341a2d9a6fef480f06c"

// dcrInstance: the instance of ctx, of a privileged project unless
// tenant: no host to check.
func dcrInstance(ctx M, tenant bool) *instance {
	inst := testInstance(ctx)
	inst.okdp = M{"ingress": M{"suffix": "example.org"}}
	if !tenant {
		inst.okdp["policy"] = M{"privileged": []any{"demo"}}
	}
	return inst
}

// dcrObjects: the objects of these clients in ctx, by "<kind>/<name>".
func dcrObjects(ctx M, clients ...M) (map[string]M, error) {
	section := []any{}
	for _, c := range clients {
		section = append(section, value.Copy(c))
	}
	objects, err := ownObjects(context.Background(), newSession(ctx), dcrInstance(ctx, false), M{"oidcClients": section})
	if err != nil {
		return nil, err
	}
	out := map[string]M{}
	for _, o := range objects {
		m := value.AsMap(o)
		out[fmt.Sprintf("%s/%s", m["kind"], value.Dig(m, "metadata.name"))] = m
	}
	return out, nil
}

// setIssuer: the platform's issuerUri.
func setIssuer(ctx M, issuer string) {
	value.AsMap(value.AsMap(ctx["platform"])["oidc"])["issuerUri"] = issuer
}

// jobContainer: the container of a Job.
func jobContainer(t *testing.T, job M) M {
	t.Helper()
	cs := value.AsList(value.Dig(job, "spec.template.spec.containers"))
	if len(cs) != 1 {
		t.Fatalf("containers: %#v", cs)
	}
	return value.AsMap(cs[0])
}

// jobEnv: the literal environment of a container, by name.
func jobEnv(c M) map[string]any {
	out := map[string]any{}
	for _, v := range value.AsList(c["env"]) {
		out[value.Str(value.AsMap(v)["name"])] = value.AsMap(v)["value"]
	}
	return out
}

// jobRequest: the registration request the Job sends.
func jobRequest(t *testing.T, c M) M {
	t.Helper()
	var req M
	if err := json.Unmarshal([]byte(value.Str(jobEnv(c)["REQUEST"])), &req); err != nil {
		t.Fatal(err)
	}
	return req
}

// TestDcrDefaults: the main client, confidential, named after the release:
// its Job, hardened, by digest, a hook before the package's; the Role that
// changes its one Secret; the removal at uninstall.
func TestDcrDefaults(t *testing.T) {
	objects, err := dcrObjects(dcrCtx(), M{"grantTypes": []any{"authorization_code"}, "redirectUris": []any{"https://x/cb"}})
	if err != nil {
		t.Fatal(err)
	}
	kinds := sortedKeys(objects)
	for _, want := range []string{"ServiceAccount/demo-x-oidc-dcr", "Role/demo-x-oidc-dcr", "RoleBinding/demo-x-oidc-dcr", "ConfigMap/demo-x-oidc-dcr", "Service/demo-x-oidc-dcr-headless", "Job/demo-x-oidc-dcr",
		"ServiceAccount/demo-x-oidc-dcr-delete", "Role/demo-x-oidc-dcr-delete", "RoleBinding/demo-x-oidc-dcr-delete", "ConfigMap/demo-x-oidc-dcr-delete", "Job/demo-x-oidc-dcr-delete"} {
		if objects[want] == nil {
			t.Errorf("no %s in %v", want, kinds)
		}
	}
	if len(objects) != 11 {
		t.Errorf("objects: %v", kinds)
	}
	job := objects["Job/demo-x-oidc-dcr"]
	pod := value.AsMap(value.Dig(job, "spec.template.spec"))
	c := jobContainer(t, job)
	if pod["automountServiceAccountToken"] != true || pod["serviceAccountName"] != "demo-x-oidc-dcr" {
		t.Errorf("identity: %#v", pod)
	}
	if value.Dig(pod, "securityContext.runAsNonRoot") != true || value.Dig(pod, "securityContext.runAsUser") != float64(65534) || value.Dig(c, "securityContext.readOnlyRootFilesystem") != true || value.Dig(c, "securityContext.allowPrivilegeEscalation") != false {
		t.Errorf("security context: %#v %#v", pod["securityContext"], c["securityContext"])
	}
	if c["image"] != "quay.io/example/dcr:0.6.0@"+dcrDigest || c["imagePullPolicy"] != "IfNotPresent" {
		t.Errorf("image: %#v", c["image"])
	}
	if value.Dig(c, "resources.requests.memory") == nil || value.Dig(c, "resources.limits.memory") == nil {
		t.Errorf("resources: %#v", c["resources"])
	}
	if value.Dig(job, "spec.backoffLimit") != float64(6) || value.Dig(job, "spec.ttlSecondsAfterFinished") != float64(30) {
		t.Errorf("job: %#v", job["spec"])
	}
	want := M{"application_type": "web", "client_name": "demo-x-demo", "grant_types": []any{"authorization_code"}, "scope": "profile email", "redirect_uris": []any{"https://x/cb"}}
	if got := jobRequest(t, c); !reflect.DeepEqual(got, want) {
		t.Errorf("request: got %#v, want %#v", got, want)
	}
	env := jobEnv(c)
	if env["SECRET_NAME"] != "demo-x-demo-dcr" || env["MODE"] != "register" || env["PUBLIC"] != "false" || env["DCR_REGISTRATION_URL"] != "https://idp.example.org/register" {
		t.Errorf("env: %#v", env)
	}
	if _, ok := env["REGISTRATION_TOKEN_FILE"]; ok {
		t.Errorf("anonymous, no token: %#v", env)
	}
	if _, ok := env["DISCOVERY_URL"]; ok {
		t.Errorf("no issuer, no wait: %#v", env)
	}
	annotations := value.AsMap(value.Dig(job, "metadata.annotations"))
	for k, v := range map[string]string{"helm.sh/hook": "pre-install,pre-upgrade", "helm.sh/hook-weight": "-5", "argocd.argoproj.io/hook": "PreSync", "argocd.argoproj.io/sync-wave": "-5"} {
		if annotations[k] != v {
			t.Errorf("%s: %#v", k, annotations[k])
		}
	}
	if got := value.Dig(objects["ConfigMap/demo-x-oidc-dcr"], "metadata.annotations"); value.AsMap(got)["helm.sh/hook-weight"] != "-10" {
		t.Errorf("before the Job: %#v", got)
	}
	rules := value.AsList(objects["Role/demo-x-oidc-dcr"]["rules"])
	wantRules := []any{
		M{"apiGroups": []any{""}, "resources": []any{"secrets"}, "verbs": []any{"create"}},
		M{"apiGroups": []any{""}, "resources": []any{"secrets"}, "resourceNames": []any{"demo-x-demo-dcr"}, "verbs": []any{"get", "patch"}},
	}
	if !reflect.DeepEqual(rules, wantRules) {
		t.Errorf("role: %#v", rules)
	}
	removal := objects["Job/demo-x-oidc-dcr-delete"]
	if a := value.AsMap(value.Dig(removal, "metadata.annotations")); a["helm.sh/hook"] != "pre-delete" || a["argocd.argoproj.io/hook"] != "PostDelete" {
		t.Errorf("removal: %#v", a)
	}
	if got := jobEnv(jobContainer(t, removal)); got["MODE"] != "delete" || got["SECRET_NAME"] != "demo-x-demo-dcr" {
		t.Errorf("removal env: %#v", got)
	}
	if got := value.AsList(objects["Role/demo-x-oidc-dcr-delete"]["rules"]); !reflect.DeepEqual(got, []any{M{"apiGroups": []any{""}, "resources": []any{"secrets"}, "resourceNames": []any{"demo-x-demo-dcr"}, "verbs": []any{"get", "delete"}}}) {
		t.Errorf("removal role: %#v", got)
	}
	script := value.Str(value.Dig(objects["ConfigMap/demo-x-oidc-dcr"], "data")) + value.Str(value.AsMap(objects["ConfigMap/demo-x-oidc-dcr"]["data"])["oidc-dcr.sh"])
	if strings.Contains(script, "set -x") || !strings.Contains(script, "PUT") {
		t.Errorf("script: %q", script)
	}
}

// TestDcrScriptLogsNoSecret: nothing the script prints reads an answer of
// the provider, a client secret or a token.
func TestDcrScriptLogsNoSecret(t *testing.T) {
	objects, err := dcrObjects(dcrCtx(), M{"grantTypes": []any{"client_credentials"}})
	if err != nil {
		t.Fatal(err)
	}
	script := value.Str(value.AsMap(objects["ConfigMap/demo-x-oidc-dcr"]["data"])["oidc-dcr.sh"])
	for line := range strings.SplitSeq(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "echo") && (strings.Contains(line, "$body") || strings.Contains(line, "$current") || strings.Contains(line, "$token") || strings.Contains(line, "$REQUEST") || strings.Contains(line, "$initial")) {
			t.Errorf("printed: %s", line)
		}
		if strings.Contains(line, "Authorization") && !strings.Contains(line, `>"$headers"`) {
			t.Errorf("a token outside the header file: %s", line)
		}
	}
}

// TestDcrNames: the objects are named after the release, a named client's
// after the release and its name, cut with room for the suffixes, and so
// is its Secret and client name; the wait for the provider uses its
// discovery URL (one trailing slash of issuerUri dropped).
func TestDcrNames(t *testing.T) {
	ctx := dcrCtx()
	setIssuer(ctx, "https://idp.example.org/realms/data/")
	long := "demo-" + strings.Repeat("x", 48) // the longest release, 53 characters
	for name, tc := range map[string]struct {
		release, client, fullname, secret, clientName string
	}{
		"main":           {"demo-x", "", "demo-x-oidc-dcr", "demo-x-demo-dcr", "demo-x-demo"},
		"named":          {"demo-x", "console", "demo-x-console-oidc-dcr", "demo-x-demo-console-dcr", "demo-x-demo-console"},
		"a long release": {long, "consoles", long + "-co", long + "-demo-consoles-dcr", long + "-demo-consoles"},
	} {
		t.Run(name, func(t *testing.T) {
			value.AsMap(ctx["release"])["name"] = tc.release
			objects, err := dcrObjects(ctx, M{"grantTypes": []any{"client_credentials"}, "name": tc.client})
			if err != nil {
				t.Fatal(err)
			}
			job := objects["Job/"+tc.fullname]
			if job == nil || objects["Role/"+tc.fullname] == nil || objects["Job/"+tc.fullname+"-delete"] == nil {
				t.Fatalf("objects: %v", sortedKeys(objects))
			}
			c := jobContainer(t, job)
			if env := jobEnv(c); env["SECRET_NAME"] != tc.secret || env["DISCOVERY_URL"] != "https://idp.example.org/realms/data/.well-known/openid-configuration" || env["WAIT_TTL_SECONDS"] != "600" {
				t.Errorf("env: %#v", env)
			}
			if got := jobRequest(t, c)["client_name"]; got != tc.clientName {
				t.Errorf("client name %#v", got)
			}
			for k := range objects {
				if n := k[strings.Index(k, "/")+1:]; len(n) > 63 {
					t.Errorf("%s is longer than a DNS label", k)
				}
			}
		})
	}
}

// sortedKeys: the keys of objects, sorted, for the messages.
func sortedKeys(objects map[string]M) []string {
	return slices.Sorted(maps.Keys(objects))
}

// TestDcrManyClients: any number of clients, a Job each.
func TestDcrManyClients(t *testing.T) {
	var clients []M
	for i := range 7 {
		clients = append(clients, M{"grantTypes": []any{"client_credentials"}, "name": fmt.Sprintf("c%d", i)})
	}
	objects, err := dcrObjects(dcrCtx(), clients...)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		if objects[fmt.Sprintf("Job/demo-x-c%d-oidc-dcr", i)] == nil {
			t.Errorf("client %d: %v", i, sortedKeys(objects))
		}
	}
}

// TestDcrArgs: a public client with every name given, a scope without
// openid, the CA Secret given and a request override.
func TestDcrArgs(t *testing.T) {
	objects, err := dcrObjects(dcrCtx(), M{
		"grantTypes": []any{"authorization_code"},
		"public":     true,
		"scope":      "openid groups",
		"clientName": "console",
		"secret":     "demo-x-oidc",
		"caSecret":   "ca",
		"request":    M{"application_type": "native"},
	})
	if err != nil {
		t.Fatal(err)
	}
	job := objects["Job/demo-x-oidc-dcr"]
	c := jobContainer(t, job)
	req := jobRequest(t, c)
	if req["token_endpoint_auth_method"] != "none" || req["scope"] != "groups" || req["client_name"] != "console" || req["application_type"] != "native" {
		t.Errorf("request: %#v", req)
	}
	if _, ok := req["redirect_uris"]; ok {
		t.Errorf("no redirect URI: %#v", req)
	}
	env := jobEnv(c)
	if env["PUBLIC"] != "true" || env["SECRET_NAME"] != "demo-x-oidc" {
		t.Errorf("env: %#v", env)
	}
	found := false
	for _, v := range value.AsList(value.Dig(job, "spec.template.spec.volumes")) {
		found = found || value.Dig(value.AsMap(v), "secret.secretName") == "ca"
	}
	if !found {
		t.Errorf("volumes: %#v", value.Dig(job, "spec.template.spec.volumes"))
	}
	// The scope is the platform's when the client has none; public: false
	// is a confidential client; an empty redirectUris is left out.
	objects, err = dcrObjects(dcrCtx(), M{"grantTypes": []any{"client_credentials"}, "public": false, "redirectUris": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	c = jobContainer(t, objects["Job/demo-x-oidc-dcr"])
	req = jobRequest(t, c)
	if req["scope"] != "profile email" || jobEnv(c)["PUBLIC"] != "false" {
		t.Errorf("defaults: %#v", req)
	}
	if _, ok := req["redirect_uris"]; ok {
		t.Errorf("an empty redirectUris is left out: %#v", req)
	}
}

// TestDcrPlatform: the platform's DCR settings (platform.oidc.dcr): the Job
// TTL and retries, the CA Secret (the client's caSecret wins), the
// initial access token, the wait for the provider and the image.
func TestDcrPlatform(t *testing.T) {
	ctx := dcrCtx(M{"ttlSeconds": float64(300), "backoffLimit": float64(2), "caSecret": "corp-ca", "waitTtlSeconds": float64(120), "authMethod": "initialAccessToken",
		"image": M{"registry": "mirror.example.org", "pullPolicy": "Always", "pullSecrets": []any{"regcred"}}})
	setIssuer(ctx, "https://idp.example.org/realms/data")
	grants := []any{"client_credentials"}
	objects, err := dcrObjects(ctx, M{"grantTypes": grants})
	if err != nil {
		t.Fatal(err)
	}
	job := objects["Job/demo-x-oidc-dcr"]
	c := jobContainer(t, job)
	if value.Dig(job, "spec.ttlSecondsAfterFinished") != float64(300) || value.Dig(job, "spec.backoffLimit") != float64(2) {
		t.Errorf("job: %#v", job["spec"])
	}
	env := jobEnv(c)
	if env["DISCOVERY_URL"] != "https://idp.example.org/realms/data/.well-known/openid-configuration" || env["WAIT_TTL_SECONDS"] != "120" || env["REGISTRATION_TOKEN_FILE"] != "/etc/okdp/token/token" {
		t.Errorf("env: %#v", env)
	}
	if c["image"] != "mirror.example.org/example/dcr:0.6.0@"+dcrDigest || c["imagePullPolicy"] != "Always" {
		t.Errorf("image: %#v %#v", c["image"], c["imagePullPolicy"])
	}
	if got := value.Dig(job, "spec.template.spec.imagePullSecrets"); !reflect.DeepEqual(got, []any{M{"name": "regcred"}}) {
		t.Errorf("pull secrets: %#v", got)
	}
	secrets := map[string]bool{}
	for _, v := range value.AsList(value.Dig(job, "spec.template.spec.volumes")) {
		if s := value.Str(value.Dig(value.AsMap(v), "secret.secretName")); s != "" {
			secrets[s] = true
		}
	}
	if !secrets["corp-ca"] || !secrets["okdp-dcr-token"] {
		t.Errorf("volumes: %v", secrets)
	}
	// The removal needs no initial access token.
	for _, v := range value.AsList(value.Dig(objects["Job/demo-x-oidc-dcr-delete"], "spec.template.spec.volumes")) {
		if value.Dig(value.AsMap(v), "secret.secretName") == "okdp-dcr-token" {
			t.Errorf("the removal mounts the initial access token")
		}
	}
	// No image in the platform values: the default, by digest.
	ctx = dcrCtx()
	delete(value.AsMap(value.Dig(ctx, "platform.oidc.dcr")), "image")
	if objects, err = dcrObjects(ctx, M{"grantTypes": grants}); err != nil {
		t.Fatal(err)
	} else if got := jobContainer(t, objects["Job/demo-x-oidc-dcr"])["image"]; got != "quay.io/adaltas/oidc-dcr-job:0.6.0@"+dcrDigest {
		t.Errorf("default image: %#v", got)
	}
	if _, err := dcrObjects(dcrCtx(M{"image": M{"tag": "1.0.0"}}), M{"grantTypes": grants}); err == nil || !strings.Contains(err.Error(), "tag 1.0.0 without a digest") {
		t.Errorf("a tag without a digest: %v", err)
	}
	objects, err = dcrObjects(dcrCtx(M{"image": M{"repository": "m/j", "tag": "1.0.0", "digest": "sha256:ab"}}), M{"grantTypes": grants})
	if err != nil {
		t.Fatal(err)
	}
	if got := jobContainer(t, objects["Job/demo-x-oidc-dcr"])["image"]; got != "quay.io/m/j:1.0.0@sha256:ab" {
		t.Errorf("image: %#v", got)
	}
}

// TestDcrModes: outside dcr mode no object is compiled, but the clients are
// still checked; the Secret of each client in both modes.
func TestDcrModes(t *testing.T) {
	ctx := dcrCtx()
	value.AsMap(value.Dig(ctx, "platform.oidc.dcr"))["enabled"] = false
	objects, err := dcrObjects(ctx, M{"grantTypes": []any{"client_credentials"}}, M{"name": "console", "grantTypes": []any{"authorization_code"}})
	if err != nil || len(objects) != 0 {
		t.Errorf("existing mode: %v %#v", err, objects)
	}
	if _, err := dcrObjects(ctx, M{}, M{}); err == nil || !strings.Contains(err.Error(), "has no name either") {
		t.Errorf("existing mode, checked: %v", err)
	}
	// No client: nothing to check on the platform.
	value.AsMap(value.Dig(ctx, "platform.oidc.dcr"))["enabled"] = true
	value.AsMap(value.Dig(ctx, "platform.oidc.dcr"))["authMethod"] = "bearer"
	if objects, err := dcrObjects(ctx); err != nil || len(objects) != 0 {
		t.Errorf("no client: %v %#v", err, objects)
	}
	for _, tc := range []struct {
		dcr        bool
		name, want string
	}{
		{true, "", "demo-x-demo-dcr"},
		{true, "console", "demo-x-demo-console-dcr"},
		{false, "", "creds-demo-x-oauth2"},
		{false, "console", "creds-demo-x-oauth2-console"},
	} {
		ctx := dcrCtx()
		value.AsMap(value.Dig(ctx, "platform.oidc.dcr"))["enabled"] = tc.dcr
		res, _, err := runMapping(context.Background(), testrules.Standard(), newSession(ctx), ctx, mapping("", "x: {s: '${{ oidcClientSecret(\""+tc.name+"\") }}'}\n")...)
		if err != nil {
			t.Fatal(err)
		}
		if got := value.Dig(res, "output.x.s"); got != tc.want {
			t.Errorf("oidcClientSecret(%q), dcr %v: got %#v, want %q", tc.name, tc.dcr, got, tc.want)
		}
	}
}

// TestDcrErrors: the platform must register anonymously at a registration
// URL (dcr mode), the clients must be told apart and a client's secret be
// the release's, <release>-<client name> exactly (every mode); their
// form is mapping.schema.json's (TestMappingErrors). An error of the
// template names the client, then the template's file.
func TestDcrErrors(t *testing.T) {
	grants := []any{"client_credentials"}
	for name, tc := range map[string]struct {
		dcr     M // the platform's settings, over dcrCtx's
		clients []M
		want    string
	}{
		"auth method":      {M{"authMethod": "bearer"}, []M{{"grantTypes": grants}}, `global.okdp.oidc.dcr.authMethod "bearer" is not supported`},
		"registration url": {M{"registrationUrl": ""}, []M{{"grantTypes": grants}}, "global.okdp.oidc.dcr.registrationUrl is required"},
		"same name":        {nil, []M{{"grantTypes": grants, "name": "a"}, {"grantTypes": grants, "name": "a"}}, "oidcClients[1] (a): oidcClients[0] (a) has the same name: client names must be unique"},
		"same secret":      {nil, []M{{"grantTypes": grants, "secret": "demo-x-oidc"}, {"grantTypes": grants, "name": "oidc", "secret": "demo-x-oidc"}}, `oidcClients[1] (oidc): oidcClients[0] writes the same Secret, "demo-x-oidc": give each client its own secret`},
		"another Secret":   {nil, []M{{"grantTypes": grants, "name": "b", "secret": "demo-db-credentials"}}, `oidcClients[0] (b): secret "demo-db-credentials": the Secret the client is written to is the release's, "demo-x-b" (<release>-<client name>, <release>-oidc for the main client)`},
		"another's prefix": {nil, []M{{"grantTypes": grants, "name": "b", "secret": "demo-x-b-credentials"}}, `oidcClients[0] (b): secret "demo-x-b-credentials": the Secret the client is written to is the release's, "demo-x-b"`},
		"another mode":     {M{"enabled": false}, []M{{"grantTypes": grants, "secret": "demo-x"}}, `oidcClients[0]: secret "demo-x": the Secret the client is written to is the release's, "demo-x-oidc"`},
		"two main clients": {nil, []M{{"grantTypes": grants}, {"grantTypes": grants, "name": ""}}, "oidcClients[1]: oidcClients[0] has no name either: only the service's main client (chart oidc-dcr) has none"},
		"template error":   {nil, []M{{"grantTypes": grants, "scope": float64(1)}}, "oidcClients[0]: oidc-dcr.cel.yaml: vars scope: ${{ first("},
		"request widens":   {nil, []M{{"grantTypes": grants, "request": M{"redirect_uris": []any{"https://evil/cb"}}}}, "oidcClients[0]: request.redirect_uris: a URL the provider trusts is not set through request: use redirectUris"},
		"request logout":   {nil, []M{{"grantTypes": grants, "request": M{"post_logout_redirect_uris": []any{"https://evil/"}}}}, "oidcClients[0]: request.post_logout_redirect_uris"},
		"request keys":     {nil, []M{{"grantTypes": grants, "request": M{"jwks_uri": "https://evil/jwks"}}}, "oidcClients[0]: request.jwks_uri"},
		"existing mode":    {M{"enabled": false}, []M{{"grantTypes": grants, "request": M{"redirect_uris": []any{"https://evil/cb"}}}}, "request.redirect_uris"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := dcrCtx()
			maps.Copy(value.AsMap(value.Dig(ctx, "platform.oidc.dcr")), tc.dcr)
			_, err := dcrObjects(ctx, tc.clients...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestDcrRedirectHosts: a project that is not privileged registers redirect
// URIs only on hosts it serves, the rule of its Ingresses; a privileged
// project any; outside dcr mode nothing is registered, nothing checked.
func TestDcrRedirectHosts(t *testing.T) {
	check := func(ctx M, tenant bool, uris ...any) error {
		inst := dcrInstance(ctx, tenant)
		inst.okdp["policy"] = value.Merge(value.AsMap(inst.okdp["policy"]), M{"ingressHosts": M{"demo": []any{"listed.example.net"}}})
		_, err := ownObjects(context.Background(), newSession(ctx), inst, M{"oidcClients": []any{M{"grantTypes": []any{"authorization_code"}, "redirectUris": uris}}})
		return err
	}
	for _, ok := range []string{"https://app.demo.example.org/cb", "https://app.demo.example.org:8443/a/*", "https://LISTED.example.net/cb", "http://app.demo.example.org/cb"} {
		if err := check(dcrCtx(), true, ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for uri, want := range map[string]string{
		"https://keycloak.example.org/cb":          "is not served by demo",
		"https://a.b.demo.example.org/cb":          "is not served by demo",
		"https://app.other.example.org/cb":         "is not served by demo",
		"https://*.demo.example.org/cb":            "is not served by demo",
		"https://app.demo.example.org@evil.net/cb": "no user information",
		"myapp://callback":                         "an http or https URL",
		"/relative":                                "an http or https URL",
		"https://evil.net\\@app.demo.example.org/": "an http or https URL",
	} {
		err := check(dcrCtx(), true, uri)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "oidcClients[0]: redirectUris[0]") {
			t.Errorf("%s: got %v, want an error containing %q", uri, err, want)
		}
	}
	if err := check(dcrCtx(), false, "https://keycloak.example.org/cb"); err != nil {
		t.Errorf("a privileged project: %v", err)
	}
	ctx := dcrCtx()
	value.AsMap(value.Dig(ctx, "platform.oidc.dcr"))["enabled"] = false
	if err := check(ctx, true, "https://keycloak.example.org/cb"); err != nil {
		t.Errorf("existing mode: %v", err)
	}
}

// TestDcrCompile: a mapping's oidcClients compiled with the service: in dcr
// mode, the objects of each client among the instance's, under the policy
// over them, and no chart; the chart of the objects is the compiler's:
// Chart.yaml and values.cel.yaml may not list it.
func TestDcrCompile(t *testing.T) {
	platform := M{"global": M{"okdp": M{"oidc": M{"clientProvisioning": "dcr", "dcr": M{"authMethod": "anonymous", "registrationUrl": "https://idp/register"}}}}}
	okdpYAML := `
vars:
  host: app.${{ release.namespace }}.${{ global.okdp.ingress.suffix }}
oidcClients:
  - redirectUris: ['https://${{ host }}/callback']
    grantTypes: [authorization_code]
  - $if: ${{ values.replicas > 1 }}
    name: console
    public: true
    grantTypes: [authorization_code]
`
	valuesYAML := `
app:
  labels:
    main: ${{ oidcClientSecret('') }}
    console: ${{ oidcClientSecret('console') }}
%s
`
	compile := func(output string, existing bool, deps []chart.Dependency, values M) (*Result, error) {
		d := *def
		d.Okdp, d.Values = []byte(okdpYAML), []byte(strings.Replace(valuesYAML, "%s", output, 1))
		d.Chart.Dependencies = deps
		p := value.AsMap(value.Copy(platform))
		if existing {
			value.Dig(p, "global.okdp.oidc").(M)["clientProvisioning"] = "existing"
		}
		return Compile(context.Background(), &d, inputs(p, values))
	}
	app := []chart.Dependency{appDep}
	jobs := func(res *Result) []string {
		var out []string
		for _, o := range res.Objects {
			if o["kind"] == "Job" {
				out = append(out, value.Str(value.Dig(o, "metadata.name")))
			}
		}
		return out
	}
	for name, tc := range map[string]struct {
		values   M
		existing bool
		jobs     []string
	}{
		"dcr mode":          {M{"replicas": 2}, false, []string{"demo-app-oidc-dcr", "demo-app-oidc-dcr-delete", "demo-app-console-oidc-dcr", "demo-app-console-oidc-dcr-delete"}},
		"a client left out": {M{"replicas": 1}, false, []string{"demo-app-oidc-dcr", "demo-app-oidc-dcr-delete"}},
		"existing mode":     {M{"replicas": 2}, true, nil},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := compile("", tc.existing, app, tc.values)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(jobs(res), tc.jobs) {
				t.Errorf("jobs: got %v, want %v", jobs(res), tc.jobs)
			}
			if want := map[string]chart.Ref{"app": appSource}; !reflect.DeepEqual(res.Charts, want) || len(res.Values) != 1 {
				t.Errorf("charts: got %v", res.Charts)
			}
			wantLabels := M{"main": "demo-app-demo-dcr", "console": "demo-app-demo-console-dcr"}
			if tc.existing {
				wantLabels = M{"main": "creds-demo-app-oauth2", "console": "creds-demo-app-oauth2-console"}
			}
			if got := res.Values["app"]["labels"]; !reflect.DeepEqual(got, wantLabels) {
				t.Errorf("Secrets: got %#v, want %#v", got, wantLabels)
			}
		})
	}
	for name, tc := range map[string]struct {
		output string
		deps   []chart.Dependency
		want   string
	}{
		"in values":     {"okdp-objects: {}", app, "values.cel.yaml: okdp-objects: the chart of the instance's objects is compiled from the objects section: remove it"},
		"in Chart.yaml": {"", []chart.Dependency{appDep, {Name: "okdp-objects", Repository: "https://charts.example.org", Version: "1.0.0"}}, "Chart.yaml: dependencies[1] (okdp-objects): the chart of the instance's objects is added by the compiler: remove it"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := compile(tc.output, false, tc.deps, M{"replicas": 2})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
