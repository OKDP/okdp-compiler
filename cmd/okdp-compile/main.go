// okdp-compile --package <dir> --charts <dir> --contracts <dir>
//
//	--platform platform.yaml [--platform ...] -f values.yaml [-f ...]
//	--release <name> --namespace <name> -o <dir>
//	[--umbrella] [--mkinstall] [--mkdag] [--indent 2]

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	compiler "github.com/okdp/okdp-compiler"
)

// version: set at build time (-ldflags "-X main.version=..."): make build,
// the release binaries. Unset, --version prints the module's (versionOf).
var version string

// multi: a repeatable string flag (--platform, -f).
type multi []string

// String: the values, comma separated (flag.Value).
func (m *multi) String() string { return strings.Join(*m, ",") }

// Set: adds a value (flag.Value).
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

// options: the command-line flags.
type options struct {
	pkg, charts        string
	contracts          string
	release, namespace string
	platform, values   multi
	output             string
	umbrella           bool
	mkinstall, mkdag   bool
	indent             int
	version            bool
}

// main: parses the flags and runs the compile.
func main() {
	o, err := parse(os.Args[1:], os.Stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return
	case err != nil:
		os.Exit(2)
	case o.version:
		info, _ := debug.ReadBuildInfo()
		fmt.Println(versionOf(version, info))
		return
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// versionOf: the version --version prints. set, the version set at build
// time, when there is one; else the version of the module of the main
// package in info, the build info, without the leading v of its tag: go
// install ...@v0.1.0 gives 0.1.0, go build in a checkout the pseudo-version
// Go derives from the last tag and the commit (+dirty with uncommitted
// changes), go tool in another module the version it requires; else dev: no
// build info, a build without VCS information ((devel)), a module replaced
// by a directory.
func versionOf(set string, info *debug.BuildInfo) string {
	if set != "" {
		return set
	}
	if info == nil {
		return "dev"
	}
	// The module of the main package: the longest module path it is in.
	var mod *debug.Module
	for _, m := range append([]*debug.Module{&info.Main}, info.Deps...) {
		in := info.Path == m.Path || strings.HasPrefix(info.Path, m.Path+"/")
		if in && (mod == nil || len(m.Path) > len(mod.Path)) {
			mod = m
		}
	}
	if mod != nil && mod.Replace != nil {
		mod = mod.Replace
	}
	if mod == nil || mod.Version == "" || mod.Version == "(devel)" {
		return "dev"
	}
	return strings.TrimPrefix(mod.Version, "v")
}

// parse: the flags of args, the arguments after the program's name. A usage
// error (an unknown flag, a flag without its argument, an argument that is
// not a flag) is written to stderr with the usage. An argument that is not a
// flag would end the flags, and those after it would be ignored: a values
// file without its -f.
func parse(args []string, stderr io.Writer) (options, error) {
	var o options
	f := flag.NewFlagSet("okdp-compile", flag.ContinueOnError)
	f.SetOutput(stderr)
	f.BoolVar(&o.version, "version", false, "print the version and exit")
	f.StringVar(&o.pkg, "package", "", "package directory (service.yaml, values.cel.yaml, config.schema.json)")
	f.StringVar(&o.charts, "charts", os.Getenv("OKDP_CHARTS"), "chart cache directory: the upstream charts, <repository>/<chart>-<version>.tgz (helm pull -d <dir>/<repository>) or <repository>/<chart>-<version>/, <repository> the URL without its scheme (default $OKDP_CHARTS)")
	f.StringVar(&o.contracts, "contracts", os.Getenv("OKDP_CONTRACTS"), "contract schemas directory (default $OKDP_CONTRACTS)")
	f.StringVar(&o.release, "release", "", "release name, required: the Helm release the values are compiled for")
	f.StringVar(&o.namespace, "namespace", "", "namespace, required: the release's")
	f.Var(&o.platform, "platform", "platform values file (global, global.okdp among them), repeatable, before the -f files")
	f.Var(&o.values, "f", "instance values file, repeatable; it sets no global but the keys the parameter schema declares")
	f.StringVar(&o.output, "output", "", "output directory, required: the final values of each chart, <dir>/<chart>.values.yaml; <dir> holds only the files okdp-compile writes")
	f.StringVar(&o.output, "o", "", "shorthand for --output")
	f.BoolVar(&o.umbrella, "umbrella", false, "write the instance as one chart instead: Chart.yaml, an umbrella over the upstream charts, and "+compiler.UmbrellaValues+", its values (helm -f)")
	f.BoolVar(&o.mkinstall, "mkinstall", false, "also write "+compiler.InstallScript+": a script that installs the instance with Helm, one release per chart, or one release of the umbrella chart with --umbrella")
	f.BoolVar(&o.mkdag, "mkdag", false, "also write "+compiler.DependenciesFile+": the instances the connection references point to, for a deployer that orders the releases")
	f.IntVar(&o.indent, "indent", compiler.DefaultIndent, "indentation of the YAML files written, 2 to 9 spaces")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() > 0 {
		err := fmt.Errorf("unexpected argument %q: every input is a flag (-f before each values file)", f.Arg(0))
		_, _ = fmt.Fprintln(stderr, err)
		f.Usage()
		return o, err
	}
	return o, nil
}

// run: checks the options, loads the inputs and compiles, then builds every
// output before it writes any: a compile or an output that fails writes
// nothing.
func run(o options) error {
	switch {
	case o.pkg == "":
		return errors.New("--package is required")
	case o.charts == "":
		return errors.New("--charts (or $OKDP_CHARTS) is required")
	case o.contracts == "":
		return errors.New("--contracts (or $OKDP_CONTRACTS) is required")
	case o.release == "":
		return errors.New("--release is required")
	case o.namespace == "":
		return errors.New("--namespace is required")
	case o.output == "":
		return errors.New("--output (-o) is required")
	case o.indent < 2 || o.indent > 9:
		return fmt.Errorf("--indent %d: 2 to 9 spaces", o.indent)
	}
	for _, d := range [][2]string{{"package", o.pkg}, {"charts", o.charts}, {"contracts", o.contracts}} {
		if err := inputDir(d[0], d[1]); err != nil {
			return err
		}
	}
	def, err := compiler.LoadDefinition(os.DirFS(o.pkg))
	if err != nil {
		return err
	}
	in := compiler.Inputs{
		Charts:    os.DirFS(o.charts),
		Contracts: os.DirFS(o.contracts),
		Release:   o.release,
		Namespace: o.namespace,
	}
	if in.Platform, err = readValues(o.platform); err != nil {
		return err
	}
	if in.Values, err = readValues(o.values); err != nil {
		return err
	}
	res, err := compiler.Compile(def, in)
	if err != nil {
		return err
	}
	var files map[string][]byte
	if o.umbrella {
		files, err = compiler.Umbrella(def, res, o.indent)
	} else {
		files, err = compiler.Files(res, o.indent)
	}
	if err != nil {
		return err
	}
	if o.mkinstall {
		install := compiler.Install
		if o.umbrella {
			install = compiler.InstallUmbrella
		}
		if files[compiler.InstallScript], err = install(res); err != nil {
			return err
		}
	}
	if o.mkdag {
		if files[compiler.DependenciesFile], err = compiler.Dependencies(res, o.indent); err != nil {
			return err
		}
	}
	d := outputDir{o.output, files}
	stale, err := d.stale()
	if err != nil {
		return err
	}
	return d.write(stale)
}

// inputDir: an error unless dir, given by --<name>, is a directory.
func inputDir(name, dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--%s: %w", name, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--%s %s: not a directory", name, dir)
	}
	return nil
}

