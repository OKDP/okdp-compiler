// okdp-compile --package <dir> --charts <dir> --contracts <dir>
//
//	--platform platform.yaml [--platform ...] [--project project.yaml ...]
//	-f values.yaml [-f ...]
//	--release <name> --namespace <name> -o <dir>
//	[--mkdag]

package main

import (
	"cmp"
	"context"
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
// the release binaries. Unset, --version prints the main module's version,
// without the leading v of its tag: go install ...@v0.1.0 gives 0.1.0, go
// build in a checkout the pseudo-version Go derives from the last tag and
// the commit; else dev: no build info, a build without VCS information, go
// tool in another module.
var version string

// multi: a repeatable string flag (--platform, --project, -f).
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
	platform, project  multi
	values             multi
	output             string
	mkdag              bool
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
		if info, ok := debug.ReadBuildInfo(); ok && version == "" && info.Main.Version != "(devel)" {
			version = strings.TrimPrefix(info.Main.Version, "v")
		}
		fmt.Println(cmp.Or(version, "dev"))
		return
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
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
	f.Var(&o.platform, "platform", "platform values file (global, global.okdp among them), repeatable, before the --project and -f files")
	f.Var(&o.project, "project", "project values file, repeatable, after the --platform files: what the instance's project adds to global and connections; it changes no value the platform sets")
	f.Var(&o.values, "f", "instance values file, repeatable; it sets no global but the keys the parameter schema declares")
	f.StringVar(&o.output, "output", "", "output directory, required: the instance as one chart, Chart.yaml, an umbrella over the upstream charts, and "+compiler.UmbrellaValues+", its values (helm -f); <dir> holds only the files okdp-compile writes")
	f.StringVar(&o.output, "o", "", "shorthand for --output")
	f.BoolVar(&o.mkdag, "mkdag", false, "also write "+compiler.DependenciesFile+": the instances the connection references point to, for a deployer that orders the releases")
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

// run: checks the options, loads the inputs and compiles, then builds every output (compiler.Write) before it writes any:
// a compile or an output that fails writes nothing.
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
	if in.Project, err = readValues(o.project); err != nil {
		return err
	}
	if in.Values, err = readValues(o.values); err != nil {
		return err
	}
	res, err := compiler.Compile(context.Background(), def, in)
	if err != nil {
		return err
	}
	files, err := compiler.Write(res, compiler.Output{Dependencies: o.mkdag})
	if err != nil {
		return err
	}
	d := outputDir{o.output, files}
	stale, err := d.stale()
	if err != nil {
		return err
	}
	return d.write(stale)
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

// stale: the files of d.dir a previous compile wrote that this one does not
// (compiler.IsOutputFile: okdp.dependencies.yaml without --mkdag). A directory that holds anything
// else, or a non-regular file of these names, is refused: a directory
// given by mistake (-o .) loses nothing.
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
		if !e.Type().IsRegular() || !current && !compiler.IsOutputFile(name) {
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
		if err := os.WriteFile(filepath.Join(d.dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
