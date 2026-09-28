package compiler

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Compare checks a compile result against what the wrapper chart renders
// today: the okdp.io/vendor-values ConfigMaps and the OKDP objects of a
// `helm template` output of the wrapper (rendered with the same instance
// values). It reports each chart and object to w, and fails on any
// difference.
func Compare(def *Definition, in Inputs, res *Result, rendered []byte, w io.Writer) error {
	svc := &def.Service
	cms := map[string]M{}
	objects := map[string]M{} // kind/name -> object
	// Index the rendered objects, and the values ConfigMaps by name.
	dec := yaml.NewDecoder(bytes.NewReader(rendered))
	for {
		var doc any
		if err := dec.Decode(&doc); err != nil {
			break
		}
		d := asMap(normalize(doc))
		md := asMap(d["metadata"])
		objects[str(d["kind"])+"/"+str(md["name"])] = d
		if d["kind"] == "ConfigMap" && asMap(md["labels"])["okdp.io/vendor-values"] != nil {
			var v any
			if err := yaml.Unmarshal([]byte(str(asMap(d["data"])["values.yaml"])), &v); err != nil {
				return err
			}
			cms[str(md["name"])] = asMap(normalize(v))
		}
	}
	// OKDP objects: compared whole.
	bad := 0
	for _, om := range res.Objects {
		key := str(om["kind"]) + "/" + str(asMap(om["metadata"])["name"])
		want, ok := objects[key]
		if !ok {
			fmt.Fprintf(w, "  %-14s DIFF: compiled %s, the wrapper renders none\n", "objects", key)
			bad++
			continue
		}
		if d := diffPaths(asMap(normalize(om)), want, ""); len(d) == 0 {
			fmt.Fprintf(w, "  %-14s identical to %s\n", "objects", key)
		} else {
			bad++
			fmt.Fprintf(w, "  %-14s DIFF vs %s:\n", "objects", key)
			for _, l := range d {
				fmt.Fprintln(w, "      "+l)
			}
		}
	}
	// Final values: compared with the chart's values ConfigMap.
	seen := map[string]bool{}
	charts := make([]string, 0, len(res.Values))
	for c := range res.Values {
		charts = append(charts, c)
	}
	sort.Strings(charts)
	for _, chart := range charts {
		name := svc.Charts[chart].Values
		if name == "" {
			name = in.Release + "-" + chart + "-values"
		}
		name = strings.ReplaceAll(name, "{release}", in.Release)
		seen[name] = true
		want, ok := cms[name]
		if !ok {
			fmt.Fprintf(w, "  %-14s DIFF: compiled, but the wrapper renders no %s\n", chart, name)
			bad++
			continue
		}
		d := diffPaths(res.Values[chart], want, "")
		if len(d) == 0 {
			fmt.Fprintf(w, "  %-14s identical to %s\n", chart, name)
		} else {
			bad++
			fmt.Fprintf(w, "  %-14s DIFF vs %s:\n", chart, name)
			for _, l := range d {
				fmt.Fprintln(w, "      "+l)
			}
		}
	}
	// A values ConfigMap with no compiled chart is a difference too.
	for name := range cms {
		if !seen[name] {
			fmt.Fprintf(w, "  %-14s DIFF: the wrapper renders %s, not compiled\n", "?", name)
			bad++
		}
	}
	if bad > 0 {
		return fmt.Errorf("%d difference(s)", bad)
	}
	return nil
}

// diffPaths: the differences between two values, one line per path.
func diffPaths(a, b any, path string) []string {
	am, aok := a.(M)
	bm, bok := b.(M)
	if aok && bok {
		var out []string
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			x, inA := am[k]
			y, inB := bm[k]
			p := path + "." + k
			switch {
			case !inA:
				out = append(out, fmt.Sprintf("missing %s (want %v)", p, short(y)))
			case !inB:
				out = append(out, fmt.Sprintf("extra   %s = %v", p, short(x)))
			default:
				out = append(out, diffPaths(x, y, p)...)
			}
		}
		return out
	}
	// Lists of the same length: item by item. Otherwise compared whole.
	al, aok := a.([]any)
	bl, bok := b.([]any)
	if aok && bok && len(al) == len(bl) {
		var out []string
		for i := range al {
			out = append(out, diffPaths(al[i], bl[i], fmt.Sprintf("%s[%d]", path, i))...)
		}
		return out
	}
	if fmt.Sprintf("%#v", a) != fmt.Sprintf("%#v", b) {
		return []string{fmt.Sprintf("changed %s: got %v, want %v", path, short(a), short(b))}
	}
	return nil
}

// short: a value for a message, cut at 160 characters.
func short(v any) string {
	s := fmt.Sprintf("%#v", v)
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}
