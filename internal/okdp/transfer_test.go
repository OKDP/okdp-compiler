package okdp

import (
	"context"
	"reflect"
	"testing"
)

// TestSecretStore: the store of the rule set's account of
// global.okdp.secrets, which a host writes in each project's namespace: a
// store with a provider, the ServiceAccount defaulted, none without a
// provider.
func TestSecretStore(t *testing.T) {
	provider := M{"vault": M{"server": "http://openbao.openbao.svc:8200", "path": "okdp", "version": "v2", "auth": M{"kubernetes": M{"mountPath": "kubernetes", "role": "okdp", "serviceAccountRef": M{"name": "okdp-secrets"}}}}}
	v, err := storeOf(context.Background(), Mandatory(), M{"secrets": M{"store": "okdp-secrets", "provider": provider}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"name": "okdp-secrets", "serviceAccount": "okdp-secrets", "provider": provider}); !reflect.DeepEqual(v["store"], want) {
		t.Errorf("store = %v", v["store"])
	}
	v, err = storeOf(context.Background(), Mandatory(), M{"secrets": M{"store": "okdp-secrets"}})
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := v["store"]; ok {
		t.Errorf("store = %v, want none without a provider", s)
	}
}
