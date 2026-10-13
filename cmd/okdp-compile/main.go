// okdp-compile --package <dir> --charts <dir> --contracts <dir>
//
//	--context [<level>:]context.yaml [--context ...]
//	-f values.yaml [-f ...]
//	--release <name> --namespace <name> -o <dir>

package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	compiler "github.com/okdp/okdp-compiler"
)

// version: set at build time (-ldflags "-X main.version=..."): make build,
// the release binaries; --version prints dev without it.
var version string

// options: the command-line flags.
type options struct {
	pkg, charts        string
	contracts          string
	release, namespace string
	contexts           []string
	values             []string
	output             string
}

// main: parses the flags and runs the compile. A usage error (an unknown
// flag, a flag without its argument, an argument that is not a flag) exits
// with status 2, the message and the usage on stderr. An argument that is
// not a flag would end the flags, and those after it would be ignored: a
// values file without its -f.
func main() {
	var o options
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.StringVar(&o.pkg, "package", "", "package directory (service.yaml, values.cel.yaml, config.schema.json)")
	flag.StringVar(&o.charts, "charts", os.Getenv("OKDP_CHARTS"), "chart cache directory: the upstream charts unpacked, <repository>/<chart>-<version>/<chart>/ (helm pull --untar -d <dir>/<repository>/<chart>-<version>), <repository> the URL without its scheme (default $OKDP_CHARTS)")
	flag.StringVar(&o.contracts, "contracts", os.Getenv("OKDP_CONTRACTS"), "contract schemas directory (default $OKDP_CONTRACTS)")
	flag.StringVar(&o.release, "release", "", "release name, required: the Helm release the values are compiled for")
	flag.StringVar(&o.namespace, "namespace", "", "namespace, required: the release's")
	flag.Func("context", "context values file, [<level>:]<file>, repeatable, before the -f files: global, global.okdp and connections among them; level 0 (the default), the platform's, first, a level's files merged in order; a level adds values, it changes none of a lower level's", func(s string) error { o.contexts = append(o.contexts, s); return nil })
	flag.Func("f", "instance values file, repeatable; it sets no global but the keys the parameter schema declares, and no connections: an instance's own are a --context file of its level", func(s string) error { o.values = append(o.values, s); return nil })
	flag.StringVar(&o.output, "output", "", "output directory, required: the instance as one chart, Chart.yaml, an umbrella over the upstream charts, "+compiler.UmbrellaValues+", its values (helm -f), and "+compiler.DependenciesFile+", the instances the connection references point to")
	flag.StringVar(&o.output, "o", "", "shorthand for --output")
	flag.Parse()
	switch {
	case flag.NArg() > 0:
		fmt.Fprintf(os.Stderr, "unexpected argument %q: every input is a flag (-f before each values file)\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	case *showVersion:
		fmt.Println(cmp.Or(version, "dev"))
	default:
		if err := run(o); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
}

// run: checks the options, loads the inputs and compiles, then writes the
// files of compiler.Write into the output directory, created if needed: a
// compile that fails writes nothing.
func run(o options) error {
	for _, f := range [][2]string{{"--package", o.pkg}, {"--charts (or $OKDP_CHARTS)", o.charts}, {"--contracts (or $OKDP_CONTRACTS)", o.contracts}, {"--release", o.release}, {"--namespace", o.namespace}, {"--output (-o)", o.output}} {
		if f[1] == "" {
			return fmt.Errorf("%s is required", f[0])
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
	for _, c := range o.contexts {
		// [<level>:]<file>: a file without a level is the platform's, 0.
		var level uint64
		if l, file, ok := strings.Cut(c, ":"); ok {
			if n, err := strconv.ParseUint(l, 10, 0); err == nil {
				level, c = n, file
			}
		}
		v, err := readValues(c)
		if err != nil {
			return err
		}
		in.Context = append(in.Context, compiler.ContextLayer{Level: uint(level), Values: v})
	}
	for _, f := range o.values {
		v, err := readValues(f)
		if err != nil {
			return err
		}
		in.Values = append(in.Values, v)
	}
	res, err := compiler.Compile(context.Background(), def, in)
	if err != nil {
		return err
	}
	files, err := compiler.Write(res)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.output, 0o755); err != nil {
		return err
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(o.output, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// readValues: a values file, as a values layer.
func readValues(file string) (map[string]any, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	v, err := compiler.ParseValues(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return v, nil
}
