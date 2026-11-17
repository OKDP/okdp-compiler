// okdp-compile --repository <dir> [--repository <dir>]... [--catalog <dir>]... -o <dir>
//
//	[--rules <dir>] [--cost-limit <n>] [--instance-timeout <duration>] [--timeout <duration>]
//	[--force] [--allow-plain-secrets]
//
// The command compiles every instance of a tree together and writes the
// files of each for helm install. With --check [--charts <dir>] in place
// of -o, it checks the compile and writes nothing.
//
// It is a thin wrapper of compiler.Build and compiler.Write. The Flux and
// Argo CD targets belong to okdp-gitops' okdp-compile.

package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	compiler "github.com/okdp/okdp-compiler"
	"github.com/okdp/okdp-compiler/internal/dirfs"
	"github.com/okdp/okdp-compiler/internal/outdir"
)

// version is set by make build with -X main.version; unset, it is dev.
var version string

type options struct {
	repositories      []string      // --repository: the tree's parts (compiler.RoleTree)
	catalogs          []string      // --catalog: catalog/ and contracts/ only (compiler.RoleCatalog)
	rules             string        // --rules: the rule set added to the mandatory one; "" adds none
	charts            string        // --charts, with --check
	output            string        // --output, -o
	check             bool          // --check
	costLimit         uint64        // --cost-limit, per instance; 0 for no limit
	timeout           time.Duration // --timeout, for the whole compile; 0 for no limit
	instanceTimeout   time.Duration // --instance-timeout, per instance; 0 for no limit
	force             bool          // --force: write into an output without its marker
	allowPlainSecrets bool          // --allow-plain-secrets, for every instance
}

// The defaults of the flags, the same as okdp-gitops' okdp-compile's.
const (
	defaultCostLimit       = compiler.DefaultCostLimit
	defaultInstanceTimeout = time.Minute
	defaultTimeout         = 10 * time.Minute
)

// errReported is a failure whose messages are already printed: main exits
// with status 1 and prints nothing more.
var errReported = errors.New("reported")

// main parses the flags and runs the compile. A usage error prints the
// usage on stderr and exits with status 2.
func main() {
	var o options
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Func("repository", "a repository of the tree, its directory, required, repeatable: any part of the tree (platform.yaml, contracts/, catalog/, projects/), each part in one repository", func(s string) error { o.repositories = append(o.repositories, s); return nil })
	flag.Func("catalog", "a catalog of packages, its directory, repeatable: catalog/ and contracts/ only; one that holds platform.yaml or projects/ is refused", func(s string) error { o.catalogs = append(o.catalogs, s); return nil })
	flag.StringVar(&o.rules, "rules", "", "a rule set's directory, rules.yaml at its root (okdp-gitops' rules/v1, this repository's testdata/rules), added to the compiler's mandatory rules, which every compile applies: without it nothing is refused")
	flag.BoolVar(&o.check, "check", false, "compile and check, write nothing: the errors and warnings, no --output; with --charts, the final values checked against the upstream charts")
	flag.StringVar(&o.charts, "charts", "", "chart cache directory, read by --check only: the upstream charts unpacked, <repository>/<chart>-<version>/<chart>/ (helm pull --untar -d <dir>/<repository>/<chart>-<version>), <repository> the URL without its scheme (default $OKDP_CHARTS with --check)")
	flag.StringVar(&o.output, "output", "", "output directory, required but with --check: projects/<namespace>/instances/<release>/, values.yaml, the values of the catalog's chart (helm install <release> <package> -f values.yaml), okdp-objects.values.yaml, those of the "+compiler.ObjectsChart+" chart, when it has objects, installed first, okdp.info.yaml, what it publishes, when it publishes something; then the dependencies printed on stderr")
	flag.StringVar(&o.output, "o", "", "shorthand for --output")
	flag.Uint64Var(&o.costLimit, "cost-limit", defaultCostLimit, "the CEL cost each instance's expressions may use together, past which the instance is refused; 0: no limit")
	flag.DurationVar(&o.instanceTimeout, "instance-timeout", defaultInstanceTimeout, "how long one instance's compile may take, past which that instance is refused; 0: no limit")
	flag.DurationVar(&o.timeout, "timeout", defaultTimeout, "how long the compile may take, every instance together, past which nothing is written; 0: no limit")
	flag.BoolVar(&o.allowPlainSecrets, "allow-plain-secrets", false, "accept credentials written in clear (Inputs.AllowPlainSecrets of every instance): a provides secret without generate, a Secret of the objects with data or stringData")
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

// run compiles the tree and prints its bindings, refusals and warnings,
// then the problems of its graph. Unless --check, it then writes the
// files of each instance and prints the dependencies. It checks every
// option first, and writes nothing when anything fails.
func run(o options) error {
	if err := checkOptions(o); err != nil {
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
	t, err := buildTree(o, in)
	if err != nil {
		return err
	}
	if printOutcomes(t) {
		return errReported
	}
	for _, ti := range t.Instances {
		graphErrors(ti, ti.GraphErr)
	}
	if t.Graph == nil {
		return errReported
	}
	if o.check {
		return nil
	}
	files := map[string][]byte{outdir.MarkerFile: []byte(outdir.Marker)}
	for _, ti := range t.Instances {
		res := ti.Outcome.Result
		written, err := compiler.Write(res)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", res.Namespace, res.Release, err)
		}
		for name, b := range written {
			files[path.Join(compiler.ProjectsDir, res.Namespace, compiler.InstancesDir, res.Release, name)] = b
		}
	}
	if err := outdir.Write(o.output, files); err != nil {
		return err
	}
	printDependencies(t)
	return nil
}

// checkOptions returns nil when the options go together.
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
	}
	return nil
}

