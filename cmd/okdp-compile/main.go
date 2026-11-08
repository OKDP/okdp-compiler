// okdp-compile --repository <dir> [--repository <dir>]... [--catalog <dir>]... -o <dir>
//
//	[--target helm|flux|argocd] [--path-prefix <path>]
//	[--flux-source <namespace>/<name>] [--repo-url <url>] [--revision <rev>]
//	[--cost-limit <n>] [--instance-timeout <duration>] [--timeout <duration>]
//	[--force] [--report <file>|-]
//	[--partial] [--allow-plain-secrets]
//
// every instance of a tree, its parts in one or more repositories, its
// packages in catalogs of their own too, compiled together, written for a deployer; --check [--charts <dir>] in
// place of -o: the compile checked, the upstream charts too with
// --charts; nothing written.

package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	compiler "github.com/okdp/okdp-compiler"
	"github.com/okdp/okdp-compiler/internal/dirfs"
	"github.com/okdp/okdp-compiler/internal/outdir"
)

// version: set at build time (-ldflags "-X main.version=..."): make build,
// the release binaries; --version prints dev without it.
var version string

// options: the command-line flags.
type options struct {
	repositories      []string      // --repository: the tree's parts (compiler.RoleTree)
	catalogs          []string      // --catalog: catalogs, catalog/ and contracts/ only (compiler.RoleCatalog)
	charts            string        // --charts, with --check
	output            string        // --output, -o
	check             bool          // --check
	target            string        // --target
	pathPrefix        string        // --path-prefix: flux, argocd
	fluxSource        string        // --flux-source: flux
	repoURL, revision string        // --repo-url, --revision: argocd
	costLimit         uint64        // --cost-limit: each instance's, 0 none
	timeout           time.Duration // --timeout: the compile's, 0 none
	instanceTimeout   time.Duration // --instance-timeout: each instance's, 0 none
	force             bool          // --force: an output without its marker
	report            string        // --report: a file, - for stdout
	partial           bool          // --partial: the projects that compile written
	allowPlainSecrets bool          // --allow-plain-secrets: each instance's
}

// The defaults of --cost-limit, --instance-timeout and --timeout: the
// command compiles packages it may not have reviewed, an expression
// bounded as okdp-gitops bounds it, each instance and the whole compile in
// time.
const (
	defaultCostLimit       = compiler.DefaultCostLimit
	defaultInstanceTimeout = time.Minute
	defaultTimeout         = 10 * time.Minute
)

// errReported: a failure whose messages are printed already: exit 1,
// nothing more.
var errReported = errors.New("reported")

