// Package compiler compiles an OKDP service instance into the values of
// its upstream charts. It reads a package from any fs.FS (LoadDefinition),
// compiles it for one instance (Compile), and gives the files that
// okdp-compile writes (Write).
//
//	def, err := compiler.LoadDefinition(os.DirFS("db/catalog/myservice/1.0.0"), nil)
//	res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
//		Contracts: os.DirFS("contracts"),
//		Values:    []map[string]any{settings, instanceValues},
//		Release:   "demo-myservice",
//		Namespace: "demo",
//	})
//	files, err := compiler.Write(res) // -o <dir>
//
// A package is a Helm chart. Each dependency of its Chart.yaml is the
// upstream chart that renders the values of its key, and a deployer
// renders that chart with the final values (Result.Charts). A compile
// reads no upstream chart: fetching the charts and checking the final
// values against their defaults and schemas belong to the host, in its
// Finalize (Inputs.Finalize).
//
// CompileAll compiles instances together, each with what the others
// publish, and binds their claims to their providers. LoadInputs reads
// instances from input documents laid out in any files, and LoadCatalog
// the packages and contracts they name. How the files of a deployment are
// laid out, and writing them for a deployer engine (Flux, Argo CD), belong
// to the host. Every compile applies the compiler's mandatory rule set;
// Inputs.Rules adds a platform's own sets to it (LoadRules), the policy
// among them: the compiler calls their hooks, and refuses what they
// refuse.
//
// Everything is read through fs.FS and in-memory values, so a caller can
// compile from a checkout, an embedded filesystem or a git tree without
// touching the disk.
//
// This package is the public API; the implementation is under internal/.
// A host gives settings of its own beside the values layers
// (Inputs.Settings): the compile reads them with global.okdp of the
// layers, and no chart receives them.
//
// Two packages are public beside it: chart, which is what the compiler
// reproduces of Helm, and value, the values they all share.
package compiler

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/catalog"
	"github.com/okdp/okdp-compiler/internal/inputs"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/value"
)

// The types a caller names. Each is documented in full on the type it
// aliases.
type (
	// Definition is a package as LoadDefinition reads it.
	Definition = okdp.Definition
	// Inputs is what a compile reads besides the package.
	Inputs = okdp.Inputs
	// RuleSet holds, as data, what the compiler refuses: its mandatory
	// rules and those a platform adds (LoadRules).
	RuleSet = okdp.RuleSet
	// Finalize makes the final values of a compile (Inputs.Finalize).
	Finalize = okdp.Finalize
	// FinalInput is what a Finalize makes the final values from.
	FinalInput = okdp.FinalInput
	// Result is what a compile produces, with its warnings.
	Result = okdp.Result
	// Info is what an instance publishes (Result.Info, InfoFile).
	Info = okdp.Info
	// Output is a connection an instance provides (Info.Provides).
	Output = okdp.Output
	// SecretRef names the Secret that holds an output's credentials.
	SecretRef = okdp.SecretRef
	// Dependency is an instance that a connection reference points to.
	Dependency = okdp.Dependency
	// Claim is a connection claimed from a provider's instance, whose
	// package provisions it (Inputs.Claims).
	Claim = okdp.Claim
	// Binding is the provider that a reference's claim is bound to
	// (Inputs.Bindings), with the output it publishes for the claim.
	Binding = okdp.Binding
	// Connection is an external connection, connections.<name> of the
	// values, as written (ParseConnection).
	Connection = okdp.Connection
	// ClaimSpec is a claim as written: a named claim, claims.<name> of the
	// values (ParseClaim), or the claim template of a reference.
	ClaimSpec = okdp.ClaimSpec
	// Instance is an instance that CompileAll compiles: its package, its
	// inputs and its name, by which a claim or a class names its provider.
	Instance = okdp.Instance
	// Outcome is the compile of one instance by CompileAll.
	Outcome = okdp.Outcome
	// Bound is a claim of a reference that CompileAll bound to a provider.
	Bound = okdp.Bound
	// CatalogFiles is one directory of a catalog (LoadCatalog): its name in
	// messages and its files, CatalogDir and ContractsDir.
	CatalogFiles = catalog.Files
	// Catalog holds the packages and contracts of a catalog (LoadCatalog).
	Catalog = catalog.Catalog
	// CatalogService is a service of a Catalog, its versions highest first.
	CatalogService = catalog.Service
	// InputFiles is a directory of input documents, or one file of it
	// (LoadInputs).
	InputFiles = inputs.Files
	// ChartRef is a pinned upstream chart, which renders a key's values.
	ChartRef = chart.Ref
	// ChartMetadata is a package's Chart.yaml.
	ChartMetadata = chart.Metadata
)

