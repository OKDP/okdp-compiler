// Service: the package as a compile reads it: service.yaml (Service), its
// mapping and its other files (Definition).
package okdp

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Definition: a package, as fsio.LoadDefinition reads it: service.yaml, its
// mapping, and the package's other files.
type Definition struct {
	Service Service
	Mapping []byte // mapping.cel.yaml
	Files   fs.FS  // the package: values.yaml, values.schema.json, the base files
}

// Service: service.yaml, the engine-neutral part of a package.
type Service struct {
	Name       string       `yaml:"name"`       // the service: labels, descriptor, parameter schema errors
	Version    string       `yaml:"version"`    // the package's version: descriptor
	AppVersion string       `yaml:"appVersion"` // the application's version: label app.kubernetes.io/version
	Roles      []string     `yaml:"roles"`      // catalog: what the service provides (not read by the compiler)
	DependsOn  []string     `yaml:"dependsOn"`  // catalog: the platform services it needs (not read by the compiler)
	Charts     chart.Specs  `yaml:"charts"`
	Require    Requirements `yaml:"require"` // the global.okdp keys the service needs: in every case, or under a condition
}

// Requirements: the require section of service.yaml, the platform values
// the service needs, in order.
type Requirements []Requirement

// Requirement: a platform value the service needs, a key under global.okdp
// that must be set and not empty: in every case (no If), checked before the
// platform context is derived (buildContext), or when its condition holds,
// checked on the mapping's context (requireWhen).
type Requirement struct {
	Key string // dotted, under global.okdp: ingress.suffix
	If  string // the condition, ${{ <CEL> }} on the mapping's context, true or false; "": in every case
}

// entryForms: the forms of an entry of require, for messages.
const entryForms = "an entry is a key, or {key: <key>, $if: ${{ <condition> }}}"

// UnmarshalYAML: a list, each entry a key, required in every case, or
// {key: <key>, $if: ${{ <condition> }}}: both fields and no other, the key
// a string, the condition one expression. An entry of another form fails
// with its position, and its key once read (entry).
func (rs *Requirements) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("require must be a list: %s", entryForms)
	}
	out := Requirements{}
	for i, e := range n.Content {
		r, err := requirement(e)
		if err != nil {
			return fmt.Errorf("%s: %w", entry(i, r.Key), err)
		}
		out = append(out, r)
	}
	*rs = out
	return nil
}

// entry: an entry of require, for messages: require[<i>], followed by
// (<key>) once its key is read.
func entry(i int, key string) string {
	if key == "" {
		return fmt.Sprintf("require[%d]", i)
	}
	return fmt.Sprintf("require[%d] (%s)", i, key)
}

// requirement: one entry of require, a key or {key, $if}; on an error of
// its condition, with its key.
func requirement(n *yaml.Node) (Requirement, error) {
	var v any
	if err := n.Decode(&v); err != nil {
		return Requirement{}, err
	}
	switch t := v.(type) {
	case string:
		return Requirement{Key: t}, nil
	case nil: // an empty item: an empty key (check)
		return Requirement{}, nil
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			if k != "key" && k != "$if" {
				return Requirement{}, fmt.Errorf("unknown field %s: %s", k, entryForms)
			}
		}
		key, hasKey := t["key"]
		cond, hasIf := t["$if"]
		switch {
		case !hasKey:
			return Requirement{}, fmt.Errorf("no key: %s", entryForms)
		case !hasIf:
			return Requirement{}, fmt.Errorf("no $if: %s (a key alone is required in every case)", entryForms)
		}
		k, ok := key.(string)
		if !ok {
			return Requirement{}, fmt.Errorf("key must be a key under global.okdp (ingress.suffix), got %s", value.Short(key))
		}
		if err := compile.CheckCondition(cond); err != nil {
			return Requirement{Key: k}, fmt.Errorf("$if: %w", err)
		}
		return Requirement{Key: k, If: cond.(string)}, nil
	}
	return Requirement{}, fmt.Errorf("%s, got %s", entryForms, value.Short(v))
}

// check: every key set, every condition one expression; for entries built
// in memory too.
func (rs Requirements) check() error {
	for i, r := range rs {
		if r.Key == "" {
			return fmt.Errorf("service.yaml: %s: an empty key: a key under global.okdp (ingress.suffix)", entry(i, ""))
		}
		if r.If == "" {
			continue
		}
		if err := compile.CheckCondition(r.If); err != nil {
			return fmt.Errorf("service.yaml: %s: $if: %w", entry(i, r.Key), err)
		}
	}
	return nil
}

// required: the error of a platform value key that is missing or empty
// (null, false, "", 0, [] or {}) in okdpG, global.okdp; nil when it is set.
func required(okdpG M, key string) error {
	if value.Truthy(value.Dig(okdpG, key)) {
		return nil
	}
	return fmt.Errorf("global.okdp.%s is required. It is a platform value: set it in platform/platform-values.yaml", key)
}

// meta: what the mapping and OKDP's objects see of the package.
func (s *Service) meta() serviceMeta {
	return serviceMeta{Name: s.Name, Version: s.Version, AppVersion: s.AppVersion}
}

// Check: what service.yaml may not hold, refused when it is loaded
// (fsio.LoadDefinition) and before every compile: no name or version, a
// require entry without its key or with a condition that is not one
// expression, the chart of OKDP's own objects (the compiler adds it), then
// what its charts may not hold (chart.Specs.Check).
func (s *Service) Check() error {
	switch {
	case s.Name == "":
		return errors.New("service.yaml: name is required")
	case s.Version == "":
		return errors.New("service.yaml: version is required")
	}
	if err := s.Require.check(); err != nil {
		return err
	}
	if _, ok := s.Charts[ObjectsChart]; ok {
		return fmt.Errorf("service.yaml: charts.%s: the chart of OKDP's own objects is added by the compiler: remove it from service.yaml", ObjectsChart)
	}
	return s.Charts.Check()
}