// main: parses the flags and runs the compile. A usage error (an unknown
// flag, a flag without its argument, an argument that is not a flag) exits
// with status 2, the message and the usage on stderr. An argument that is
// not a flag would end the flags, and those after it would be ignored: a
// directory without its --repository.
func main() {
	var o options
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Func("repository", "a repository of the tree, its directory, required, repeatable: any part of the tree (platform.yaml, contracts/, catalog/, projects/), each part in one repository; every instance of the tree, projects/<project>/instances/<instance>/, the release <project>-<instance> of the namespace <project>, compiled together, each with what the others of its namespace publish, after them, each claim bound to its provider, the dependencies checked, written for --target under <output>/projects/, which the command owns", func(s string) error { o.repositories = append(o.repositories, s); return nil })
	flag.Func("catalog", "a catalog of packages, its directory, repeatable: catalog/ and contracts/ only, as --repository's, each service in one repository; one that holds platform.yaml or projects/ is refused, its maintainers adding no project nor the platform's values", func(s string) error { o.catalogs = append(o.catalogs, s); return nil })
	flag.BoolVar(&o.check, "check", false, "compile and check, write nothing: the errors and warnings, no --output; with --charts, the final values checked against the upstream charts")
	flag.StringVar(&o.charts, "charts", "", "chart cache directory, read by --check only: the upstream charts unpacked, <repository>/<chart>-<version>/<chart>/ (helm pull --untar -d <dir>/<repository>/<chart>-<version>), <repository> the URL without its scheme; the final values, over each chart's defaults, checked against its values.schema.json, and an instance list appended to that leaves out items of the chart's default list warned of (default $OKDP_CHARTS with --check)")
	flag.StringVar(&o.output, "output", "", "output directory, required but with --check: projects/<namespace>/instances/<release>/, each instance's umbrella chart, Chart.yaml, "+compiler.UmbrellaValues+", its values (helm -f), "+compiler.ObjectsFile+" and "+compiler.ObjectsTemplate+", OKDP's own objects, when it has any, "+compiler.InfoFile+", what it publishes, when it publishes something, and the files of --target, every symbolic link under <output>/projects/ removed first, nothing written through one; then the dependencies printed on stderr, one line each: <namespace>/<release>: depends on <instance> (<contract> <output>, <field>)")
	flag.StringVar(&o.output, "o", "", "shorthand for --output")
	flag.StringVar(&o.target, "target", "", "what is written: helm (the default), each instance's umbrella chart; flux, with its HelmRelease, waiting for the releases it depends on, and the Kustomizations, each project's and the root's, projects/kustomization.yaml; argocd, with its Application and its sync wave; both with each project's manifests, projects/<namespace>/project/, its Namespace, the platform's secret store in it and the identity its releases are confined to the namespace with")
	flag.StringVar(&o.pathPrefix, "path-prefix", "", "--target flux and argocd: the path of <output> in the repository the deployer reads (default its root)")
	flag.StringVar(&o.fluxSource, "flux-source", "", "--target flux: the GitRepository of that repository, <namespace>/<name> (default "+compiler.DefaultFluxSource+")")
	flag.StringVar(&o.repoURL, "repo-url", "", "--target argocd, required: the repository Argo CD reads <output> from")
	flag.StringVar(&o.revision, "revision", "", "--target argocd: its revision (default HEAD)")
	flag.Uint64Var(&o.costLimit, "cost-limit", defaultCostLimit, "the CEL cost each instance's expressions may use together, its mapping's and the compiler's templates', past which the instance is refused; 0: no limit")
	flag.DurationVar(&o.instanceTimeout, "instance-timeout", defaultInstanceTimeout, "how long one instance's compile may take, past which that instance is refused and the others compiled (--partial keeps its project); 0: no limit")
	flag.DurationVar(&o.timeout, "timeout", defaultTimeout, "how long the compile may take, every instance together, past which nothing is written; 0: no limit")
	flag.StringVar(&o.report, "report", "", "write a JSON report, to the file, or stdout for -, even with --check and when the compile fails: each instance, its namespace, instance, release, service, version, privileged, status (compiled, refused or skipped), error, warnings, dependencies, claims and charts, and the tree's errors")
	flag.BoolVar(&o.allowPlainSecrets, "allow-plain-secrets", false, "accept credentials written in clear (Inputs.AllowPlainSecrets of every instance): a provides secret without generate, a Secret of the objects with data or stringData; refused without it, the output being committed to the deployments repository")
	flag.BoolVar(&o.partial, "partial", false, "write the projects whose instances all compile, each refusal printed, and keep what <output> holds of the others' instances, their manifests and identity of today's settings, and of the projects of a release name two namespaces share (--target flux and argocd); status 1 all the same; a failure of the tree (an option, a repository, the target) writes nothing")
	flag.BoolVar(&o.force, "force", false, "write into an <output>/projects/ that okdp-compile did not write (no "+outdir.MarkerFile+"), removing from it every file it does not write")
	flag.Parse()
	if o.check && o.charts == "" {
		o.charts = os.Getenv("OKDP_CHARTS")
	}
	switch {
	case flag.NArg() > 0:
		fmt.Fprintf(os.Stderr, "unexpected argument %q: every input is a flag (--repository before each directory)\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	case *showVersion:
		fmt.Println(cmp.Or(version, "dev"))
	default:
		if err := run(o); err != nil {
			if !errors.Is(err, errReported) {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			os.Exit(1)
		}
	}
}

// run: the tree compiled and written (compileTree); with --report, the
// report written too, checked first (checkReport), whatever the compile
// gives, its error among the report's.
func run(o options) error {
	if o.report == "" {
		return compileTree(o, newReport())
	}
	if err := checkReport(o); err != nil {
		return err
	}
	rep := newReport()
	err := compileTree(o, rep)
	return errors.Join(err, rep.write(o.report, err))
}

// checkReport: nil when --report is - or a file out of the output's
// projects/, which the command owns, and of every repository, which it
// reads.
func checkReport(o options) error {
	if o.report == "-" {
		return nil
	}
	file, err := filepath.Abs(o.report)
	if err != nil {
		return err
	}
	dirs := slices.Concat(o.repositories, o.catalogs)
	if o.output != "" {
		dirs = append(dirs, filepath.Join(o.output, "projects"))
	}
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err == nil && inside(file, abs) {
			return fmt.Errorf("--report %s is inside %s: the command reads or owns it", o.report, dir)
		}
	}
	return nil
}

// compileTree: the tree the repositories make together
// (compiler.Build), each directory read bounded, no symbolic link
// followed (openDir), each instance within --cost-limit and with
// --allow-plain-secrets, the chart cache of --check --charts set
// (compiler.StaticCharts), all within --timeout (past it, an error,
// nothing printed of the instances); each claim's binding printed,
// <namespace>/<release>: <field>: claim <id> (<contract>) bound to
// <namespace>/<release>, then ", class <class>" for the class it names,
// ", the default class <class>" when it names none, nothing when it names
// its provider, then each refusal, error: <namespace>/<release>: <error>,
// each warning, warning: <namespace>/<release>: <warning>, in their
// order; then each problem of the graph, error: <namespace>/<release>:
// <error>; then the files of the target and outdir.MarkerFile
// (compiler.TreeFiles), written under the output directory (outdir.Write),
// whose projects/ the command wrote before, unless --force
// (outdir.Check), and the dependencies, <namespace>/<release>: depends on
// <instance> (<contract> <output>, <field>), <namespace>/<instance> for
// one of another namespace. Every option is checked first, the target's
// and the output's too; nothing is written when anything fails, nor with
// --check; a release name two namespaces share (--target flux and
// argocd), error: <namespace>/<release>: release ... need distinct
// names, an instance each, fails it too; but with --partial, the projects
// compiler.TreeFiles writes are, those it keeps written as the output has
// them, read through openDir for the target's files of them (the root
// Kustomization's entry, the releases they hold), but their manifests and
// their instances' identity, of today's settings, each printed, error:
// <namespace>: not written, ..., after the problems that
// keep them, a refusal, a problem of the graph of its own, a release name
// two namespaces share, or error: <namespace>/<release>: it depends on
// <namespace>/<release>, of the project <namespace>, whose output is
// kept; a project that does not load (a file of its own that does not
// parse, a stray file) is refused as a whole, each problem printed, error:
// <path>: <message>, and kept alike, error: <namespace>: not written, it
// does not load ...; and the command fails all the same. Each instance is
// recorded in rep as its compile ends, the problems of the graph and the
// shared release names at their instances, and whether the files were
// written.
func compileTree(o options, rep *report) error {
	if err := checkOptions(o); err != nil {
		return err
	}
	tg, err := newTarget(o)
	if err != nil {
		return err
	}
	if err := checkDirs(o); err != nil {
		return err
	}
	if !o.check {
		if err := outdir.Check(o.output, o.force); err != nil {
			return err
		}
	}
	in, err := openInputs(o)
	if err != nil {
		return err
	}
	defer in.close()
	t, err := buildTree(o, in, rep)
	if err != nil {
		return err
	}
	refused := printOutcomes(t, rep)
	if !o.partial {
		if refused {
			return errReported
		}
		for _, ti := range t.Instances {
			graphErrors(ti, ti.GraphErr, rep)
		}
		if t.Graph == nil {
			return errReported
		}
	}
	// The projects kept (compiler.TreeFiles: --partial's, and those of a
	// release name two namespaces share), why each, but the refusals
	// printed; the output read with --partial only, nothing written
	// without it.
	previous, closePrevious, err := openPrevious(o)
	if err != nil {
		return err
	}
	defer closePrevious()
	files, kept, err := compiler.TreeFiles(t, []compiler.Target{tg}, previous)
	if err != nil {
		return err
	}
	k := recordKept(o, kept, rep)
	switch {
	case !o.partial && len(kept) > 0:
		return errReported
	case o.check:
		return nil
	}
	files[outdir.MarkerFile] = []byte(outdir.Marker)
	if err := outdir.Write(o.output, files); err != nil {
		return err
	}
	rep.Written = true
	printNotWritten(o, k, rep)
	printDependencies(t, k.namespaces)
	if len(k.namespaces) > 0 {
		return errReported
	}
	return nil
}

// checkOptions: nil when the options go together, before anything is read.
func checkOptions(o options) error {
	switch {
	case o.repositories == nil:
		return errors.New("--repository is required")
	case o.check && o.output != "":
		return errors.New("--check writes nothing: no --output (-o)")
	case !o.check && o.charts != "":
		return errors.New("--charts is read by --check only: a compile reads no chart")
	case !o.check && o.output == "":
		return errors.New("--output (-o) is required")
	case o.check && o.force:
		return errors.New("--check writes nothing: no --force")
	case o.check && o.partial:
		return errors.New("--check writes nothing: no --partial")
	}
	return nil
}

// inputs: the repositories and the chart cache the command reads, open.
type inputs struct {
	repos   []compiler.Repository
	charts  compiler.ChartSource // nil without --charts
	closers []func()
}

// close: the directories closed.
func (in *inputs) close() {
	for _, c := range in.closers {
		c()
	}
}

// openInputs: the directories of --repository and --catalog (openDir) and
// of --charts (dirfs.NewCharts, compiler.StaticCharts) open; the ones
// opened closed when another fails.
func openInputs(o options) (*inputs, error) {
	in := &inputs{}
	for _, d := range inputDirs(o) {
		fsys, err := openDir(d.flag, d.dir)
		if err != nil {
			in.close()
			return nil, err
		}
		in.closers = append(in.closers, fsys.Close)
		in.repos = append(in.repos, compiler.Repository{Name: d.dir, FS: fsys, Role: d.role})
	}
	if o.charts != "" {
		fsys, err := dirfs.NewCharts("--charts", o.charts)
		if err != nil {
			in.close()
			return nil, err
		}
		in.closers = append(in.closers, fsys.Close)
		in.charts = compiler.StaticCharts(fsys)
	}
	return in, nil
}

// buildTree: the tree of the repositories compiled (compiler.Build) within
// --timeout, each instance within --cost-limit and --instance-timeout; past
// the timeout, every instance is recorded as skipped in rep and an error
// returned.
func buildTree(o options, in *inputs, rep *report) (*compiler.Tree, error) {
	ctx := context.Background()
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}
	t, err := compiler.Build(ctx, in.repos, compiler.BuildOptions{CostLimit: o.costLimit, InstanceTimeout: o.instanceTimeout, AllowPlainSecrets: o.allowPlainSecrets, Charts: in.charts})
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		for _, ti := range t.Instances {
			rep.skip(ti.Instance)
		}
		return nil, fmt.Errorf("the compile takes longer than --timeout %s: nothing written", o.timeout)
	}
	return t, nil
}

