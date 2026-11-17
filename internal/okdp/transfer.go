// Transfer: credentials that go through the platform's secret store.
// Those of a claim cross namespaces through it, and those of a connection
// may already be in it (secretRef.remoteKey).

package okdp

import (
	"context"
	"fmt"
	"slices"

	"github.com/okdp/okdp-compiler/value"
)

// transfer describes credentials read from the platform's secret store
// into a Secret of the instance's namespace.
type transfer struct {
	secret, key, contract string   // the Secret read into, the key in the store, the contract
	fields                []string // the contract's secret fields
}

// storeName returns global.okdp.secrets.store, or "" when it is not set.
// The store is a SecretStore of each namespace, or a ClusterSecretStore.
func storeName(okdp M) string { return value.Str(value.Dig(okdp, "secrets.store")) }

// SecretStore is the platform's secret store as the targets write it in
// the namespace of each project (Result.SecretStore). Provider is its
// spec.provider, an External Secrets SecretStore provider, verbatim.
// ServiceAccount is the one of the namespace that it authenticates with.
type SecretStore struct {
	Name, ServiceAccount string
	Provider             map[string]any
}

// storeOf returns the rule set's account of the secret store that
// global.okdp.secrets describes: store, ref and refreshInterval.
func storeOf(ctx context.Context, rules *RuleSet, okdp M) (M, error) {
	return rules.Values(ctx, "secretStore", M{"settings": okdp})
}

// secretStore returns nil without a store or a provider.
func secretStore(ctx context.Context, rules *RuleSet, okdp M) (*SecretStore, error) {
	v, err := storeOf(ctx, rules, okdp)
	if err != nil {
		return nil, err
	}
	s := value.AsMap(v["store"])
	if len(s) == 0 {
		return nil, nil
	}
	return &SecretStore{Name: value.Str(s["name"]), ServiceAccount: value.Str(s["serviceAccount"]), Provider: value.AsMap(s["provider"])}, nil
}

// platformKeys is the first segment of the platform's keys in the secret
// store, platform/<key>. They hold the credentials of the platform's
// connections. With a ClusterSecretStore, every namespace reads them and
// none writes them. With secrets.provider, no namespace reads them: the
// platform's credentials are copied under the namespaces' own keys, and
// a connection that names a platform key is refused. No project is named
// platform.
const platformKeys = "platform"

// storeKey returns the key of a claim's credentials in the platform's
// secret store:
//
//	<provider namespace>/<id>
//
// The first segment is the namespace of the provider that pushes them.
// The second is the claim's ID, which starts with the namespace of its
// claimers. A path-based store can thus scope a writer by the first
// segment and a reader by the first label of the second, whichever
// namespace provides them.
func storeKey(namespace, id string) string { return namespace + "/" + id }

// pushSecrets returns the PushSecrets for the claims of a namespace
// other than the instance's. Each pushes the Secret of the claim's
// output to the platform's secret store, under storeKey. The key is
// deleted from the store with the PushSecret, when the claim is gone.
// A claim has none when its output has no secretRef or its contract no
// secret field. Otherwise the compile fails when the store is not set.
func pushSecrets(ctx context.Context, inst *instance, contracts *contractCache, claims []Claim, outputs []Output) ([]M, error) {
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
		o, err := storeObject(ctx, inst, "pushSecret", M{"id": c.ID, "key": storeKey(inst.namespace, c.ID), "secret": outputs[i].SecretRef.Name, "fields": fieldList, "labels": inst.rules.Labels(inst.svc.Name, inst.release, "", false), "namespace": inst.namespace})
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// pullSecret returns the ExternalSecret that reads the credentials x
// from the platform's secret store into the Secret x names, under that
// Secret's name. Both carry the label of a provider's credentials
// Secret, okdp.io/credentials-<contract>.
func pullSecret(ctx context.Context, inst *instance, x transfer) (M, error) {
	l := inst.rules.Labels(inst.svc.Name, inst.release, "", false)
	l[inst.rules.Name("credentialsLabel", "contract", x.contract)] = "true"
	fields := make([]any, len(x.fields))
	for i, f := range x.fields {
		fields[i] = f
	}
	return storeObject(ctx, inst, "pullSecret", M{"secret": x.secret, "key": x.key, "fields": fields, "labels": l, "namespace": inst.namespace})
}

// storeObject returns the object that the document of the rule set
// builds from arg and the platform's secret store.
func storeObject(ctx context.Context, inst *instance, document string, arg M) (M, error) {
	store, err := storeOf(ctx, inst.rules, inst.okdp)
	if err != nil {
		return nil, err
	}
	v, err := inst.rules.Values(ctx, document, M{"arg": arg, "settings": store})
	if err != nil {
		return nil, err
	}
	o := value.AsMap(v["object"])
	if len(o) == 0 {
		return nil, fmt.Errorf("the rule set of the compile has no %s: the credentials of another namespace go through the platform's secret store", document)
	}
	return o, nil
}
