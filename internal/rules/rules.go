// Rules say what the compiler refuses and what it knows of a platform's
// names. They are given as data (RuleSet); the walkers that apply them stay
// in Go.

package rules

import (
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/value"
)

// RulesFile is the file of a rule set, at the root of its file system.
const RulesFile = "rules.yaml"

// The tables of the mandatory rule set. LoadRules requires every one of
// them: a table left out would silently turn its rule off. A set added to
// it gives tables of other names besides.
const (
	TableEngineNamespaces       = "engineNamespaces"
	TableClusterScopedKinds     = "clusterScopedKinds"
	TableDeployerAPIGroups      = "deployerApiGroups"
	TablePlatformObjectNames    = "platformObjectNames"
	TablePlatformObjectSettings = "platformObjectSettings"
	TableDefaultProjectGlobals  = "defaultProjectGlobals"
	TableWholePaths             = "wholePaths"
	TableProjectRoles           = "projectRoles"
	TablePlatformKeysAbove      = "projectPlatformKeys"
)

// The roles a project's group may hold (TableProjectRoles). The code of the
// targets says what each may do, so a rule set lists some of these only.
const (
	RoleViewer = "viewer"
	RoleEditor = "editor"
	RoleAdmin  = "admin"
)

// ruleTables lists the names of the tables of the mandatory set, sorted.
var ruleTables = []string{
	TableClusterScopedKinds, TableDefaultProjectGlobals, TableDeployerAPIGroups, TableEngineNamespaces,
	TablePlatformObjectNames, TablePlatformObjectSettings, TablePlatformKeysAbove, TableProjectRoles, TableWholePaths,
}

// ruleTable is a table of a rule set.
type ruleTable struct {
	values []string // in order
	// extensible says a set read later may add values to the table (add).
	extensible bool
}

// RuleSet is the rules a compile applies: the mandatory set alone
// (Mandatory), its checks among them, or with a set added to it
// (LoadRules). It does not change once read, so it is safe for concurrent
// use. The checks of the zero value fail (ErrNotLoaded): they never count as
// a rule passed.
type RuleSet struct {
	loaded  bool
	named   []namedSet // the sets read, in order
	tables  map[string]*ruleTable
	sets    M // each table as a set, as the checks read it (ready)
	lists   M // each table as a list, as the checks read it (ready)
	sources map[string]compile.Source
	free    chan *checker

	platformSchema, mappingSchema *chart.Schema        // what SchemaValues reads; nil, none
	fragments                     []Fragment           // the functions, by name
	templates                     map[string]Template  // the templates, by name
	names                         map[string]string    // the names the compiler gives credentials
	keep                          KeepSync             // what Argo CD is told of the objects Helm keeps
	documentFuncs                 []DocumentFunc       // the functions a document computes, by name
	sections                      []string             // the sections of a mapping
	charts                        map[string]chart.Ref // the defaults of global.okdp.charts, by name
	ownObjects                    []OwnObjectsSpec     // the sections whose items add objects
	selectorLabels, commonLabels  map[string]string
	componentLabel                string
	wholePaths                    [][]string          // TableWholePaths, split
	hooks                         map[string][]string // the documents of each hook, in the order the sets list them

	schemaFiles map[string]*schemaFile // the schemas as the sets give them, by key
	scopes      []string               // the scope documents, in order
}

// Loaded reports whether r was read. It is false for the zero value.
func (r *RuleSet) Loaded() bool { return r != nil && r.loaded }

// PlatformSchema returns the schema of global.okdp.
func (r *RuleSet) PlatformSchema() *chart.Schema { return r.platformSchema }

// MappingSchema returns the schema of a mapping's sections, nil when none.
func (r *RuleSet) MappingSchema() *chart.Schema { return r.mappingSchema }

// Fragments returns the functions of mappings that are a template each.
func (r *RuleSet) Fragments() []Fragment { return r.fragments }

// Fragment returns the fragment function name, if r has one.
func (r *RuleSet) Fragment(name string) (Fragment, bool) {
	i := slices.IndexFunc(r.fragments, func(f Fragment) bool { return f.Name == name })
	if i < 0 {
		return Fragment{}, false
	}
	return r.fragments[i], true
}

