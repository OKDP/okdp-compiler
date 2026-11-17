// Descriptor: a contract as the compile reads it, for a caller that
// shows it.

package okdp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/value"
)

// ContractDescriptor is a contract as ReadContract reads and checks it.
type ContractDescriptor struct {
	Name        string          // from its file <name>.schema.json; references and outputs use it
	Label       string          // x-okdp-contract, the name the messages give the contract
	Title       string          // title
	Description string          // description
	Fields      []ContractField // the properties, in the order of the file
	Claim       map[string]any  // x-okdp-claim as written, nil when the contract takes no request
	// Schema is the document as written. It holds the keywords the
	// compile does not read (x-okdp-icon, x-okdp-external...).
	Schema   map[string]any
	Required []string // the fields required after completion, sorted, without the secret ones
}

// ContractField is a field of a contract, a property of its schema.
type ContractField struct {
	Name      string         // its key in properties
	Secret    bool           // x-okdp-secret: the field goes in the credentials Secret only
	Required  bool           // after completion; a secret field never is
	Default   any            // default, nil when none
	Derived   map[string]any // x-okdp-derived, {from, map}, nil when none
	Condition map[string]any // x-ui-condition, {field, value}: when the default applies; nil: always
	Property  map[string]any // the field's schema as written, x-ui-* hints included
}

// ReadContract reads and checks the contract name as a compile does.
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

// Field returns the field name of the contract, or nil when it has none.
func (d *ContractDescriptor) Field(name string) *ContractField {
	for i := range d.Fields {
		if d.Fields[i].Name == name {
			return &d.Fields[i]
		}
	}
	return nil
}

// ReadContracts reads the contracts at the root of contracts, in name
// order. It returns those it can read and reports all the others.
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

// propertyOrder returns the property keys of the JSON object b, in file
// order.
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
