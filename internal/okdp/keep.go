// Keep: the objects Helm keeps, helm.sh/resource-policy: keep, which the
// release leaves behind when it drops them or is uninstalled, kept alike
// by Argo CD, which renders the chart with helm template and reads none
// of Helm's annotations: its sync options say so.

package okdp

import (
	"maps"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

const (
	resourcePolicy = "helm.sh/resource-policy"         // Helm's: keep, the object outlives its release
	syncOptions    = "argocd.argoproj.io/sync-options" // Argo CD's, comma-separated: Prune=false, Delete=false...
)

// keepOptions: the sync options of Argo CD an object Helm keeps carries:
// not pruned when it leaves the Application's manifests, not deleted
// with the Application.
var keepOptions = []string{"Prune=false", "Delete=false"}

// keepSynced: objects, each one Helm keeps (kept) with Argo CD's sync
// options of keepOptions added to those of its sync-options annotation,
// comma-separated, unless it sets an option of that name already
// (Prune=confirm stays); its metadata and annotations copied, not
// changed in place; an item of a List alike (eachObject). The other
// objects are left as they are.
func keepSynced(objects []map[string]any) {
	_ = eachObject(objects, func(o M) error {
		meta, ok := o["metadata"].(M)
		if !ok {
			return nil
		}
		annotations, ok := meta["annotations"].(M)
		if !ok || !kept(annotations) {
			return nil
		}
		var options []string
		if s, ok := annotations[syncOptions].(string); ok && strings.TrimSpace(s) != "" {
			options = strings.Split(s, ",")
		}
		set := map[string]bool{}
		for _, opt := range options {
			name, _, _ := strings.Cut(strings.TrimSpace(opt), "=")
			set[name] = true
		}
		for _, opt := range keepOptions {
			if name, _, _ := strings.Cut(opt, "="); !set[name] {
				options = append(options, opt)
			}
		}
		annotations = maps.Clone(annotations)
		annotations[syncOptions] = strings.Join(options, ",")
		meta = maps.Clone(meta)
		meta["annotations"] = annotations
		o["metadata"] = meta
		return nil
	})
}

// kept: annotations say Helm keeps the object, helm.sh/resource-policy
// keep, as Helm reads it: trimmed, in any case.
func kept(annotations M) bool {
	return strings.EqualFold(strings.TrimSpace(value.Str(annotations[resourcePolicy])), "keep")
}
