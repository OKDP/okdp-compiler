// okdp-compile --package <dir> [--contracts <dir>] [--published <dir>]
//
//	--context [<level>:]context.yaml [--context ...]
//	-f values.yaml [-f ...]
//	--release <name> --namespace <name> -o <dir>
//
// okdp-compile --check [--charts <dir>], the same inputs, no -o: the
// compile checked, the upstream charts too with --charts; nothing written.

package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	compiler "github.com/okdp/okdp-compiler"
)

// version: set at build time (-ldflags "-X main.version=..."): make build,
// the release binaries; --version prints dev without it.
var version string

// options: the command-line flags.
type options struct {
	pkg, charts        string
	contracts          string
	published          string
	release, namespace string
	contexts           []string
	values             []string
	output             string
	check              bool
}

// main: parses the flags and runs the compile. A usage error (an unknown
// flag, a flag without its argument, an argument that is not a flag) exits
// with status 2, the message and the usage on stderr. An argument that is
// not a flag would end the flags, and those after it would be ignored: a
// values file without its -f.
func main() {
	var o options
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.StringVar(&o.pkg, "package", "", "package directory (Chart.yaml, okdp.yaml, values.cel.yaml or values.yaml, config.schema.json)")
	flag.BoolVar(&o.check, "check", false, "compile and check, write nothing: the compile's errors and warnings, no --output; with --charts, the final values checked against the upstream charts")
	flag.StringVar(&o.charts, "charts", "", "chart cache directory, read by --check only: the upstream charts unpacked, <repository>/<chart>-<version>/<chart>/ (helm pull --untar -d <dir>/<repository>/<chart>-<version>), <repository> the URL without its scheme; the final values, over each chart's defaults, checked against its values.schema.json, and an instance list appended to that leaves out items of the chart's default list warned of (default $OKDP_CHARTS with --check)")
	flag.StringVar(&o.contracts, "contracts", os.Getenv("OKDP_CONTRACTS"), "contract schemas directory (default $OKDP_CONTRACTS), needed when a connection reference or a provided connection names a contract")
	flag.StringVar(&o.published, "published", "", "compiled instances directory: what the other instances of the namespace publish, <dir>/<release>/"+compiler.InfoFile+" (-o <dir>/<release>), the compiled release's left out; a connection reference of a contract without a naming convention may name one of their outputs. Without it, a reference resolves to a connection or by naming convention only")
	flag.StringVar(&o.release, "release", "", "release name, required: the Helm release the values are compiled for")
	flag.StringVar(&o.namespace, "namespace", "", "namespace, required: the release's")
	flag.Func("context", "context values file, [<level>:]<file>, repeatable, before the -f files: global, global.okdp and connections among them; level 0 (the default), the platform's, first, a level's files merged in order; a level adds values, it changes none of a lower level's", func(s string) error { o.contexts = append(o.contexts, s); return nil })
	flag.Func("f", "instance values file, repeatable; it sets no global but the keys the parameter schema declares, and no connections: an instance's own are a --context file of its level", func(s string) error { o.values = append(o.values, s); return nil })
	flag.StringVar(&o.output, "output", "", "output directory, required: the instance as one chart, Chart.yaml, an umbrella over the upstream charts, "+compiler.UmbrellaValues+", its values (helm -f), "+compiler.ObjectsFile+" and "+compiler.ObjectsTemplate+", OKDP's own objects, when the instance has any, and "+compiler.InfoFile+", what it publishes (url, usage, provides), when it publishes something; the instances the connection references point to are printed on stderr, one line each: depends on <instance> (<contract>, <field>), (<contract> <output>, <field>) for an output of another name than the instance's")
	flag.StringVar(&o.output, "o", "", "shorthand for --output")
	flag.Parse()
	if o.check && o.charts == "" {
		o.charts = os.Getenv("OKDP_CHARTS")
	}
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

// run: checks the options, loads the inputs and compiles, prints the
// compile's warnings on stderr, then writes the files of compiler.Write
// into the output directory, created if needed, with the directories of
// their paths, and removes the files an earlier compile wrote there that
// this one does not: the objects files, which Helm would render, and the
// info file, which a control plane would read; then prints on stderr
// the instances the connection references point to (Result.Dependencies),
// one line each: depends on <instance> (<contract>, <field>), the output
// after the contract when it is not named after the instance. A compile
// that fails writes nothing; --check writes nothing, the files built all
// the same, and prints no dependency.
func run(o options) error {
	switch {
	case o.check && o.output != "":
		return errors.New("--check writes nothing: no --output (-o)")
	case !o.check && o.charts != "":
		return errors.New("--charts is read by --check only: a compile reads no chart")
	}
	required := [][2]string{{"--package", o.pkg}, {"--release", o.release}, {"--namespace", o.namespace}}
	if !o.check {
		required = append(required, [2]string{"--output (-o)", o.output})
	}
	for _, f := range required {
		if f[1] == "" {
			return fmt.Errorf("%s is required", f[0])
		}
	}
	def, err := compiler.LoadDefinition(os.DirFS(o.pkg))
	if err != nil {
		return err
	}
	in := compiler.Inputs{
		Contracts: noContracts{},
		Release:   o.release,
		Namespace: o.namespace,
	}
	if o.charts != "" {
		in.Charts = os.DirFS(o.charts)
	}
	if o.contracts != "" {
		in.Contracts = os.DirFS(o.contracts)
	}
	if o.published != "" {
		if in.Published, err = readPublished(o.published, o.release); err != nil {
			return err
		}
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
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	files, err := compiler.Write(res)
	if err != nil || o.check {
		return err
	}
	if err := os.MkdirAll(o.output, 0o755); err != nil {
		return err
	}
	for name, b := range files {
		path := filepath.Join(o.output, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return err
		}
	}
	for _, name := range []string{compiler.ObjectsFile, compiler.ObjectsTemplate, compiler.InfoFile} {
		if _, ok := files[name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(o.output, filepath.FromSlash(name))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	for _, d := range res.Dependencies {
		contract := d.Contract
		if d.Output != d.Instance {
			contract += " " + d.Output
		}
		fmt.Fprintf(os.Stderr, "depends on %s (%s, %s)\n", d.Instance, contract, d.Field)
	}
	return nil
}

// readPublished: what the instances compiled into dir publish, by release:
// <dir>/<release>/okdp.info.yaml, as -o <dir>/<release> writes it, read
// strictly. The compiled release's own is left out, and so is a directory
// without the file: an instance that publishes nothing.
func readPublished(dir, release string) (map[string]compiler.Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	published := map[string]compiler.Info{}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == release {
			continue
		}
		file := filepath.Join(dir, e.Name(), compiler.InfoFile)
		b, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var i compiler.Info
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&i); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		published[e.Name()] = i
	}
	return published, nil
}

// noContracts: the contracts without --contracts, none: a compile reads a
// contract only for a connection reference or a provided connection that
// names it, which then fails, the flag named.
type noContracts struct{}

func (noContracts) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("no contracts directory: give --contracts (or $OKDP_CONTRACTS)")}
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
