// Transfer: the credentials of a claim across namespaces, through the
// platform's secret store, and those of a connection that are in it
// (secretRef.remoteKey).

package okdp

import (
	"fmt"
	"slices"

	"github.com/okdp/okdp-compiler/value"
)

// transfer: credentials read from the platform's secret store, a claim's
// across namespaces or a connection's (secretRef.remoteKey): the Secret of
// the instance's namespace they are read into, the key they are under in
// the store, the contract of the claim or the connection and its secret
// fields.
type transfer struct {
	secret, key, contract string
	fields                []string
}

// storeName: global.okdp.secrets.store, the store claims' credentials
// cross namespaces through, and the connections' of a secretRef.remoteKey
// are in: a SecretStore of each namespace, or a ClusterSecretStore
// (storeRef); "" when not set.
func storeName(okdp M) string { return value.Str(value.Dig(okdp, "secrets.store")) }

// SecretStore: the platform's secret store as the targets write it in
// each project's namespace (Result.SecretStore): a SecretStore Name, its
// Provider (External Secrets' SecretStore spec.provider, verbatim), the
// ServiceAccount of the namespace it authenticates with.
type SecretStore struct {
	Name, ServiceAccount string
	Provider             map[string]any
}

// storeOf: the rule set's account of the secret store of global.okdp.secrets
// in okdp: store, ref and refreshInterval (objects/secret-store.cel.yaml).
func storeOf(rules *RuleSet, okdp M) (M, error) {
	return rules.Values("secretStore", M{"settings": okdp})
}

// secretStore: Result.SecretStore of global.okdp.secrets: nil without
// store or provider.
func secretStore(rules *RuleSet, okdp M) (*SecretStore, error) {
	v, err := storeOf(rules, okdp)
	if err != nil {
		return nil, err
	}
	s := value.AsMap(v["store"])
	if len(s) == 0 {
		return nil, nil
	}
	return &SecretStore{Name: value.Str(s["name"]), ServiceAccount: value.Str(s["serviceAccount"]), Provider: value.AsMap(s["provider"])}, nil
}

// platformKeys: the first segment of the platform's keys of the secret
// store, platform/<key>, the credentials of its connections, which every
// namespace of a ClusterSecretStore reads and none writes; with
// secrets.provider no namespace reads them, the platform's credentials
// being copied under the namespaces' own keys, and a connection naming
// one is refused (storeSecret). No project is named so (Compile).
const platformKeys = "platform"

// storeKey: the key of a claim's credentials in the platform's secret
// store: <provider namespace>/<id>, the namespace of the provider that
// pushes them, then the claim's ID, which starts with the namespace of
// its claimers: a path-based store scopes a writer by the first segment,
// its own namespace, and a reader by the first label of the second, its
// own claims, whichever namespace provides them (docs/specs: what the
// store must enforce). The platform's keys are under platform/, a
// namespace no project may have (Compile).
func storeKey(namespace, id string) string { return namespace + "/" + id }

// pushSecrets: the PushSecrets of the credentials of the claims of another
// namespace than the instance's, each the Secret of its output (outputs)
// pushed to the platform's secret store, under storeKey, each secret
// field of its contract (contracts) a property of its key, deleted from
// the store with the PushSecret, when the claim is gone. A claim whose
// output has no secretRef, or whose contract no secret field, has none.
// The store is then required.
func pushSecrets(inst *instance, contracts *contractCache, claims []Claim, outputs []Output) ([]M, error) {
	var out []M
	for _, c := range claims {
		i := slices.IndexFunc(outputs, func(o Output) bool { return o.Contract == c.Contract && o.Name == c.ID })
		if c.Namespace == inst.namespace || i < 0 || outputs[i].SecretRef == nil {
			continue
		}
		ct, err := contracts.get(c.Contract)
		if err != nil {
			return nil, err
		}
		fields := ct.secretFields()
		if fields == nil {
			continue
		}
		if storeName(inst.okdp) == "" {
			return nil, fmt.Errorf("claim %q (%s): claimed of namespace %s: its credentials go through the platform's secret store, global.okdp.secrets.store, which is not set", c.ID, c.Contract, c.Namespace)
		}
		fieldList := make([]any, len(fields))
		for k, f := range fields {
			fieldList[k] = f
		}
		o, err := storeObject(inst, "pushSecret", M{"id": c.ID, "key": storeKey(inst.namespace, c.ID), "secret": outputs[i].SecretRef.Name, "fields": fieldList, "labels": inst.rules.Labels(inst.svc.Name, inst.release, "", false), "namespace": inst.namespace})
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// pullSecret: the ExternalSecret, named after the Secret x names, that
// reads the credentials of a claim of another namespace, or of a
// connection (secretRef.remoteKey), from the platform's secret store into
// that Secret, in the instance's namespace, each secret field a property
// of x's key; it carries the labels of a provider's credentials Secret,
// okdp.io/credentials-<contract>, on the ExternalSecret and on the Secret
// it writes.
func pullSecret(inst *instance, x transfer) (M, error) {
	l := inst.rules.Labels(inst.svc.Name, inst.release, "", false)
	l[inst.rules.Name("credentialsLabel", "contract", x.contract)] = "true"
	fields := make([]any, len(x.fields))
	for i, f := range x.fields {
		fields[i] = f
	}
	return storeObject(inst, "pullSecret", M{"secret": x.secret, "key": x.key, "fields": fields, "labels": l, "namespace": inst.namespace})
}

// storeObject: the object of the document, of the rule set, for arg and the
// platform's secret store.
func storeObject(inst *instance, document string, arg M) (M, error) {
	store, err := storeOf(inst.rules, inst.okdp)
	if err != nil {
		return nil, err
	}
	v, err := inst.rules.Values(document, M{"arg": arg, "settings": store})
	if err != nil {
		return nil, err
	}
	return value.AsMap(v["object"]), nil
}
