// okdp-compile --package <dir> --charts <dir> --contracts <dir>
//
//	[--instance <dir>] -f values.yaml [-f ...] [--release demo-<service>] [--namespace demo]
//	[--out <file>] [--emit-dir <dir>] [--indent 2]

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	compiler "github.com/okdp/okdp-compiler"
)

// version: set at build time (-ldflags "-X main.version=...").
var version = "dev"

// multi: a repeatable string flag (-f).
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
	values             multi
	out, emitDir       string
	indent             int
}

// main: parses the flags and runs the compile.
func main() {
	var o options
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.StringVar(&o.pkg, "package", "", "package directory (service.yaml, mapping.cel.yaml, values.yaml, values.schema.json)")
	flag.StringVar(&o.charts, "charts", os.Getenv("OKDP_CHARTS"), "chart cache directory: the upstream charts, <chart>-<version>.tgz (helm pull -d) or <chart>-<version>/ (default $OKDP_CHARTS)")
	flag.StringVar(&o.contracts, "contracts", os.Getenv("OKDP_CONTRACTS"), "contract schemas directory (default $OKDP_CONTRACTS)")
	flag.StringVar(&o.instance, "instance", "", "instance directory (upstream values files: upstream.<chart>.yaml)")
	flag.StringVar(&o.release, "release", "", "release name (default demo-<service name>)")
	flag.StringVar(&o.namespace, "namespace", "demo", "namespace")
	flag.Var(&o.values, "f", "values file (repeatable)")
	flag.StringVar(&o.out, "out", "", "write the compiled values (chart -> values), the upstream chart of each chart, objects and dependencies to this file")
	flag.IntVar(&o.indent, "indent", compiler.DefaultIndent, "indentation of the YAML files --out and --emit-dir write, 2 to 9 spaces")
	flag.StringVar(&o.emitDir, "emit-dir", "", "write the final values of each chart to <dir>/<chart>.values.yaml, the upstream chart that renders each (okdp.charts.yaml) and the dependencies (okdp.dependencies.yaml)")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run: loads the inputs, compiles, then writes the result.
func run(o options) error {
	switch {
	case o.pkg == "":
		return errors.New("--package is required")
	case o.charts == "":
		return errors.New("--charts (or $OKDP_CHARTS) is required")
	case o.contracts == "":
		return errors.New("--contracts (or $OKDP_CONTRACTS) is required")
	case o.indent < 2 || o.indent > 9:
		return fmt.Errorf("--indent %d: 2 to 9 spaces", o.indent)
	}
	def, err := compiler.LoadDefinition(os.DirFS(o.pkg))
	if err != nil {
		return err
	}
	if o.release == "" {
		o.release = "demo-" + def.Service.Name
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
	for _, f := range o.values {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		v, err := compiler.ParseValues(b)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		in.Values = append(in.Values, v)
	}
	res, err := compiler.Compile(def, in)
	if err != nil {
		return err
	}
	if o.emitDir != "" {
		if err := emit(o.emitDir, res, o.indent); err != nil {
			return err
		}
	}
	if o.out != "" {
		b, err := compiler.Document(res, o.indent)
		if err != nil {
			return err
		}
		if err := os.WriteFile(o.out, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// emit: the files of the compile (compiler.Files) in dir, indented by indent
// spaces.
func emit(dir string, res *compiler.Result, indent int) error {
	files, err := compiler.Files(res, indent)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
