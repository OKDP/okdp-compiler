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

// keepSync: the annotation and the value that say Helm keeps an object, the
// annotation of Argo CD's sync options, and those to add (the rule set's).
type keepSync struct {
	annotation, value, syncOptions string
	options                        []string
}

// keepSynced: objects, each one Helm keeps (kept) with Argo CD's sync
// options of the rule set added to those of its sync-options annotation,
// comma-separated, unless it sets an option of that name already
// (Prune=confirm stays); its metadata and annotations copied, not
// changed in place; an item of a List alike (eachObject). The other
// objects are left as they are.
func keepSynced(k keepSync, objects []map[string]any) {
	_ = eachObject(objects, func(o M) error {
		meta, ok := o["metadata"].(M)
		if !ok {
			return nil
		}
		annotations, ok := meta["annotations"].(M)
		if !ok || !k.kept(annotations) {
			return nil
		}
		var options []string
		if s, ok := annotations[k.syncOptions].(string); ok && strings.TrimSpace(s) != "" {
			options = strings.Split(s, ",")
		}
		set := map[string]bool{}
		for _, opt := range options {
			name, _, _ := strings.Cut(strings.TrimSpace(opt), "=")
			set[name] = true
		}
		for _, opt := range k.options {
			if name, _, _ := strings.Cut(opt, "="); !set[name] {
				options = append(options, opt)
			}
		}
		annotations = maps.Clone(annotations)
		annotations[k.syncOptions] = strings.Join(options, ",")
		meta = maps.Clone(meta)
		meta["annotations"] = annotations
		o["metadata"] = meta
		return nil
	})
}

// kept: annotations say Helm keeps the object, as Helm reads it: trimmed,
// in any case.
func (k keepSync) kept(annotations M) bool {
	return strings.EqualFold(strings.TrimSpace(value.Str(annotations[k.annotation])), k.value)
}
