// Rules: what the compiler refuses and what it knows of a platform's names,
// given as data (RuleSet) rather than written here. The walkers that apply
// them stay in Go; a rule set says what they look for.

package rules

import (
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/value"
)

// RulesFile: the file of a rule set, at the root of its file system.
const RulesFile = "rules.yaml"

// The tables a rule set holds, every one of them: a table left out would
// silently turn its rule off.
const (
	TableEngineNamespaces      = "engineNamespaces"
	TableClusterScopedKinds    = "clusterScopedKinds"
	TableDeployerAPIGroups     = "deployerApiGroups"
	TableDeniedUpstreamKeys    = "deniedUpstreamKeys"
	TableImageKeySuffixes      = "imageKeySuffixes"
	TablePlatformObjectNames   = "platformObjectNames"
	TableHostAnnotations       = "hostAnnotations"
	TableIngressValueKeys      = "ingressValueKeys"
	TableIngressHostKeys       = "ingressHostKeys"
	TableIngressHostListKeys   = "ingressHostListKeys"
	TableDefaultProjectGlobals = "defaultProjectGlobals"
	TableWholePaths            = "wholePaths"
	TableProjectRoles          = "projectRoles"
	TablePlatformKeysAbove     = "projectPlatformKeys"
)

// ruleTables: the names of the tables of a rule set, sorted.
var ruleTables = []string{
	TableClusterScopedKinds, TableDefaultProjectGlobals, TableDeniedUpstreamKeys, TableDeployerAPIGroups,
	TableEngineNamespaces, TableHostAnnotations, TableImageKeySuffixes, TableIngressHostKeys,
	TableIngressHostListKeys, TableIngressValueKeys, TablePlatformObjectNames, TablePlatformKeysAbove, TableProjectRoles, TableWholePaths,
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

	PlatformSchema, MappingSchema func() (*chart.Schema, error) // what schemaValues reads
	Fragments                     []Fragment                    // the functions, by name
	GeneratedSecret               Template                      // the objects of a generated Secret
	names                         map[string]string             // the names the compiler gives credentials
	Keep                          KeepSync                      // what Argo CD is told of the objects Helm keeps
	DocumentFuncs                 []DocumentFunc                // the functions a document computes, by name
	Sections                      []string                      // the sections of a mapping
	Charts                        map[string]chart.Ref          // the defaults of global.okdp.charts, by name
	OwnCharts                     []OwnChartSpec                // the sections whose items add charts
	selectorLabels, commonLabels  map[string]string
	componentLabel                string
	wholePaths                    [][]string // TableWholePaths, split
}