// printOutcomes: the problems of the projects that do not load, then for
// each instance its claims' bindings, its refusal or its warnings, in
// their order, on stderr and recorded in rep; whether anything is refused.
func printOutcomes(t *compiler.Tree, rep *report) (refused bool) {
	// A project that does not load: its problems, which refuse it and not
	// the tree.
	for _, r := range t.Refused {
		for _, p := range r.Problems {
			fmt.Fprintf(os.Stderr, "error: %s\n", p)
			rep.Errors = append(rep.Errors, p)
		}
		refused = true
	}
	for _, ti := range t.Instances {
		out := ti.Outcome
		rep.add(ti.Instance, out)
		node := ti.Namespace() + "/" + ti.Release()
		for _, b := range out.Claims {
			how := ""
			switch {
			case b.Class == "":
			case b.Named:
				how = ", class " + b.Class
			default:
				how = ", the default class " + b.Class
			}
			fmt.Fprintf(os.Stderr, "%s: %s: claim %s (%s) bound to %s/%s%s\n", node, b.Field, b.ID, b.Contract, b.Namespace, b.Release, how)
		}
		if out.Err != nil {
			fmt.Fprintf(os.Stderr, "error: %s: %v\n", node, out.Err)
			refused = true
			continue
		}
		for _, w := range out.Result.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", node, w)
		}
	}
	return refused
}

