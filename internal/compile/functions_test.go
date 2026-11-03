package compile

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// bigValue: a map of one top-level key of at least n bytes: entries of a
// number and a text of 1000 characters, as a values file that holds a
// large configuration.
func bigValue(n int) M {
	text := strings.Repeat("x", 1000)
	var items []any
	for size := 0; size < n; size += len(text) {
		items = append(items, M{"a": float64(len(items)), "b": text})
	}
	return M{"big": M{"items": items}}
}

// TestHostCostBytes: a host function pays the bytes of its argument, not
// its entries (hostCost): sha256 of a value of a mebibyte under one key
// costs about a mebibyte, as str, toJson, toYaml and quote do; an
// expression that hashes it 300 times passes 50000000, the default limit
// of okdp-compile, where it once cost under 20000.
func TestHostCostBytes(t *testing.T) {
	const mib = 1 << 20
	scope := M{"values": bigValue(mib)}
	for _, src := range []string{"sha256(values)", "str(values)", "toJson(values)", "toYaml(values)", "quote(values)", "first([values])"} {
		s := NewSession(context.Background(), 1<<40)
		if _, err := s.Eval(scope, nil, mapping("vars:\n  a: ${{ "+src+" }}\n")); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if s.used < mib || s.used > 4*mib {
			t.Errorf("%s: cost %d, want about %d", src, s.used, mib)
		}
	}
	const limit = 50_000_000
	_, err := NewSession(context.Background(), limit).Eval(scope, nil, mapping("vars:\n  n: ${{ lists.range(300).map(i, sha256(values)).size() }}\n"))
	if err == nil || !strings.HasSuffix(err.Error(), fmt.Sprintf("exceed its CEL cost limit (%d)", limit)) {
		t.Errorf("300 hashes of a mebibyte: got %v, want the limit exceeded", err)
	}
}

// TestCosted: a caller's host function, Costed, pays the bytes of its
// argument as the compiler's do: one that reads a long text and returns
// its length exhausts a small limit.
func TestCosted(t *testing.T) {
	fn := Costed(cel.Function("count", cel.Overload("count_dyn", []*cel.Type{cel.DynType}, cel.IntType,
		Unary(func(v any) (int, error) { return len(fmt.Sprint(v)), nil }))), "count_dyn")
	scope := M{"values": M{"big": strings.Repeat("x", 10000)}}
	const limit = 5000
	_, err := NewSession(context.Background(), limit, fn).Eval(scope, nil, mapping("vars:\n  a: ${{ count(values.big) }}\n"))
	if err == nil || !strings.HasSuffix(err.Error(), fmt.Sprintf("exceed its CEL cost limit (%d)", limit)) {
		t.Errorf("got %v, want the limit exceeded", err)
	}
	s := NewSession(context.Background(), limit, fn)
	if _, err := s.Eval(scope, nil, mapping("vars:\n  a: ${{ count('x') }}\n")); err != nil || s.used == 0 || s.used > 10 {
		t.Errorf("a small argument: %v, cost %d", err, s.used)
	}
}

// FuzzHostCost: the bytes byteSize counts of a value, any nesting of the
// values' model (JSON data), are at least the bytes of its keys and texts,
// which any serialisation writes, and at most 8 times its JSON, a number
// counted 8 bytes; hostCost of a call is 1 more than its argument's,
// plus its result's length when a string, for each argument.
func FuzzHostCost(f *testing.F) {
	for _, seed := range []string{
		`null`, `""`, `0`, `true`, `[]`, `{}`,
		`{"a": 1, "b": [true, null, "x"], "c": {"d": {"e": []}}}`,
		`[[[[[[[[[["deep"]]]]]]]]]]`,
		`{"k": {"k": {"k": {"k": "` + strings.Repeat("v", 300) + `"}}}}`,
		`[1e300, -0.5, 12345678901234567890, "é\u0000", {"": ""}]`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		var d any
		if json.Unmarshal([]byte(src), &d) != nil {
			return
		}
		j, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		v := types.DefaultTypeAdapter.NativeToValue(d)
		n := byteSize(v)
		if min := textBytes(d); n < min {
			t.Errorf("%s: byteSize %d, under its %d bytes of keys and texts", j, n, min)
		}
		if max := 8 * uint64(len(j)); n > max {
			t.Errorf("%s: byteSize %d, over 8 times its JSON (%d)", j, n, max)
		}
		if c := *hostCost([]ref.Val{v}, types.String("abc")); c != n+4 {
			t.Errorf("%s: cost %d with a result of 3 bytes, want %d", j, c, n+4)
		}
		if _, ok := v.(types.String); !ok {
			if c := *hostCost([]ref.Val{v, v}, v); c != 2*n+1 {
				t.Errorf("%s: cost %d of two arguments, want %d", j, c, 2*n+1)
			}
		}
	})
}

// textBytes: the bytes of the keys and the texts of d.
func textBytes(d any) uint64 {
	switch t := d.(type) {
	case string:
		return uint64(len(t))
	case M:
		var n uint64
		for k, e := range t {
			n += uint64(len(k)) + textBytes(e)
		}
		return n
	case []any:
		var n uint64
		for _, e := range t {
			n += textBytes(e)
		}
		return n
	}
	return 0
}
