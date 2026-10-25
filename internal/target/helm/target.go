package helm

import (
	"fmt"
	"path"

	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
)

// Target: the helm target: the umbrella chart of each instance, what Write
// gives, in its target.InstanceDir, for a deployer that installs each with
// Helm (helm upgrade --install <release> <dir> -f instance.values.yaml).
type Target struct{}

// Files: the files of Write of each result, in its target.InstanceDir. The
// graph is not read.
func (Target) Files(results []*okdp.Result, _ *graph.Graph) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, res := range results {
		written, err := Write(res)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", res.Namespace, res.Release, err)
		}
		for name, data := range written {
			files[path.Join(target.InstanceDir(res.Namespace, res.Release), name)] = data
		}
	}
	return files, nil
}
