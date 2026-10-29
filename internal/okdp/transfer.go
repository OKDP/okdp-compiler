// Transfer: the credentials of a claim across namespaces, through the
// platform's secret store.

package okdp

import (
	"fmt"
	"io/fs"
	"slices"

	"github.com/okdp/okdp-compiler/internal/value"
)

// transfer: credentials that cross namespaces through the platform's
// secret store: the Secret of the instance's namespace they are in, the
// key they are under in the store, the contract of the claim and its secret
// fields.
type transfer struct {
	secret, key, contract string
	fields                []string
}

// storeName: global.okdp.secrets.store, the ClusterSecretStore claims'
// credentials cross namespaces through; "" when not set.
func storeName(okdp M) string { return value.Str(value.Dig(okdp, "secrets.store")) }

// storeKey: the key of a claim's credentials in the platform's secret
// store: okdp.<id>, the ID starting with the namespace of its claimers, its
// key's second label, so that the store's rules can give each namespace
// okdp.<namespace>.*.
func storeKey(id string) string { return "okdp." + id }

// pushSecrets: the PushSecrets of the credentials of the claims of another
// namespace than the instance's, each the Secret of its output (outputs)
// pushed to the platform's secret store, each secret field of its contract
// (contracts) a property of its key, kept there when the claim is gone. A
// claim whose output has no secretRef, or whose contract no secret field,
// has none. The store is then required.
func pushSecrets(inst *instance, contracts fs.FS, claims []Claim, outputs []Output) ([]M, error) {
	var out []M
	store := storeName(inst.okdp)
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
		if store == "" {
			return nil, fmt.Errorf("claim %q (%s): claimed of namespace %s: its credentials go through the platform's secret store, global.okdp.secrets.store, which is not set", c.ID, c.Contract, c.Namespace)
		}
		var data []any
		for _, f := range fields {
			data = append(data, M{"match": M{"secretKey": f, "remoteRef": M{"remoteKey": storeKey(c.ID), "property": f}}})
		}
		out = append(out, M{
			"apiVersion": "external-secrets.io/v1alpha1",
			"kind":       "PushSecret",
			"metadata":   M{"name": c.ID, "namespace": inst.namespace, "labels": labels(inst.svc.Name, inst.release, "", false)},
			"spec": M{
				"refreshInterval": "1h",
				"updatePolicy":    "Replace",
				"deletionPolicy":  "None",
				"secretStoreRefs": []any{M{"name": store, "kind": "ClusterSecretStore"}},
				"selector":        M{"secret": M{"name": outputs[i].SecretRef.Name}},
				"data":            data,
			},
		})
	}
	return out, nil
}

// pullSecret: the ExternalSecret that reads the credentials of a claim of
// another namespace from the platform's secret store into the Secret x
// names, in the instance's namespace, which carries the labels of the
// provider's credentials Secret: okdp.io/credentials-<contract>, on the
// ExternalSecret and on the Secret it writes.
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
			"refreshInterval": "1h",
			"secretStoreRef":  M{"name": storeName(inst.okdp), "kind": "ClusterSecretStore"},
			"target":          M{"name": x.secret, "creationPolicy": "Owner", "template": M{"metadata": M{"labels": value.Copy(l)}}},
			"data":            data,
		},
	}
}