// The files Write gives besides Chart.yaml.
const (
	// ObjectsChart is the key of the umbrella's values and dependencies
	// that holds Result.Objects, when there are objects.
	ObjectsChart = okdp.ObjectsChart
	// ObjectsValue is the value of ObjectsChart: the objects, a YAML stream.
	ObjectsValue = okdp.ObjectsValue
)

// The directories of a catalog (LoadCatalog).
const (
	// ContractsDir holds the contracts, <contract>.schema.json.
	ContractsDir = catalog.ContractsDir
	// CatalogDir holds the packages, <service>/<version>/.
	CatalogDir = catalog.CatalogDir
)

// The kinds of the input documents (LoadInputs).
const (
	// KindSettings gives the values every instance receives first.
	KindSettings = inputs.KindSettings
	// KindProject is a namespace, and the values its instances receive.
	KindProject = inputs.KindProject
	// KindInstance is an instance: its project, its name, its package and
	// its values.
	KindInstance = inputs.KindInstance
)

// IsName reports whether name may name a project, an instance, a
// connection or a named claim: a DNS-1123 label. The compile checks names
// the same way.
func IsName(name string) bool { return chart.IsDNSLabel(name) }

// CheckNamespace returns nil when name may name a namespace of
// instances. A program that creates namespaces calls it first. The
// compile makes the same check, and refuses every instance of a namespace
// that fails it.
//
// values is a values layer whose global.okdp gives the settings, as
// written, or nil for none. The name must be a name (IsName) that the
// rule sets' namespace hook passes on with the settings: the mandatory set
// refuses platform and kube-*, the security set those the settings
// reserve (global.okdp.policy.reserved), a host's set the namespaces of
// its deployer engines.
func CheckNamespace(ctx context.Context, rules *RuleSet, name string, values map[string]any) error {
	v, err := value.Normalize(values)
	if err != nil {
		return fmt.Errorf("the values: %w", err)
	}
	return okdp.CheckNamespace(ctx, rules, "namespace", name, value.AsMap(value.Dig(value.AsMap(v), "global.okdp")))
}

// LoadDefinition reads and checks the package at the root of fsys for
// the compiles of rules: Chart.yaml, then okdp.yaml and its values file
// (values.cel.yaml or values.yaml) when the package has them. okdp.yaml
// may hold, besides its own keys, the sections the rule set adds to a
// mapping (rules.yaml's sections), and no other key. Nil rules stand for
// the mandatory set alone, as for Inputs.Rules. Each compile reads the
// other files of the package. A values.yaml that holds expressions is
// still read, with a warning.
func LoadDefinition(fsys fs.FS, rules *RuleSet) (*Definition, error) {
	return okdp.LoadDefinition(fsys, rules)
}

// Compile runs the mapping of def on one instance. ctx cancels it, between
// stages and within the evaluation of an expression. Compile keeps no
// state between calls, so it is safe for concurrent use.
//
// Whatever the package says, it enforces these rules
// (docs/04_threat_model.md):
//   - names: the release, the namespace and the instance name are labels,
//     and the rule sets' namespace and release hooks pass on them;
//   - settings: global.okdp of the values, with Inputs.Settings, meets
//     the schema of the rule sets, which requires the sets it names
//     (global.okdp.ruleSets);
//   - upstream values: they respect the package's protect and append
//     paths; the rule sets' finalValues hook passes on the final values of
//     each chart, and a host's Finalize adds its own policy;
//   - connections: each is of its contract, and the claims are those
//     CompileAll bound;
//   - objects: no two are the same, no provides secret is written in clear
//     unless Inputs.AllowPlainSecrets, and the rule sets' objects hook
//     passes on each;
//   - cost: the CEL cost of the expressions (Inputs.CostLimit) and the
//     time of the compile (Inputs.Timeout, ctx).
//
// What a platform's policy refuses besides (an object of a cluster-scoped
// kind or of another namespace, a Secret in clear, a namespace the
// platform reserves, but in its privileged projects) is the security
// set's, through the hooks: a compile without it accepts all of that.
func Compile(ctx context.Context, def *Definition, in Inputs) (*Result, error) {
	return okdp.Compile(ctx, def, in)
}

