package okdp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

// PlatformObjectNames: the names of the objects the platform writes in the
// namespace of every project besides its instances' (the targets'): the
// Role and the NetworkPolicy okdp-project, the control plane's Role and
// RoleBinding, the RoleBindings of the project's groups, and the secret
// store's defaults: the ServiceAccount and the DCR token's Secret. The
// names the platform's settings give the store, its ServiceAccount and the
// token are added to them (platformObjects). The targets' own constants
// are checked against them (output.TestPlatformObjectNames).
var PlatformObjectNames = []string{
	"okdp-project", "okdp-control-plane",
	"okdp-access-viewer", "okdp-access-editor", "okdp-access-admin",
	"okdp-secrets", "okdp-dcr-token",
}

// platformObjects: PlatformObjectNames and the names global.okdp gives the
// secret store (secrets.store, secrets.serviceAccount) and the DCR token's
// Secret (oidc.dcr.tokenSecret), sorted, once each.
func platformObjects(okdp M) []string {
	names := slices.Clone(PlatformObjectNames)
	for _, path := range []string{"secrets.store", "secrets.serviceAccount", "oidc.dcr.tokenSecret"} {
		if n := value.Str(value.Dig(okdp, path)); n != "" {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// checkRelease: nil when release is none of the platform's objects, nor
// the prefix of one up to a "-": a package names its objects after its
// release, <release> or <release>-<x>, and one named so would take the
// platform's place in the namespace (the project okdp's instance project,
// release okdp-project, writing the Role okdp-project that confines every
// release of the namespace). okdp is the platform's settings.
func checkRelease(field, release string, okdp M) error {
	for _, name := range platformObjects(okdp) {
		if name == release || strings.HasPrefix(name, release+"-") {
			return fmt.Errorf("%s %q: the name of an object the platform writes in every namespace (%s), or its beginning up to a \"-\": the objects of a release are named <release> and <release>-<x>, and would take its place in the namespace", field, release, name)
		}
	}
	return nil
}