// Keep returns what Argo CD is told of the objects Helm keeps.
func (r *RuleSet) Keep() KeepSync { return r.keep }

// DocumentFuncs returns the functions of mappings that a document computes.
func (r *RuleSet) DocumentFuncs() []DocumentFunc { return r.documentFuncs }

// Sections returns the sections of a mapping that the rule set adds.
func (r *RuleSet) Sections() []string { return r.sections }

// Charts returns the defaults of global.okdp.charts, by name. The caller
// must not change the map.
func (r *RuleSet) Charts() map[string]chart.Ref { return r.charts }

// OwnObjects returns the sections whose items add objects.
func (r *RuleSet) OwnObjects() []OwnObjectsSpec { return r.ownObjects }

// Whole reports whether the path keys is one a level gives whole
// (TableWholePaths). A "*" of a path of the table stands for any key.
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

// DocumentFunc is a function of mappings that a document computes. The
// document reads the argument as arg, and its value at Result is returned.
type DocumentFunc struct{ Name, Document, Result string }

// OwnObjectsSpec is a section whose items add objects. The document List
// gives the items, and Objects renders the objects of each.
type OwnObjectsSpec struct {
	Section, List string
	Objects       Template
}

// Template is a template file. Name is the first segment of its errors.
type Template struct {
	Name string
	Data []byte
}

// Fragment is a function of mappings: a template evaluated on its
// arguments, checked against the mapping schema. Result names its value.
type Fragment struct {
	Name     string
	Template Template
	Result   string
}

// KeepSync says how to tell Argo CD of the objects Helm keeps. Annotation
// and Value say Helm keeps an object. SyncOptions is the annotation of Argo
// CD's sync options, and Options are those to add.
type KeepSync struct {
	Annotation, Value, SyncOptions string
	Options                        []string
}

func readTemplate(fsys fs.FS, file string) (Template, error) {
	b, err := fs.ReadFile(fsys, path.Clean(file))
	if err != nil {
		return Template{}, fmt.Errorf("%s: %w", RulesFile, err)
	}
	return Template{Name: path.Base(file), Data: b}, nil
}

// loadNames reads names, keepSync and labels, which one set only may give,
// and sections, which each set adds to.
func (r *RuleSet) loadNames(doc M) error {
	if names, ok := doc["names"]; ok {
		if r.names != nil {
			return taken("names", "")
		}
		r.names = stringMap(names)
	}
	if k, ok := doc["keepSync"]; ok {
		if r.keep.Annotation != "" {
			return taken("keepSync", "")
		}
		m := value.AsMap(k)
		r.keep = KeepSync{Annotation: value.Str(m["annotation"]), Value: value.Str(m["value"]), SyncOptions: value.Str(m["syncOptions"])}
		for _, o := range value.AsList(m["options"]) {
			r.keep.Options = append(r.keep.Options, value.Str(o))
		}
	}
	if l, ok := doc["labels"]; ok {
		if r.componentLabel != "" {
			return taken("labels", "")
		}
		labels := value.AsMap(l)
		r.componentLabel, r.selectorLabels, r.commonLabels = value.Str(labels["component"]), stringMap(labels["selector"]), stringMap(labels["common"])
	}
	for _, v := range value.AsList(doc["sections"]) {
		if slices.Contains(r.sections, value.Str(v)) {
			return taken("sections", value.Str(v))
		}
		r.sections = append(r.sections, value.Str(v))
	}
	return nil
}

func (r *RuleSet) loadCharts(doc M) error {
	charts := value.AsMap(doc["charts"])
	for _, name := range slices.Sorted(maps.Keys(charts)) {
		if _, ok := r.charts[name]; ok {
			return taken("charts", name)
		}
		m := value.AsMap(charts[name])
		ref := chart.Ref{Repository: value.Str(m["repository"]), Chart: value.Str(m["chart"]), Version: value.Str(m["version"])}
		if err := ref.Check(RulesFile + ": charts." + name); err != nil {
			return err
		}
		r.charts[name] = ref
	}
	return nil
}

