// okdp-compile --package <dir> --charts <dir> --contracts <dir>
//
//	[--instance <dir>] --platform platform.yaml [--platform ...] -f values.yaml [-f ...]
//	[--release demo-<service>] [--namespace demo]
//	[--out <file>] [--emit-dir <dir>] [--umbrella <dir>] [--indent 2]

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
	flag.Var(&o.platform, "platform", "platform values file (global, global.okdp among them), repeatable, before the -f files")
	flag.Var(&o.values, "f", "instance values file, repeatable; it sets no global but the keys the parameter schema declares")
	flag.StringVar(&o.out, "out", "", "write the compiled values (chart -> values), the upstream chart of each chart, objects and dependencies to this file")
	flag.IntVar(&o.indent, "indent", compiler.DefaultIndent, "indentation of the YAML files --out, --emit-dir and --umbrella write, 2 to 9 spaces")
	flag.StringVar(&o.emitDir, "emit-dir", "", "write the final values of each chart to <dir>/<chart>.values.yaml, the upstream chart that renders each (okdp.charts.yaml) and the dependencies (okdp.dependencies.yaml)")
	flag.StringVar(&o.umbrella, "umbrella", "", "write the instance as one chart to <dir>: Chart.yaml, an umbrella over the upstream charts, and "+compiler.UmbrellaValues+", its values (helm -f)")
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
	if o.emitDir != "" {
		files, err := compiler.Files(res, o.indent)
		if err != nil {
			return err
		}
		if err := write(o.emitDir, files); err != nil {
			return err
		}
	}
	if o.umbrella != "" {
		files, err := compiler.Umbrella(def, res, o.indent)
		if err != nil {
			return err
		}
		if err := write(o.umbrella, files); err != nil {
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

// write: files (name -> content) in dir, created if needed.
func write(dir string, files map[string][]byte) error {
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
