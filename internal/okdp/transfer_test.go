package okdp

import (
	"context"
	"reflect"
	"testing"
)

// TestSecretStore: Result.SecretStore of global.okdp.secrets: a store with
// a provider, the ServiceAccount defaulted, none without a provider.
func TestSecretStore(t *testing.T) {
	provider := M{"vault": M{"server": "http://openbao.openbao.svc:8200", "path": "okdp", "version": "v2", "auth": M{"kubernetes": M{"mountPath": "kubernetes", "role": "okdp", "serviceAccountRef": M{"name": "okdp-secrets"}}}}}
	res, err := Compile(context.Background(), def, inputs(map[string]any{"global": M{"okdp": M{"secrets": M{"store": "okdp-secrets", "provider": provider}}}}))
	if err != nil {
		t.Fatal(err)
	}
	if res.SecretStore == nil || res.SecretStore.Name != "okdp-secrets" || res.SecretStore.ServiceAccount != "okdp-secrets" || !reflect.DeepEqual(res.SecretStore.Provider, provider) {
		t.Errorf("SecretStore = %+v", res.SecretStore)
	}
	res, err = Compile(context.Background(), def, inputs(map[string]any{"global": M{"okdp": M{"secrets": M{"store": "okdp-secrets"}}}}))
	if err != nil {
		t.Fatal(err)
	}
	if res.SecretStore != nil {
		t.Errorf("SecretStore = %+v, want nil without a provider", res.SecretStore)
	}
}

// TestStoreRef: how the PushSecrets and the ExternalSecrets reference the
// store: the SecretStore of the namespace with a provider, else the
// ClusterSecretStore; the refresh interval, 1h unless set; a claim's key,
// the provider's namespace then the claim's ID.
func TestStoreRef(t *testing.T) {
	store := M{"secrets": M{"store": "okdp-secrets"}}
	if got := storeRef(store); !reflect.DeepEqual(got, M{"name": "okdp-secrets", "kind": "ClusterSecretStore"}) || refreshInterval(store) != "1h" {
		t.Errorf("without a provider: %v, %s", got, refreshInterval(store))
	}
	store = M{"secrets": M{"store": "okdp-secrets", "refreshInterval": "15m", "provider": M{"vault": M{"server": "http://openbao.openbao.svc:8200"}}}}
	if got := storeRef(store); !reflect.DeepEqual(got, M{"name": "okdp-secrets", "kind": "SecretStore"}) || refreshInterval(store) != "15m" {
		t.Errorf("with a provider: %v, %s", got, refreshInterval(store))
	}
	if got := storeKey("data", "demo.app.db"); got != "data/demo.app.db" {
		t.Errorf("storeKey: %s", got)
	}
}