// openPrevious: with --partial, the output directory as it is, read for
// the target's files of the projects kept (nil when it does not exist yet;
// nil without --partial), and what closes it.
func openPrevious(o options) (fs.FS, func(), error) {
	if !o.partial {
		return nil, func() {}, nil
	}
	fsys, err := openDir("--output", o.output)
	switch {
	case err == nil:
		return fsys, fsys.Close, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, nil, err
	}
	return nil, func() {}, nil
}

// keptProjects: the projects of the output kept (compiler.TreeFiles), by
// namespace in order, which of them do not load, and the reasons the
// others were dropped, by namespace.
type keptProjects struct {
	namespaces []string
	unloaded   []string
	dropped    map[string]string
}

// recordKept: the projects kept, and the problems of the graph at their
// instances, printed and recorded in rep: an instance of a kept project
// that depends on a release of one whose output is kept (--partial), or a
// problem of the graph of its own.
func recordKept(o options, kept []compiler.KeptProject, rep *report) keptProjects {
	k := keptProjects{dropped: map[string]string{}}
	for _, p := range kept {
		k.namespaces = append(k.namespaces, p.Namespace)
		if p.Unloaded {
			k.unloaded = append(k.unloaded, p.Namespace)
		}
		if p.Dropped != "" {
			k.dropped[p.Namespace] = p.Dropped
		}
		for _, ki := range p.Instances {
			switch {
			case ki.Outcome.Err != nil, ki.Depends && !o.partial:
			case ki.Depends:
				fmt.Fprintf(os.Stderr, "error: %s/%s: %v\n", ki.Namespace(), ki.Release(), ki.Err)
			default:
				graphErrors(ki.Instance, ki.Err, rep)
			}
		}
	}
	return k
}

