// Package compiler compiles an OKDP service instance into the values of its
// upstream charts. A package (Chart.yaml, okdp.yaml, values.cel.yaml or
// values.yaml, its parameters and OKDP's defaults for its charts) is read
// from any fs.FS (LoadDefinition), compiled for one instance (Compile), and
// written as the files of okdp-compile (Write):
//
//	def, err := compiler.LoadDefinition(os.DirFS("db/catalog/myservice/1.0.0"))
//	res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
//		Charts:    os.DirFS("charts"),
//		Contracts: os.DirFS("contracts"),
//		Context:   []compiler.ContextLayer{{Values: platformValues}, {Level: 2, Values: instanceConnections}},
//		Values:    []map[string]any{instanceValues},
//		Release:   "demo-myservice",
//		Namespace: "demo",
//	})
//	files, err := compiler.Write(res) // -o <dir>
//
// A package is a Helm chart: each dependency of its Chart.yaml is the
// upstream chart that renders the values of its key (repository, chart,
// version), and Result.Charts maps every compiled chart to it: a deployer
// renders the upstream chart from its repository, the final values over its
// defaults. A compile reads no chart; with a chart cache (Inputs.Charts), it
// checks the final values against each chart's defaults and schema. Write gives
// the instance as one chart, an umbrella over these upstream charts, its
// values and OKDP's own objects, and what the instance publishes, whose
// outputs the other instances of the namespace reference by name
// (Inputs.Published); the instances it depends on are for a deployer
// (Result.Dependencies). CompileAll compiles instances together, each with
// what the others publish (a tree's, LoadRepository), NewGraph orders
// their releases, the dependencies checked, and a Target writes them for
// a deployer.
//
// Everything is read through fs.FS and in-memory values, so a caller (a
// server, a CLI) can compile from a checkout, an embedded filesystem or a
// git tree without touching the disk.
//
// This package is the public API; the implementation is under internal/:
// OKDP's rules and the order of the stages (okdp), the tree of a
// deployment read (repository), the deployment graph of compiled
// instances (graph), the formats compiled instances are written in
// (target: helm, the umbrella of Write, flux, argocd), the mapping
// language (compile), what the compiler reproduces of Helm (chart), and
// the values they share (value).
package compiler

import (
	"context"
	"io/fs"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/repository"
	"github.com/okdp/okdp-compiler/internal/target"
	"github.com/okdp/okdp-compiler/internal/target/argocd"
	"github.com/okdp/okdp-compiler/internal/target/flux"
	"github.com/okdp/okdp-compiler/internal/target/helm"
	"github.com/okdp/okdp-compiler/internal/tree"
)

