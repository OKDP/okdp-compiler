// Secrets: the Secrets OKDP generates for an instance: Helm must render the
// same output on every run (Argo CD renders with `helm template`), so the random values
// come from External Secrets Operator Password generators, in the cluster.

package compiler

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/okdp/okdp-compiler/internal/helmfn"
	"github.com/okdp/okdp-compiler/internal/value"
)

// generatedSecret: okdpGeneratedSecret, the objects of a generated Secret
// for the instance inst: one generators.external-secrets.io/v1alpha1
// Password per key, then the ExternalSecret writing the Secret `name`.
//
// args, checked against mapping.schema.json (okdpGeneratedSecret): name,
// keys (each the spec of its Password: password), stringData (static keys,
// as text; a value containing "{{" is written as a template string
// literal), labels (extra labels of the ExternalSecret and the Secret),
// annotations (of every object), type (of the Secret), namespace (the
// release namespace), refreshInterval ("0": generated once), apiVersion (of
// the ExternalSecret, external-secrets.io/v1); an empty value takes the
// default, as Helm's default does.
//
// A generator is named <name>-<key> (lowercased, "_" and "." as "-"),
// longer than 63 characters cut to 54 and suffixed with 8 hex characters of
// the key's SHA-256; two keys giving the same name fail.
func generatedSecret(inst *instance, args M) ([]any, error) {
	name := value.Str(args["name"])
	ns := cmp.Or(value.Str(args["namespace"]), inst.namespace)
	api := cmp.Or(value.Str(args["apiVersion"]), "external-secrets.io/v1")
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
		s := value.Str(v)
		if strings.Contains(s, "{{") {
			s = fmt.Sprintf("{{ %q }}", s)
		}
		template[k] = s
	}
	var out, dataFrom []any
	gens := map[string]string{}
	for _, x := range value.AsList(args["keys"]) {
		spec := value.AsMap(x)
		key := value.Str(spec["key"])
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
			"spec":       password(spec),
		})
		dataFrom = append(dataFrom, M{
			"sourceRef": M{"generatorRef": M{"apiVersion": "generators.external-secrets.io/v1alpha1", "kind": "Password", "name": gen}},
			"rewrite":   []any{M{"regexp": M{"source": "^password$", "target": key}}},
		})
		switch spec["transform"] {
		case "sha256":
			template[key] = fmt.Sprintf("{{ index . %q | sha256sum }}", key)
		case "b64enc":
			template[key] = fmt.Sprintf("{{ index . %q | b64enc }}", key)
		default:
			template[key] = fmt.Sprintf("{{ index . %q }}", key)
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
	if t := value.Str(args["type"]); t != "" {
		tpl["type"] = t
	}
	out = append(out, M{
		"apiVersion": api,
		"kind":       "ExternalSecret",
		"metadata":   meta(name, esLabels),
		"spec": M{
			"refreshInterval": cmp.Or(value.Str(args["refreshInterval"]), "0"),
			"target":          M{"name": name, "creationPolicy": "Owner", "template": tpl},
			"dataFrom":        dataFrom,
		},
	})
	return out, nil
}

// password: the spec of the Password generating the key k (a generatedKey
// of mapping.schema.json): length (32, 0 included, as Helm's default), digits (6, unless given), symbols (0), noUpper (false).
func password(k M) M {
	length, _ := k["length"].(float64)
	pw := M{"length": cmp.Or(length, 32), "digits": float64(6), "symbols": float64(0), "noUpper": false, "allowRepeat": true}
	for _, f := range []string{"digits", "symbols", "noUpper"} {
		if v, ok := k[f]; ok {
			pw[f] = v
		}
	}
	return pw
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
			"stringData": secret["stringData"],
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