// printNotWritten: for each project kept, why its output is as it was, or
// dropped (--partial), and recorded as not written in rep.
func printNotWritten(o options, k keptProjects, rep *report) {
	for _, ns := range k.namespaces {
		dir := filepath.Join(o.output, filepath.FromSlash(compiler.ProjectDir(ns))) + string(filepath.Separator)
		if why, ok := k.dropped[ns]; ok {
			fmt.Fprintf(os.Stderr, "error: %s: dropped from the output (--partial): %s: nothing of %s kept\n", ns, why, dir)
			rep.notWritten(ns)
			continue
		}
		why := "an instance of it refused"
		if slices.Contains(k.unloaded, ns) {
			why = "it does not load"
		}
		fmt.Fprintf(os.Stderr, "error: %s: not written, %s (--partial): %s kept as it is, its namespace, identity and roles of today's settings\n", ns, why, dir)
		rep.notWritten(ns)
	}
}

// printDependencies: the dependencies of the instances of t that compile,
// those of a project kept left out, on stderr: <namespace>/<release>:
// depends on <instance> (<contract> <output>, <field>).
func printDependencies(t *compiler.Tree, keep []string) {
	for _, ti := range t.Instances {
		res := ti.Outcome.Result
		if res == nil || slices.Contains(keep, res.Namespace) {
			continue
		}
		for _, d := range res.Dependencies {
			instance := d.Instance
			if d.Namespace != "" {
				instance = d.Namespace + "/" + instance
			}
			fmt.Fprintf(os.Stderr, "%s/%s: depends on %s (%s %s, %s)\n", res.Namespace, res.Release, instance, d.Contract, d.Output, d.Field)
		}
	}
}

// openDir: the files under dir, given as flag (--repository, --output),
// as dirfs.New reads them: a symbolic link refused wherever it points, a
// file that is neither regular nor a directory refused, a file over
// 4 MiB, or the files read over 64 MiB together, refused. --charts is
// read as dirfs.NewCharts reads it: 64 MiB for each chart's files.
func openDir(flag, dir string) (*dirfs.FS, error) { return dirfs.New(flag, dir) }

// graphErrors: err, the problems of the graph at the release of ti, each
// printed, error: <namespace>/<release>: <error>, and recorded in rep.
func graphErrors(ti *compiler.TreeInstance, err error, rep *report) {
	if err == nil {
		return
	}
	for _, e := range unjoin(err) {
		ge := &compiler.GraphError{Node: compiler.GraphNode{Namespace: ti.Namespace(), Release: ti.Release()}, Err: e}
		fmt.Fprintln(os.Stderr, "error:", ge)
		rep.graphError(ge)
	}
}

