// Policy: the platform's policy, global.okdp.policy: which projects are
// the platform's own (privileged).

package okdp

import (
	"slices"

	"github.com/okdp/okdp-compiler/internal/value"
)

// privileged: whether namespace is one of global.okdp.policy.privileged,
// the platform's own projects.
func privileged(okdp M, namespace string) bool {
	return slices.ContainsFunc(value.AsList(value.Dig(okdp, "policy.privileged")), func(v any) bool { return value.Str(v) == namespace })
}
