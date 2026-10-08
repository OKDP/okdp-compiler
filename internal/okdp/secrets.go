// Secrets: the Secrets OKDP generates for an instance: Helm must render the
// same output on every run (Argo CD renders with `helm template`), so the random values
// come from External Secrets Operator Password generators, in the cluster.
package okdp

import (
	"fmt"
	"math"
	"strings"

	"github.com/okdp/okdp-compiler/internal/helmfn"
	"github.com/okdp/okdp-compiler/internal/value"
)

// secretArgs: the arguments of okdpGeneratedSecret; stringData holds
// numbers and booleans too, written as text.
var secretArgs = []field{
	{name: "name", kind: aString, required: true},
	{name: "keys", kind: aList},
	{name: "stringData", kind: scalarMap},
	{name: "labels", kind: stringMap},
	{name: "annotations", kind: stringMap},
	{name: "type", kind: aString},
	{name: "namespace", kind: aString},
	{name: "refreshInterval", kind: aString},
	{name: "apiVersion", kind: aString},
}

// keyFields: the fields of a generated key, an item of okdpGeneratedSecret's
// keys or of a provides secret's generate. length, digits, symbols and
// noUpper are checked as printed (password), the transform and a missing
// key by generatedSecret, with Helm's messages.
var keyFields = []field{
	{name: "key", kind: aString},
	{name: "length", kind: anyValue},
	{name: "digits", kind: anyValue},
	{name: "symbols", kind: anyValue},
	{name: "noUpper", kind: anyValue},
	{name: "transform", kind: anyValue},
}

// generatedSecret: okdpGeneratedSecret, the objects of a generated Secret
// for the instance inst: one generators.external-secrets.io/v1alpha1
// Password per key, then the ExternalSecret writing the Secret `name`.
//
// args: name (required), keys (list of {key (required), length (32),
// digits (6), symbols (0), noUpper (false), transform (sha256 or b64enc)}),
// stringData (static keys; a value containing "{{" is written as a template
// string literal), labels (extra labels of the ExternalSecret and the
// Secret), annotations (of every object), type (of the Secret), namespace
// (the release namespace), refreshInterval ("0": generated once),
// apiVersion (of the ExternalSecret, external-secrets.io/v1). They are
// checked first (secretArgs, a null one being not set), each key as its
// Password renders it (password).
//
// A generator is named <name>-<key> (lowercased, "_" and "." as "-"),
// longer than 63 characters cut to 54 and suffixed with 8 hex characters of
// the key's SHA-256; two keys giving the same name fail.
func generatedSecret(inst *instance, args M) ([]any, error) {
	if err := fields("okdp.generatedSecret", args, secretArgs); err != nil {
		return nil, err
	}
	name := value.Str(args["name"])
	ns := value.Str(helmfn.Default(args["namespace"], inst.namespace))
	api := value.Str(helmfn.Default(args["apiVersion"], "external-secrets.io/v1"))
	extra := value.AsMap(args["labels"])
	annotations := value.AsMap(args["annotations"])
	okdpLabels := labels(inst.svc, inst.release, "")
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
		s := helmfn.ToString(v)
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
			gen = helmfn.TruncName(gen, 54) + "-" + helmfn.Sha256Sum(key)[:8]
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
		switch t := helmfn.ToString(helmfn.Default(spec["transform"], "")); t {
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
		tpl["type"] = helmfn.Printed(t)
	}
	out = append(out, M{
		"apiVersion": api,
		"kind":       "ExternalSecret",
		"metadata":   meta(name, esLabels),
		"spec": M{
			"refreshInterval": helmfn.ToString(helmfn.Default(args["refreshInterval"], "0")),
			"target":          M{"name": name, "creationPolicy": "Owner", "template": tpl},
			"dataFrom":        dataFrom,
		},
	})
	return out, nil
}

// password: the spec of the Password generating the key x, at where, as
// okdp.generatedSecret renders it, checked: x a map of keyFields; length
// (32), digits (6) and symbols (0), their default applied as Helm's, whole
// numbers >= 0 as printed into the spec ('32' is 32); noUpper (false) true
// or false as printed ('true' is true).
func password(where string, x any) (M, error) {
	spec, err := fieldMap(where, x, keyFields)
	if err != nil {
		return nil, err
	}
	if err := fields(where, spec, keyFields); err != nil {
		return nil, err
	}
	// digits is printed when given, 0 or null included (hasKey, not default).
	digits := any(float64(6))
	if d, ok := spec["digits"]; ok {
		digits = helmfn.Printed(d)
	}
	pw := M{
		"length":      helmfn.Printed(helmfn.Default(spec["length"], float64(32))),
		"digits":      digits,
		"symbols":     helmfn.Printed(helmfn.Default(spec["symbols"], float64(0))),
		"noUpper":     helmfn.Printed(helmfn.Default(spec["noUpper"], false)),
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
// okdp.io/credentials-<contract>, in the namespace of the instance inst.
func credentialsSecret(inst *instance, contract, output, name string, secret M) ([]any, error) {
	label := "okdp.io/credentials-" + contract
	if value.Truthy(secret["generate"]) {
		return generatedSecret(inst, M{
			"name":       name,
			"keys":       secret["generate"],
			"stringData": helmfn.Default(secret["stringData"], M{}),
			"labels":     M{label: "true"},
		})
	}
	if !value.Truthy(secret["stringData"]) {
		return nil, fmt.Errorf("%s: output %q: secret needs stringData or generate", inst.svc.Name, output)
	}
	l := labels(inst.svc, inst.release, "")
	l[label] = "true"
	return []any{M{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   M{"name": name, "namespace": inst.namespace, "labels": l},
		"type":       "Opaque",
		"stringData": value.Copy(secret["stringData"]),
	}}, nil
}
