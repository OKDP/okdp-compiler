package okdp

import (
	"context"
	"testing"
)

// TestReleaseNamedLikeAPlatformObject: the mandatory set refuses no
// release, nor a namespace of a deployer engine: the names of the objects
// a host writes in every namespace (okdp-project, its Role), and the
// namespaces of its engines (argocd), are its own set's to refuse. A bare
// compile accepts both.
func TestReleaseNamedLikeAPlatformObject(t *testing.T) {
	in := inputs()
	in.Namespace, in.Release = "argocd", "okdp-project"
	if _, err := Compile(context.Background(), def, in); err != nil {
		t.Errorf("the release okdp-project in argocd: %v", err)
	}
}
