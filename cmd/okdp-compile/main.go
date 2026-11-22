// okdp-compile -f <file|dir> [-f <file|dir>]... --catalog <dir> [--catalog <dir>]... -o <dir>
//
//	[--rules <dir>] [--cost-limit <n>] [--instance-timeout <duration>] [--timeout <duration>]
//	[--force] [--allow-plain-secrets]
//
// The command reads the input documents of every -f, a file or a
// directory of them laid out in any way (compiler.LoadInputs), compiles
// every instance together, each claim bound to its provider, and writes
// the files of each for helm install. With --check in place of -o, it
// checks the compile and writes nothing. It reads no upstream chart and
// runs no check of the final values against the charts: a host does
// those.
//
// It is a thin wrapper of compiler.LoadCatalog, compiler.LoadInputs,
// compiler.CompileAll and compiler.Write. It writes no file for a
// deployer engine (Flux, Argo CD): a host does.

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
	files             []string      // -f, --file: input documents, a file or a directory
	catalogs          []string      // --catalog: catalog/ and contracts/
	rules             string        // --rules: the rule set added to the mandatory one; "" adds none
	output            string        // --output, -o
	check             bool          // --check
	costLimit         uint64        // --cost-limit, per instance; 0 for no limit
	timeout           time.Duration // --timeout, for the whole compile; 0 for no limit
	instanceTimeout   time.Duration // --instance-timeout, per instance; 0 for no limit
	force             bool          // --force: write into an output without its marker
	allowPlainSecrets bool          // --allow-plain-secrets, for every instance
}

// The defaults of the flags, the compiler's own.
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
	addFile := func(s string) error { o.files = append(o.files, s); return nil }
	flag.Func("file", "input documents, required, repeatable: a YAML file, or a directory whose .yaml and .yml files are read, laid out in any way; each document is of kind Settings, Project or Instance, and the files they name in valuesFiles are values files", addFile)
	flag.Func("f", "shorthand for --file", addFile)
	flag.Func("catalog", "a catalog, its directory, required, repeatable: its catalog/<service>/<version>/ and contracts/, nothing else of it read", func(s string) error { o.catalogs = append(o.catalogs, s); return nil })
	flag.StringVar(&o.rules, "rules", "", "a rule set's directory, rules.yaml at its root, added to the compiler's mandatory rules (the functions, the templates and the checks), which every compile applies; it adds rules and replaces none")
	flag.BoolVar(&o.check, "check", false, "compile and check, write nothing: the errors and warnings, no --output")
	flag.StringVar(&o.output, "output", "", "output directory, required but with --check: projects/<namespace>/instances/<release>/, values.yaml, the values of the catalog's chart (helm install <release> <package> -f values.yaml), okdp-objects.values.yaml, those of the "+compiler.ObjectsChart+" chart, when it has objects, installed first, okdp.info.yaml, what it publishes, when it publishes something; then the dependencies printed on stderr")
	flag.StringVar(&o.output, "o", "", "shorthand for --output")
	flag.Uint64Var(&o.costLimit, "cost-limit", defaultCostLimit, "the CEL cost each instance's expressions may use together, past which the instance is refused; 0: no limit")
	flag.DurationVar(&o.instanceTimeout, "instance-timeout", defaultInstanceTimeout, "how long one instance's compile may take, past which that instance is refused; 0: no limit")
	flag.DurationVar(&o.timeout, "timeout", defaultTimeout, "how long the compile may take, every instance together, past which nothing is written; 0: no limit")
	flag.BoolVar(&o.allowPlainSecrets, "allow-plain-secrets", false, "accept credentials written in clear (Inputs.AllowPlainSecrets of every instance): a provides secret without generate, a Secret of the objects with data or stringData")
	flag.BoolVar(&o.force, "force", false, "write into an <output>/projects/ that okdp-compile did not write (no "+outdir.MarkerFile+"), removing from it every file it does not write")
	flag.Parse()
	switch {
	case flag.NArg() > 0:
		fmt.Fprintf(os.Stderr, "unexpected argument %q: every input is a flag (-f before each file or directory)\n", flag.Arg(0))
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

// run compiles the instances of the input documents and prints their
// bindings, refusals and warnings. Unless --check, it then writes the
// files of each instance and prints the dependencies. It checks every
// option first, and writes nothing when anything fails.
func run(o options) error {
	if err := checkOptions(o); err != nil {
		return err
	}
	if err := checkPaths(o); err != nil {
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
	instances, outcomes, err := compile(o, in)
	if err != nil {
		return err
	}
	if printOutcomes(instances, outcomes) {
		return errReported
	}
	if o.check {
		return nil
	}
	files := map[string][]byte{outdir.MarkerFile: []byte(outdir.Marker)}
	for _, out := range outcomes {
		res := out.Result
		written, err := compiler.Write(res)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", res.Namespace, res.Release, err)
		}
		for name, b := range written {
			files[path.Join("projects", res.Namespace, "instances", res.Release, name)] = b
		}
	}
	if err := outdir.Write(o.output, files); err != nil {
		return err
	}
	printDependencies(outcomes)
	return nil
}

// checkOptions returns nil when the options go together.
func checkOptions(o options) error {
	switch {
	case o.files == nil:
		return errors.New("-f (--file) is required")
	case o.catalogs == nil:
		return errors.New("--catalog is required")
	case o.check && o.output != "":
		return errors.New("--check writes nothing: no --output (-o)")
	case !o.check && o.output == "":
		return errors.New("--output (-o) is required")
	case o.check && o.force:
		return errors.New("--check writes nothing: no --force")
	}
	return nil
}

// inputs holds what the command reads, once open.
type inputs struct {
	files   []compiler.InputFiles
	catalog *compiler.Catalog
	rules   *compiler.RuleSet // nil without --rules: the mandatory set alone
}

// openInputs opens the directories of -f, --catalog and --rules through
// dirfs, which bounds what is read, and reads the catalog and the rule
// set. A file of -f is read in the directory that holds it.
func openInputs(o options) (*inputs, error) {
	in := &inputs{}
	var catalogs []compiler.CatalogFiles
	for _, dir := range o.catalogs {
		fsys, err := dirfs.New("--catalog", dir)
		if err != nil {
			return nil, err
		}
		catalogs = append(catalogs, compiler.CatalogFiles{Name: dir, FS: fsys})
	}
	for _, p := range o.files {
		dir, file := p, ""
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			dir, file = filepath.Dir(p), filepath.Base(p)
		}
		fsys, err := dirfs.New("-f", dir)
		if err != nil {
			return nil, err
		}
		in.files = append(in.files, compiler.InputFiles{Name: filepath.ToSlash(dir), FS: fsys, Path: file})
	}
	if o.rules != "" {
		fsys, err := dirfs.New("--rules", o.rules)
		if err != nil {
			return nil, err
		}
		if in.rules, err = compiler.LoadRules(fsys); err != nil {
			return nil, fmt.Errorf("--rules %s: %w", o.rules, err)
		}
	}
	cat, err := compiler.LoadCatalog(catalogs)
	if err != nil {
		return nil, err
	}
	in.catalog = cat
	return in, nil
}

