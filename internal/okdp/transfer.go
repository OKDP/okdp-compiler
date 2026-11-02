// Transfer: the credentials of a claim across namespaces, through the
// platform's secret store, and those of a connection that are in it
// (secretRef.remoteKey).

package okdp

import (
	"cmp"
	"fmt"
	"io/fs"
	"slices"

	"github.com/okdp/okdp-compiler/internal/value"
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

// secretStore: Result.SecretStore of global.okdp.secrets: nil without
// store or provider; the ServiceAccount okdp-secrets when unset.
func secretStore(okdp M) *SecretStore {
	provider := value.AsMap(value.Dig(okdp, "secrets.provider"))
	if storeName(okdp) == "" || len(provider) == 0 {
		return nil
	}
	return &SecretStore{Name: storeName(okdp), ServiceAccount: cmp.Or(value.Str(value.Dig(okdp, "secrets.serviceAccount")), "okdp-secrets"), Provider: value.Copy(provider).(map[string]any)}
}

// storeRef: the store as the PushSecrets and the ExternalSecrets
// reference it, {name, kind}: the SecretStore of the namespace when the
// platform gives the store's provider (secretStore), else the
// ClusterSecretStore of that name.
func storeRef(okdp M) M {
	kind := "ClusterSecretStore"
	if secretStore(okdp) != nil {
		kind = "SecretStore"
	}
	return M{"name": storeName(okdp), "kind": kind}
}

// refreshInterval: global.okdp.secrets.refreshInterval, how often the
// PushSecrets push and the ExternalSecrets read the store; 1h when unset.
func refreshInterval(okdp M) string {
	return cmp.Or(value.Str(value.Dig(okdp, "secrets.refreshInterval")), "1h")
}

// platformKeys: the first segment of the platform's keys of the secret
// store, platform/<key>, the credentials of its connections, which every
// namespace reads and none writes: no project is named so (Compile).
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
func pushSecrets(inst *instance, contracts fs.FS, claims []Claim, outputs []Output) ([]M, error) {
	var out []M
	for _, c := range claims {
		i := slices.IndexFunc(outputs, func(o Output) bool { return o.Contract == c.Contract && o.Name == c.ID })
		if c.Namespace == inst.namespace || i < 0 || outputs[i].SecretRef == nil {
			continue
		}
		ct, err := readContract(contracts, c.Contract)
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
		key := storeKey(inst.namespace, c.ID)
		var data []any
		for _, f := range fields {
			data = append(data, M{"match": M{"secretKey": f, "remoteRef": M{"remoteKey": key, "property": f}}})
		}
		out = append(out, M{
			"apiVersion": "external-secrets.io/v1alpha1",
			"kind":       "PushSecret",
			"metadata":   M{"name": c.ID, "namespace": inst.namespace, "labels": labels(inst.svc.Name, inst.release, "", false)},
			"spec": M{
				"refreshInterval": refreshInterval(inst.okdp),
				"updatePolicy":    "Replace",
				"deletionPolicy":  "Delete",
				"secretStoreRefs": []any{storeRef(inst.okdp)},
				"selector":        M{"secret": M{"name": outputs[i].SecretRef.Name}},
				"data":            data,
			},
		})
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
func pullSecret(inst *instance, x transfer) M {
	var data []any
	for _, f := range x.fields {
		data = append(data, M{"secretKey": f, "remoteRef": M{"key": x.key, "property": f}})
	}
	l := labels(inst.svc.Name, inst.release, "", false)
	l["okdp.io/credentials-"+x.contract] = "true"
	return M{
		"apiVersion": "external-secrets.io/v1",
		"kind":       "ExternalSecret",
		"metadata":   M{"name": x.secret, "namespace": inst.namespace, "labels": l},
		"spec": M{
			"refreshInterval": refreshInterval(inst.okdp),
			"secretStoreRef":  storeRef(inst.okdp),
			"target":          M{"name": x.secret, "creationPolicy": "Owner", "template": M{"metadata": M{"labels": value.Copy(l)}}},
			"data":            data,
		},
	}
}
