// Secrets: the Secrets OKDP generates for an instance: Helm must render the
// same output on every run (Argo CD renders with `helm template`), so the random values
// come from External Secrets Operator Password generators, in the cluster.
package okdp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/okdp/okdp-compiler/internal/value"
)

// secretArgs: the arguments of okdpGeneratedSecret.
var secretArgs = []string{"name", "keys", "stringData", "labels", "annotations", "type", "namespace", "refreshInterval", "apiVersion"}

// keyFields: the fields of a generated key, an item of okdpGeneratedSecret's
// keys or of a provides secret's generate.
var keyFields = []string{"key", "length", "digits", "symbols", "noUpper", "transform"}

// generatedSecret: okdpGeneratedSecret, the objects okdp.generatedSecret
// renders: one generators.external-secrets.io/v1alpha1 Password per key,
// then the ExternalSecret writing the Secret `name`.
//
// args: name (required), keys (list of {key (required), length (32),
// digits (6), symbols (0), noUpper (false), transform (sha256 or b64enc)}),
// stringData (static keys; a value containing "{{" is written as a template
// string literal), labels (extra labels of the ExternalSecret and the
// Secret), annotations (of every object), type (of the Secret), namespace
// (the release namespace), refreshInterval ("0": generated once),
// apiVersion (of the ExternalSecret, external-secrets.io/v1). They are
// checked first (checkSecretArgs), each key as its Password renders it
// (password).
//
// A generator is named <name>-<key> (lowercased, "_" and "." as "-"),
// longer than 63 characters cut to 54 and suffixed with 8 hex characters of
// the key's SHA-256; two keys giving the same name fail.
func generatedSecret(ctx M, args M) ([]any, error) {
	if err := checkSecretArgs(args); err != nil {
		return nil, err
	}
	name := value.Str(args["name"])
	rel := value.AsMap(ctx["release"])
	ns := value.Str(orDefault(args["namespace"], rel["namespace"]))
	api := value.Str(orDefault(args["apiVersion"], "external-secrets.io/v1"))
	extra := value.AsMap(args["labels"])
	annotations := value.AsMap(args["annotations"])
	okdpLabels := labels(ctxService(ctx), value.Str(rel["name"]), "")
	meta := func(name string, labels M) M {
		m := M{"name": name, "namespace": ns, "labels": labels}
		if len(annotations) > 0 {
			m["annotations"] = value.Copy(annotations)
		}
		return m
	}
	// The Secret's data: the static keys, then the generated ones.
	template := M{}
	for k, v := range value.AsMap(args["stringData"]) {
		s := toString(v)
		if strings.Contains(s, "{{") {
			s = fmt.Sprintf("{{ %q }}", s)
		}
		template[k] = s
	}
	var out, dataFrom []any
	gens := map[string]string{}
	for i, x := range value.AsList(args["keys"]) {
		pw, err := password(fmt.Sprintf("okdp.generatedSecret: keys[%d]", i), x)
		if err != nil {
			return nil, err
		}
		spec := value.AsMap(x)
		key := value.Str(spec["key"])
		if key == "" {
			return nil, fmt.Errorf("okdp.generatedSecret: every key needs a name")
		}
		gen := name + "-" + strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(key))
		if len(gen) > 63 {
			gen = strings.TrimSuffix(trunc(gen, 54), "-") + "-" + sha256Hex(key)[:8]
		}
		if other, dup := gens[gen]; dup {
			return nil, fmt.Errorf("okdpGeneratedSecret: %s: keys %q and %q give the same generator name %q", name, other, key, gen)
		}
		gens[gen] = key
		out = append(out, M{
			"apiVersion": "generators.external-secrets.io/v1alpha1",
			"kind":       "Password",
			"metadata":   meta(gen, value.Copy(okdpLabels).(M)),
			"spec":       pw,
		})
		dataFrom = append(dataFrom, M{
			"sourceRef": M{"generatorRef": M{"apiVersion": "generators.external-secrets.io/v1alpha1", "kind": "Password", "name": gen}},
			"rewrite":   []any{M{"regexp": M{"source": "^password$", "target": key}}},
		})
		switch t := toString(orDefault(spec["transform"], "")); t {
		case "sha256":
			template[key] = fmt.Sprintf("{{ index . %q | sha256sum }}", key)
		case "b64enc":
			template[key] = fmt.Sprintf("{{ index . %q | b64enc }}", key)
		case "":
			template[key] = fmt.Sprintf("{{ index . %q }}", key)
		default:
			return nil, fmt.Errorf("okdp.generatedSecret: unknown transform %q (supported: sha256, b64enc)", t)
		}
	}
	if dataFrom == nil {
		dataFrom = []any{}
	}
	// The ExternalSecret, and the Secret it writes, carry the extra labels.
	esLabels := value.Merge(value.Copy(okdpLabels).(M), extra)
	tpl := M{
		"engineVersion": "v2",
		"metadata":      M{"labels": value.Copy(esLabels)},
		"data":          template,
	}
	if t := args["type"]; value.Truthy(t) {
		tpl["type"] = printed(t)
	}
	out = append(out, M{
		"apiVersion": api,
		"kind":       "ExternalSecret",
		"metadata":   meta(name, esLabels),
		"spec": M{
			"refreshInterval": toString(orDefault(args["refreshInterval"], "0")),
			"target":          M{"name": name, "creationPolicy": "Owner", "template": tpl},
			"dataFrom":        dataFrom,
		},
	})
	return out, nil
}

