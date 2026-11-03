package treeedit

import (
	"bytes"
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// str: a string scalar node.
func str(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// node: value as a node: itself when it is one, else encoded.
func node(value any) (*yaml.Node, error) {
	if n, ok := value.(*yaml.Node); ok {
		return n, nil
	}
	n := &yaml.Node{}
	if err := n.Encode(value); err != nil {
		return nil, err
	}
	return n, nil
}

// EditEntry: raw, a YAML file of top-level keys, with the entry name of
// its key key set to value, or removed when value is nil
// (connections.<name>, claims.<name>): its head, its comments and its
// other keys kept, an entry replaced keeping its comments, a key left
// empty removed. value is anything yaml v3 encodes (a compiler.Connection,
// a compiler.ClaimSpec); removing an entry that is not there changes
// nothing. file names the file in messages.
func EditEntry(raw []byte, file, key, name string, value any) ([]byte, error) {
	head, doc, root, err := parseDoc(raw, file)
	if err != nil {
		return nil, err
	}
	at := -1
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			at = i
		}
	}
	if at < 0 || root.Content[at+1].Tag == "!!null" {
		if value == nil {
			return raw, nil
		}
		entries := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if at < 0 {
			root.Style = 0
			root.Content = append(root.Content, str(key), entries)
			at = len(root.Content) - 2
		} else {
			root.Content[at+1] = entries
		}
	}
	entries := root.Content[at+1]
	if entries.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: %s is not a mapping: fix it in Git", file, key)
	}
	i := -1
	for j := 0; j+1 < len(entries.Content); j += 2 {
		if entries.Content[j].Value == name {
			i = j
		}
	}
	switch {
	case value == nil && i < 0:
		return raw, nil
	case value == nil:
		entries.Content = append(entries.Content[:i], entries.Content[i+2:]...)
		if len(entries.Content) == 0 {
			root.Content = append(root.Content[:at], root.Content[at+2:]...)
		}
	default:
		n, err := node(value)
		if err != nil {
			return nil, err
		}
		entries.Style = 0
		if i >= 0 {
			n.HeadComment, n.LineComment, n.FootComment = entries.Content[i+1].HeadComment, entries.Content[i+1].LineComment, entries.Content[i+1].FootComment
			entries.Content[i+1] = n
		} else {
			entries.Content = append(entries.Content, str(name), n)
		}
	}
	return encodeDoc(head, doc)
}

// EditKey: raw, a YAML file of top-level keys, with its key key set to
// value, or removed when value is nil: its head, its comments and its
// other keys kept, a new key first, a key replaced keeping its comments;
// removing a key that is not there changes nothing. An empty body is {}.
// value is anything yaml v3 encodes. file names the file in messages.
func EditKey(raw []byte, file, key string, value any) ([]byte, error) {
	head, doc, root, err := parseDoc(raw, file)
	if err != nil {
		return nil, err
	}
	at := -1
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			at = i
		}
	}
	_, body := splitHead(raw)
	switch {
	case value == nil && at < 0 && len(bytes.TrimSpace(body)) > 0:
		return raw, nil
	case value == nil && at >= 0:
		root.Content = append(root.Content[:at], root.Content[at+2:]...)
	case value != nil:
		n, err := node(value)
		if err != nil {
			return nil, err
		}
		if at >= 0 {
			old := root.Content[at+1]
			n.HeadComment, n.LineComment, n.FootComment = old.HeadComment, old.LineComment, old.FootComment
			root.Content[at+1] = n
		} else {
			root.Style = 0
			root.Content = append([]*yaml.Node{str(key), n}, root.Content...)
		}
	}
	return encodeDoc(head, doc)
}

// splitHead: the head of a YAML file, its leading comment and blank
// lines (a licence), kept as written when the body is edited, and its
// body.
func splitHead(raw []byte) (head, body []byte) {
	i := 0
	for i < len(raw) {
		line, next := raw[i:], len(raw)
		if end := bytes.IndexByte(raw[i:], '\n'); end >= 0 {
			line, next = raw[i:i+end], i+end+1
		}
		if t := bytes.TrimSpace(line); len(t) > 0 && t[0] != '#' {
			break
		}
		i = next
	}
	return raw[:i], raw[i:]
}

// parseDoc: a YAML file, its head (splitHead), kept as written, its
// document and the document's top-level mapping, an empty one when the
// body is empty. file names the file in messages.
func parseDoc(raw []byte, file string) (head []byte, doc, root *yaml.Node, err error) {
	head, body := splitHead(raw)
	doc = &yaml.Node{}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := yaml.Unmarshal(body, doc); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", file, err)
		}
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root = doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, nil, fmt.Errorf("%s is not a YAML mapping: fix it in Git", file)
	}
	return head, doc, root, nil
}

// encodeDoc: doc after head, indented by two spaces.
func encodeDoc(head []byte, doc *yaml.Node) ([]byte, error) {
	out, err := MarshalYAML(doc)
	if err != nil {
		return nil, err
	}
	return append(slices.Clone(head), out...), nil
}