// unjoin: the errors errors.Join joined, err alone otherwise.
func unjoin(err error) []error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		return joined.Unwrap()
	}
	return []error{err}
}

// newTarget: the target --target names, its options; each option given to
// a target that does not read it refused, the target's own checked
// before anything is compiled (the files of no instance).
func newTarget(o options) (compiler.Target, error) {
	var tg compiler.Target
	reads := map[string]bool{}
	switch o.target {
	case "", "helm":
		tg = compiler.Helm{}
	case "flux":
		tg, reads = compiler.Flux{Source: o.fluxSource, PathPrefix: o.pathPrefix}, map[string]bool{"--flux-source": true, "--path-prefix": true}
	case "argocd":
		if o.repoURL == "" {
			return nil, errors.New("--repo-url is required by --target argocd")
		}
		tg, reads = compiler.ArgoCD{RepoURL: o.repoURL, Revision: o.revision, PathPrefix: o.pathPrefix}, map[string]bool{"--repo-url": true, "--revision": true, "--path-prefix": true}
	default:
		return nil, fmt.Errorf("--target %q: helm, flux or argocd", o.target)
	}
	for _, f := range [][3]string{{"--path-prefix", o.pathPrefix, "flux and argocd"}, {"--flux-source", o.fluxSource, "flux"}, {"--repo-url", o.repoURL, "argocd"}, {"--revision", o.revision, "argocd"}} {
		if f[1] != "" && !reads[f[0]] {
			return nil, fmt.Errorf("%s is read by --target %s only", f[0], f[2])
		}
	}
	if _, err := tg.Files(nil, nil); err != nil {
		return nil, err
	}
	return tg, nil
}

// inputDir: a directory the command reads, the flag that gives it and its
// role in the tree.
type inputDir struct {
	flag, dir string
	role      compiler.Role
}

// inputDirs: the directories of o the command reads, --repository's
// (compiler.RoleTree) then --catalog's (compiler.RoleCatalog), in order.
func inputDirs(o options) []inputDir {
	var out []inputDir
	for _, dir := range o.repositories {
		out = append(out, inputDir{"--repository", dir, compiler.RoleTree})
	}
	for _, dir := range o.catalogs {
		out = append(out, inputDir{"--catalog", dir, compiler.RoleCatalog})
	}
	return out
}

// checkDirs: nil when each --repository and --catalog is a directory,
// none given twice or inside another, and none contains the output
// directory or is inside it: the command removes from it what it does not
// write.
func checkDirs(o options) error {
	dirs := inputDirs(o)
	abs := make([]string, len(dirs))
	for i, d := range dirs {
		if info, err := os.Stat(d.dir); err != nil {
			return fmt.Errorf("%s: %w", d.flag, err)
		} else if !info.IsDir() {
			return fmt.Errorf("%s %s: not a directory", d.flag, d.dir)
		}
		var err error
		if abs[i], err = filepath.Abs(d.dir); err != nil {
			return err
		}
		for j := range i {
			switch {
			case abs[i] == abs[j]:
				return fmt.Errorf("%s %s is given twice", d.flag, d.dir)
			case inside(abs[i], abs[j]):
				return fmt.Errorf("%s %s is inside %s %s", d.flag, d.dir, dirs[j].flag, dirs[j].dir)
			case inside(abs[j], abs[i]):
				return fmt.Errorf("%s %s is inside %s %s", dirs[j].flag, dirs[j].dir, d.flag, d.dir)
			}
		}
	}
	if o.output == "" {
		return nil
	}
	out, err := filepath.Abs(o.output)
	if err != nil {
		return err
	}
	for i, d := range dirs {
		if inside(out, abs[i]) || inside(abs[i], out) {
			return fmt.Errorf("--output (-o) %s and %s %s must not contain one another: the command removes what it does not write", o.output, d.flag, d.dir)
		}
	}
	return nil
}

// inside reports whether dir is parent or inside it, both absolute.
func inside(dir, parent string) bool {
	rel, err := filepath.Rel(parent, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
