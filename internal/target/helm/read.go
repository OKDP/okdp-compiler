package helm

import (
	"errors"
	"fmt"
	"io/fs"
	"path"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
)

// Published: what a compiled instance publishes, read back from what a
// target wrote of it (ReadPublished): its release, its package (the
// name and version of its Chart.yaml) and its InfoFile, zero without it.
type Published struct {
	Release string
	Service string
	Version string
	Info    okdp.Info
}

// Compiled: an instance's umbrella chart as a target wrote it, read back
// (ReadCompiled): what it publishes, its Chart.yaml as written, and its
// ValuesFile, the values of each key under the key, as chart.ParseValues
// reads them (every number a float64).
type Compiled struct {
	Published
	Chart  []byte
	Values map[string]any
}

// ReadPublished: what the compiled instances of namespace publish in
// output, a tree a target wrote (projects/ at its root): each directory of
// target.InstanceDir that holds a Chart.yaml, by release; none when the
// output has no instance of namespace.
func ReadPublished(output fs.FS, namespace string) ([]Published, error) {
	entries, err := fs.ReadDir(output, path.Join(target.ProjectDir(namespace), "instances"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Published
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, _, err := readPublished(output, namespace, e.Name())
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, nil
}

// ReadCompiled: the instance of release of namespace in output, a tree a
// target wrote; an error wrapping fs.ErrNotExist when it has no Chart.yaml
// there (an instance not compiled yet).
func ReadCompiled(output fs.FS, namespace, release string) (*Compiled, error) {
	p, raw, err := readPublished(output, namespace, release)
	if err != nil {
		return nil, err
	}
	file := path.Join(target.InstanceDir(namespace, release), ValuesFile)
	b, err := fs.ReadFile(output, file)
	if err != nil {
		return nil, err
	}
	values, err := chart.ParseValues(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if values == nil {
		values = map[string]any{}
	}
	return &Compiled{Published: *p, Chart: raw, Values: values}, nil
}

// readPublished: what the instance of release publishes, and its
// Chart.yaml as written.
func readPublished(output fs.FS, namespace, release string) (*Published, []byte, error) {
	dir := target.InstanceDir(namespace, release)
	raw, err := fs.ReadFile(output, path.Join(dir, "Chart.yaml"))
	if err != nil {
		return nil, nil, err
	}
	var md struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(raw, &md); err != nil {
		return nil, nil, fmt.Errorf("%s/Chart.yaml: %w", dir, err)
	}
	p := &Published{Release: release, Service: md.Name, Version: md.Version}
	info, err := fs.ReadFile(output, path.Join(dir, InfoFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, nil, err
	default:
		if err := yaml.Unmarshal(info, &p.Info); err != nil {
			return nil, nil, fmt.Errorf("%s/%s: %w", dir, InfoFile, err)
		}
	}
	return p, raw, nil
}