// The types a caller names.
type (
	// Definition: a package, as LoadDefinition reads it: Chart.yaml,
	// okdp.yaml, its values file, values.cel.yaml or values.yaml, and the
	// package's other files; its warnings (Warnings).
	Definition = okdp.Definition
	// Inputs: what a compile reads besides the package.
	Inputs = okdp.Inputs
	// ContextLayer: a values layer of the context (Inputs.Context) at its
	// level: 0, the platform's, then each level adds to those below it.
	ContextLayer = okdp.ContextLayer
	// Result: what a compile produces, its warnings among them (Warnings),
	// which do not fail it.
	Result = okdp.Result
	// Info: what an instance publishes (Result.Info), as
	// InfoFile holds it: its URL, its usage, the connections it
	// provides.
	Info = okdp.Info
	// Output: a connection an instance provides (Info.Provides).
	Output = okdp.Output
	// SecretRef: the Secret of an output's credentials, by name
	// (Output.SecretRef).
	SecretRef = okdp.SecretRef
	// Dependency: an instance a connection reference points to, by one of
	// its outputs or a claim (Result.Dependencies), what a deployer orders
	// the releases with.
	Dependency = okdp.Dependency
	// Claim: a connection claimed of a provider's instance, which its
	// package provisions (Inputs.Claims): it publishes an output of its
	// contract named its ID.
	Claim = okdp.Claim
	// Binding: the provider a reference's claim is bound to
	// (Inputs.Bindings), and the output it publishes for it.
	Binding = okdp.Binding
	// Connection: an external connection, connections.<name> of the
	// context, as written (ParseConnection): its contract, a description,
	// the contract's fields, the Secret or the key of the platform's secret
	// store its secret fields are in; it marshals to YAML as it is written.
	Connection = okdp.Connection
	// ClaimSpec: a claim, as written: a named claim, claims.<name> of a
	// project's context (ParseClaim), or a reference's claim template; its
	// tags the keys it is written with.
	ClaimSpec = okdp.ClaimSpec
	// Instance: an instance CompileAll compiles, its package and its
	// inputs, Name, Published, Claims and Bindings left out, and its name
	// (Instance.Name): a claim's or a class's provider names an instance
	// of its namespace by its name or by its release, <namespace>/<name>
	// or <name> of the claimer's namespace; its outputs that name none
	// and its claims' IDs are named after it (Inputs.Name). LoadRepository
	// names each after its directory; a caller that leaves Name empty
	// names it by its release only. A provider serves the claims of
	// another namespace through a class of the platform's, or those of the
	// namespaces it lists (Instance.Claimers: Inputs.Claimers, its
	// instance.yaml's, else its package's provisions').
	Instance = okdp.Instance
	// Outcome: the compile of one instance of CompileAll: its result, nil
	// when it is refused, and why; the claims of its references, bound.
	Outcome = okdp.Outcome
	// Bound: a claim of a reference, bound to its provider by CompileAll:
	// the one it names, or the provider of its class, the one it names or
	// the default one of the claimer's context (global.okdp.provisioning).
	Bound = okdp.Bound
	// Repository: a part of a tree LoadRepository reads: its name in
	// messages and its files.
	Repository = repository.Repository
	// Catalog: the packages and the contracts of a tree's repositories
	// together (LoadCatalog): its services (Services, Service), the files
	// of a package (Package), the contracts (Contracts).
	Catalog = repository.Catalog
	// CatalogService: a service of a Catalog, catalog/<service>/ of one
	// repository: its name, its repository's, its versions, highest first.
	CatalogService = repository.Service
	// Graph: the deployment graph of compiled instances (NewGraph): what
	// each release waits for (DependsOn), its Argo CD sync wave (Wave).
	Graph = graph.Graph
	// GraphNode: a release of the graph, in its namespace.
	GraphNode = graph.Node
	// GraphError: a problem of the graph, at the release it is found at.
	GraphError = graph.Error
	// Tree: instances compiled together (CompileTree), in their order, and
	// the deployment graph of those that compile: by instance (Instance,
	// Release), why each that does not deploy does not (Problems).
	Tree = tree.Tree
	// TreeInstance: an instance of a Tree and its compile: its Outcome,
	// the problem of the graph at its release (GraphErr), the claims bound
	// to it as a provider (Provided), what it granted each (Granted).
	TreeInstance = tree.Instance
	// ProvidedClaim: a claim bound to a provider (TreeInstance.Provided),
	// and the instance that claims it (Claimer).
	ProvidedClaim = tree.Provided
	// ChartSource: a chart cache that fetches the upstream charts it lacks
	// (Ensure), unpacked as Inputs.Charts reads them: what CompileTree
	// checks the final values against.
	ChartSource = tree.ChartSource
	// Target: an output format of compiled instances (Helm, Flux, ArgoCD):
	// their files, path -> content, under projects/, each instance's
	// umbrella chart in InstanceDir: all an engine needs of the compiled
	// tree, which a deployer commits as they are.
	Target = target.Target
	// Helm: the helm target: the umbrella chart of each instance, the files
	// of Write.
	Helm = helm.Target
	// Flux: the flux target: the helm target's files, each instance's Flux
	// HelmRelease, waiting for the releases it depends on, and the
	// Kustomizations, each instance's, each project's and the root's,
	// projects/kustomization.yaml, which a Flux Kustomization of the path
	// projects/ syncs; okdp-gitops' format. Its fields are its options.
	Flux = flux.Target
	// ArgoCD: the argocd target: the helm target's files, and each
	// instance's Argo CD Application, its sync wave after those of the
	// releases it depends on, which an Application of the directory
	// projects/ applies (recurse, include */instances/*/application.yaml);
	// okdp-gitops' format. Its fields are its options.
	ArgoCD = argocd.Target
	// ReleaseError: a release name in two namespaces or more, which the
	// Flux and ArgoCD targets refuse (CheckReleases): their objects, named
	// after the releases, share one namespace.
	ReleaseError = target.ReleaseError
	// ChartRef: an upstream chart, pinned: what renders the values of a key.
	ChartRef = chart.Ref
	// ChartMetadata: a package's Chart.yaml (Definition.Chart,
	// Result.Chart).
	ChartMetadata = chart.Metadata
)

// The files Write gives besides Chart.yaml.
const (
	// UmbrellaValues: the umbrella's values, given to Helm as user values
	// (helm -f, Argo CD valueFiles, Flux valuesFrom).
	UmbrellaValues = helm.ValuesFile
	// InfoFile: Result.Info, what the instance publishes (url,
	// usage, provides), written when it publishes something, for a control
	// plane or a console that reads the deployments repository.
	InfoFile = helm.InfoFile
	// ObjectsFile: Result.Objects as a YAML stream, one document per object,
	// written when there are objects: plain manifests, which ObjectsTemplate
	// renders as written in the umbrella's release.
	ObjectsFile = helm.ObjectsFile
	// ObjectsTemplate: the umbrella's template of ObjectsFile, a path in
	// the chart, written with it.
	ObjectsTemplate = helm.ObjectsTemplate
)

