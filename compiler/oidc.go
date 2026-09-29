// OIDC: the platform's settings of dynamic client registration (DCR), the
// oidc-dcr Jobs that register the services' OAuth clients when the platform
// provisions them by DCR (global.okdp.oidc.clientProvisioning: dcr).
package compiler

import (
	"fmt"
	"math"
)

// dcrDefaults: the settings of global.okdp.oidc.dcr a platform may leave
// out, and their defaults. waitTtlSeconds has none: the chart's own
// waitForProvider.ttlSeconds (600 s) applies.
var dcrDefaults = []struct {
	key string
	def any
}{
	{"ttlSeconds", float64(30)},  // TTL of the finished Job (ttlSeconds), its logs kept that long
	{"backoffLimit", float64(6)}, // retries of the Job (backoffLimit)
	{"waitTtlSeconds", nil},      // longest wait for the provider (waitForProvider.ttlSeconds)
}

// dcrSettings: global.okdp.oidc.dcr, checked, with its defaults applied in
// place: caSecret (the Secret holding the CA bundle the Jobs trust,
// certs-bundle), ttlSeconds (30), backoffLimit (6), waitTtlSeconds (unset);
// image, the Jobs' image, a map whose snake_case keys of oidc-dcr < 0.5.0
// are renamed (dcrImage). registrationUrl and authMethod are left as
// written: they are checked when a client is registered.
func dcrSettings(dcr M) error {
	switch img := dcr["image"].(type) {
	case nil:
		delete(dcr, "image")
	case M:
		dcr["image"] = dcrImage(img)
	default:
		return fmt.Errorf("global.okdp.oidc.dcr.image must be a map (registry, repository, tag, pullPolicy, pullSecrets), got %s", short(img))
	}
	switch ca := dcr["caSecret"].(type) {
	case nil:
		dcr["caSecret"] = "certs-bundle"
	case string:
		if ca == "" {
			dcr["caSecret"] = "certs-bundle"
		}
	default:
		return fmt.Errorf("global.okdp.oidc.dcr.caSecret must be the name of a Secret, got %s", short(ca))
	}
	for _, d := range dcrDefaults {
		v := dcr[d.key]
		if v == nil {
			delete(dcr, d.key)
			if d.def != nil {
				dcr[d.key] = d.def
			}
			continue
		}
		if n, ok := v.(float64); !ok || n < 0 || n != math.Trunc(n) {
			return fmt.Errorf("global.okdp.oidc.dcr.%s must be a whole number >= 0, got %s", d.key, short(v))
		}
	}
	return nil
}

// dcrImage: the platform's image of the oidc-dcr Jobs, with the snake_case
// keys of oidc-dcr < 0.5.0 renamed; a camelCase key wins over its former
// spelling.
func dcrImage(img M) M {
	out := asMap(deepCopy(img))
	for old, key := range map[string]string{"pull_policy": "pullPolicy", "pull_secrets": "pullSecrets"} {
		if v, ok := out[old]; ok {
			if _, set := out[key]; !set {
				out[key] = v
			}
			delete(out, old)
		}
	}
	return out
}
