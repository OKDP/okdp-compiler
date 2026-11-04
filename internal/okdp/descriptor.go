// Descriptor: a contract as the compile reads it (readContract), for a
// caller that shows it: its fields in the order of the file, each what
// the compile reads of it.

package okdp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

// ContractDescriptor: a contract of Inputs.Contracts, read and checked as
// every compile reads it (ReadContract).
type ContractDescriptor struct {
	Name        string          // the contract's, its file's name, <name>.schema.json: what a reference and an output name
	Label       string          // x-okdp-contract, the name the messages give it
	Title       string          // title
	Description string          // description
	Fields      []ContractField // its properties, in the order of the file
	Claim       map[string]any  // x-okdp-claim, its claim schema as written, nil when it takes no request
	Schema      map[string]any  // the document, as written: the keywords the compile does not read (x-okdp-icon, x-okdp-external...)
	Required    []string        // the fields required after completion, the secret ones left out, sorted
}

// ContractField: a field of a contract, a property of its schema.
type ContractField struct {
	Name      string         // its key in properties
	Secret    bool           // x-okdp-secret: in the credentials Secret, never in a connection nor an output
	Required  bool           // required after completion, a secret field never
	Default   any            // default, nil when none
	Derived   map[string]any // x-okdp-derived, {from, map}, nil when none
	Condition map[string]any // x-ui-condition, {field, value}: when its default applies, nil when always
	Property  map[string]any // its schema, as written: its type, title, enum, bounds, x-ui-* hints
}

// ReadContract: the contract name of contracts, read and checked as a
// compile reads it, the error a compile gives when it cannot.
func ReadContract(contracts fs.FS, name string) (*ContractDescriptor, error) {
	c, err := readContract(contracts, name)
	if err != nil {
		return nil, err
	}
	b, err := fs.ReadFile(contracts, name+".schema.json")
	if err != nil {
		return nil, err
	}
	order, err := propertyOrder(b)
	if err != nil {
		return nil, fmt.Errorf("the %s contract cannot be read: %s.schema.json: %w", name, name, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	d := &ContractDescriptor{Name: name, Label: c.label, Title: value.Str(doc["title"]), Description: value.Str(doc["description"]), Schema: doc, Required: slices.Clone(c.required)}
	if claim, ok := doc["x-okdp-claim"].(map[string]any); ok {
		d.Claim = claim
	}
	for _, k := range order {
		p := value.AsMap(c.props[k])
		raw, _ := value.AsMap(doc["properties"])[k].(map[string]any)
		f := ContractField{Name: k, Secret: c.secret[k], Required: slices.Contains(c.required, k), Default: p["default"], Property: raw}
		if v, ok := raw["x-okdp-derived"].(map[string]any); ok {
			f.Derived = v
		}
		if v, ok := raw["x-ui-condition"].(map[string]any); ok {
			f.Condition = v
		}
		d.Fields = append(d.Fields, f)
	}
	return d, nil
}

// Field: the field name of the contract, nil when it has none.
func (d *ContractDescriptor) Field(name string) *ContractField {
	for i := range d.Fields {
		if d.Fields[i].Name == name {
			return &d.Fields[i]
		}
	}
	return nil
}

// ReadContracts: every contract of contracts, <name>.schema.json at its
// root, by name (ReadContract); every one that cannot be read, at once.
func ReadContracts(contracts fs.FS) ([]ContractDescriptor, error) {
	entries, err := fs.ReadDir(contracts, ".")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var out []ContractDescriptor
	var errs []error
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".schema.json")
		if e.IsDir() || !ok {
			continue
		}
		d, err := ReadContract(contracts, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, *d)
	}
	return out, errors.Join(errs...)
}

// propertyOrder: the keys of the properties of the JSON object b, in the
// order of the file.
func propertyOrder(b []byte) ([]string, error) {
	var doc struct {
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || len(doc.Properties) == 0 {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(doc.Properties))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("properties: an object")
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}