// readValues: values files, as values layers.
func readValues(files []string) ([]map[string]any, error) {
	var layers []map[string]any
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		v, err := compiler.ParseValues(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		layers = append(layers, v)
	}
	return layers, nil
}

// outputDir: the directory okdp-compile writes (--output), and its files,
// name -> content.
type outputDir struct {
	dir   string
	files map[string][]byte
}

// ours: a file okdp-compile writes in its output directory, in any compile:
// <key>.values.yaml (compiler.Files), Chart.yaml and
// compiler.UmbrellaValues (--umbrella), compiler.InstallScript
// (--mkinstall), compiler.DependenciesFile (--mkdag); and okdp.charts.yaml,
// which v0.1.0 wrote.
func ours(name string) bool {
	switch name {
	case "Chart.yaml", compiler.UmbrellaValues, compiler.InstallScript, compiler.DependenciesFile, "okdp.charts.yaml":
		return true
	}
	return strings.HasSuffix(name, ".values.yaml")
}

// stale: the files of d.dir a previous compile wrote that this one does not
// (a chart no longer compiled, the files of the other mode, --umbrella or
// not). A directory that holds anything else, or a non-regular file of
// these names, is refused: a directory given by mistake (-o .) loses
// nothing.
func (d outputDir) stale() ([]string, error) {
	entries, err := os.ReadDir(d.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, e := range entries {
		name := e.Name()
		_, current := d.files[name]
		if !e.Type().IsRegular() || !current && !ours(name) {
			return nil, fmt.Errorf("refusing to write in %s: %s is not an output of okdp-compile", d.dir, name)
		}
		if !current {
			stale = append(stale, name)
		}
	}
	return stale, nil
}

// write: removes the stale files of d.dir, then writes its files, the
// directory created if needed.
func (d outputDir) write(stale []string) error {
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return err
	}
	for _, name := range stale {
		if err := os.Remove(filepath.Join(d.dir, name)); err != nil {
			return err
		}
	}
	for name, b := range d.files {
		perm := os.FileMode(0o644)
		if name == compiler.InstallScript {
			perm = 0o755
		}
		path := filepath.Join(d.dir, name)
		if err := os.WriteFile(path, b, perm); err != nil {
			return err
		}
		// WriteFile sets the mode of a file it creates only.
		if err := os.Chmod(path, perm); err != nil {
			return err
		}
	}
	return nil
}
