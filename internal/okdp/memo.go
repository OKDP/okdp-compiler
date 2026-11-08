// Memo: the inputs a compile reads that do not change while the compiles
// of a set of instances run, read once for all of them: an instance's
// parameters (its schema, the layers merged and checked), a contract
// (read, decoded and compiled), an upstream chart (read, its schema
// compiled).

package okdp

import (
	"io/fs"
	"reflect"
	"sync"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Memo: what the compiles of the instances of one set read once
// (CompileAllWith): per instance, its parameters, and for the instances
// that read the same contracts and the same chart cache, those. It is
// valid for the same instances, whose files and layers do not change, and
// for the contracts and charts file systems not to change: one run of
// compiles, not kept beyond it. Safe for concurrent use.
type Memo struct {
	mu        sync.Mutex
	params    map[int]*paramsMemo
	contracts []*contractCache
	charts    []*chart.ChartCache
}

// NewMemo: an empty Memo.
func NewMemo() *Memo { return &Memo{params: map[int]*paramsMemo{}} }

// instanceMemo: what one compile of an instance reads of its Memo
// (Inputs.memo); nil fields, nothing shared.
type instanceMemo struct {
	params    *paramsMemo
	contracts *contractCache
	charts    *chart.ChartCache
}

// forInstance: the memo of the compile of instance i of the set, def its
// package, in its inputs. The parameters of i are those of the instance
// at i when def is the one they were read from; else read again.
func (m *Memo) forInstance(i int, def *Definition, in Inputs) *instanceMemo {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.params[i]
	if p == nil || p.def != def {
		p = &paramsMemo{def: def}
		m.params[i] = p
	}
	out := &instanceMemo{params: p}
	for _, c := range m.contracts {
		if sameFS(c.fsys, in.Contracts) {
			out.contracts = c
			break
		}
	}
	if out.contracts == nil {
		out.contracts = newContractCache(in.Contracts)
		m.contracts = append(m.contracts, out.contracts)
	}
	if in.Charts == nil {
		return out
	}
	for _, c := range m.charts {
		if sameFS(c.FS(), in.Charts) {
			out.charts = c
			break
		}
	}
	if out.charts == nil {
		out.charts = chart.NewChartCache(in.Charts)
		m.charts = append(m.charts, out.charts)
	}
	return out
}

// sameFS reports whether a and b are the same file system: of one type
// and equal, or the same map (a fstest.MapFS). Two that cannot be compared
// are not.
func sameFS(a, b fs.FS) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	switch {
	case va.Type() != vb.Type():
		return false
	case va.Kind() == reflect.Map:
		return va.UnsafePointer() == vb.UnsafePointer()
	}
	return va.Comparable() && vb.Comparable() && va.Equal(vb)
}

// paramsMemo: the parameters of one instance (parameters), computed by the
// first compile that asks. def: the package they are read from.
type paramsMemo struct {
	def              *Definition
	once             sync.Once
	schema           *chart.Schema
	values, settings M
	err              error
}

// get: parameters(files, in, svc), computed once. The schema is shared,
// never changed by its readers; the values and the settings are copies:
// the compile changes them. A nil p computes them.
func (p *paramsMemo) get(files fs.FS, in Inputs, svc *Service) (*chart.Schema, M, M, error) {
	if p == nil {
		return parameters(files, in, svc)
	}
	p.once.Do(func() { p.schema, p.values, p.settings, p.err = parameters(files, in, svc) })
	if p.err != nil {
		return nil, nil, nil, p.err
	}
	return p.schema, value.AsMap(value.Copy(p.values)), value.AsMap(value.Copy(p.settings)), nil
}

// contractCache: the contracts of a file system (Inputs.Contracts), each read
// once (readContract), whoever asks; the contract is shared, never changed
// by its readers. Safe for concurrent use.
type contractCache struct {
	fsys fs.FS
	mu   sync.Mutex
	read map[string]contractRead
}

// contractRead: a contract read, or why it cannot be.
type contractRead struct {
	c   *contract
	err error
}

// newContractCache: the cache of the contracts of fsys.
func newContractCache(fsys fs.FS) *contractCache {
	return &contractCache{fsys: fsys, read: map[string]contractRead{}}
}

// get: the contract name, readContract's, read by the first call.
func (s *contractCache) get(name string) (*contract, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.read[name]
	if !ok {
		r.c, r.err = readContract(s.fsys, name)
		s.read[name] = r
	}
	return r.c, r.err
}

// contractCacheOf: the contract cache of in, the Memo's when the compile has one.
func contractCacheOf(in Inputs) *contractCache {
	if in.memo != nil {
		return in.memo.contracts
	}
	return newContractCache(in.Contracts)
}

// paramsOf: the parameters of the instance, the Memo's; nil without one.
func (m *instanceMemo) paramsOf() *paramsMemo {
	if m == nil {
		return nil
	}
	return m.params
}
