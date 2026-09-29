// Secrets: the Secrets OKDP generates for an instance: Helm must render the
// same output on every run (Argo CD renders with `helm template`), so the random values
// come from External Secrets Operator Password generators, in the cluster.
package compiler

import (
	"fmt"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

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
// apiVersion (of the ExternalSecret, external-secrets.io/v1).
//
// A generator is named <name>-<key> (lowercased, "_" and "." as "-"),
// longer than 63 characters cut to 54 and suffixed with 8 hex characters of
// the key's SHA-256; two keys giving the same name fail.
func generatedSecret(ctx M, args M) ([]any, error) {
	name := str(args["name"])
	if name == "" {
		return nil, fmt.Errorf("okdp.generatedSecret: name is required")
	}
	rel := asMap(ctx["release"])
	ns := str(orDefault(args["namespace"], rel["namespace"]))
	api := str(orDefault(args["apiVersion"], "external-secrets.io/v1"))
	extra := asMap(args["labels"])
	annotations := asMap(args["annotations"])
	okdpLabels := labels(ctxChart(ctx), str(rel["name"]), "")
	meta := func(name string, labels M) M {
		m := M{"name": name, "namespace": ns, "labels": labels}
		if len(annotations) > 0 {
			m["annotations"] = deepCopy(annotations)
		}
		return m
	}
	// The Secret's data: the static keys, then the generated ones.
	template := M{}
	for k, v := range asMap(args["stringData"]) {
		s := toString(v)
		if strings.Contains(s, "{{") {
			s = fmt.Sprintf("{{ %q }}", s)
		}
		template[k] = s
	}
	var out, dataFrom []any
	gens := map[string]string{}
	for _, x := range asList(args["keys"]) {
		spec := asMap(x)
		key := str(spec["key"])
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
		digits := any(float64(6))
		if d, ok := spec["digits"]; ok {
			digits = printed(d)
		}
		out = append(out, M{
			"apiVersion": "generators.external-secrets.io/v1alpha1",
			"kind":       "Password",
			"metadata":   meta(gen, deepCopy(okdpLabels).(M)),
			"spec": M{
				"length":      printed(orDefault(spec["length"], float64(32))),
				"digits":      digits,
				"symbols":     printed(orDefault(spec["symbols"], float64(0))),
				"noUpper":     printed(orDefault(spec["noUpper"], false)),
				"allowRepeat": true,
			},
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
	esLabels := mergeMaps(deepCopy(okdpLabels).(M), extra)
	tpl := M{
		"engineVersion": "v2",
		"metadata":      M{"labels": deepCopy(esLabels)},
		"data":          template,
	}
	if t := args["type"]; truthy(t) {
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

// credentialsSecret: the credentials Secret of an output published with a
// `secret`: {generate: [keys], stringData} as a
// generated Secret, {stringData} as a plain Secret; both labelled
// okdp.io/credentials-<contract>.
func credentialsSecret(ctx M, contract, output, name string, secret M) ([]any, error) {
	label := "okdp.io/credentials-" + contract
	if truthy(secret["generate"]) {
		return generatedSecret(ctx, M{
			"name":       name,
			"keys":       secret["generate"],
			"stringData": orDefault(secret["stringData"], M{}),
			"labels":     M{label: "true"},
		})
	}
	if !truthy(secret["stringData"]) {
		return nil, fmt.Errorf("%s: output %q: secret needs stringData or generate", ctxChart(ctx).Name, output)
	}
	rel := asMap(ctx["release"])
	l := labels(ctxChart(ctx), str(rel["name"]), "")
	l[label] = "true"
	return []any{M{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   M{"name": name, "namespace": rel["namespace"], "labels": l},
		"type":       "Opaque",
		"stringData": deepCopy(secret["stringData"]),
	}}, nil
}

// ctxChart: the wrapper chart of a mapping context.
func ctxChart(ctx M) chartMeta {
	return chartMeta{Name: str(dig(ctx, "chart.name")), Version: str(dig(ctx, "chart.version")), AppVersion: str(dig(ctx, "chart.appVersion"))}
}

// orDefault: sprig's default: v, or d when v is empty (null, "", 0, false,
// an empty list or map).
func orDefault(v, d any) any {
	if truthy(v) {
		return v
	}
	return d
}

// toString: sprig's toString (a null is "<nil>").
func toString(v any) string {
	if v == nil {
		return "<nil>"
	}
	return str(v)
}

// printed: v printed into a template ({{ v }}) and read back as YAML.
func printed(v any) any {
	var out any
	if err := sigsyaml.Unmarshal([]byte(str(v)), &out); err != nil {
		return str(v)
	}
	return out
}
