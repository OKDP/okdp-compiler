// Package merge computes the final values of a compile. It merges OKDP's
// defaults, what the mapping computed and the instance's upstream values,
// as Helm merges -f files. When a chart cache is given, it checks the
// result against the upstream charts.
package merge

import (
	"io/fs"
	"reflect"
	"sync"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

type (
	M             = value.M
	Ref           = chart.Ref
	Spec          = chart.Spec
	Specs         = chart.Specs
	Schema        = chart.Schema
	UpstreamCheck = chart.UpstreamCheck
)

// Caches holds the chart caches of a set of compiles, one per file system,
// so that each chart is read once. It is safe for concurrent use.
type Caches struct {
	mu     sync.Mutex
	caches []*ChartCache
}

// Get returns the cache of fsys, created on first use, nil for a nil fsys.
func (c *Caches) Get(fsys fs.FS) *ChartCache {
	if fsys == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.caches {
		if sameFS(x.FS(), fsys) {
			return x
		}
	}
	x := NewChartCache(fsys)
	c.caches = append(c.caches, x)
	return x
}

// sameFS reports whether a and b are one file system: equal values of one
// type, or the same map (fstest.MapFS). Uncomparable values never are.
func sameFS(a, b fs.FS) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	switch {
	case va.Type() != vb.Type():
		return false
	case va.Kind() == reflect.Map:
		return va.UnsafePointer() == vb.UnsafePointer()
	}
	return va.Comparable() && vb.Comparable() && va.Equal(vb)
}
