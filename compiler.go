// Package compiler compiles an OKDP service instance into the values of
// its upstream charts. It reads a package from any fs.FS (LoadDefinition),
// compiles it for one instance (Compile), and gives the files that
// okdp-compile writes (Write).
//
//	def, err := compiler.LoadDefinition(os.DirFS("db/catalog/myservice/1.0.0"), nil)
//	res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
//		Contracts: os.DirFS("contracts"),
//		Context:   []compiler.ContextLayer{{Values: platformValues}, {Level: 2, Values: instanceConnections}},
//		Values:    []map[string]any{instanceValues},
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
// publish. LoadRepository reads the instances of a tree, and Build
// compiles a tree as a CLI or a control plane asks for it. Writing a tree
// for a deployer engine (Flux, Argo CD) belongs to the host. Every
// compile applies the compiler's mandatory rule set, its checks among
// them; Inputs.Rules adds a platform's own set to it.
//
// Everything is read through fs.FS and in-memory values, so a caller can
// compile from a checkout, an embedded filesystem or a git tree without
// touching the disk.
//
// This package is the public API; the implementation is under internal/.
// Two packages are public beside it: chart, which is what the compiler
// reproduces of Helm, and value, the values they all share.
package compiler

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/repository"
	"github.com/okdp/okdp-compiler/internal/tree"
	"github.com/okdp/okdp-compiler/value"
)

