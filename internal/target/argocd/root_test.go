package argocd

import (
	"reflect"
	"testing"
)

// TestReadRoot: the source of the first Application of the stream that
// has a repoURL, HEAD without a targetRevision; none, an error.
func TestReadRoot(t *testing.T) {
	for raw, want := range map[string]*Source{
		"kind: AppProject\nspec: {source: {repoURL: https://x}}\n---\nkind: Application\nspec: {source: {repoURL: https://git.example.org/out.git, targetRevision: main}}\n": {RepoURL: "https://git.example.org/out.git", Revision: "main"},
		"kind: Application\nspec: {}\n---\nkind: Application\nspec: {source: {repoURL: https://y}}\n":                                                                        {RepoURL: "https://y", Revision: "HEAD"},
	} {
		if got, err := ReadRoot([]byte(raw)); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %+v %v", raw, got, err)
		}
	}
	for raw, want := range map[string]string{
		"kind: Application\nspec: {}\n": "no Application with spec.source",
		"kind: [\n":                     "yaml: line 1: did not find expected node content",
	} {
		if _, err := ReadRoot([]byte(raw)); err == nil || err.Error() != want {
			t.Errorf("%q: %v, want %s", raw, err, want)
		}
	}
}
