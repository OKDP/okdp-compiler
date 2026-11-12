// Package merge is Helm's merge of the final values of a compile: OKDP's
// defaults, what the mapping computed and the instance's upstream values, as
// Helm merges -f files, checked against the upstream charts when a chart
// cache is given.
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

// Caches: the chart caches of a set of compiles, one per file system, each
// chart read once for all of them. Safe for concurrent use.
type Caches struct {
	mu     sync.Mutex
	caches []*ChartCache
}

// Get: the cache of fsys, nil without one.
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

// sameFS: a and b are the same file system: of one type and equal, or the
// same map (a fstest.MapFS). Two that cannot be compared are not.
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