// whole: whether the path keys is one a level gives whole (TableWholePaths).
func (r *RuleSet) Whole(keys []string) bool {
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

// DocumentFunc: a function of mappings, the values of a document of the
// rule set, its argument arg: the value at result.
type DocumentFunc struct{ Name, Document, Result string }

// OwnChartSpec: a section whose items add charts: list, the document that
// gives them, of the source chart; values, the template of the values of
// each it renders.
type OwnChartSpec struct {
	Section, Chart, List string
	Values               Template
}

// Template: a template of a rule set: the name of its file, the first
// segment of its errors, and its content.
type Template struct {
	Name string
	Data []byte
}

// Fragment: a function of mappings, a template evaluated on its arguments,
// checked against the mapping schema under its name; Result names the value
// it returns.
type Fragment struct {
	Name     string
	Template Template
	Result   string
}

// KeepSync: the annotation and the value that say Helm keeps an object, the
// annotation of Argo CD's sync options, and those to add.
type KeepSync struct {
	Annotation, Value, SyncOptions string
	Options                        []string
}

// readTemplate: the template file of fsys.
func readTemplate(fsys fs.FS, file string) (Template, error) {
	b, err := fs.ReadFile(fsys, path.Clean(file))
	if err != nil {
		return Template{}, fmt.Errorf("%s: %w", RulesFile, err)
	}
	return Template{Name: path.Base(file), Data: b}, nil
}

// readSchema: the JSON schema file of fsys, parsed once, as schemaValues
// reads it.
func readSchema(fsys fs.FS, file string) (func() (*chart.Schema, error), error) {
	t, err := readTemplate(fsys, file)
	if err != nil {
		return nil, err
	}
	s, err := chart.ParseSchema(t.Name, t.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", RulesFile, err)
	}
	return func() (*chart.Schema, error) { return s, nil }, nil
}

// loadFiles: the schemas, the functions and the templates of doc, each
// file of fsys read and parsed: one missing or invalid refuses the set.
func (r *RuleSet) loadFiles(fsys fs.FS, doc M) (err error) {
	schemas, templates := value.AsMap(doc["schemas"]), value.AsMap(doc["templates"])
	if r.PlatformSchema, err = readSchema(fsys, value.Str(schemas["platform"])); err != nil {
		return err
	}
	if r.MappingSchema, err = readSchema(fsys, value.Str(schemas["mapping"])); err != nil {
		return err
	}
	if r.GeneratedSecret, err = readTemplate(fsys, value.Str(templates["generatedSecret"])); err != nil {
		return err
	}
	byName := map[string]Template{}
	for name, file := range templates {
		if byName[name], err = readTemplate(fsys, value.Str(file)); err != nil {
			return err
		}
	}
	r.names = stringMap(doc["names"])
	k := value.AsMap(doc["keepSync"])
	r.Keep = KeepSync{Annotation: value.Str(k["annotation"]), Value: value.Str(k["value"]), SyncOptions: value.Str(k["syncOptions"])}
	for _, o := range value.AsList(k["options"]) {
		r.Keep.Options = append(r.Keep.Options, value.Str(o))
	}
	documents := value.AsMap(doc["documents"])
	for _, v := range value.AsList(doc["sections"]) {
		r.Sections = append(r.Sections, value.Str(v))
	}
	r.Charts = map[string]chart.Ref{}
	for name, v := range value.AsMap(doc["charts"]) {
		m := value.AsMap(v)
		ref := chart.Ref{Repository: value.Str(m["repository"]), Chart: value.Str(m["chart"]), Version: value.Str(m["version"])}
		if err := ref.Check(RulesFile + ": charts." + name); err != nil {
			return err
		}
		r.Charts[name] = ref
	}
	for _, v := range value.AsList(doc["ownCharts"]) {
		m := value.AsMap(v)
		spec := OwnChartSpec{Section: value.Str(m["section"]), Chart: value.Str(m["chart"]), List: value.Str(m["list"])}
		t, ok := byName[value.Str(m["values"])]
		if !ok {
			return fmt.Errorf("%s: ownCharts: %s: values %s is no template", RulesFile, spec.Section, value.Str(m["values"]))
		}
		spec.Values = t
		if _, ok := documents[spec.List]; !ok {
			return fmt.Errorf("%s: ownCharts: %s: list %s is no document", RulesFile, spec.Section, spec.List)
		}
		if _, ok := r.Charts[spec.Chart]; !ok {
			return fmt.Errorf("%s: ownCharts: %s: chart %s is none of charts", RulesFile, spec.Section, spec.Chart)
		}
		r.OwnCharts = append(r.OwnCharts, spec)
	}
	labels := value.AsMap(doc["labels"])
	r.componentLabel, r.selectorLabels, r.commonLabels = value.Str(labels["component"]), stringMap(labels["selector"]), stringMap(labels["common"])
	if _, ok := r.Charts["objects"]; !ok {
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
			r.DocumentFuncs = append(r.DocumentFuncs, DocumentFunc{name, d, value.Str(spec["result"])})
			continue
		}
		t, err := readTemplate(fsys, value.Str(spec["fragment"]))
		if err != nil {
			return err
		}
		r.Fragments = append(r.Fragments, Fragment{Name: name, Template: t, Result: value.Str(spec["result"])})
	}
	return nil
}

// Version: the version the rule set gives itself.
func (r *RuleSet) Version() string { return r.version }

// has: whether v is in the table.
func (r *RuleSet) Has(table, v string) bool { return r.tables[table].set[v] }

// list: the values of the table, in order; not to be changed.
func (r *RuleSet) List(table string) []string { return r.tables[table].values }

// added: what the platform adds to the table, the list of okdp, its
// settings, at the table's addFrom.
func (r *RuleSet) Added(table string, okdp M) map[string]bool {
	out := map[string]bool{}
	for _, v := range value.AsList(value.Dig(okdp, r.tables[table].addFrom)) {
		out[value.Str(v)] = true
	}
	return out
}

// M: a values map.
type M = value.M

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
	doc, err := SchemaValues(rulesSchema, RulesFile+" doesn't meet the specifications of rules.schema.json", user)
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
	for _, p := range r.List(TableWholePaths) {
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
func (r *RuleSet) Name(kind string, pairs ...string) string {
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
func (r *RuleSet) Labels(name, release, component string, selector bool) M {
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

//go:embed v1
var standardRules embed.FS

// Default: the rule set of a compile that gives none. Temporary: it
// goes when the hosts give the rule set, and a compile without one is
// refused.
var Default = sync.OnceValue(func() *RuleSet {
	sub, err := fs.Sub(standardRules, "v1")
	if err != nil {
		panic(err)
	}
	r, err := LoadRules(sub)
	if err != nil {
		panic(err)
	}
	return r
})

// SchemaValues: user, values a schema describes, coalesced over the schema's
// defaults and checked against it (chart.Schema.Values), as Helm reads a
// chart's: the values, or every violation at once, by path, under heading.
func SchemaValues(schema func() (*chart.Schema, error), heading string, user M) (M, error) {
	s, err := schema()
	if err != nil {
		return nil, err
	}
	values, v, err := s.Values(user)
	if err != nil {
		return nil, err
	}
	if v != "" {
		return nil, fmt.Errorf("%s:\n%s", heading, v)
	}
	return values, nil
}

// Kept: annotations say Helm keeps the object, as Helm reads it: trimmed, in
// any case.
func (k KeepSync) Kept(annotations M) bool {
	return strings.EqualFold(strings.TrimSpace(value.Str(annotations[k.Annotation])), k.Value)
}
