// okdp-compile --package <dir> --charts <dir> --contracts <dir>
//
//	[--instance <dir>] --platform platform.yaml [--platform ...] -f values.yaml [-f ...]
//	--release <name> --namespace <name>
//	[--out <file>] [--emit-dir <dir>] [--umbrella <dir>] [--indent 2]

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	compiler "github.com/okdp/okdp-compiler"
)

// version: set at build time (-ldflags "-X main.version=...").
var version = "dev"

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
	instance           string
	release, namespace string
	platform, values   multi
	out, emitDir       string
	umbrella           string
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
		fmt.Println(version)
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
	f.StringVar(&o.pkg, "package", "", "package directory (service.yaml, mapping.cel.yaml, values.yaml, values.schema.json)")
	f.StringVar(&o.charts, "charts", os.Getenv("OKDP_CHARTS"), "chart cache directory: the upstream charts, <repository>/<chart>-<version>.tgz (helm pull -d <dir>/<repository>) or <repository>/<chart>-<version>/, <repository> the URL without its scheme (default $OKDP_CHARTS)")
	f.StringVar(&o.contracts, "contracts", os.Getenv("OKDP_CONTRACTS"), "contract schemas directory (default $OKDP_CONTRACTS)")
	f.StringVar(&o.instance, "instance", "", "instance directory (upstream values files: upstream.<chart>.yaml)")
	f.StringVar(&o.release, "release", "", "release name, required: the Helm release the values are compiled for")
	f.StringVar(&o.namespace, "namespace", "", "namespace, required: the release's")
	f.Var(&o.platform, "platform", "platform values file (global, global.okdp among them), repeatable, before the -f files")
	f.Var(&o.values, "f", "instance values file, repeatable; it sets no global but the keys the parameter schema declares")
	f.StringVar(&o.out, "out", "", "write the compiled values (chart -> values), the upstream chart of each chart, objects and dependencies to this file")
	f.IntVar(&o.indent, "indent", compiler.DefaultIndent, "indentation of the YAML files --out, --emit-dir and --umbrella write, 2 to 9 spaces")
	f.StringVar(&o.emitDir, "emit-dir", "", "write the final values of each chart to <dir>/<chart>.values.yaml, the upstream chart that renders each (okdp.charts.yaml) and the dependencies (okdp.dependencies.yaml); <dir> holds only these files")
	f.StringVar(&o.umbrella, "umbrella", "", "write the instance as one chart to <dir>: Chart.yaml, an umbrella over the upstream charts, and "+compiler.UmbrellaValues+", its values (helm -f); <dir> holds only these files")
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
	case o.indent < 2 || o.indent > 9:
		return fmt.Errorf("--indent %d: 2 to 9 spaces", o.indent)
	case o.emitDir != "" && o.umbrella != "" && sameDir(o.emitDir, o.umbrella):
		return fmt.Errorf("--emit-dir and --umbrella name the same directory, %s: give each its own", o.umbrella)
	}
	for _, d := range [][2]string{{"package", o.pkg}, {"charts", o.charts}, {"contracts", o.contracts}, {"instance", o.instance}} {
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
	if o.instance != "" {
		in.Instance = os.DirFS(o.instance)
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
	var dirs []outputDir
	if o.emitDir != "" {
		files, err := compiler.Files(res, o.indent)
		if err != nil {
			return err
		}
		dirs = append(dirs, outputDir{o.emitDir, files, emitted})
	}
	if o.umbrella != "" {
		files, err := compiler.Umbrella(def, res, o.indent)
		if err != nil {
			return err
		}
		dirs = append(dirs, outputDir{o.umbrella, files, umbrellaFile})
	}
	var doc []byte
	if o.out != "" {
		if doc, err = compiler.Document(res, o.indent); err != nil {
			return err
		}
	}
	stale := make([][]string, len(dirs))
	for i, d := range dirs {
		if stale[i], err = d.stale(); err != nil {
			return err
		}
	}
	for i, d := range dirs {
		if err := d.write(stale[i]); err != nil {
			return err
		}
	}
	if o.out != "" {
		return os.WriteFile(o.out, doc, 0o644)
	}
	return nil
}

// inputDir: an error unless dir, given by --<name>, is a directory; none is
// given when dir is empty (--instance).
func inputDir(name, dir string) error {
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--%s: %w", name, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--%s %s: not a directory", name, dir)
	}
	return nil
}

// sameDir: whether two paths name the same directory.
func sameDir(a, b string) bool {
	a, errA := filepath.Abs(a)
	b, errB := filepath.Abs(b)
	return errA == nil && errB == nil && a == b
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

// outputDir: a directory okdp-compile writes: its files (name -> content),
// and ours, whether a file name is one it writes there, in any compile.
type outputDir struct {
	dir   string
	files map[string][]byte
	ours  func(name string) bool
}

// emitted: a file --emit-dir writes (compiler.Files): <key>.values.yaml,
// okdp.charts.yaml, okdp.dependencies.yaml.
func emitted(name string) bool {
	return strings.HasSuffix(name, ".values.yaml") || name == "okdp.charts.yaml" || name == "okdp.dependencies.yaml"
}

// umbrellaFile: a file --umbrella writes (compiler.Umbrella).
func umbrellaFile(name string) bool {
	return name == "Chart.yaml" || name == compiler.UmbrellaValues
}

// stale: the files of d.dir a previous compile wrote that this one does not
// (a chart no longer compiled). A directory that holds anything else, or a
// non-regular file of these names, is refused: a directory given by
// mistake (--emit-dir .) loses nothing.
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
		if !e.Type().IsRegular() || !current && !d.ours(name) {
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
