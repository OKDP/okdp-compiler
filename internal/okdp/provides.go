// Provides: what the instance publishes (Instance): its instance section,
// and the connections it provides, each checked against its contract, with
// its credentials Secret.

package okdp

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Instance: what the instance publishes, the mapping's instance and
// provides sections, checked: what a control plane or a console reads of it
// in the deployments repository (okdp.instance.yaml, umbrella.Write).
type Instance struct {
	URL      string   `json:"url" yaml:"url"`           // what the console opens, trimmed; "" when none
	Usage    string   `json:"usage" yaml:"usage"`       // how to use the instance, trimmed, with one final newline; "" when none
	Provides []Output `json:"provides" yaml:"provides"` // the connections it provides, in the order of provides; nil when none
}

// Output: a connection the instance provides, an item of provides checked
// against its contract: what an instance that references it by name gets.
type Output struct {
	Name      string         `json:"name" yaml:"name"`                               // the item's name; the release when it names none
	Contract  string         `json:"contract" yaml:"contract"`                       // the contract it provides
	Values    map[string]any `json:"values" yaml:"values"`                           // its values, completed against the contract
	SecretRef *SecretRef     `json:"secretRef,omitempty" yaml:"secretRef,omitempty"` // the Secret of its credentials; nil when none
}

// SecretRef: a Secret, by name.
type SecretRef struct {
	Name string `json:"name" yaml:"name"`
}

// published: what the instance publishes, its instance section info, of
// the form the mapping schema checks (runMapping), and outputs (provided).
func published(info M, outputs []Output) Instance {
	usage := strings.TrimSpace(value.Str(info["usage"]))
	if usage != "" {
		usage += "\n"
	}
	return Instance{URL: strings.TrimSpace(value.Str(info["url"])), Usage: usage, Provides: outputs}
}

// provided: the outputs of the instance inst, its provides section, of the
// form the mapping schema checks (runMapping), and their credentials
// Secrets. Every output is named after the release unless the mapping names
// it, and checked against its contract, from contracts (Inputs.Contracts),
// in the order of provides, its fields in key order (contract.fields): the
// first error is reported, every required field not set at once
// (contract.complete), every violation of the contract's types at once
// (contract.check). An output given a `secret` ({generate, stringData} or
// {stringData}) gets its credentials Secret, named after its secretRef
// (default <name>-<contract>-credentials). Then two outputs of one contract
// and name fail (twice).
func provided(session *compile.Session, inst *instance, contracts fs.FS, provides []any) ([]Output, []M, error) {
	// No connections: only to compute what internal references resolve to
	// (what it records is not read).
	r := resolver{contracts: contracts, conns: M{}, namespace: inst.namespace, suffix: inst.suffix}
	var outputs []Output
	var credentials []M
	for _, x := range provides {
		p := value.AsMap(x)
		contract := value.Str(p["contract"])
		out := value.Str(p["name"])
		if out == "" {
			out = inst.release
		}
		where := fmt.Sprintf("provides %q (%s)", out, contract)
		c, err := readContract(contracts, contract)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", where, err)
		}
		// Only contract fields, and no secret: those go to the credentials
		// Secret. In key order, as every check of an output: the first error
		// is the same from one compile to the next.
		values := value.AsMap(value.Copy(p["values"]))
		if err := c.fields(values, where); err != nil {
			return nil, nil, err
		}
		if err := c.complete(values, where); err != nil {
			return nil, nil, err
		}
		if err := c.check(values, where); err != nil {
			return nil, nil, err
		}
		// Published under the release name, the values must be what an internal
		// reference to this instance resolves to (x-okdp-internal).
		if c.internal != nil && out == inst.release {
			expected, err := r.resolve(out, contract, where)
			if err != nil {
				return nil, nil, err
			}
			for _, k := range slices.Sorted(maps.Keys(c.internal)) {
				if value.Str(values[k]) != value.Str(expected[k]) {
					return nil, nil, fmt.Errorf("%s: %s is %q but internal references to %q resolve it to %q",
						where, k, value.Str(values[k]), out, value.Str(expected[k]))
				}
			}
		}
		o := Output{Name: out, Contract: contract, Values: values}
		secret := value.AsMap(p["secret"])
		if ref := value.AsMap(p["secretRef"]); len(ref) > 0 {
			o.SecretRef = &SecretRef{Name: value.Str(ref["name"])}
		} else if value.Truthy(secret) {
			o.SecretRef = &SecretRef{Name: out + "-" + contract + "-credentials"}
		}
		outputs = append(outputs, o)
		// The credentials Secret, when the mapping describes it.
		if value.Truthy(secret) {
			objs, err := credentialsSecret(session, inst, contract, out, o.SecretRef.Name, secret)
			if err != nil {
				return nil, nil, err
			}
			for _, o := range objs {
				credentials = append(credentials, value.AsMap(o))
			}
		}
	}
	return outputs, credentials, twice(outputs)
}

// twice: the error of the outputs of one contract and name, one line per
// name, in the order of their first output; nil when there is none.
func twice(outputs []Output) error {
	count := map[[2]string]int{}
	var names [][2]string // contract and name, in the order of their first output
	for _, o := range outputs {
		k := [2]string{o.Contract, o.Name}
		if count[k] == 0 {
			names = append(names, k)
		}
		count[k]++
	}
	var msgs []string
	for _, k := range names {
		if count[k] > 1 {
			msgs = append(msgs, fmt.Sprintf("two %s outputs are named %q", k[0], k[1]))
		}
	}
	if msgs == nil {
		return nil
	}
	return errors.New(strings.Join(msgs, "\n"))
}

// credentialsSecret: the credentials Secret of an output published with a
// `secret`: {generate: [keys], stringData} as a generated Secret
// (generatedSecret, in the compile's session), {stringData} as a plain
// Secret; both labelled okdp.io/credentials-<contract>, in the namespace of
// the instance inst.
func credentialsSecret(session *compile.Session, inst *instance, contract, output, name string, secret M) ([]any, error) {
	label := "okdp.io/credentials-" + contract
	if value.Truthy(secret["generate"]) {
		return generatedSecret(session, inst, M{
			"name":       name,
			"keys":       secret["generate"],
			"stringData": value.AsMap(secret["stringData"]),
			"labels":     M{label: "true"},
		})
	}
	if !value.Truthy(secret["stringData"]) {
		return nil, fmt.Errorf("%s: output %q: secret needs stringData or generate", inst.svc.Name, output)
	}
	l := labels(inst.svc.Name, inst.release, "", false)
	l[label] = "true"
	return []any{M{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   M{"name": name, "namespace": inst.namespace, "labels": l},
		"type":       "Opaque",
		"stringData": value.Copy(secret["stringData"]),
	}}, nil
}
