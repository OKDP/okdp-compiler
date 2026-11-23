// Memo: what the compiles of a set of instances read once.

package okdp

import (
	"io/fs"
	"reflect"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// setMemo holds what the compiles of the instances of one CompileAll
// read once: the parameters of each instance, and the contracts that
// several instances read from the same file system. CompileAll compiles
// its instances one after the other, and no Inputs that a caller sees
// holds a memo, so nothing reads it concurrently.
type setMemo struct {
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

// paramsMemo holds the parameters of one instance and its settings, of
// its values layers and its host's settings (Inputs.Values,
// Inputs.Settings), which no compile of the set changes. The first
// compile that asks computes them.
type paramsMemo struct {
	done             bool
	schema           *chart.Schema
	values, settings M
	err              error
}

// get returns parameters(files, in, svc), computed once. The schema is
// shared and its readers never change it; the values and the settings
// are copies, because the compile changes them. A nil p computes them at
// each call.
func (p *paramsMemo) get(files fs.FS, in Inputs, svc *Service) (*chart.Schema, M, M, error) {
	if p == nil {
		return parameters(files, in, svc)
	}
	if !p.done {
		p.schema, p.values, p.settings, p.err = parameters(files, in, svc)
		p.done = true
	}
	if p.err != nil {
		return nil, nil, nil, p.err
	}
	return p.schema, value.AsMap(value.Copy(p.values)), value.AsMap(value.Copy(p.settings)), nil
}

// contractCache reads each contract of a file system once. The contract
// is shared and its readers never change it. A cache is of one compile,
// or of the instances of one CompileAll (setMemo).
type contractCache struct {
	fsys fs.FS
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
