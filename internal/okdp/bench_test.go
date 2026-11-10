package okdp

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkCompileAll: a namespace of 20 instances that each write objects,
// with upstream values and hosts for the policy to walk: what the rules
// cost beside the mapping.
func BenchmarkCompileAll(b *testing.B) {
	d := *nodeDef
	d.Okdp = append([]byte(nil), nodeDef.Okdp...)
	d.Okdp = append(d.Okdp, []byte(`
objects:
  - $for: i in [1, 2, 3, 4, 5, 6, 7, 8]
    $each:
      apiVersion: v1
      kind: ConfigMap
      metadata: {name: '${{ release.name + "-" + string(i) }}'}
      data: {a: b}
`)...)
	instances := make([]Instance, 20)
	for i := range instances {
		inst := node(fmt.Sprintf("n%d", i), M{})
		inst.Definition = &d
		instances[i] = inst
	}
	b.ResetTimer()
	for b.Loop() {
		for _, o := range CompileAll(context.Background(), instances) {
			if o.Err != nil {
				b.Fatal(o.Err)
			}
		}
	}
}
