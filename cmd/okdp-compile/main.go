// okdp-compile --repository <dir> [--repository <dir>]... -o <dir>
//
//	[--target helm|flux|argocd] [--path-prefix <path>]
//	[--flux-source <namespace>/<name>] [--repo-url <url>] [--revision <rev>]
//
// every instance of a tree, its parts in one or more repositories,
// compiled together, written for a deployer; --check [--charts <dir>] in
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

	compiler "github.com/okdp/okdp-compiler"
)

// version: set at build time (-ldflags "-X main.version=..."): make build,
// the release binaries; --version prints dev without it.
var version string

// options: the command-line flags.
type options struct {
	repositories      []string // --repository: the tree's parts, every input
	charts            string   // --charts, with --check
	output            string   // --output, -o
	check             bool     // --check
	target            string   // --target
	pathPrefix        string   // --path-prefix: flux, argocd
	fluxSource        string   // --flux-source: flux
	repoURL, revision string   // --repo-url, --revision: argocd
}

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
	flag.Func("repository", "a repository of the tree, its directory, required, repeatable: any part of the tree (platform.yaml, contracts/, catalog/, projects/), each part in one repository; every instance of the tree compiled together, each with what the others of its namespace publish, after them, the dependencies checked, written for --target under <output>/projects/, which the command owns", func(s string) error { o.repositories = append(o.repositories, s); return nil })
	flag.BoolVar(&o.check, "check", false, "compile and check, write nothing: the errors and warnings, no --output; with --charts, the final values checked against the upstream charts")
	flag.StringVar(&o.charts, "charts", "", "chart cache directory, read by --check only: the upstream charts unpacked, <repository>/<chart>-<version>/<chart>/ (helm pull --untar -d <dir>/<repository>/<chart>-<version>), <repository> the URL without its scheme; the final values, over each chart's defaults, checked against its values.schema.json, and an instance list appended to that leaves out items of the chart's default list warned of (default $OKDP_CHARTS with --check)")
	flag.StringVar(&o.output, "output", "", "output directory, required but with --check: projects/<namespace>/instances/<release>/, each instance's umbrella chart, Chart.yaml, "+compiler.UmbrellaValues+", its values (helm -f), "+compiler.ObjectsFile+" and "+compiler.ObjectsTemplate+", OKDP's own objects, when it has any, "+compiler.InfoFile+", what it publishes, when it publishes something, and the files of --target; then the dependencies printed on stderr, one line each: <namespace>/<release>: depends on <instance> (<contract>, <field>)")
	flag.StringVar(&o.output, "o", "", "shorthand for --output")
	flag.StringVar(&o.target, "target", "", "what is written: helm (the default), each instance's umbrella chart; flux, with its HelmRelease, waiting for the releases it depends on, and the Kustomizations; argocd, with its Application and its sync wave")
	flag.StringVar(&o.pathPrefix, "path-prefix", "", "--target flux and argocd: the path of <output> in the repository the deployer reads (default its root)")
	flag.StringVar(&o.fluxSource, "flux-source", "", "--target flux: the GitRepository of that repository, <namespace>/<name> (default "+compiler.DefaultFluxSource+")")
	flag.StringVar(&o.repoURL, "repo-url", "", "--target argocd, required: the repository Argo CD reads <output> from")
	flag.StringVar(&o.revision, "revision", "", "--target argocd: its revision (default HEAD)")
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