// CompileAll compiles instances together. It returns one Outcome per
// instance, in the order of instances; that order changes no outcome.
//
// It binds each claim of a reference to the instance that provisions the
// claim's contract: the provider the claim names, else the provider of
// its class. It compiles each instance after the instances whose outputs
// it references and after the providers of its claims, with what they
// publish. To that end it sets Inputs.Name, Published, Claims and
// Bindings itself.
//
// Beside the rules of Compile, it enforces those that need several
// instances: a release, a name or an output is unique in its namespace,
// a provider of another namespace is bound only through a class of the
// platform's or with its consent (Instance.Claimers), each Secret of a
// namespace has one writer, and the connections of every context are
// checked, referenced or not.
func CompileAll(ctx context.Context, instances []Instance) []Outcome {
	return okdp.CompileAll(ctx, instances)
}

// DefaultCostLimit is the CEL cost that the expressions of one instance
// may use, for a caller that compiles packages it has not reviewed. It is
// about ten times the cost of the dearest case of the catalogs.
const DefaultCostLimit = 1_000_000

// LoadRules reads the rule sets of sets, each rules.yaml at its root, and
// adds them to the compiler's mandatory one, in order: a compile applies
// all of them. A set never turns a rule off: it is an error for it to
// give a rule a set read before gives, or to bear the name of another. It
// adds to what those before it give where they allow it: an extensible
// table, a schema's properties, a hook's documents, the scopes.
func LoadRules(sets ...fs.FS) (*RuleSet, error) { return okdp.LoadRules(sets...) }

// Mandatory returns the compiler's mandatory rule set, read once: what a
// nil Inputs.Rules stands for, and what every set LoadRules gives holds.
// Its hooks refuse the names OKDP keeps on every platform.
func Mandatory() *RuleSet { return okdp.Mandatory() }

// ParseConnection reads v, the entry name of connections, as an external
// connection, as written. It checks nothing: the compile of the instances
// that reference a connection checks it.
func ParseConnection(name string, v any) Connection {
	return okdp.ParseConnection(name, v)
}

// ParseClaim reads v, the entry name of claims, as a named claim, as
// written. The error tells why the compile refuses the claim, if it
// does. Even then, the claim returned holds what v holds.
func ParseClaim(name string, v any) (ClaimSpec, error) {
	return okdp.ParseClaim(name, v)
}

// ErrNotInCatalog reports a service or a version that no catalog has.
// The errors of a Catalog's Service and Package wrap it.
var ErrNotInCatalog = catalog.ErrNotInCatalog

// LoadCatalog returns the catalog that dirs make together, each read for
// CatalogDir and ContractsDir alone: each service is in one directory
// with every version of it, and a contract may be in several with the
// same bytes. The order of dirs changes nothing. It reports every problem
// at once, each naming its directories.
func LoadCatalog(dirs []CatalogFiles) (*Catalog, error) { return catalog.Load(dirs) }

// LoadInputs reads the input documents of files and returns the instances
// they describe, sorted by project, then by name, ready for CompileAll
// (docs/specs/01_inputs_outputs.md, input documents). The files may be
// laid out in any way: each YAML document says what it is by its kind
// (KindSettings, KindProject, KindInstance), and the files a document
// names in valuesFiles are values files. The packages are cat's, read for
// rules as LoadDefinition reads them (nil: the mandatory set).
//
// An instance name of the project p is the release p-name of the
// namespace p. Every problem is reported at once.
func LoadInputs(ctx context.Context, files []InputFiles, cat *Catalog, rules *RuleSet) ([]Instance, error) {
	return inputs.Load(ctx, files, cat, rules)
}

// ParseValues reads a values file as Helm 3 does: its first document, as
// YAML 1.1, with string keys and every number a float64. The result fits
// Inputs.Values.
func ParseValues(b []byte) (map[string]any, error) {
	return chart.ParseValues(b)
}