// inputs holds what the command reads, once open.
type inputs struct {
	repos   []compiler.Repository
	rules   *compiler.RuleSet    // nil without --rules: the mandatory set alone
	charts  compiler.ChartSource // nil without --charts
	closers []func()
}

func (in *inputs) close() {
	for _, c := range in.closers {
		c()
	}
}

// openInputs opens the directories of --repository, --catalog, --rules
// and --charts through dirfs, which bounds what is read and follows no
// symbolic link. When one fails, it closes those already opened.
func openInputs(o options) (*inputs, error) {
	in := &inputs{}
	fail := func(err error) (*inputs, error) {
		in.close()
		return nil, err
	}
	for _, d := range inputDirs(o) {
		fsys, err := dirfs.New(d.flag, d.dir)
		if err != nil {
			return fail(err)
		}
		in.closers = append(in.closers, fsys.Close)
		in.repos = append(in.repos, compiler.Repository{Name: d.dir, FS: fsys, Role: d.role})
	}
	if o.rules != "" {
		fsys, err := dirfs.New("--rules", o.rules)
		if err != nil {
			return fail(err)
		}
		in.closers = append(in.closers, fsys.Close)
		if in.rules, err = compiler.LoadRules(fsys); err != nil {
			return fail(fmt.Errorf("--rules %s: %w", o.rules, err))
		}
	}
	if o.charts != "" {
		fsys, err := dirfs.NewCharts("--charts", o.charts)
		if err != nil {
			return fail(err)
		}
		in.closers = append(in.closers, fsys.Close)
		in.charts = compiler.StaticCharts(fsys)
	}
	return in, nil
}

// buildTree compiles the tree of the repositories within --timeout.
func buildTree(o options, in *inputs) (*compiler.Tree, error) {
	ctx := context.Background()
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}
	t, err := compiler.Build(ctx, in.repos, compiler.BuildOptions{
		CostLimit:         o.costLimit,
		InstanceTimeout:   o.instanceTimeout,
		AllowPlainSecrets: o.allowPlainSecrets,
		Charts:            in.charts,
		Rules:             in.rules,
	})
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("the compile takes longer than --timeout %s: nothing written", o.timeout)
	}
	return t, nil
}

// printOutcomes prints the load problems, then each instance's bindings
// and its refusal or warnings. It reports whether anything is refused.
func printOutcomes(t *compiler.Tree) (refused bool) {
	for _, r := range t.Refused {
		for _, p := range r.Problems {
			fmt.Fprintf(os.Stderr, "error: %s\n", p)
		}
		refused = true
	}
	for _, ti := range t.Instances {
		out := ti.Outcome
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

// graphErrors prints each problem of err, the problems of the graph at
// the release of ti, as error: <namespace>/<release>: <error>.
func graphErrors(ti *compiler.TreeInstance, err error) {
	if err == nil {
		return
	}
	errs := []error{err}
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		errs = joined.Unwrap()
	}
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "error:", &compiler.GraphError{Node: compiler.GraphNode{Namespace: ti.Namespace(), Release: ti.Release()}, Err: e})
	}
}

// printDependencies prints the dependencies of the instances of t on
// stderr:
//
//	<namespace>/<release>: depends on <instance> (<contract> <output>, <field>)
func printDependencies(t *compiler.Tree) {
	for _, ti := range t.Instances {
		res := ti.Outcome.Result
		for _, d := range res.Dependencies {
			instance := d.Instance
			if d.Namespace != "" {
				instance = d.Namespace + "/" + instance
			}
			fmt.Fprintf(os.Stderr, "%s/%s: depends on %s (%s %s, %s)\n", res.Namespace, res.Release, instance, d.Contract, d.Output, d.Field)
		}
	}
}

type inputDir struct {
	flag, dir string
	role      compiler.Role
}

// inputDirs returns the directories of --repository, then of --catalog.
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

// checkDirs returns nil when each --repository and --catalog is a
// directory, and none is given twice or is inside another. The output
// must be apart from them and from --rules: the command removes from its
// output what it does not write.
func checkDirs(o options) error {
	dirs := inputDirs(o)
	abs := make([]string, 0, len(dirs)+1)
	for i, d := range dirs {
		if info, err := os.Stat(d.dir); err != nil {
			return fmt.Errorf("%s: %w", d.flag, err)
		} else if !info.IsDir() {
			return fmt.Errorf("%s %s: not a directory", d.flag, d.dir)
		}
		a, err := filepath.Abs(d.dir)
		if err != nil {
			return err
		}
		abs = append(abs, a)
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
	if o.rules != "" {
		dirs = append(dirs, inputDir{flag: "--rules", dir: o.rules})
		rules, err := filepath.Abs(o.rules)
		if err != nil {
			return err
		}
		abs = append(abs, rules)
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