// loadOwnObjects needs the templates and documents of the sets read so far.
func (r *RuleSet) loadOwnObjects(doc M) error {
	for _, v := range value.AsList(doc["ownObjects"]) {
		m := value.AsMap(v)
		spec := OwnObjectsSpec{Section: value.Str(m["section"]), List: value.Str(m["list"])}
		if slices.ContainsFunc(r.ownObjects, func(o OwnObjectsSpec) bool { return o.Section == spec.Section }) {
			return taken("ownObjects", spec.Section)
		}
		t, ok := r.templates[value.Str(m["objects"])]
		if !ok {
			return fmt.Errorf("%s: ownObjects: %s: objects %s is no template", RulesFile, spec.Section, value.Str(m["objects"]))
		}
		spec.Objects = t
		if _, ok := r.sources[spec.List]; !ok {
			return fmt.Errorf("%s: ownObjects: %s: list %s is no document", RulesFile, spec.Section, spec.List)
		}
		r.ownObjects = append(r.ownObjects, spec)
	}
	return nil
}

// loadFunctions needs the documents of the sets read so far.
func (r *RuleSet) loadFunctions(fsys fs.FS, doc M) error {
	functions := value.AsMap(doc["functions"])
	for _, name := range slices.Sorted(maps.Keys(functions)) {
		if r.hasFunction(name) {
			return taken("functions", name)
		}
		spec := value.AsMap(functions[name])
		if d := value.Str(spec["document"]); d != "" {
			if _, ok := r.sources[d]; !ok {
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

func (r *RuleSet) hasFunction(name string) bool {
	_, ok := r.Fragment(name)
	return ok || slices.ContainsFunc(r.documentFuncs, func(f DocumentFunc) bool { return f.Name == name })
}

// taken is the error of a rule that a set read before already gives: the
// rule name of section, or the whole section when name is empty.
func taken(section, name string) error {
	if name != "" {
		section += ": " + name
	}
	return fmt.Errorf("%s: %s: given by another rule set, the compiler's mandatory one: a rule set adds rules, it replaces none", RulesFile, section)
}

// Version returns the version the last set read gives itself: the
// mandatory set's alone, the added set's with one (SetVersion for each).
func (r *RuleSet) Version() string {
	if r == nil || len(r.named) == 0 {
		return ""
	}
	return r.named[len(r.named)-1].version
}

// table returns the named table, an empty one when r does not hold it.
func (r *RuleSet) table(name string) *ruleTable {
	if r != nil {
		if t, ok := r.tables[name]; ok {
			return t
		}
	}
	return &ruleTable{}
}

// Has reports whether v is in the table.
func (r *RuleSet) Has(table, v string) bool { return slices.Contains(r.List(table), v) }

// List returns the values of the table, in order, not to be changed.
func (r *RuleSet) List(table string) []string { return r.table(table).values }

// M is a values map.
type M = value.M

//go:embed rules.schema.json
var rulesSchemaFile []byte

var rulesSchema = sync.OnceValues(func() (*chart.Schema, error) {
	return chart.ParseSchema("rules.schema.json", rulesSchemaFile)
})

//go:embed mandatory
var mandatoryFiles embed.FS

func mandatoryFS() fs.FS {
	sub, err := fs.Sub(mandatoryFiles, "mandatory")
	if err != nil {
		panic(err)
	}
	return sub
}

// Mandatory returns the compiler's mandatory rule set alone, read once:
// every table, every check and the schemas, what every compile applies.
func Mandatory() *RuleSet { return mandatorySet() }

// mandatorySet panics on an error: the tests check what it embeds.
var mandatorySet = sync.OnceValue(func() *RuleSet {
	r, err := loadOver(mandatoryFS(), nil)
	if err != nil {
		panic(err)
	}
	return r
})

// LoadRules reads the rule set of fsys, with RulesFile at its root, and
// adds it to the mandatory one. It fails closed: a rule the mandatory set
// already gives, its checks and tables among them, a check that is
// unknown, and values added to a table that is not extensible, are errors,
// never a rule turned off or replaced. An empty table is written
// values: [].
func LoadRules(fsys fs.FS) (*RuleSet, error) { return loadOver(mandatoryFS(), fsys) }

// loadOver reads the mandatory set of base, then the set of fsys added to
// it, if any.
func loadOver(base, fsys fs.FS) (*RuleSet, error) {
	r := newRuleSet()
	if err := r.add(base, true); err != nil {
		return nil, fmt.Errorf("the mandatory rule set: %w", err)
	}
	if fsys != nil {
		if err := r.add(fsys, false); err != nil {
			return nil, err
		}
	}
	if err := r.parseSchemas(); err != nil {
		return nil, err
	}
	if err := r.complete(); err != nil {
		return nil, err
	}
	if err := r.ready(); err != nil {
		return nil, err
	}
	return r, nil
}

// add adds the set of fsys to r. It refuses a rule that a set read before
// already gives: the sets are disjoint, so that all of them apply. The
// mandatory set is the first read.
func (r *RuleSet) add(fsys fs.FS, mandatory bool) error {
	b, err := fs.ReadFile(fsys, RulesFile)
	if err != nil {
		return fmt.Errorf("the rule set: %w", err)
	}
	user, err := chart.ParseValues(b)
	if err != nil {
		return fmt.Errorf("%s: %w", RulesFile, err)
	}
	schema, err := rulesSchema()
	if err != nil {
		return err
	}
	doc, err := SchemaValues(schema, RulesFile+" doesn't meet the specifications of rules.schema.json", user)
	if err != nil {
		return err
	}
	checks := value.AsMap(doc["checks"])
	if err := knownKeys("checks", checks, ruleChecks); err != nil {
		return err
	}
	if err := r.addSet(value.Str(doc["name"]), value.Str(doc["version"])); err != nil {
		return err
	}
	if err := r.loadTables(value.AsMap(doc["tables"]), mandatory); err != nil {
		return err
	}
	if err := r.loadChecks(fsys, checks, value.AsMap(doc["documents"])); err != nil {
		return err
	}
	if err := r.loadHooks(value.AsMap(doc["hooks"])); err != nil {
		return err
	}
	schemas := value.AsMap(doc["schemas"])
	if err := r.loadSchema(fsys, schemas, schemaPlatform); err != nil {
		return err
	}
	if err := r.loadSchema(fsys, schemas, schemaMapping); err != nil {
		return err
	}
	if err := r.loadScopes(doc); err != nil {
		return err
	}
	templates := value.AsMap(doc["templates"])
	for _, name := range slices.Sorted(maps.Keys(templates)) {
		if _, ok := r.templates[name]; ok {
			return taken("templates", name)
		}
		t, err := readTemplate(fsys, value.Str(templates[name]))
		if err != nil {
			return err
		}
		r.templates[name] = t
	}
	if err := r.loadNames(doc); err != nil {
		return err
	}
	if err := r.loadCharts(doc); err != nil {
		return err
	}
	if err := r.loadOwnObjects(doc); err != nil {
		return err
	}
	return r.loadFunctions(fsys, doc)
}

// loadTables reads the tables of a set. The mandatory set gives those of
// ruleTables, no other. A set added to it gives tables of any name that no
// set read before gives, and adds values to a table marked extensible
// (add): it never removes one.
func (r *RuleSet) loadTables(tables M, mandatory bool) error {
	if mandatory {
		if err := knownKeys("tables", tables, ruleTables); err != nil {
			return err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(tables)) {
		t := value.AsMap(tables[name])
		before, given := r.tables[name]
		if add, ok := t["add"]; ok {
			switch {
			case !given:
				return fmt.Errorf("%s: tables: %s: add: no rule set read before gives the table", RulesFile, name)
			case !before.extensible:
				return fmt.Errorf("%s: tables: %s: add: the table is not extensible: a rule set adds values to the tables marked extensible only", RulesFile, name)
			}
			for _, v := range stringList(add) {
				if !slices.Contains(before.values, v) {
					before.values = append(before.values, v)
				}
			}
			continue
		}
		if given {
			return taken("tables", name)
		}
		r.tables[name] = &ruleTable{values: stringList(t["values"]), extensible: value.Truthy(t["extensible"])}
	}
	return nil
}

// stringList returns the list v as strings.
func stringList(v any) []string {
	var out []string
	for _, x := range value.AsList(v) {
		out = append(out, value.Str(x))
	}
	return out
}

// complete checks that the sets read give, together, every table, every
// check, the mapping schema and the objects chart.
func (r *RuleSet) complete() error {
	var missing []string
	for _, name := range ruleTables {
		if _, ok := r.tables[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: tables: missing %s", RulesFile, strings.Join(missing, ", "))
	}
	for _, name := range ruleChecks {
		if _, ok := r.sources[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: checks: missing %s", RulesFile, strings.Join(missing, ", "))
	}
	if r.mappingSchema == nil {
		return fmt.Errorf("%s: schemas: mapping is required", RulesFile)
	}
	if _, ok := r.charts["objects"]; !ok {
		return fmt.Errorf("%s: charts: objects is required", RulesFile)
	}
	return nil
}

// ready finishes the rule set once every set is read: the tables as the
// checks read them, the whole paths split. It prepares the checks and the
// documents together, so that each can call any other, and keeps that
// session for the first compile.
func (r *RuleSet) ready() error {
	for _, role := range r.List(TableProjectRoles) {
		if !slices.Contains([]string{RoleViewer, RoleEditor, RoleAdmin}, role) {
			return fmt.Errorf("%s: tables: %s: %q is no role: %s, %s or %s", RulesFile, TableProjectRoles, role, RoleViewer, RoleEditor, RoleAdmin)
		}
	}
	r.sets, r.lists = make(M, len(r.tables)), make(M, len(r.tables))
	for name, t := range r.tables {
		set, list := make(M, len(t.values)), make([]any, len(t.values))
		for i, v := range t.values {
			set[v], list[i] = true, v
		}
		r.sets[name], r.lists[name] = set, list
	}
	for _, p := range r.List(TableWholePaths) {
		r.wholePaths = append(r.wholePaths, strings.Split(p, "."))
	}
	first, err := prepare(r.sources)
	if err != nil {
		return fmt.Errorf("%s: checks: %w", RulesFile, err)
	}
	r.free = make(chan *checker, runtime.GOMAXPROCS(0))
	r.free <- first
	return nil
}

// Name returns the name of the given kind, from the rule set's names. pairs
// alternates a word and the value that $<word> stands for in the name.
func (r *RuleSet) Name(kind string, pairs ...string) string {
	args := make([]string, 0, len(pairs))
	for i := 0; i < len(pairs); i += 2 {
		args = append(args, "$"+pairs[i], pairs[i+1])
	}
	return strings.NewReplacer(args...).Replace(r.names[kind])
}

func stringMap(v any) map[string]string {
	out := map[string]string{}
	for k, x := range value.AsMap(v) {
		out[k] = value.Str(x)
	}
	return out
}

// Labels returns the labels of the objects of the service name in release:
// those of the rule set, where $service and $release stand for name and
// release. With selector, Labels returns the stable subset alone.
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

// knownKeys refuses a key outside want: a rule no walker would apply.
func knownKeys(what string, m M, want []string) error {
	var unknown []string
	for _, name := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(want, name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("%s: %s: unknown %s", RulesFile, what, strings.Join(unknown, ", "))
	}
	return nil
}

func newRuleSet() *RuleSet {
	return &RuleSet{
		loaded: true, tables: map[string]*ruleTable{}, sources: map[string]compile.Source{}, templates: map[string]Template{},
		charts: map[string]chart.Ref{}, schemaFiles: map[string]*schemaFile{}, hooks: map[string][]string{},
	}
}

// SchemaValues coalesces user over the defaults of schema and checks the
// result against it, as Helm reads a chart's values. The error is heading,
// then every violation on the lines below it. Without a schema it returns
// user as it is.
func SchemaValues(schema *chart.Schema, heading string, user M) (M, error) {
	if schema == nil {
		return user, nil
	}
	values, v, err := schema.Values(user)
	if err != nil {
		return nil, err
	}
	if v != "" {
		return nil, fmt.Errorf("%s:\n%s", heading, v)
	}
	return values, nil
}

// Kept reports whether annotations say Helm keeps the object. It reads the
// annotation as Helm does: trimmed, in any case.
func (k KeepSync) Kept(annotations M) bool {
	return k.Annotation != "" && strings.EqualFold(strings.TrimSpace(value.Str(annotations[k.Annotation])), k.Value)
}
