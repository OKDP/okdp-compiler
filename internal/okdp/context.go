// Context: what a mapping sees. It loads the values layers, the platform's
// then the instance's (valuesLayers, chart.Parameters), checks the platform
// requirements, derives the platform context from global.okdp (its settings
// checked against their types), resolves the connection references
// (connections.go), and reads the instance's upstream values
// (chart.ReadUpstream).
package okdp

import (
	"fmt"
	"io/fs"
	"maps"
	"sort"
	"strings"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Context: what a mapping sees, the merged values, the instance's upstream
// values of each chart that accepts them (applied by chart.FinalValues), and
// the instances the connection references point to. files: the package.
func buildContext(files fs.FS, in Inputs, svc *Service) (M, M, chart.Upstreams, []Dependency, error) {
	// 1. values layers, as Helm: the platform's then the instance's (global
	// is the platform's: valuesLayers), merged in order as -f files,
	// coalesced over the package's defaults, checked against the parameter
	// schema.
	layers, err := valuesLayers(files, in)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	values, err := chart.Parameters(files, svc.Name, layers)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	global := value.AsMap(values["global"])
	okdpG := value.AsMap(global["okdp"])
	// 2. platform requirements.
	for _, k := range svc.Require {
		if !value.Truthy(value.Dig(okdpG, k)) {
			return nil, nil, nil, nil, fmt.Errorf("global.okdp.%s is required. It is a platform value: set it in platform/platform-values.yaml", k)
		}
	}
	// 3. platform context, from the settings checked against their types.
	if err := checkPlatform(values["global"]); err != nil {
		return nil, nil, nil, nil, err
	}
	// OIDC: defaults, then the client provisioning mode (existing or dcr).
	oidc := value.AsMap(value.Copy(okdpG["oidc"]))
	if _, ok := oidc["enabled"]; !ok {
		oidc["enabled"] = true
	}
	if !value.Truthy(oidc["scope"]) {
		oidc["scope"] = "openid profile email groups"
	}
	if !value.Truthy(oidc["clientProvisioning"]) {
		oidc["clientProvisioning"] = "existing"
	}
	mode := value.Str(oidc["clientProvisioning"])
	if mode != "existing" && mode != "dcr" {
		return nil, nil, nil, nil, fmt.Errorf("global.okdp.oidc.clientProvisioning %q is not one of existing, dcr", mode)
	}
	// The settings of the oidc-dcr Jobs, defaults applied (oidc.go).
	dcr := value.AsMap(value.Copy(oidc["dcr"]))
	if err := dcrSettings(dcr); err != nil {
		return nil, nil, nil, nil, err
	}
	dcr["enabled"] = value.Truthy(oidc["enabled"]) && mode == "dcr"
	oidc["dcr"] = dcr
	oidc["existing"] = value.Truthy(oidc["enabled"]) && mode == "existing"
	// The Secret that holds the service's main OAuth client: provisioned, or
	// registered by DCR (oidc.go; oidcClientSecret(name) for the others).
	mainSecret := clientSecret(in.Release, in.Namespace, dcr["enabled"] == true, "")
	// Proxy settings, as environment variables.
	proxy := value.AsMap(okdpG["proxy"])
	proxyEnv := M{}
	for k, env := range map[string]string{"httpProxy": "HTTP_PROXY", "httpsProxy": "HTTPS_PROXY", "noProxy": "NO_PROXY"} {
		if value.Truthy(proxy[k]) {
			proxyEnv[env] = proxy[k]
		}
	}
	// What the compiler derives from global.okdp.
	platform := M{
		"oidc":               oidc,
		"oidcClientSecret":   mainSecret,
		"ingressAnnotations": M{"cert-manager.io/cluster-issuer": value.Str(value.Dig(okdpG, "certificateIssuers.selfSigned.name"))},
		"proxyEnv":           proxyEnv,
	}
	// 4. connection references, resolved in place (x-okdp-connection-ref);
	// every reference that does not resolve is reported.
	schema, err := readJSON(files, "values.schema.json")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// The mapping gets a copy: the merged values stay as the user wrote them.
	params := value.AsMap(value.Copy(values))
	var deps []Dependency
	r := resolver{in: in, conns: value.AsMap(values["connections"]), suffix: value.Str(value.Dig(okdpG, "ingress.suffix")), deps: &deps}
	if err := r.resolveAll(schema, params); err != nil {
		return nil, nil, nil, nil, err
	}
	// Not parameters: global is exposed apart, connections are resolved.
	delete(params, "global")
	delete(params, "connections")
	// 5. the instance's upstream values: values.upstream.<chart> is what
	// will be applied to the chart, its files merged with its inline values
	// (Upstreams.Show).
	ups := chart.ReadUpstream(in.Instance, svc.Charts, values)
	ups.Show(params, svc.Charts)
	ctx := M{
		"release":  M{"name": in.Release, "namespace": in.Namespace},
		"values":   params,
		"global":   M{"okdp": value.Copy(okdpG)}, // platform-values.yaml, as written
		"platform": platform,                     // derived from it by the compiler
	}
	// The package: service (name, version, appVersion), labels and
	// selectorLabels.
	maps.Copy(ctx, serviceContext(svc.meta(), in.Release))
	// By parameter path, in string order, as the errors of the references.
	sort.Slice(deps, func(i, j int) bool { return deps[i].Field < deps[j].Field })
	return ctx, values, ups, deps, nil
}

// platformSettings: the global.okdp settings the compiler reads, by dotted
// path, and their type. charts is checked by platformCharts, the keys of
// oidc.dcr by dcrSettings.
var platformSettings = []struct {
	path string
	want string         // the type, as the error names it
	is   func(any) bool // whether a value is of that type
}{
	{"ingress.suffix", "a string", is[string]},
	{"certificateIssuers.selfSigned.name", "a string", is[string]},
	{"oidc.enabled", "true or false", is[bool]},
	{"oidc.clientProvisioning", "a string", is[string]},
	{"oidc.scope", "a string", is[string]},
	{"oidc.dcr", "a map", is[M]},
	{"oidc.issuerUri", "a string", is[string]},
	{"proxy.httpProxy", "a string", is[string]},
	{"proxy.httpsProxy", "a string", is[string]},
	{"proxy.noProxy", "a string", is[string]},
}

// is: whether v is a T.
func is[T any](v any) bool {
	_, ok := v.(T)
	return ok
}

// checkPlatform: global, the platform values: each setting the compiler
// reads (platformSettings) of its type, and global, global.okdp and each
// map on the way to a setting a map, when set (null is not set). A value of
// another type would read as not set: global.okdp.oidc: false would leave
// OIDC enabled. The first violation is reported, in the order of
// platformSettings.
func checkPlatform(global any) error {
	for _, s := range platformSettings {
		v, at := global, "global"
		for seg := range strings.SplitSeq("okdp."+s.path, ".") {
			if v == nil {
				break
			}
			m, ok := v.(M)
			if !ok {
				return fmt.Errorf("%s must be a map, got %s", at, value.Short(v))
			}
			v, at = m[seg], at+"."+seg
		}
		if v != nil && !s.is(v) {
			return fmt.Errorf("%s must be %s, got %s", at, s.want, value.Short(v))
		}
	}
	return nil
}
