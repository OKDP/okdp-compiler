// Memo: what the compiles of a set of instances read once.

package okdp

import (
	"context"
	"io/fs"
	"reflect"
	"sync"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// setMemo holds what the compiles of the instances of one CompileAll
// read once: the parameters of each instance, and the contracts that
// several instances read from the same file system. It is safe for
// concurrent use.
type setMemo struct {
	mu        sync.Mutex
	params    map[int]*paramsMemo
	contracts []*contractCache
}

func newSetMemo() *setMemo { return &setMemo{params: map[int]*paramsMemo{}} }

// instanceMemo is the part of a setMemo that one compile of an instance
// reads (Inputs.memo). A nil field shares nothing.
type instanceMemo struct {
	params    *paramsMemo
	contracts *contractCache
}

// forInstance returns the memo for the compile of instance i of the set.
func (m *setMemo) forInstance(i int, in Inputs) *instanceMemo {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.params[i]
	if p == nil {
		p = &paramsMemo{}
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
	return out
}

// sameFS reports whether a and b are the same file system: equal values
// of one type, or the same map. Two values that cannot be compared are
// not the same.
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

// paramsMemo holds the parameters of one instance. The first compile
// that asks computes them.
type paramsMemo struct {
	once             sync.Once
	schema           *chart.Schema
	values, settings M
	err              error
}

// get returns parameters(ctx, files, in, svc), computed once. The schema
// is shared and its readers never change it; the values and the settings
// are copies, because the compile changes them. A nil p computes them at
// each call. The computation ignores the cancellation of the compile
// that happens to ask first, so that another compile of the instance
// reads the same result.
func (p *paramsMemo) get(ctx context.Context, files fs.FS, in Inputs, svc *Service) (*chart.Schema, M, M, error) {
	if p == nil {
		return parameters(ctx, files, in, svc)
	}
	p.once.Do(func() {
		p.schema, p.values, p.settings, p.err = parameters(context.WithoutCancel(ctx), files, in, svc)
	})
	if p.err != nil {
		return nil, nil, nil, p.err
	}
	return p.schema, value.AsMap(value.Copy(p.values)), value.AsMap(value.Copy(p.settings)), nil
}

// contractCache reads each contract of a file system once. The contract
// is shared and its readers never change it. Safe for concurrent use.
type contractCache struct {
	fsys fs.FS
	mu   sync.Mutex
	read map[string]contractRead
}

type contractRead struct {
	c   *contract
	err error
}

func newContractCache(fsys fs.FS) *contractCache {
	return &contractCache{fsys: fsys, read: map[string]contractRead{}}
}

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

// contractCacheOf returns the memo's contract cache when the compile has
// one, else a new one.
func contractCacheOf(in Inputs) *contractCache {
	if in.memo != nil {
		return in.memo.contracts
	}
	return newContractCache(in.Contracts)
}

func (m *instanceMemo) paramsOf() *paramsMemo {
	if m == nil {
		return nil
	}
	return m.params
}
