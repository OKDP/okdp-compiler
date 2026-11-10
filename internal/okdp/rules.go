// Rules: what the compiler refuses and what it knows of a platform's names,
// given as data (RuleSet) rather than written here. The walkers that apply
// them stay in Go; a rule set says what they look for.

package okdp

import (
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// RulesFile: the file of a rule set, at the root of its file system.
const RulesFile = "rules.yaml"

// The tables a rule set holds, every one of them: a table left out would
// silently turn its rule off.
const (
	tableEngineNamespaces      = "engineNamespaces"
	tableClusterScopedKinds    = "clusterScopedKinds"
	tableDeployerAPIGroups     = "deployerApiGroups"
	tableDeniedUpstreamKeys    = "deniedUpstreamKeys"
	tableImageKeySuffixes      = "imageKeySuffixes"
	tablePlatformObjectNames   = "platformObjectNames"
	tableHostAnnotations       = "hostAnnotations"
	tableIngressValueKeys      = "ingressValueKeys"
	tableIngressHostKeys       = "ingressHostKeys"
	tableIngressHostListKeys   = "ingressHostListKeys"
	tableDefaultProjectGlobals = "defaultProjectGlobals"
	tableWholePaths            = "wholePaths"
	tableProjectRoles          = "projectRoles"
	tablePlatformKeysAbove     = "projectPlatformKeys"
)

// ruleTables: the names of the tables of a rule set, sorted.
var ruleTables = []string{
	tableClusterScopedKinds, tableDefaultProjectGlobals, tableDeniedUpstreamKeys, tableDeployerAPIGroups,
	tableEngineNamespaces, tableHostAnnotations, tableImageKeySuffixes, tableIngressHostKeys,
	tableIngressHostListKeys, tableIngressValueKeys, tablePlatformObjectNames, tablePlatformKeysAbove, tableProjectRoles, tableWholePaths,
}

// ruleTable: a table of a rule set: its values in order, the same as a set,
// and the path under global.okdp of the list the platform adds to it.
type ruleTable struct {
	values  []string
	set     map[string]bool
	addFrom string
}

// RuleSet: a rule set read (LoadRules). Safe for concurrent use: it does
// not change once read.
type RuleSet struct {
	version string
	tables  map[string]*ruleTable
	sets    M // each table as a set, as the checks read it
	lists   M // each table as a list, as the checks read it
	sources map[string]compile.Source
	free    chan checker

	platformSchema, mappingSchema func() (*chart.Schema, error) // what schemaValues reads
	fragments                     []fragment                    // the functions, by name
	generatedSecret               template                      // the objects of a generated Secret
	names                         map[string]string             // the names the compiler gives credentials
	keep                          keepSync                      // what Argo CD is told of the objects Helm keeps
	documentFuncs                 []documentFunc                // the functions a document computes, by name
	sections                      []string                      // the sections of a mapping
	charts                        map[string]chart.Ref          // the defaults of global.okdp.charts, by name
	ownCharts                     []ownChartSpec                // the sections whose items add charts
	selectorLabels, commonLabels  map[string]string
	componentLabel                string
	wholePaths                    [][]string // tableWholePaths, split
}

// whole: whether the path keys is one a level gives whole (tableWholePaths).
func (r *RuleSet) whole(keys []string) bool {
	return slices.ContainsFunc(r.wholePaths, func(pattern []string) bool {
		if len(pattern) != len(keys) {
			return false
		}
		for i, p := range pattern {
			if p != "*" && p != keys[i] {
				return false
			}
		}
		return true
	})
}

// documentFunc: a function of mappings, the values of a document of the
// rule set, its argument arg: the value at result.
type documentFunc struct{ name, document, result string }

// ownChartSpec: a section whose items add charts: list, the document that
// gives them, of the source chart; values, the template of the values of
// each it renders.
type ownChartSpec struct {
	section, chart, list string
	values               template
}

// template: a template of a rule set: the name of its file, the first
// segment of its errors, and its content.
type template struct {
	name string
	data []byte
}

// readTemplate: the template file of fsys.
func readTemplate(fsys fs.FS, file string) (template, error) {
	b, err := fs.ReadFile(fsys, path.Clean(file))
	if err != nil {
		return template{}, fmt.Errorf("%s: %w", RulesFile, err)
	}
	return template{name: path.Base(file), data: b}, nil
}

// readSchema: the JSON schema file of fsys, parsed once, as schemaValues
// reads it.
func readSchema(fsys fs.FS, file string) (func() (*chart.Schema, error), error) {
	t, err := readTemplate(fsys, file)
	if err != nil {
		return nil, err
	}
	s, err := chart.ParseSchema(t.name, t.data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", RulesFile, err)
	}
	return func() (*chart.Schema, error) { return s, nil }, nil
}

// loadFiles: the schemas, the functions and the templates of doc, each
// file of fsys read and parsed: one missing or invalid refuses the set.
func (r *RuleSet) loadFiles(fsys fs.FS, doc M) (err error) {
	schemas, templates := value.AsMap(doc["schemas"]), value.AsMap(doc["templates"])
	if r.platformSchema, err = readSchema(fsys, value.Str(schemas["platform"])); err != nil {
		return err
	}
	if r.mappingSchema, err = readSchema(fsys, value.Str(schemas["mapping"])); err != nil {
		return err
	}
	if r.generatedSecret, err = readTemplate(fsys, value.Str(templates["generatedSecret"])); err != nil {
		return err
	}
	byName := map[string]template{}
	for name, file := range templates {
		if byName[name], err = readTemplate(fsys, value.Str(file)); err != nil {
			return err
		}
	}
	r.names = stringMap(doc["names"])
	k := value.AsMap(doc["keepSync"])
	r.keep = keepSync{annotation: value.Str(k["annotation"]), value: value.Str(k["value"]), syncOptions: value.Str(k["syncOptions"])}
	for _, o := range value.AsList(k["options"]) {
		r.keep.options = append(r.keep.options, value.Str(o))
	}
	documents := value.AsMap(doc["documents"])
	for _, v := range value.AsList(doc["sections"]) {
		r.sections = append(r.sections, value.Str(v))
	}
	r.charts = map[string]chart.Ref{}
	for name, v := range value.AsMap(doc["charts"]) {
		m := value.AsMap(v)
		ref := chart.Ref{Repository: value.Str(m["repository"]), Chart: value.Str(m["chart"]), Version: value.Str(m["version"])}
		if err := ref.Check(RulesFile + ": charts." + name); err != nil {
			return err
		}
		r.charts[name] = ref
	}
	for _, v := range value.AsList(doc["ownCharts"]) {
		m := value.AsMap(v)
		spec := ownChartSpec{section: value.Str(m["section"]), chart: value.Str(m["chart"]), list: value.Str(m["list"])}
		t, ok := byName[value.Str(m["values"])]
		if !ok {
			return fmt.Errorf("%s: ownCharts: %s: values %s is no template", RulesFile, spec.section, value.Str(m["values"]))
		}
		spec.values = t
		if _, ok := documents[spec.list]; !ok {
			return fmt.Errorf("%s: ownCharts: %s: list %s is no document", RulesFile, spec.section, spec.list)
		}
		if _, ok := r.charts[spec.chart]; !ok {
			return fmt.Errorf("%s: ownCharts: %s: chart %s is none of charts", RulesFile, spec.section, spec.chart)
		}
		r.ownCharts = append(r.ownCharts, spec)
	}
	labels := value.AsMap(doc["labels"])
	r.componentLabel, r.selectorLabels, r.commonLabels = value.Str(labels["component"]), stringMap(labels["selector"]), stringMap(labels["common"])
	if _, ok := r.charts["objects"]; !ok {
		return fmt.Errorf("%s: charts: objects is required", RulesFile)
	}
	if _, ok := documents["scope"]; !ok {
		return fmt.Errorf("%s: documents: scope is required", RulesFile)
	}
	functions := value.AsMap(doc["functions"])
	for _, name := range slices.Sorted(maps.Keys(functions)) {
		spec := value.AsMap(functions[name])
		if d := value.Str(spec["document"]); d != "" {
			if _, ok := documents[d]; !ok {
				return fmt.Errorf("%s: functions: %s: document %s is none of documents", RulesFile, name, d)
			}
			r.documentFuncs = append(r.documentFuncs, documentFunc{name, d, value.Str(spec["result"])})
			continue
		}
		t, err := readTemplate(fsys, value.Str(spec["fragment"]))
		if err != nil {
			return err
		}
		r.fragments = append(r.fragments, fragment{name: name, template: t, result: value.Str(spec["result"])})
	}
	return nil
}

// Version: the version the rule set gives itself.
func (r *RuleSet) Version() string { return r.version }

// has: whether v is in the table.
func (r *RuleSet) has(table, v string) bool { return r.tables[table].set[v] }

// list: the values of the table, in order; not to be changed.
func (r *RuleSet) list(table string) []string { return r.tables[table].values }

// added: what the platform adds to the table, the list of okdp, its
// settings, at the table's addFrom.
func (r *RuleSet) added(table string, okdp M) map[string]bool {
	out := map[string]bool{}
	for _, v := range value.AsList(value.Dig(okdp, r.tables[table].addFrom)) {
		out[value.Str(v)] = true
	}
	return out
}

//go:embed rules.schema.json
var rulesSchemaFile []byte

// rulesSchema: rulesSchemaFile parsed once.
var rulesSchema = sync.OnceValues(func() (*chart.Schema, error) {
	return chart.ParseSchema("rules.schema.json", rulesSchemaFile)
})

// LoadRules: the rule set of fsys, RulesFile at its root. It fails closed:
// a file that does not meet the schema, a table missing or unknown (an
// empty table is written, values: []) is an error, never a rule turned off.
func LoadRules(fsys fs.FS) (*RuleSet, error) {
	b, err := fs.ReadFile(fsys, RulesFile)
	if err != nil {
		return nil, fmt.Errorf("the rule set: %w", err)
	}
	user, err := chart.ParseValues(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", RulesFile, err)
	}
	doc, err := schemaValues(rulesSchema, RulesFile+" doesn't meet the specifications of rules.schema.json", user)
	if err != nil {
		return nil, err
	}
	tables, checks := value.AsMap(doc["tables"]), value.AsMap(doc["checks"])
	if err := exactKeys("tables", tables, ruleTables); err != nil {
		return nil, err
	}
	if err := exactKeys("checks", checks, ruleChecks); err != nil {
		return nil, err
	}
	r := &RuleSet{version: value.Str(doc["version"]), tables: map[string]*ruleTable{}, sets: M{}, lists: M{}}
	for _, name := range ruleTables {
		t := value.AsMap(tables[name])
		rt := &ruleTable{set: map[string]bool{}, addFrom: value.Str(t["addFrom"])}
		for _, v := range value.AsList(t["values"]) {
			rt.values = append(rt.values, value.Str(v))
			rt.set[value.Str(v)] = true
		}
		r.tables[name] = rt
		set := make(M, len(rt.set))
		list := make([]any, len(rt.values))
		for i, v := range rt.values {
			set[v], list[i] = true, v
		}
		r.sets[name], r.lists[name] = set, list
	}
	for _, p := range r.list(tableWholePaths) {
		r.wholePaths = append(r.wholePaths, strings.Split(p, "."))
	}
	if err := r.loadChecks(fsys, checks, value.AsMap(doc["documents"])); err != nil {
		return nil, err
	}
	if err := r.loadFiles(fsys, doc); err != nil {
		return nil, err
	}
	return r, nil
}

// name: the name of the kind (the rule set's names), pairs the word and the
// value each $<word> stands for.
func (r *RuleSet) name(kind string, pairs ...string) string {
	args := make([]string, 0, len(pairs))
	for i := 0; i < len(pairs); i += 2 {
		args = append(args, "$"+pairs[i], pairs[i+1])
	}
	return strings.NewReplacer(args...).Replace(r.names[kind])
}

// stringMap: the map of strings v.
func stringMap(v any) map[string]string {
	out := map[string]string{}
	for k, x := range value.AsMap(v) {
		out[k] = value.Str(x)
	}
	return out
}

// labels: the labels of the objects of the service name of release, those
// of the rule set, $service and $release standing for them; with
// component, its label; selector, the stable subset alone.
func (r *RuleSet) labels(name, release, component string, selector bool) M {
	expand := func(v string) string {
		switch v {
		case "$service":
			return name
		case "$release":
			return release
		}
		return v
	}
	out := M{}
	for k, v := range r.selectorLabels {
		out[k] = expand(v)
	}
	if component != "" {
		out[r.componentLabel] = component
	}
	if !selector {
		for k, v := range r.commonLabels {
			out[k] = expand(v)
		}
	}
	return out
}

// exactKeys: nil when m has exactly the keys want: one left out would
// silently turn a rule off.
func exactKeys(what string, m M, want []string) error {
	var missing, unknown []string
	for _, name := range want {
		if _, ok := m[name]; !ok {
			missing = append(missing, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(want, name) {
			unknown = append(unknown, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: %s: missing %s", RulesFile, what, strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		return fmt.Errorf("%s: %s: unknown %s", RulesFile, what, strings.Join(unknown, ", "))
	}
	return nil
}

//go:embed rules/v1
var standardRules embed.FS

// defaultRules: the rule set of a compile that gives none. Temporary: it
// goes when the hosts give the rule set, and a compile without one is
// refused.
var defaultRules = sync.OnceValue(func() *RuleSet {
	sub, err := fs.Sub(standardRules, "rules/v1")
	if err != nil {
		panic(err)
	}
	r, err := LoadRules(sub)
	if err != nil {
		panic(err)
	}
	return r
})

// rules: the rule set of the compile.
func (in Inputs) rules() *RuleSet {
	if in.Rules != nil {
		return in.Rules
	}
	return defaultRules()
}
