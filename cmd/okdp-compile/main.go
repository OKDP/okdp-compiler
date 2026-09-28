// okdp-compile --chart <wrapper chart dir> --service <dir> --contracts <dir>
//
//	[--instance <dir>] -f values.yaml [-f ...] [--release demo-<chart>] [--namespace demo]
//	[--expect <helm template output>] [--out <file>] [--emit-dir <dir>]

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
	chart, service, contracts string
	instance                  string
	release, namespace        string
	values                    multi
	expect, out, emitDir      string
}

// main: parses the flags and runs the compile.
func main() {
	var o options
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.StringVar(&o.chart, "chart", "", "wrapper chart directory")
	flag.StringVar(&o.service, "service", "", "service definition directory (service.yaml, mapping.cel.yaml)")
	flag.StringVar(&o.contracts, "contracts", os.Getenv("OKDP_CONTRACTS"), "contract schemas directory (default $OKDP_CONTRACTS)")
	flag.StringVar(&o.instance, "instance", "", "instance directory (upstream values files: upstream.<chart>.yaml)")
	flag.StringVar(&o.release, "release", "", "release name (default demo-<chart>)")
	flag.StringVar(&o.namespace, "namespace", "demo", "namespace")
	flag.Var(&o.values, "f", "values file (repeatable)")
	flag.StringVar(&o.expect, "expect", "", "helm template output of the wrapper chart to compare with")
	flag.StringVar(&o.out, "out", "", "write the compiled values (chart -> values), objects and dependencies to this file")
	flag.StringVar(&o.emitDir, "emit-dir", "", "write the final values of each chart to <dir>/<chart>.values.yaml, the objects and the dependencies")
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

// run: loads the inputs, compiles, then writes and compares the result.
func run(o options) error {
	switch {
	case o.chart == "":
		return errors.New("--chart is required")
	case o.service == "":
		return errors.New("--service is required")
	case o.contracts == "":
		return errors.New("--contracts (or $OKDP_CONTRACTS) is required")
	}
	if o.release == "" {
		o.release = "demo-" + filepath.Base(o.chart)
	}
	in := compiler.Inputs{
		Chart:     os.DirFS(o.chart),
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
	def, err := compiler.LoadDefinition(os.DirFS(o.service))
	if err != nil {
		return err
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
		if err := enc.Encode(map[string]any{"values": res.Computed, "objects": res.Objects, "dependencies": res.Dependencies}); err != nil {
			return err
		}
		if err := os.WriteFile(o.out, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	if o.expect == "" {
		return nil
	}
	rendered, err := os.ReadFile(o.expect)
	if err != nil {
		return err
	}
	return compiler.Compare(def, in, res, rendered, os.Stdout)
}

// emit: <dir>/<chart>.values.yaml per chart, <dir>/okdp.objects.yaml and
// <dir>/okdp.dependencies.yaml.
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
	b, err := yaml.Marshal(res.Objects)
	if err != nil {
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
