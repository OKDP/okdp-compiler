// Rules: what the compiler refuses and what it knows of a platform's names,
// given as data (RuleSet) rather than written here. The walkers that apply
// them stay in Go; a rule set says what they look for.

package rules

import (
	_ "embed"
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

// The roles a project's group may hold (TableProjectRoles): what each may do
// is written in the code of the targets, so a rule set lists some of these,
// and no other.
const (
	RoleViewer = "viewer"
	RoleEditor = "editor"
	RoleAdmin  = "admin"
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

// RuleSet: a rule set read (LoadRules) or the empty one (Empty). Safe for
// concurrent use: it does not change once read. The zero value holds no
// rule: its tables are empty and its checks fail (ErrNotLoaded), never a
// rule passed.
type RuleSet struct {
	loaded  bool
	version string
	tables  map[string]*ruleTable
	sets    M // each table as a set, as the checks read it
	lists   M // each table as a list, as the checks read it
	sources map[string]compile.Source
	free    chan *checker

	platformSchema, mappingSchema *chart.Schema        // what SchemaValues reads; nil, none
	fragments                     []Fragment           // the functions, by name
	generatedSecret               Template             // the objects of a generated Secret
	names                         map[string]string    // the names the compiler gives credentials
	keep                          KeepSync             // what Argo CD is told of the objects Helm keeps
	documentFuncs                 []DocumentFunc       // the functions a document computes, by name
	sections                      []string             // the sections of a mapping
	charts                        map[string]chart.Ref // the defaults of global.okdp.charts, by name
	ownObjects                    []OwnObjectsSpec     // the sections whose items add objects
	selectorLabels, commonLabels  map[string]string
	componentLabel                string
	wholePaths                    [][]string // TableWholePaths, split
}

// Loaded: r was read (LoadRules) or is Empty: false for the zero value.
func (r *RuleSet) Loaded() bool { return r != nil && r.loaded }

// PlatformSchema: the schema of global.okdp; nil, none (Empty).
func (r *RuleSet) PlatformSchema() *chart.Schema { return r.platformSchema }

// MappingSchema: the schema of a mapping's sections; nil, none (Empty).
func (r *RuleSet) MappingSchema() *chart.Schema { return r.mappingSchema }

// Fragments: the functions of mappings, a template each, by name.
func (r *RuleSet) Fragments() []Fragment { return r.fragments }

// GeneratedSecret: the template of the objects of a generated Secret.
func (r *RuleSet) GeneratedSecret() Template { return r.generatedSecret }

// Keep: what Argo CD is told of the objects Helm keeps.
func (r *RuleSet) Keep() KeepSync { return r.keep }

// DocumentFuncs: the functions of mappings a document computes, by name.
func (r *RuleSet) DocumentFuncs() []DocumentFunc { return r.documentFuncs }

// Sections: the sections of a mapping the rule set adds.
func (r *RuleSet) Sections() []string { return r.sections }

// Charts: the defaults of global.okdp.charts, by name; not to be changed.
func (r *RuleSet) Charts() map[string]chart.Ref { return r.charts }

// OwnObjects: the sections whose items add objects.
func (r *RuleSet) OwnObjects() []OwnObjectsSpec { return r.ownObjects }

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

// OwnObjectsSpec: a section whose items add objects: list, the document
// that gives them; objects, the template of the objects of each it renders.
type OwnObjectsSpec struct {
	Section, List string
	Objects       Template
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

// readSchema: the JSON schema file of fsys, parsed once, as SchemaValues
// reads it.
func readSchema(fsys fs.FS, file string) (*chart.Schema, error) {
	t, err := readTemplate(fsys, file)
	if err != nil {
		return nil, err
	}
	s, err := chart.ParseSchema(t.Name, t.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", RulesFile, err)
	}
	return s, nil
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
	byName := map[string]Template{}
	for name, file := range templates {
		if byName[name], err = readTemplate(fsys, value.Str(file)); err != nil {
			return err
		}
	}
	r.names = stringMap(doc["names"])
	k := value.AsMap(doc["keepSync"])
	r.keep = KeepSync{Annotation: value.Str(k["annotation"]), Value: value.Str(k["value"]), SyncOptions: value.Str(k["syncOptions"])}
	for _, o := range value.AsList(k["options"]) {
		r.keep.Options = append(r.keep.Options, value.Str(o))
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
	for _, v := range value.AsList(doc["ownObjects"]) {
		m := value.AsMap(v)
		spec := OwnObjectsSpec{Section: value.Str(m["section"]), List: value.Str(m["list"])}
		t, ok := byName[value.Str(m["objects"])]
		if !ok {
			return fmt.Errorf("%s: ownObjects: %s: objects %s is no template", RulesFile, spec.Section, value.Str(m["objects"]))
		}
		spec.Objects = t
		if _, ok := documents[spec.List]; !ok {
			return fmt.Errorf("%s: ownObjects: %s: list %s is no document", RulesFile, spec.Section, spec.List)
		}
		r.ownObjects = append(r.ownObjects, spec)
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
			r.documentFuncs = append(r.documentFuncs, DocumentFunc{name, d, value.Str(spec["result"])})
			continue
		}
		t, err := readTemplate(fsys, value.Str(spec["fragment"]))
		if err != nil {
			return err
		}
		r.fragments = append(r.fragments, Fragment{Name: name, Template: t, Result: value.Str(spec["result"])})
	}
	return nil
}

// Version: the version the rule set gives itself.
func (r *RuleSet) Version() string { return r.version }

// table: the table name; an empty one for a table the rule set does not
// hold (the zero value, a name of no table).
func (r *RuleSet) table(name string) *ruleTable {
	if r != nil {
		if t, ok := r.tables[name]; ok {
			return t
		}
	}
	return &ruleTable{}
}

// Has: whether v is in the table.
func (r *RuleSet) Has(table, v string) bool { return r.table(table).set[v] }

// List: the values of the table, in order; not to be changed.
func (r *RuleSet) List(table string) []string { return r.table(table).values }

// Added: what the platform adds to the table, the list of okdp, its
// settings, at the table's addFrom.
func (r *RuleSet) Added(table string, okdp M) map[string]bool {
	out := map[string]bool{}
	for _, v := range value.AsList(value.Dig(okdp, r.table(table).addFrom)) {
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
	schema, err := rulesSchema()
	if err != nil {
		return nil, err
	}
	doc, err := SchemaValues(schema, RulesFile+" doesn't meet the specifications of rules.schema.json", user)
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
	r := newRuleSet()
	r.version = value.Str(doc["version"])
	for _, name := range ruleTables {
		t := value.AsMap(tables[name])
		var values []string
		for _, v := range value.AsList(t["values"]) {
			values = append(values, value.Str(v))
		}
		r.addTable(name, values, value.Str(t["addFrom"]))
	}
	r.splitWholePaths()
	for _, role := range r.List(TableProjectRoles) {
		if !slices.Contains([]string{RoleViewer, RoleEditor, RoleAdmin}, role) {
			return nil, fmt.Errorf("%s: tables: %s: %q is no role: %s, %s or %s", RulesFile, TableProjectRoles, role, RoleViewer, RoleEditor, RoleAdmin)
		}
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
	if component != "" && r.componentLabel != "" {
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

// Empty: the rule set of a compile that gives none: no check refuses, no
// schema reads the values, no function nor section is added. What is left is
// the claim format's own: the names of the Secrets that carry credentials,
// a project's classes, and what a level may not change of a lower one.
func Empty() *RuleSet { return emptySet() }

// emptySet: Empty's rule set, built once.
var emptySet = sync.OnceValue(func() *RuleSet {
	r := newRuleSet()
	r.names = claimNames
	for _, name := range ruleTables {
		r.addTable(name, claimTables[name], "")
	}
	r.splitWholePaths()
	return r
})

// claimNames: the names Empty gives credentials.
var claimNames = map[string]string{
	"credentialsSecret": "$output-$contract-credentials",
	"connectionSecret":  "$release.$connection-credentials",
	"claimSecret":       "$release-$secret",
	"credentialsLabel":  "okdp.io/credentials-$contract",
}

// claimTables: the values of the tables of Empty; every other table is empty.
var claimTables = map[string][]string{
	TablePlatformKeysAbove: {"provisioning"},
	TableWholePaths:        {"connections.*", "claims.*", "global.okdp.provisioning.*.classes.*", "global.okdp.provisioning.*.default"},
}

// newRuleSet: a rule set without tables.
func newRuleSet() *RuleSet {
	return &RuleSet{loaded: true, tables: map[string]*ruleTable{}, sets: M{}, lists: M{}}
}

// addTable: the table name of the rule set, and its forms the checks read.
func (r *RuleSet) addTable(name string, values []string, addFrom string) {
	rt := &ruleTable{values: values, set: make(map[string]bool, len(values)), addFrom: addFrom}
	set := make(M, len(values))
	list := make([]any, len(values))
	for i, v := range values {
		rt.set[v], set[v], list[i] = true, true, v
	}
	r.tables[name], r.sets[name], r.lists[name] = rt, set, list
}

// splitWholePaths: TableWholePaths as Whole reads it.
func (r *RuleSet) splitWholePaths() {
	for _, p := range r.List(TableWholePaths) {
		r.wholePaths = append(r.wholePaths, strings.Split(p, "."))
	}
}

// SchemaValues: user, values a schema describes, coalesced over the schema's
// defaults and checked against it (chart.Schema.Values), as Helm reads a
// chart's: the values, or every violation at once, by path, under heading
// (a *SchemaError). No schema (Empty): user as it is.
func SchemaValues(schema *chart.Schema, heading string, user M) (M, error) {
	if schema == nil {
		return user, nil
	}
	values, v, err := schema.Values(user)
	if err != nil {
		return nil, err
	}
	if v != "" {
		return nil, &SchemaError{Heading: heading, Violations: v}
	}
	return values, nil
}

// SchemaError: values that violate a schema (SchemaValues): the heading,
// then the violations, one line each, as Helm prints them.
type SchemaError struct {
	Heading, Violations string
}

// Error: the heading, a colon, and the violations on the lines below it.
func (e *SchemaError) Error() string { return e.Heading + ":\n" + e.Violations }

// Kept: annotations say Helm keeps the object, as Helm reads it: trimmed, in
// any case.
func (k KeepSync) Kept(annotations M) bool {
	return k.Annotation != "" && strings.EqualFold(strings.TrimSpace(value.Str(annotations[k.Annotation])), k.Value)
}
