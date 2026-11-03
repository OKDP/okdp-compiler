package argocd

import (
	"bytes"
	"errors"
	"io"

	"go.yaml.in/yaml/v3"
)

// Source: the repository and revision Argo CD reads a tree from, the
// target's RepoURL and Revision.
type Source struct {
	RepoURL  string
	Revision string
}

// ReadRoot: the Source of an Argo CD entry point, raw, a YAML stream
// written by hand: the spec.source of its first Application that has a
// repoURL, its targetRevision HEAD when it has none.
func ReadRoot(raw []byte) (*Source, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Source *struct {
					RepoURL        string `yaml:"repoURL"`
					TargetRevision string `yaml:"targetRevision"`
				} `yaml:"source"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New("no Application with spec.source")
			}
			return nil, err
		}
		if doc.Kind != "Application" || doc.Spec.Source == nil || doc.Spec.Source.RepoURL == "" {
			continue
		}
		src := &Source{RepoURL: doc.Spec.Source.RepoURL, Revision: doc.Spec.Source.TargetRevision}
		if src.Revision == "" {
			src.Revision = "HEAD"
		}
		return src, nil
	}
}
