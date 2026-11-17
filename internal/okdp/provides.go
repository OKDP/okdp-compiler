// Provides: what the instance publishes (Info): its instance section and
// the connections it provides, each checked against its contract.

package okdp

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/okdp/okdp-compiler/value"
)

// Info is what the instance publishes: the instance and provides sections
// of the mapping, checked. Empty fields are not written.
type Info struct {
	URL string `json:"url,omitempty" yaml:"url,omitempty"` // what the console opens, trimmed
	// Usage says how to use the instance; trimmed, it ends with one newline.
	Usage string `json:"usage,omitempty" yaml:"usage,omitempty"`
	// Provides lists the provided connections, in the order of provides.
	Provides []Output `json:"provides,omitempty" yaml:"provides,omitempty"`
}

// Output is a connection the instance provides: an item of provides,
// checked against its contract.
type Output struct {
	// Name is the item's name, or the instance's name when it gives none.
	Name     string `json:"name,omitempty" yaml:"name,omitempty"`
	Contract string `json:"contract,omitempty" yaml:"contract,omitempty"`
	// Values are its values, completed against the contract.
	Values map[string]any `json:"values,omitempty" yaml:"values,omitempty"`
	// SecretRef names the Secret of its credentials; nil when it has none.
	SecretRef *SecretRef `json:"secretRef,omitempty" yaml:"secretRef,omitempty"`
}

// SecretRef names a Secret.
type SecretRef struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
}

// secretName matches a DNS-1123 subdomain: DNS-1123 labels joined by dots.
var secretName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

func isSecretName(s string) bool { return len(s) <= 253 && secretName.MatchString(s) }

func published(info M, outputs []Output) Info {
	usage := strings.TrimSpace(value.Str(info["usage"]))
	if usage != "" {
		usage += "\n"
	}
	return Info{URL: strings.TrimSpace(value.Str(info["url"])), Usage: usage, Provides: outputs}
}

// provided reads the provides section of the mapping's result into the
// outputs of the instance and their credentials Secrets. An unnamed output
// takes the instance's name. An item with a `secret` gets a credentials
// Secret, named by its secretRef or <name>-<contract>-credentials.
func (c *run) provided() error {
	contracts := c.in.memo.contracts
	for _, x := range value.AsList(c.res["provides"]) {
		p := value.AsMap(x)
		o := Output{Name: value.Str(p["name"]), Contract: value.Str(p["contract"])}
		if o.Name == "" {
			o.Name = c.inst.name
		}
		where := fmt.Sprintf("provides %q (%s)", o.Name, o.Contract)
		contract, err := contracts.get(o.Contract)
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		// The values hold contract fields only, and no secret one: those go
		// to the credentials Secret.
		o.Values = value.AsMap(value.Copy(p["values"]))
		if err := contract.fields(o.Values, where); err != nil {
			return err
		}
		if err := contract.complete(o.Values, where); err != nil {
			return err
		}
		if err := contract.check(o.Values, where); err != nil {
			return err
		}
		secret := value.AsMap(p["secret"])
		if ref := value.AsMap(p["secretRef"]); len(ref) > 0 {
			o.SecretRef = &SecretRef{Name: value.Str(ref["name"])}
		} else if value.Truthy(secret) {
			o.SecretRef = &SecretRef{Name: c.inst.rules.Name("credentialsSecret", "output", o.Name, "contract", o.Contract)}
		}
		if o.SecretRef != nil && !isSecretName(o.SecretRef.Name) {
			return fmt.Errorf("%s: secretRef.name %q: not a Secret's name, DNS-1123 labels joined by dots, 253 characters at most", where, o.SecretRef.Name)
		}
		c.outputs = append(c.outputs, o)
		if value.Truthy(secret) {
			objs, err := c.credentialsSecret(o, secret)
			if err != nil {
				return err
			}
			for _, obj := range objs {
				c.credentials = append(c.credentials, value.AsMap(obj))
			}
		}
	}
	return twice(c.outputs)
}

// twice reports the outputs that share a contract and a name.
func twice(outputs []Output) error {
	type key struct{ contract, name string }
	count := map[key]int{}
	var names []key // in the order of their first output
	for _, o := range outputs {
		k := key{o.Contract, o.Name}
		if count[k] == 0 {
			names = append(names, k)
		}
		count[k]++
	}
	var errs []error
	for _, k := range names {
		if count[k] > 1 {
			errs = append(errs, fmt.Errorf("two %s outputs are named %q", k.contract, k.name))
		}
	}
	return errors.Join(errs...)
}

// credentialsSecret builds the credentials Secret of the output o, in the
// instance's namespace, labelled okdp.io/credentials-<contract>. With
// generate it is generated in the cluster. A plain stringData is refused
// unless Inputs.AllowPlainSecrets is set, because its values would be
// written in clear to the deployments repository.
func (c *run) credentialsSecret(o Output, secret M) ([]any, error) {
	inst := c.inst
	label := inst.rules.Name("credentialsLabel", "contract", o.Contract)
	if value.Truthy(secret["generate"]) {
		return generatedSecret(c.ctx, c.session, inst, M{
			"name":       o.SecretRef.Name,
			"keys":       secret["generate"],
			"stringData": value.AsMap(secret["stringData"]),
			"labels":     M{label: "true"},
		})
	}
	if !value.Truthy(secret["stringData"]) {
		return nil, fmt.Errorf("%s: output %q: secret needs stringData or generate", inst.svc.Name, o.Name)
	}
	if !c.in.AllowPlainSecrets {
		return nil, fmt.Errorf("%s: output %q: secret: a plain value (stringData): credentials are generated (generate) or read from the platform's secret store; Inputs.AllowPlainSecrets allows it", inst.svc.Name, o.Name)
	}
	l := inst.rules.Labels(inst.svc.Name, inst.release, "", false)
	l[label] = "true"
	return []any{M{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   M{"name": o.SecretRef.Name, "namespace": inst.namespace, "labels": l},
		"type":       "Opaque",
		"stringData": value.Copy(secret["stringData"]),
	}}, nil
}
