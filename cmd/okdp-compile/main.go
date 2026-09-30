// okdp-compile --package <dir> --contracts <dir>
//
//	[--instance <dir>] -f values.yaml [-f ...] [--release demo-<service>] [--namespace demo]
//	[--out <file>] [--emit-dir <dir>]

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/okdp/okdp-compiler/compiler"
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
	pkg, contracts     string
	instance           string
	release, namespace string
	values             multi
	out, emitDir       string
}

// main: parses the flags and runs the compile.
func main() {
	var o options
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.StringVar(&o.pkg, "package", "", "package directory (service.yaml, mapping.cel.yaml, values.yaml, values.schema.json)")
	flag.StringVar(&o.contracts, "contracts", os.Getenv("OKDP_CONTRACTS"), "contract schemas directory (default $OKDP_CONTRACTS)")
	flag.StringVar(&o.instance, "instance", "", "instance directory (upstream values files: upstream.<chart>.yaml)")
	flag.StringVar(&o.release, "release", "", "release name (default demo-<service name>)")
	flag.StringVar(&o.namespace, "namespace", "demo", "namespace")
	flag.Var(&o.values, "f", "values file (repeatable)")
	flag.StringVar(&o.out, "out", "", "write the compiled values (chart -> values), the vendored chart of each chart, objects and dependencies to this file")
	flag.StringVar(&o.emitDir, "emit-dir", "", "write the final values of each chart to <dir>/<chart>.values.yaml, the vendored chart that renders each (okdp.charts.yaml), the objects and the dependencies")
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
	case o.contracts == "":
		return errors.New("--contracts (or $OKDP_CONTRACTS) is required")
	}
	def, err := compiler.LoadDefinition(os.DirFS(o.pkg))
	if err != nil {
		return err
	}
	if o.release == "" {
		o.release = "demo-" + def.Service.Name
	}
	in := compiler.Inputs{
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
		if err := emit(o.emitDir, res); err != nil {
			return err
		}
	}
	if o.out != "" {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(map[string]any{"values": res.Computed, "charts": res.Charts, "objects": res.Objects, "dependencies": res.Dependencies}); err != nil {
			return err
		}
		if err := os.WriteFile(o.out, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// emit: <dir>/<chart>.values.yaml per chart; <dir>/okdp.charts.yaml, the
// index of the vendored chart that renders each (chart: vendored chart, the
// chart itself unless service.yaml names another: `helm template
// vendor/<vendored chart> -f <chart>.values.yaml`); <dir>/okdp.objects.yaml
// and <dir>/okdp.dependencies.yaml.
func emit(dir string, res *compiler.Result) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for chart, v := range res.Values {
		b, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, chart+".values.yaml"), b, 0o644); err != nil {
			return err
		}
	}
	b, err := yaml.Marshal(res.Charts)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "okdp.charts.yaml"), b, 0o644); err != nil {
		return err
	}
	if b, err = yaml.Marshal(res.Objects); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "okdp.objects.yaml"), b, 0o644); err != nil {
		return err
	}
	if b, err = yaml.Marshal(res.Dependencies); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "okdp.dependencies.yaml"), b, 0o644)
}