// checkSecretArgs: the arguments of okdpGeneratedSecret, checked, a null
// one being not set: no other argument; name required, a string; keys a
// list; stringData a map of strings, numbers and booleans, written as text;
// labels and annotations maps of strings; type, namespace, refreshInterval
// and apiVersion strings.
func checkSecretArgs(args M) error {
	const where = "okdp.generatedSecret"
	var unknown []string
	for k, x := range args {
		if x != nil && !slices.Contains(secretArgs, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("%s: unknown argument(s) %s (allowed: %s)", where, strings.Join(unknown, ", "), strings.Join(secretArgs, ", "))
	}
	if n := args["name"]; n == nil || n == "" {
		return fmt.Errorf("%s: name is required", where)
	}
	for _, k := range []string{"name", "type", "namespace", "refreshInterval", "apiVersion"} {
		if _, ok := args[k].(string); !ok && args[k] != nil {
			return fmt.Errorf("%s: %s must be a string, got %s", where, k, value.Short(args[k]))
		}
	}
	if _, ok := args["keys"].([]any); !ok && args["keys"] != nil {
		return fmt.Errorf("%s: keys must be a list of generated keys, got %s", where, value.Short(args["keys"]))
	}
	if err := checkTextMap(where, "stringData", args["stringData"], true); err != nil {
		return err
	}
	for _, k := range []string{"labels", "annotations"} {
		if err := checkTextMap(where, k, args[k], false); err != nil {
			return err
		}
	}
	return nil
}

// checkTextMap: v, the map key at where, checked: a map (null is none) of
// strings; of strings, numbers and booleans when scalars (a generated
// Secret's stringData, written as text).
func checkTextMap(where, key string, v any, scalars bool) error {
	want, item := "strings", "a string"
	if scalars {
		want, item = "strings, numbers or booleans", "a string, a number or a boolean"
	}
	m, ok := v.(M)
	if !ok && v != nil {
		return fmt.Errorf("%s: %s must be a map of %s, got %s", where, key, want, value.Short(v))
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		switch m[k].(type) {
		case string:
			continue
		case float64, bool:
			if scalars {
				continue
			}
		}
		return fmt.Errorf("%s: %s %q must be %s, got %s", where, key, k, item, value.Short(m[k]))
	}
	return nil
}

// password: the spec of the Password generating the key x, at where, as
// okdp.generatedSecret renders it, checked: x a map of keyFields (another
// field is refused unless null); key a string when set; length (32),
// digits (6) and symbols (0), their default applied as Helm's, whole
// numbers >= 0 as printed into the spec ('32' is 32); noUpper (false) true
// or false as printed ('true' is true). A missing key and the transform
// are checked by generatedSecret.
func password(where string, x any) (M, error) {
	spec, ok := x.(M)
	if !ok {
		return nil, fmt.Errorf("%s: must be a map (%s), got %s", where, strings.Join(keyFields, ", "), value.Short(x))
	}
	var unknown []string
	for k, v := range spec {
		if v != nil && !slices.Contains(keyFields, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("%s: unknown field(s) %s (allowed: %s)", where, strings.Join(unknown, ", "), strings.Join(keyFields, ", "))
	}
	if _, ok := spec["key"].(string); !ok && spec["key"] != nil {
		return nil, fmt.Errorf("%s: key must be a string, got %s", where, value.Short(spec["key"]))
	}
	// digits is printed when given, 0 or null included (hasKey, not default).
	digits := any(float64(6))
	if d, ok := spec["digits"]; ok {
		digits = printed(d)
	}
	pw := M{
		"length":      printed(orDefault(spec["length"], float64(32))),
		"digits":      digits,
		"symbols":     printed(orDefault(spec["symbols"], float64(0))),
		"noUpper":     printed(orDefault(spec["noUpper"], false)),
		"allowRepeat": true,
	}
	for _, k := range []string{"length", "digits", "symbols"} {
		if n, ok := pw[k].(float64); !ok || n < 0 || n != math.Trunc(n) {
			return nil, fmt.Errorf("%s: %s must be a whole number >= 0, got %s", where, k, value.Short(spec[k]))
		}
	}
	if _, ok := pw["noUpper"].(bool); !ok {
		return nil, fmt.Errorf("%s: noUpper must be true or false, got %s", where, value.Short(spec["noUpper"]))
	}
	return pw, nil
}

// credentialsSecret: the credentials Secret of an output published with a
// `secret`: {generate: [keys], stringData} as a
// generated Secret, {stringData} as a plain Secret; both labelled
// okdp.io/credentials-<contract>.
func credentialsSecret(ctx M, contract, output, name string, secret M) ([]any, error) {
	label := "okdp.io/credentials-" + contract
	if value.Truthy(secret["generate"]) {
		return generatedSecret(ctx, M{
			"name":       name,
			"keys":       secret["generate"],
			"stringData": orDefault(secret["stringData"], M{}),
			"labels":     M{label: "true"},
		})
	}
	if !value.Truthy(secret["stringData"]) {
		return nil, fmt.Errorf("%s: output %q: secret needs stringData or generate", ctxService(ctx).Name, output)
	}
	rel := value.AsMap(ctx["release"])
	l := labels(ctxService(ctx), value.Str(rel["name"]), "")
	l[label] = "true"
	return []any{M{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   M{"name": name, "namespace": rel["namespace"], "labels": l},
		"type":       "Opaque",
		"stringData": value.Copy(secret["stringData"]),
	}}, nil
}

// orDefault: sprig's default: v, or d when v is empty (null, "", 0, false,
// an empty list or map).
func orDefault(v, d any) any {
	if value.Truthy(v) {
		return v
	}
	return d
}

// toString: sprig's toString (a null is "<nil>").
func toString(v any) string {
	if v == nil {
		return "<nil>"
	}
	return value.Str(v)
}

// printed: v printed into a template ({{ v }}) and read back as YAML.
func printed(v any) any {
	var out any
	if err := sigsyaml.Unmarshal([]byte(value.Str(v)), &out); err != nil {
		return value.Str(v)
	}
	return out
}

// sha256Hex: Helm's sha256sum, the hex SHA-256 of the text.
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