// compile reads the instances of the input documents and compiles them
// together within --timeout. The outcomes are in the instances' order.
func compile(o options, in *inputs) ([]compiler.Instance, []compiler.Outcome, error) {
	ctx := context.Background()
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}
	instances, err := compiler.LoadInputs(ctx, in.files, in.catalog, in.rules)
	if err != nil {
		return nil, nil, err
	}
	if len(instances) == 0 {
		return nil, nil, errors.New("no instance: -f holds no document of kind Instance")
	}
	for i := range instances {
		instances[i].Inputs.CostLimit = o.costLimit
		instances[i].Inputs.Timeout = o.instanceTimeout
		instances[i].Inputs.AllowPlainSecrets = o.allowPlainSecrets
		instances[i].Inputs.Rules = in.rules
	}
	outcomes := compiler.CompileAll(ctx, instances)
	if ctx.Err() != nil {
		return nil, nil, fmt.Errorf("the compile takes longer than --timeout %s: nothing written", o.timeout)
	}
	return instances, outcomes, nil
}

// printOutcomes prints each instance's bindings and its refusal or
// warnings. It reports whether anything is refused.
func printOutcomes(instances []compiler.Instance, outcomes []compiler.Outcome) (refused bool) {
	for i, out := range outcomes {
		node := instances[i].Inputs.Namespace + "/" + instances[i].Inputs.Release
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

// printDependencies prints the dependencies of the instances on stderr:
//
//	<namespace>/<release>: depends on <instance> (<contract> <output>, <field>)
func printDependencies(outcomes []compiler.Outcome) {
	for _, out := range outcomes {
		res := out.Result
		for _, d := range res.Dependencies {
			instance := d.Instance
			if d.Namespace != "" {
				instance = d.Namespace + "/" + instance
			}
			fmt.Fprintf(os.Stderr, "%s/%s: depends on %s (%s %s, %s)\n", res.Namespace, res.Release, instance, d.Contract, d.Output, d.Field)
		}
	}
}

type inputPath struct{ flag, path string }

// checkPaths returns nil when each --catalog is a directory, each -f a
// file or a directory, and none is given twice. The output must be apart
// from every input and from --rules: the command removes from its output
// what it does not write.
func checkPaths(o options) error {
	var paths []inputPath
	for _, f := range o.files {
		paths = append(paths, inputPath{"-f", f})
	}
	for _, c := range o.catalogs {
		paths = append(paths, inputPath{"--catalog", c})
	}
	if o.rules != "" {
		paths = append(paths, inputPath{"--rules", o.rules})
	}
	abs := make([]string, len(paths))
	for i, p := range paths {
		info, err := os.Stat(p.path)
		switch {
		case err != nil:
			return fmt.Errorf("%s: %w", p.flag, err)
		case p.flag != "-f" && !info.IsDir():
			return fmt.Errorf("%s %s: not a directory", p.flag, p.path)
		}
		if abs[i], err = filepath.Abs(p.path); err != nil {
			return err
		}
		for j := range i {
			if abs[i] == abs[j] && paths[j].flag == p.flag {
				return fmt.Errorf("%s %s is given twice", p.flag, p.path)
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
	for i, p := range paths {
		if inside(out, abs[i]) || inside(abs[i], out) {
			return fmt.Errorf("--output (-o) %s and %s %s must not contain one another: the command removes what it does not write", o.output, p.flag, p.path)
		}
	}
	return nil
}

// inside reports whether dir is parent or inside it, both absolute.
func inside(dir, parent string) bool {
	rel, err := filepath.Rel(parent, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