// DefaultFluxSource: the GitRepository of the flux target when
// Flux.Source is empty, okdp-gitops'.
const DefaultFluxSource = flux.DefaultSource

// The names of the tree LoadRepository reads (docs/01_usage.md, repository
// layout), for a caller that writes or lists its parts:
//
//	PlatformFile                                   the platform's values
//	ContractsDir/<contract>.schema.json            a contract
//	CatalogDir/<service>/<version>/                a package
//	ProjectsDir/<project>/ProjectFile              a project's values, its ProjectDescription
//	ProjectsDir/<project>/InstancesDir/<instance>/ an instance: InstanceFile, ConnectionsFile, ConfigFile
const (
	// PlatformFile: the platform's values, the context at level 0.
	PlatformFile = repository.PlatformFile
	// ContractsDir: the contracts, <contract>.schema.json.
	ContractsDir = repository.ContractsDir
	// CatalogDir: the packages, <service>/<version>/.
	CatalogDir = repository.CatalogDir
	// ProjectsDir: the projects, <project>/, each a namespace.
	ProjectsDir = repository.ProjectsDir
	// ProjectFile: a project's values, the context at level 1, in its
	// directory.
	ProjectFile = repository.ProjectFile
	// ProjectDescription: the key of ProjectFile that describes the
	// project, a string, which LoadRepository leaves out of the context:
	// no instance receives it.
	ProjectDescription = repository.ProjectDescription
	// InstancesDir: a project's instances, <instance>/, each the instance
	// of that name (Instance.Name) and the release ReleaseName(<project>,
	// <instance>), in its directory.
	InstancesDir = repository.InstancesDir
	// InstanceFile: an instance's package, {service, version}, in its
	// directory.
	InstanceFile = repository.InstanceFile
	// ConnectionsFile: an instance's own connections, the context at level
	// 2, in its directory; optional.
	ConnectionsFile = repository.ConnectionsFile
	// ConfigFile: an instance's values, its parameters and upstream values,
	// in its directory; optional.
	ConfigFile = repository.ConfigFile
)

// ReleaseName: the release of an instance of the tree, <project>-<instance>
// (LoadRepository): the project's directory name and the instance's, its
// namespace the project. The releases of a tree are unique across its
// projects, as the HelmReleases and Applications of a deployer, which
// share a namespace, need them; the targets refuse two of one name, which
// project names that hold a "-" may give (a-b/c and a/b-c), as
// CheckReleases does.
func ReleaseName(project, instance string) string {
	return repository.ReleaseName(project, instance)
}

// CheckReleases: nil when every release name of results is in one
// namespace, as the Flux and ArgoCD targets need them, their objects
// sharing one namespace; else a *ReleaseError per release, in name order,
// what those targets refuse. A caller that adds an instance to a tree
// those targets write checks it before the write.
func CheckReleases(results []*Result) error {
	return target.Releases(results)
}

// IsName: name may name a part of a tree, a project (its namespace), an
// instance, a connection, a named claim: a DNS-1123 label, lowercase
// letters, digits and "-", a letter or a digit at each end, 63 characters
// at most, as the compile checks them; for a caller that writes a tree,
// each name a directory or a key.
func IsName(name string) bool { return chart.IsDNSLabel(name) }

// ProjectDir: projects/<namespace>, where every target writes the files of
// the instances of a namespace, the flux target its kustomization.yaml.
func ProjectDir(namespace string) string { return target.ProjectDir(namespace) }

// InstanceDir: projects/<namespace>/instances/<release>, where every
// target writes an instance's umbrella chart (the files of Write), the
// flux and argocd targets its engine's files beside it.
func InstanceDir(namespace, release string) string {
	return target.InstanceDir(namespace, release)
}

// ProjectManifestsDir: projects/<namespace>/project, where the flux and
// argocd targets write a project's own manifests, for every project that
// has an instance: its Namespace, the platform's secret store in it, and
// the identity its releases are confined to its namespace with
// (docs/specs/01_inputs_outputs.md, targets); what the platform's entry
// point applies before the instances.
func ProjectManifestsDir(namespace string) string {
	return target.ProjectManifests(namespace)
}

// LoadDefinition reads the package at the root of fsys: Chart.yaml, then
// okdp.yaml and its values file, values.cel.yaml or values.yaml, when it
// has them, checked; each compile reads its other files. A values.yaml
// that holds expressions is read all the same, with a warning
// (Definition.Warnings, then Result.Warnings).
func LoadDefinition(fsys fs.FS) (*Definition, error) {
	return okdp.LoadDefinition(fsys)
}

