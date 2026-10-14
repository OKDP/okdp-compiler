// Metadata: the Chart.yaml of a package, the umbrella chart of its
// instances: Helm's fields, its dependencies the upstream charts that render
// the values of their keys.
package chart

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

// MetadataFile: the file of a package's metadata, the first segment of its
// errors.
const MetadataFile = "Chart.yaml"

// Metadata: a Chart.yaml, Helm's fields in Helm's order, which is also the
// order the umbrella writes them in.
type Metadata struct {
	APIVersion   string            `yaml:"apiVersion" json:"apiVersion"` // v2
	Name         string            `yaml:"name" json:"name"`             // the service: labels, parameter schema errors
	Version      string            `yaml:"version" json:"version"`       // the package's version, SemVer: the umbrella's
	KubeVersion  string            `yaml:"kubeVersion,omitempty" json:"kubeVersion,omitempty"`
	Description  string            `yaml:"description,omitempty" json:"description,omitempty"`
	Type         string            `yaml:"type,omitempty" json:"type,omitempty"` // application, or absent
	Keywords     []string          `yaml:"keywords,omitempty" json:"keywords,omitempty"`
	Home         string            `yaml:"home,omitempty" json:"home,omitempty"`
	Sources      []string          `yaml:"sources,omitempty" json:"sources,omitempty"`
	Dependencies []Dependency      `yaml:"dependencies,omitempty" json:"dependencies,omitempty"` // the upstream charts, by key
	Maintainers  []Maintainer      `yaml:"maintainers,omitempty" json:"maintainers,omitempty"`
	Icon         string            `yaml:"icon,omitempty" json:"icon,omitempty"`
	AppVersion   string            `yaml:"appVersion,omitempty" json:"appVersion,omitempty"`
	Deprecated   bool              `yaml:"deprecated,omitempty" json:"deprecated,omitempty"`
	Annotations  map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// Dependency: an upstream chart of a package, whose key (Key) names its
// values: in the values file, okdp.yaml's defaults and upstream, the
// final values. The fields of Helm's that decide what is rendered are
// decoded only to be refused (Metadata.Check): the values decide it.
type Dependency struct {
	Name         string `yaml:"name" json:"name"`                       // the chart's name in the repository
	Alias        string `yaml:"alias,omitempty" json:"alias,omitempty"` // the key, when not the name
	Repository   string `yaml:"repository" json:"repository"`           // https://... or oci://...
	Version      string `yaml:"version" json:"version"`                 // a pinned version
	Condition    any    `yaml:"condition,omitempty" json:"-"`
	Tags         any    `yaml:"tags,omitempty" json:"-"`
	Enabled      any    `yaml:"enabled,omitempty" json:"-"`
	ImportValues any    `yaml:"import-values,omitempty" json:"-"`
}

// Key: the key of the dependency: its alias, else its name.
func (d Dependency) Key() string {
	if d.Alias != "" {
		return d.Alias
	}
	return d.Name
}

// Ref: the upstream chart of the dependency.
func (d Dependency) Ref() Ref {
	return Ref{Repository: d.Repository, Chart: d.Name, Version: d.Version}
}

// Maintainer: a maintainer of the chart.
type Maintainer struct {
	Name  string `yaml:"name,omitempty" json:"name,omitempty"`
	Email string `yaml:"email,omitempty" json:"email,omitempty"`
	URL   string `yaml:"url,omitempty" json:"url,omitempty"`
}

// ReadMetadata: a Chart.yaml, b, read strictly (a field Helm does not
// define fails: a misspelt alias would otherwise rename a key), one YAML
// document, then checked (Metadata.Check).
func ReadMetadata(b []byte) (Metadata, error) {
	var m Metadata
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return Metadata{}, fmt.Errorf("%s: %w", MetadataFile, err)
	}
	if err := dec.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return Metadata{}, fmt.Errorf("%s: one YAML document", MetadataFile)
	}
	return m, m.Check()
}

// refused: the fields of a dependency that decide, for Helm, whether it is
// rendered: refused, the values decide it.
func (d Dependency) refused() (string, bool) {
	for _, f := range []struct {
		name string
		v    any
	}{{"condition", d.Condition}, {"tags", d.Tags}, {"enabled", d.Enabled}, {"import-values", d.ImportValues}} {
		if f.v != nil {
			return f.name, true
		}
	}
	return "", false
}

// Check: what a Chart.yaml may not hold, refused when it is loaded and
// before every compile: an apiVersion but v2, no name, a version that is
// not SemVer (the umbrella's), a library chart; a dependency without a
// name, with a field that decides what is rendered, whose key is not a DNS
// label (it names files, which ../x would place out of their directory,
// and the umbrella's dependency alias) or is another's, or whose chart is
// not pinned in a chart repository (Ref.Check).
func (m Metadata) Check() error {
	switch {
	case m.APIVersion != "v2":
		return fmt.Errorf("%s: apiVersion %q: v2, the only one the compiler reads", MetadataFile, m.APIVersion)
	case m.Name == "":
		return fmt.Errorf("%s: name is required", MetadataFile)
	case m.Version == "":
		return fmt.Errorf("%s: version is required", MetadataFile)
	case !IsVersion(m.Version):
		return fmt.Errorf("%s: version %q: SemVer (1.2.3), the version of the umbrella chart", MetadataFile, m.Version)
	case m.Type != "" && m.Type != "application":
		return fmt.Errorf("%s: type %q: a package is an application chart", MetadataFile, m.Type)
	}
	keys := map[string]int{}
	for i, d := range m.Dependencies {
		at := fmt.Sprintf("%s: dependencies[%d]", MetadataFile, i)
		key := d.Key()
		if d.Name == "" {
			return errors.New(at + ".name is required")
		}
		if !isChartName(d.Name) {
			return fmt.Errorf("%s.name %q: the name of a chart of the repository", at, d.Name)
		}
		if f, ok := d.refused(); ok {
			return fmt.Errorf("%s (%s): %s: the values decide what is rendered ($if on %s in values.cel.yaml)", at, key, f, key)
		}
		if !IsDNSLabel(key) {
			return fmt.Errorf("%s (%s): key %q: a DNS label (lowercase letters, digits and -, a letter or digit at each end, 63 characters at most): the key names the umbrella's dependency alias", at, key, key)
		}
		if j, ok := keys[key]; ok {
			return fmt.Errorf("%s (%s): the key of dependencies[%d] too: give one of them an alias", at, key, j)
		}
		keys[key] = i
		if err := d.Ref().Check(at); err != nil {
			return err
		}
	}
	return nil
}
