// Package chart is what the compiler reproduces of Helm and needs of the
// upstream charts: values read, merged and coalesced as Helm does
// (values.go), checked against a values.schema.json (schema.go), the chart
// references and the chart cache (ref.go, cache.go), the charts section of
// service.yaml (spec.go, paths.go), a values schema, its defaults and the
// parameters of an instance (parameters.go), the final values of each
// chart key (merge.go). It knows neither the mapping language nor OKDP.
//
// Values: reading YAML as Helm values, and Helm's merge rules.
//
// Values files are read as Helm 4 reads them (loader.LoadValues): each YAML
// document of the file decoded by sigs.k8s.io/yaml (YAML 1.1: y, yes, on...
// are booleans; through JSON: string keys, every number a float64), the
// documents merged in order. The layers are merged as Helm merges its -f
// files (MergeMaps), and coalesced over the chart defaults as Helm does
// (CoalesceValues): a null deletes a default, and is kept where there is no
// default. Helm's own functions are not imported: helm.sh/helm/v4 pulls in
// client-go and a newer Go and Kubernetes than the programs that embed
// this package, for about a hundred lines. They are reproduced from
// helm v4.3.0 (pkg/chart/common/util/coalesce.go, pkg/chart/v2/loader), and
// testdata/helm holds what `helm template` computes on each case.
package chart

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/okdp/okdp-compiler/internal/value"
)

// M: a values map (value.M).
type M = value.M

// readValues: a values file of fsys, read as Helm reads values files.
func readValues(fsys fs.FS, name string) (M, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	v, err := loadValues(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return v, nil
}

// ParseValues: a values file as Helm reads it (YAML 1.1, every document of
// the file merged in order, string keys, every number a float64), for
// Inputs.Values.
func ParseValues(b []byte) (map[string]any, error) {
	return loadValues(b)
}

// loadValues: Helm's loader.LoadValues. The file is split into documents
// as Kubernetes' YAMLReader does (a line starting with "---", followed by
// nothing but a comment), each document is decoded by sigs.k8s.io/yaml into
// a map, and the documents are merged with MergeMaps.
func loadValues(b []byte) (M, error) {
	docs, err := splitDocuments(b)
	if err != nil {
		return nil, fmt.Errorf("error reading yaml document: %w", err)
	}
	values := M{}
	for _, doc := range docs {
		current := M{}
		if err := sigsyaml.Unmarshal(doc, &current); err != nil {
			return nil, fmt.Errorf("cannot unmarshal yaml document: %w", err)
		}
		values = value.Merge(values, current)
	}
	return values, nil
}

// splitDocuments: the documents of a YAML stream, as Kubernetes'
// YAMLReader reads them (lines end with "\n", a "\r\n" is read as "\n").
func splitDocuments(b []byte) ([][]byte, error) {
	var docs [][]byte
	var buf bytes.Buffer
	text := strings.TrimSuffix(string(b), "\n")
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if rest, ok := strings.CutPrefix(line, "---"); ok {
			if t := strings.TrimSpace(rest); t != "" && t[0] != '#' {
				return nil, fmt.Errorf("invalid Yaml document separator: %s", t)
			}
			if buf.Len() > 0 {
				docs = append(docs, bytes.Clone(buf.Bytes()))
				buf.Reset()
			}
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	if buf.Len() > 0 {
		docs = append(docs, buf.Bytes())
	}
	return docs, nil
}

// coalesceValues: Helm's CoalesceValues for a chart without dependencies,
// the values its templates see: user values (the merged -f files) over the
// chart defaults. A user null deletes the default (the key is gone); a null
// where the chart has no default stays; null defaults are dropped (Helm 4).
// When a value and its default are not both maps, the value wins. Neither
// argument is changed.
func coalesceValues(defaults, user M) M {
	v := value.AsMap(value.Copy(user))
	for key, val := range value.AsMap(value.Copy(defaults)) {
		uv, ok := v[key]
		switch {
		case ok && uv == nil:
			delete(v, key)
		case ok:
			dest, isMap := uv.(M)
			src, srcMap := val.(M)
			if isMap && srcMap {
				cleanNil(src)
				coalesceTables(dest, src)
			}
		case val == nil:
		default:
			if sub, isMap := val.(M); isMap {
				cleanNil(sub)
			}
			v[key] = val
		}
	}
	return v
}

// coalesceTables: Helm's coalesceTablesFullKey (merge false): dst (the
// user's) wins over src (the defaults); a null in dst deletes a non-null
// default and is kept otherwise.
func coalesceTables(dst, src M) M {
	nonNil := map[string]bool{}
	for k, v := range src {
		if v != nil {
			nonNil[k] = true
		}
	}
	for k, v := range dst {
		if v == nil {
			src[k] = nil
		}
	}
	for k, val := range src {
		dv, ok := dst[k]
		switch {
		case ok && dv == nil && nonNil[k]:
			delete(dst, k)
		case !ok:
			dst[k] = val
		default:
			sm, srcMap := val.(M)
			dm, dstMap := dv.(M)
			if srcMap && dstMap {
				coalesceTables(dm, sm)
			}
		}
	}
	return dst
}

// cleanNil: Helm's cleanNilValues, every null of m removed, at any depth of
// maps (not in lists).
func cleanNil(m M) M {
	for k, v := range m {
		if v == nil {
			delete(m, k)
		} else if sub, ok := v.(M); ok {
			cleanNil(sub)
		}
	}
	return m
}
