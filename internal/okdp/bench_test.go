package okdp

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// benchInstances: a namespace of 20 instances that each write 8 objects,
// labelled for the objects hook of testdata/hooks, compiled with rules.
func benchInstances(rules *RuleSet) []Instance {
	d := *nodeDef
	d.Okdp = append([]byte(nil), nodeDef.Okdp...)
	d.Okdp = append(d.Okdp, []byte(`
objects:
  - $for: i in [1, 2, 3, 4, 5, 6, 7, 8]
    $each:
      apiVersion: v1
      kind: ConfigMap
      metadata: {name: '${{ release.name + "-" + string(i) }}', labels: {team: a}}
      data: {a: b}
`)...)
	instances := make([]Instance, 20)
	for i := range instances {
		inst := node(fmt.Sprintf("n%d", i), M{})
		inst.Definition = &d
		inst.Inputs.Rules = rules
		instances[i] = inst
	}
	return instances
}

// benchCompile compiles instances b.N times, each without error.
func benchCompile(b *testing.B, instances []Instance) {
	b.Helper()
	b.ResetTimer()
	for b.Loop() {
		for _, o := range CompileAll(context.Background(), instances) {
			if o.Err != nil {
				b.Fatal(o.Err)
			}
		}
	}
}

// BenchmarkCompileAll: the namespace of benchInstances compiled with the
// mandatory set: what its rules cost beside the mapping, the checks of
// every object among them.
func BenchmarkCompileAll(b *testing.B) {
	benchCompile(b, benchInstances(Mandatory()))
}

// BenchmarkCompileAllHooks: the same namespace with the rule set of
// testdata/hooks added, whose documents the compile runs on every object
// and on the final values of every chart: what hooks cost, as a rule set
// that checks objects (the hosts they serve...) adds them.
func BenchmarkCompileAllHooks(b *testing.B) {
	rules, err := LoadRules(os.DirFS("testdata/hooks"))
	if err != nil {
		b.Fatal(err)
	}
	benchCompile(b, benchInstances(rules))
}