// run: every instance of the tree the repositories make together
// (compiler.LoadRepository), the chart cache set with --check --charts,
// compiled together (compiler.CompileAll), each refusal printed, error:
// <namespace>/<release>: <error>, each warning, warning:
// <namespace>/<release>: <warning>, in their order; then their graph
// (compiler.NewGraph), each of its errors printed, error:
// <namespace>/<release>: <error>; then the files of the target, written
// under the output directory (writeTree), and the dependencies,
// <namespace>/<release>: depends on <instance> (<contract>, <field>), the
// output after the contract when it is not named after the instance. Every
// option is checked first, the target's too; nothing is written when
// anything fails, nor with --check.
func run(o options) error {
	switch {
	case o.repositories == nil:
		return errors.New("--repository is required")
	case o.check && o.output != "":
		return errors.New("--check writes nothing: no --output (-o)")
	case !o.check && o.charts != "":
		return errors.New("--charts is read by --check only: a compile reads no chart")
	case !o.check && o.output == "":
		return errors.New("--output (-o) is required")
	}
	tg, err := newTarget(o)
	if err != nil {
		return err
	}
	if err := checkDirs(o); err != nil {
		return err
	}
	var repos []compiler.Repository
	for _, dir := range o.repositories {
		repos = append(repos, compiler.Repository{Name: dir, FS: os.DirFS(dir)})
	}
	instances, err := compiler.LoadRepository(repos)
	if err != nil {
		return err
	}
	if o.charts != "" {
		for i := range instances {
			instances[i].Inputs.Charts = os.DirFS(o.charts)
		}
	}
	refused := false
	var results []*compiler.Result
	for i, out := range compiler.CompileAll(context.Background(), instances) {
		node := instances[i].Inputs.Namespace + "/" + instances[i].Inputs.Release
		if out.Err != nil {
			fmt.Fprintf(os.Stderr, "error: %s: %v\n", node, out.Err)
			refused = true
			continue
		}
		for _, w := range out.Result.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", node, w)
		}
		results = append(results, out.Result)
	}
	if refused {
		return errReported
	}
	g, err := compiler.NewGraph(results)
	if err != nil {
		errs := []error{err}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			errs = joined.Unwrap()
		}
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
		return errReported
	}
	files, err := tg.Files(results, g)
	if err != nil || o.check {
		return err
	}
	if err := writeTree(o.output, files); err != nil {
		return err
	}
	for _, res := range results {
		for _, d := range res.Dependencies {
			contract, instance := d.Contract, d.Instance
			if d.Output != d.Instance {
				contract += " " + d.Output
			}
			fmt.Fprintf(os.Stderr, "%s/%s: depends on %s (%s, %s)\n", res.Namespace, res.Release, instance, contract, d.Field)
		}
	}
	return nil
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

// checkDirs: nil when each --repository is a directory, none given twice
// or inside another, and none contains the output directory or is inside
// it: the command removes from it what it does not write.
func checkDirs(o options) error {
	abs := make([]string, len(o.repositories))
	for i, dir := range o.repositories {
		if info, err := os.Stat(dir); err != nil {
			return fmt.Errorf("--repository: %w", err)
		} else if !info.IsDir() {
			return fmt.Errorf("--repository %s: not a directory", dir)
		}
		var err error
		if abs[i], err = filepath.Abs(dir); err != nil {
			return err
		}
		for j := range i {
			switch {
			case abs[i] == abs[j]:
				return fmt.Errorf("--repository %s is given twice", dir)
			case inside(abs[i], abs[j]):
				return fmt.Errorf("--repository %s is inside --repository %s", dir, o.repositories[j])
			case inside(abs[j], abs[i]):
				return fmt.Errorf("--repository %s is inside --repository %s", o.repositories[j], dir)
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
	for i, dir := range o.repositories {
		if inside(out, abs[i]) || inside(abs[i], out) {
			return fmt.Errorf("--output (-o) %s and --repository %s must not contain one another: the command removes what it does not write", o.output, dir)
		}
	}
	return nil
}

// inside reports whether dir is parent or inside it, both absolute.
func inside(dir, parent string) bool {
	rel, err := filepath.Rel(parent, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// writeTree: files into dir, created if needed, with the directories of
// their paths; then every file under dir/projects/ that is not one of
// them removed, and the directories left empty: the command owns
// projects/.
func writeTree(dir string, files map[string][]byte) error {
	for name, b := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return err
		}
	}
	var dirs []string
	err := filepath.WalkDir(filepath.Join(dir, "projects"), func(path string, e fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case e.IsDir():
			dirs = append(dirs, path)
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if _, ok := files[filepath.ToSlash(rel)]; !ok && err == nil {
			err = os.Remove(path)
		}
		return err
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// Deepest first: a directory after those under it.
	for _, d := range slices.Backward(dirs) {
		if entries, err := os.ReadDir(d); err == nil && len(entries) == 0 {
			if err := os.Remove(d); err != nil {
				return err
			}
		}
	}
	return nil
}