// The types a caller names. Each is documented in full on the type it
// aliases.
type (
	// Definition is a package as LoadDefinition reads it.
	Definition = okdp.Definition
	// Inputs is what a compile reads besides the package.
	Inputs = okdp.Inputs
	// ContextLayer is a values layer of the context (Inputs.Context) with
	// its level. Level 0 is the platform's.
	ContextLayer = okdp.ContextLayer
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
	// context, as written (ParseConnection).
	Connection = okdp.Connection
	// ClaimSpec is a claim as written: a named claim of a project's
	// context (ParseClaim), or the claim template of a reference.
	ClaimSpec = okdp.ClaimSpec
	// Instance is an instance that CompileAll compiles: its package, its
	// inputs and its name, by which a claim or a class names its provider.
	Instance = okdp.Instance
	// Outcome is the compile of one instance by CompileAll.
	Outcome = okdp.Outcome
	// Bound is a claim of a reference that CompileAll bound to a provider.
	Bound = okdp.Bound
	// Repository is one part of a tree that LoadRepository reads: its
	// name in messages, its files and its Role, which is required.
	Repository = repository.Repository
	// Role is what a Repository contributes to the tree (RoleTree,
	// RoleCatalog). The zero value is refused.
	Role = repository.Role
	// Catalog holds the packages and contracts of a tree (LoadCatalog).
	Catalog = repository.Catalog
	// CatalogService is a service of a Catalog, its versions highest first.
	CatalogService = repository.Service
	// Graph is the deployment graph of compiled instances (NewGraph): what
	// each release waits for, and its Argo CD sync wave.
	Graph = graph.Graph
	// GraphNode is a release of the graph, in its namespace.
	GraphNode = graph.Node
	// GraphError is a problem of the graph, at its release.
	GraphError = graph.Error
	// Tree holds instances compiled together (CompileTree), in their
	// order, and the deployment graph of those that compile.
	Tree = tree.Tree
	// RefusedProject is a project of a Tree that does not load
	// (Tree.Refused), and why. It has no instance.
	RefusedProject = tree.Refused
	// TreeInstance is an instance of a Tree with its compile.
	TreeInstance = tree.Instance
	// ProvidedClaim is a claim bound to a provider, with its claimer.
	ProvidedClaim = tree.Provided
	// LoadError holds the problems of a tree that does not load, each a
	// whole message that names its repository and path.
	LoadError = repository.Error
	// KeptProject is a project whose output is not written (Partial).
	KeptProject = tree.Kept
	// KeptInstance is an instance that does not deploy, and why (Err).
	KeptInstance = tree.KeptInstance
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

// The roles of a Repository (Repository.Role).
const (
	// RoleTree marks a repository of the deployment, the platform's or a
	// project's. It may hold any part of the tree.
	RoleTree = repository.RoleTree
	// RoleCatalog marks a catalog of packages. It may hold CatalogDir and
	// ContractsDir only: a PlatformFile or a ProjectsDir is refused, so
	// that its maintainers can add neither a project nor platform values.
	RoleCatalog = repository.RoleCatalog
)

// The names of the tree that LoadRepository reads (docs/01_usage.md).
const (
	// PlatformFile holds the platform's values, the context at level 0.
	PlatformFile = repository.PlatformFile
	// ContractsDir holds the contracts, <contract>.schema.json.
	ContractsDir = repository.ContractsDir
	// CatalogDir holds the packages, <service>/<version>/.
	CatalogDir = repository.CatalogDir
	// ProjectsDir holds the projects, <project>/. Each is a namespace.
	ProjectsDir = repository.ProjectsDir
	// ProjectFile, in a project's directory, holds the project's values,
	// the context at level 1, plus two keys that are the project's own:
	// ProjectDescription and ProjectAccessKey.
	ProjectFile = repository.ProjectFile
	// ProjectDescription is the key of ProjectFile that describes the
	// project, a string. No instance receives it.
	ProjectDescription = repository.ProjectDescription
	// InstancesDir, in a project's directory, holds its instances,
	// <instance>/, each named after its directory.
	InstancesDir = repository.InstancesDir
	// InstanceFile, in an instance's directory, names its package:
	// {service, version}.
	InstanceFile = repository.InstanceFile
	// ConnectionsFile, in an instance's directory, holds the instance's
	// own connections, the context at level 2. It is optional.
	ConnectionsFile = repository.ConnectionsFile
	// ConfigFile, in an instance's directory, holds the instance's
	// values: its parameters and upstream values. It is optional.
	ConfigFile = repository.ConfigFile
)

// ReleaseName returns the release of an instance of the tree,
// <project>-<instance>. Its namespace is the project.
//
// The releases of a tree must be unique across its projects, because the
// HelmReleases and Applications of a deployer share one namespace.
// Project names that hold a "-" can give one release twice (a-b/c and
// a/b-c). The targets refuse that, as CheckReleases does.
func ReleaseName(project, instance string) string {
	return repository.ReleaseName(project, instance)
}

// IsName reports whether name may name a project, an instance, a
// connection or a named claim: a DNS-1123 label. The compile checks names
// the same way.
func IsName(name string) bool { return chart.IsDNSLabel(name) }

// CheckProjectName returns nil when name may name a project, that is its
// namespace. A program that creates projects calls it first. The compile
// makes the same check, and refuses every instance of a project that
// fails it.
//
// platform is the platform's values (PlatformFile), or nil for none. The
// name must be a name (IsName), and none that the compiler reserves
// (platform, kube-*, the namespaces of the deployer engines and of the
// targets) or that the platform reserves (global.okdp.policy.reserved).
//
// A namespace that a target writes in by its options is not refused
// here: the target refuses it when it writes the tree.
func CheckProjectName(ctx context.Context, rules *RuleSet, name string, platform map[string]any) error {
	v, err := value.Normalize(platform)
	if err != nil {
		return fmt.Errorf("the platform's values (%s): %w", PlatformFile, err)
	}
	return okdp.CheckNamespace(ctx, rules, "project", name, value.AsMap(value.Dig(value.AsMap(v), "global.okdp")))
}

// AccessRole is the role of a group within a project, set by the access
// of its project.yaml (ProjectAccessKey).
type AccessRole = okdp.Role

// The roles of a project's groups. Each may do what the one before it
// may, and more. The flux and argocd targets write them as Argo CD roles
// and as RoleBindings to Kubernetes' view, edit and admin
// (docs/specs/01_inputs_outputs.md, access).
const (
	// AccessViewer sees the project's Applications, and the objects of
	// its namespace except its Secrets.
	AccessViewer = okdp.RoleViewer
	// AccessEditor is a viewer who also syncs the Applications, runs
	// their actions and reads their logs, without exec, and who edits the
	// objects of the namespace.
	AccessEditor = okdp.RoleEditor
	// AccessAdmin is an editor who also deletes and overrides the
	// Applications, and administers the namespace.
	AccessAdmin = okdp.RoleAdmin
)

// ProjectAccessKey is access, the key of ProjectFile that maps the
// project's groups to their roles. No instance receives it.
const ProjectAccessKey = okdp.AccessKey

// DefaultProjectGroup returns okdp-project-<project>, the group of a
// project's people when its ProjectFile gives no access. That group is
// an editor.
func DefaultProjectGroup(project string) string { return okdp.DefaultGroup(project) }

// IsGroupName reports whether name may name a group, as the identity
// provider's tokens give it: a letter or a digit, then letters, digits,
// ".", "_", "@", ":" and "-", 255 characters at most. It allows no "/":
// the name is that of a Keycloak group, not its path.
func IsGroupName(name string) bool { return okdp.IsGroup(name) }

// PlatformAdmins returns the groups of the platform's administrators,
// global.okdp.access.admins of the platform's values, checked as the
// compile checks them. It returns none when the key is not set.
//
// The compiler writes nothing from them: the platform's bootstrap binds
// them.
func PlatformAdmins(ctx context.Context, rules *RuleSet, platform map[string]any) ([]string, error) {
	return okdp.PlatformAdmins(ctx, rules, platform)
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
//     and none is reserved by the platform or the compiler;
//   - values: the layers of an instance hold no connections, claims,
//     access nor template code, and no layer sets a key of global that
//     its level may not set, nor changes a value of a lower level;
//   - upstream values: they respect the package's protect and append
//     paths; for a project that is not privileged, the rule set's
//     finalValues hook passes on the final values of each chart, and a
//     host's Finalize adds its own policy;
//   - connections: each is of its contract, a store key is under the
//     project's own, and the claims are those CompileAll bound;
//   - objects, for a project that is not privileged: each is in the
//     instance's namespace, none is cluster-scoped or of an engine's API
//     group, and the rule set's objects hook passes on each;
//   - objects, for any project: no two are the same, and none holds a
//     credential in clear unless Inputs.AllowPlainSecrets;
//   - cost: the CEL cost of the expressions (Inputs.CostLimit) and the
//     time of the compile (Inputs.Timeout, ctx).
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

// CompileTree compiles instances together (CompileAll), then builds the
// deployment graph of those that compile (NewGraph). For each instance
// the Tree gives its outcome, the problem of the graph at its release
// and the claims bound to it as a provider. It does not change instances.
//
// The compile reads no upstream chart: a host that checks the final
// values against the charts does it in its Finalize (Inputs.Finalize).
func CompileTree(ctx context.Context, instances []Instance) *Tree {
	return tree.Compile(ctx, instances)
}

// LoadRepository returns every instance of the tree that repos make
// together (docs/01_usage.md, repository layout), by project then
// instance, as CompileAll compiles them. Each instance is named after
// its directory, and its namespace is its project.
//
// Each part of the tree is in one repository, so the order of repos
// changes nothing. Only a repository of RoleTree may hold platform.yaml
// and projects/. A part of the tree holds only the files and keys it may
// have.
//
// It reports every problem at once in a *LoadError. Any problem fails
// the whole load. Build is more lenient: it refuses only the project
// that does not load.
//
// The packages are read for the compiles of rules, as LoadDefinition
// reads them; nil rules stand for the mandatory set. Build reads them
// for BuildOptions.Rules.
func LoadRepository(ctx context.Context, repos []Repository, rules *RuleSet) ([]Instance, error) {
	return repository.Load(ctx, repos, rules)
}

// AddedProblems returns the problems of after that before does not have,
// in the order of after: what a change added to the reasons a tree does
// not load. A nil before has no problem.
func AddedProblems(before, after *LoadError) []string {
	return repository.Added(before, after)
}

// DefaultCostLimit is the CEL cost that the expressions of one instance
// may use, for a caller that compiles packages it has not reviewed. It is
// about ten times the cost of the dearest case of the catalogs.
const DefaultCostLimit = 1_000_000

// BuildOptions is what Build sets on each instance of a tree.
type BuildOptions struct {
	CostLimit uint64 // Inputs.CostLimit of each instance; 0 sets no limit
	// InstanceTimeout is Inputs.Timeout of each instance; 0 sets no
	// limit. Past it, that instance is refused and the others are still
	// compiled. The deadline of ctx bounds the whole tree beside it.
	InstanceTimeout   time.Duration
	AllowPlainSecrets bool // Inputs.AllowPlainSecrets of each instance
	// Rules is Inputs.Rules of each instance. Nil applies the mandatory
	// rules alone.
	Rules        *RuleSet
	Finalize     Finalize                                  // Inputs.Finalize; nil is Helm's merge
	CheckGlobals func(context.Context, GlobalsInput) error // Inputs.CheckGlobals; nil checks nothing
	// Loaded, when set, is given the instances of the projects that load,
	// before they compile, with ctx: what a host's Finalize reads that the
	// compiler does not (the upstream charts of their Chart.yaml, fetched
	// there so that no fetch counts against InstanceTimeout).
	// It does not change them.
	Loaded func(context.Context, []Instance)
}

// LoadRules reads the rule set of fsys, rules.yaml at its root, and adds
// it to the compiler's mandatory one: a compile applies both. The file
// never turns a rule off: it is an error for it to give a rule the
// mandatory set gives, or to name a check that is unknown.
func LoadRules(fsys fs.FS) (*RuleSet, error) { return okdp.LoadRules(fsys) }

// Mandatory returns the compiler's mandatory rule set, read once: what a
// nil Inputs.Rules or BuildOptions.Rules stands for, and what every set
// LoadRules gives holds. Its checks refuse what OKDP refuses of a
// platform.
func Mandatory() *RuleSet { return okdp.Mandatory() }

// Build loads the tree that repos make together (as LoadRepository reads
// it), sets opts on each instance and compiles the tree (CompileTree).
// ctx cancels it.
//
// A problem of the platform's parts (a repository, platform.yaml,
// catalogs.yaml, a catalog) fails the build with a *LoadError that lists
// every problem. A project that does not load is refused as a whole, but
// the tree is not: the project is listed in the tree's Refused and the
// other projects compile. A refused instance is not an error either: it
// is the tree's answer (TreeInstance.Err).
func Build(ctx context.Context, repos []Repository, opts BuildOptions) (*Tree, error) {
	loaded, err := repository.LoadProjects(ctx, repos, opts.Rules)
	if err != nil {
		return nil, err
	}
	instances := loaded.Instances
	for i := range instances {
		instances[i].Inputs.CostLimit = opts.CostLimit
		instances[i].Inputs.Timeout = opts.InstanceTimeout
		instances[i].Inputs.AllowPlainSecrets = opts.AllowPlainSecrets
		instances[i].Inputs.Finalize = opts.Finalize
		instances[i].Inputs.Rules = opts.Rules
		instances[i].Inputs.CheckGlobals = opts.CheckGlobals
	}
	if opts.Loaded != nil {
		opts.Loaded(ctx, slices.Clone(instances))
	}
	t := CompileTree(ctx, instances)
	for _, r := range loaded.Refused {
		r.Inputs.Rules = opts.Rules // CheckProject reads them for a project that does not load
		t.Refused = append(t.Refused, r)
	}
	return t, nil
}

// LoadErrorOf returns why the projects of t that do not load
// (Tree.Refused) do not, as the *LoadError that the whole tree would give
// with them. It is nil when every project loads.
func LoadErrorOf(t *Tree) *LoadError {
	if t == nil {
		return nil
	}
	var errs []error
	for _, r := range t.Refused {
		errs = append(errs, r.Errs...)
	}
	return repository.JoinErrors(errs)
}

// Partial splits the projects of t into those written and those kept.
// results are the compiled instances of the projects written, and g is
// their graph. kept holds the other projects, by namespace.
//
// A project is kept when one of its instances is refused, or is in also,
// which treats it as refused for the error it maps to (nil also keeps
// none this way). Then, round after round, a project is kept when one of
// its instances has a problem in the graph of the projects not kept yet,
// or depends on a release of a kept project: that instance was compiled
// with what the release publishes now, which is not deployed.
//
// g is nil only when the graph has a problem at no instance of t.
func Partial(t *Tree, also map[*TreeInstance]error) (results []*Result, g *Graph, kept []KeptProject) {
	return tree.Partial(t, also)
}

// Regressions returns the instances of after that do not deploy
// (TreeInstance.Err) though they did in before: what a change broke.
// Instances are matched by namespace and name, in the order of after.
func Regressions(before, after *Tree) []*TreeInstance {
	return tree.Regressions(before, after)
}

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
var ErrNotInCatalog = repository.ErrNotInCatalog

// LoadCatalog returns the catalog that repos make together, as
// LoadRepository reads it: each service is in one repository with every
// version of it, and a contract may be in several with the same bytes.
// It reports every problem at once, each naming its repositories.
func LoadCatalog(repos []Repository) (*Catalog, error) {
	return repository.LoadCatalog(repos)
}

// NewGraph builds the deployment graph of results, which are the compiled
// instances of their namespaces. A release waits for the releases its
// connection references point to (Result.Dependencies). NewGraph reports
// every problem at once, each a *GraphError: a dependency on a release
// that is missing or does not provide what is referenced, a cycle, a
// release given twice.
func NewGraph(results []*Result) (*Graph, error) {
	return graph.New(results)
}

// ParseValues reads a values file as Helm 3 does: its first document, as
// YAML 1.1, with string keys and every number a float64. The result fits
// Inputs.Context and Inputs.Values.
func ParseValues(b []byte) (map[string]any, error) {
	return chart.ParseValues(b)
}