// Compile runs the mapping of def on one instance, cancelled with ctx
// (between stages, and within the evaluation of an expression). It is safe
// for concurrent use: it keeps no state between calls.
func Compile(ctx context.Context, def *Definition, in Inputs) (*Result, error) {
	return okdp.Compile(ctx, def, in)
}

// CompileAll compiles instances together: each claim of a reference bound
// to the instance that provisions its contract, the one it names or the
// provider of its class, the one it names or the default one of the
// claimer's context (Outcome.Claims), named by its name (Instance.Name)
// or its release, a name of two instances refused, one of another
// namespace through a class of the platform's or with its consent
// (Instance.Claimers); each instance after
// the instances of its namespace whose outputs it references and the
// providers of its claims, with what they publish (Inputs.Name,
// Published, Claims and Bindings, set by CompileAll). Two instances of a
// namespace of one release, or of one name, are refused. An output two instances of a
// namespace publish refuses both, and the instances whose references
// resolved to it. Outcomes in the order of instances, which changes none
// of them.
func CompileAll(ctx context.Context, instances []Instance) []Outcome {
	return okdp.CompileAll(ctx, instances)
}

// CompileTree: instances compiled together (CompileAll), then the
// deployment graph of those that compile (NewGraph), each of its problems
// at the instance of its release (TreeInstance.GraphErr), and the claims
// bound to each provider, by ID (TreeInstance.Provided): the compile of a
// tree (LoadRepository) as a caller that reads it asks of it, each
// instance's outcome, why it does not deploy (TreeInstance.Err), where
// each claim is bound and what its provider granted. With charts, the
// upstream charts the compile names (Result.Charts) are fetched, and the
// instances compiled again with them, their final values checked against
// them (Inputs.Charts); a chart that cannot be fetched leaves the values
// unchecked, and says why (Tree.ChartsErr). instances are not changed.
func CompileTree(ctx context.Context, instances []Instance, charts ChartSource) *Tree {
	return tree.Compile(ctx, instances, charts)
}

// LoadRepository: every instance of the tree that repos make together
// (docs/01_usage.md, repository layout), by project then instance, as
// CompileAll compiles them, each named after its directory
// (Instance.Name), the release ReleaseName of its project and its name,
// in the namespace of its project: each part of the tree in one
// repository (platform.yaml, catalog/<service>/, projects/<project>/), a
// contract in several with the same bytes, so that the order of repos
// changes nothing. Every problem at once, each naming its repository and
// path.
func LoadRepository(repos []Repository) ([]Instance, error) {
	return repository.Load(repos)
}

// ParseConnection: the external connection name of connections, v, as
// written: for a caller that lists or edits the connections of a tree,
// each checked by the compile of the instances that reference it.
func ParseConnection(name string, v any) Connection {
	return okdp.ParseConnection(name, v)
}

// ParseClaim: the named claim name of claims, v, as written, and why the
// compile refuses it, if it does: the claim holds what v does all the
// same, for a caller that lists the claims of a tree.
func ParseClaim(name string, v any) (ClaimSpec, error) {
	return okdp.ParseClaim(name, v)
}

// ErrNotInCatalog: a service, or a version of a service, that the catalog
// of no repository has: what a Catalog's Service and Package wrap.
var ErrNotInCatalog = repository.ErrNotInCatalog

// LoadCatalog: the catalog that repos make together, as LoadRepository
// reads it: each service in one repository, every version of it, a
// contract in several with the same bytes; every problem at once, each
// naming its repositories. Its services list the versions of each,
// highest first, the SemVer ones by precedence; a package is what
// LoadRepository reads for an instance of that service and version.
func LoadCatalog(repos []Repository) (*Catalog, error) {
	return repository.LoadCatalog(repos)
}

// NewGraph: the deployment graph of results, the compiled instances of
// their namespaces: a release waits for the releases its connection
// references point to (Result.Dependencies). Every problem at once, each
// a *GraphError: a dependency on a release no result has, or on one that
// does not provide what is referenced; a cycle; a release given twice.
func NewGraph(results []*Result) (*Graph, error) {
	return graph.New(results)
}

// Write: what `okdp-compile -o <dir>` writes for res, path in the
// directory -> content: Chart.yaml, the instance as one chart, the
// package's Chart.yaml with the dependencies compiled, an umbrella over the
// upstream charts of its keys; UmbrellaValues, the final values of each key
// under the key; InfoFile, when the instance publishes a URL, a usage
// or a connection; and, when res has objects, ObjectsFile and
// ObjectsTemplate. res is what Compile returned: Write does not check it
// again. Every file is built before Write returns.
func Write(res *Result) (map[string][]byte, error) {
	return helm.Write(res)
}

// ParseValues: a values file as Helm 3 reads it (its first document, YAML
// 1.1, string keys, every number a float64), for Inputs.Context and
// Inputs.Values.
func ParseValues(b []byte) (map[string]any, error) {
	return chart.ParseValues(b)
}
