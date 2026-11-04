// Package treeedit: the reads and the edits of a tree as written
// (compiler.LoadRepository reads it), for a caller that edits the tree,
// a control plane, and keeps it where it likes (Git): every rule of an
// edit is here, the caller holds the files.
//
// A tree is read through a Reader. The readers give its parts: the
// instances (ReadInstance, ListInstances), the connections, the named
// claims, the projects, the catalogs of CatalogsFile and the console's
// menu, and, of an output a target wrote, what an instance publishes
// (ListPublished) and its compiled files (ReadCompiled). An edit
// (CreateInstance, PutConnection, DeleteProject...) is an Edit: pure, it
// reads the tree and returns its Change, the files to write and to
// remove, and the Check of the change by the compile of the tree before
// and after it (Check.Verify: the instances it writes compile, those
// that compiled still do, a removal of what is in use refused, a new
// release that is another's refused). Apply runs an edit on a Writer, a
// transaction, with the caller's compile of the tree.
//
// The files are edited in place: an instance's instance.yaml, a values
// file, a project's description, an entry of connections or claims, a
// document merged key by key, each its head (a licence), its comments
// and its other keys kept, so that a hand-written file survives an edit
// of the console. They are written as every file of a tree: yaml v3,
// two-space indentation (the yq v4 defaults).
package treeedit

import (
	"bytes"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/repository"
)

// InstanceSpec: an instance's instance.yaml, as written: its package,
// catalog/<service>/<version>/, and its claimers, the namespaces whose
// instances may claim of it from another namespace (namespace names, or
// "*"), nil when absent (its package's then), empty for none
// (claimers: []).
type InstanceSpec = repository.InstanceSpec

// ParseInstance: an instance.yaml as the compile reads it, strictly: a
// key it does not know refused; claimers nil when absent, empty for
// "claimers: []". Its checks (service and version required, the
// claimers) are the compile's.
func ParseInstance(raw []byte) (InstanceSpec, error) { return repository.ParseInstance(raw) }

// EncodeInstance: the instance.yaml of spec: service, version, and its
// claimers when not nil, "claimers: []" for none.
func EncodeInstance(spec InstanceSpec) ([]byte, error) {
	doc := struct {
		Service  string    `yaml:"service"`
		Version  string    `yaml:"version"`
		Claimers *[]string `yaml:"claimers,omitempty"`
	}{Service: spec.Service, Version: spec.Version}
	if spec.Claimers != nil {
		doc.Claimers = &spec.Claimers
	}
	return MarshalYAML(doc)
}

// MarshalYAML: v as every file of a tree is written: yaml v3, two-space
// indentation.
func MarshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeValues: a values file of values, "{}" for none.
func EncodeValues(values map[string]any) ([]byte, error) {
	if values == nil {
		values = map[string]any{}
	}
	return MarshalYAML(values)
}

// DecodeValues: a values file as the compile reads it
// (compiler.ParseValues: its first document, every number a float64,
// string keys), JSON-compatible, the shape a JSON schema validator and a
// REST API expect; {} for an empty file or a null document.
func DecodeValues(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	values, err := chart.ParseValues(raw)
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = map[string]any{}
	}
	return values, nil
}

// RoundTrip: values as a file holds them, written (EncodeValues) and read
// back (DecodeValues): the types DecodeValues gives, for a comparison
// with values read from a file; values as they are when they cannot be
// written.
func RoundTrip(values map[string]any) map[string]any {
	data, err := EncodeValues(values)
	if err != nil {
		return values
	}
	out, err := DecodeValues(data)
	if err != nil {
		return values
	}
	return out
}

// ReadDescription: a project's description, the key
// compiler.ProjectDescription of its project.yaml, raw: the project's
// own, which the compile leaves out of the context of its instances. A
// file that is not YAML has none: the compile reports it.
func ReadDescription(raw []byte) string {
	values, _ := DecodeValues(raw)
	d, _ := values[repository.ProjectDescription].(string)
	return strings.TrimSpace(d)
}

// WriteDescription: raw, a project's project.yaml, with description, on
// one line, as its key compiler.ProjectDescription, first when new, the
// rest of the file kept; an empty description removes the key. An empty
// file is {}. file names it in messages.
func WriteDescription(raw []byte, file, description string) ([]byte, error) {
	var value any
	if description = strings.Join(strings.Fields(description), " "); description != "" {
		value = str(description)
	}
	return EditKey(raw, file, repository.ProjectDescription, value)
}

// MergeDocument: updated written over the YAML document raw, top-level
// key by top-level key: the keys whose value did not change keep their
// nodes, and the comments of the file (its licence, the notes a person
// left) survive; the keys updated has not are removed, those it adds
// written last, in name order. A document that is not a mapping, or does
// not parse, is replaced.
func MergeDocument(raw []byte, updated map[string]any) ([]byte, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(raw)) == 0 || yaml.Unmarshal(raw, &doc) != nil ||
		len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return MarshalYAML(updated)
	}
	root := doc.Content[0]
	original, err := DecodeValues(raw)
	if err != nil {
		return MarshalYAML(updated)
	}
	kept := root.Content[:0]
	seen := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		newValue, present := updated[key.Value]
		if !present {
			continue
		}
		seen[key.Value] = true
		if !reflect.DeepEqual(RoundTrip(map[string]any{"v": original[key.Value]}), RoundTrip(map[string]any{"v": newValue})) {
			var replacement yaml.Node
			if err := replacement.Encode(newValue); err != nil {
				return nil, err
			}
			replacement.HeadComment, replacement.LineComment, replacement.FootComment = value.HeadComment, value.LineComment, value.FootComment
			value = &replacement
		}
		kept = append(kept, key, value)
	}
	root.Content = kept
	var added []string
	for k := range updated {
		if !seen[k] {
			added = append(added, k)
		}
	}
	slices.Sort(added)
	for _, k := range added {
		var value yaml.Node
		if err := value.Encode(updated[k]); err != nil {
			return nil, err
		}
		root.Content = append(root.Content, str(k), &value)
	}
	return MarshalYAML(&doc)
}
