package compiler_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"log"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	compiler "github.com/okdp/okdp-compiler"
)

// The program of README.md, on the example: the package, the chart cache
// and the contracts as fs.FS, the values layers parsed.
func Example() {
	packageFS := os.DirFS("examples/02_advanced/input/service")
	chartsFS := os.DirFS("examples/02_advanced/input/charts")
	contractsFS := os.DirFS("examples/02_advanced/input/contracts")
	platformValues := values("examples/02_advanced/input/platform.yaml")
	projectValues := map[string]any{} // a project's values, none here: examples/03_full/input/demo.yaml is one
	instanceValues := values("examples/02_advanced/input/config.yaml")

	def, err := compiler.LoadDefinition(packageFS) // service.yaml, values.cel.yaml; each compile reads config.schema.json, <key>.defaults.yaml
	if err != nil {
		log.Fatal(err)
	}
	res, err := compiler.Compile(def, compiler.Inputs{
		Charts:    chartsFS,                         // chart cache: each upstream chart, <repository>/<chart>-<version>.tgz (helm pull) or <repository>/<chart>-<version>/
		Contracts: contractsFS,                      // the contract schemas, <contract>.schema.json
		Platform:  []map[string]any{platformValues}, // the platform's layers, in order (helm -f); compiler.ParseValues reads one
		Project:   []map[string]any{projectValues},  // the project's, after them: they add values, never change the platform's
		Values:    []map[string]any{instanceValues}, // the instance's, last: no global
		Release:   "demo-webapp",
		Namespace: "demo",
	})
	if err != nil {
		log.Fatal(err) // the whole report, e.g. every schema violation
	}
	// res.Values:   chart -> final values of the upstream chart (helm template <res.Charts[chart]> -f them)
	// res.Charts:   chart -> the upstream chart that renders them (compiler.ChartRef: repository, chart, version):
	//               its service.yaml source, the platform's objects chart for okdp,
	//               the platform's oidc-dcr chart for the OAuth clients (oidcClients)
	// res.Objects:  OKDP's own objects: the descriptor ConfigMap, the credentials
	//               Secrets of the provided connections, the mapping's objects section
	// res.Dependencies: instances the connection references point to (Flux dependsOn, Argo sync waves)

	// What okdp-compile -o <dir> --mkinstall writes, file name -> content
	// (compiler.Output: --umbrella, --mkinstall, --mkdag, --indent).
	files, err := compiler.Write(res, compiler.Output{Install: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(slices.Sorted(maps.Keys(res.Values)))
	fmt.Println(res.Values["app"]["resources"])
	fmt.Println(res.Dependencies)
	fmt.Println(slices.Sorted(maps.Keys(files)))
	// Output:
	// [app okdp]
	// map[limits:map[memory:512Mi] requests:map[cpu:100m memory:128Mi]]
	// [{demo-db database database}]
	// [app.values.yaml install.sh okdp.values.yaml]
}

// The first program of docs/01_usage.md: the example, from a checkout.
func ExampleCompile() {
	def, err := compiler.LoadDefinition(os.DirFS("examples/02_advanced/input/service")) // the package
	if err != nil {
		log.Fatal(err)
	}
	in := compiler.Inputs{
		Charts:    os.DirFS("examples/02_advanced/input/charts"), // the chart cache
		Contracts: os.DirFS("examples/02_advanced/input/contracts"),
		Release:   "demo-webapp",
		Namespace: "demo",
	}
	// The values layers, as helm -f files: the platform's, then the
	// instance's.
	read := func(f string) map[string]any {
		b, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		v, err := compiler.ParseValues(b)
		if err != nil {
			log.Fatalf("%s: %v", f, err)
		}
		return v
	}
	in.Platform = []map[string]any{read("examples/02_advanced/input/platform.yaml")}
	in.Values = []map[string]any{read("examples/02_advanced/input/config.yaml")}
	res, err := compiler.Compile(def, in)
	if err != nil {
		log.Fatal(err) // the whole report, e.g. every schema violation
	}
	fmt.Println("charts:", len(res.Values), "objects:", len(res.Objects))
	fmt.Println("replicaCount:", res.Values["app"]["replicaCount"])
	// Each chart key is rendered by the upstream chart res.Charts[key].
	for _, key := range slices.Sorted(maps.Keys(res.Charts)) {
		c := res.Charts[key]
		fmt.Printf("%s: %s %s %s\n", key, c.Repository, c.Chart, c.Version)
	}
	for _, d := range res.Dependencies {
		fmt.Printf("dependency: %s -> %s (%s)\n", d.Field, d.Instance, d.Contract)
	}
	// Output:
	// charts: 2 objects: 1
	// replicaCount: 2
	// app: https://charts.example.org app 1.8.0
	// okdp: oci://registry.example.org/okdp okdp-objects 1.0.0
	// dependency: database -> demo-db (database)
}

// The second program of docs/01_usage.md: a package and its inputs in
// memory, written as okdp-compile -o <dir> writes them.
func ExampleWrite() {
	def, err := compiler.LoadDefinition(fstest.MapFS{
		"service.yaml": {Data: []byte(`name: demo
version: 1.2.3
charts:
  app:
    source: {repository: https://charts.example.org, chart: app, version: 1.0.0}
    defaults: app.defaults.yaml
`)},
		"values.cel.yaml":    {Data: []byte("output:\n  app:\n    replicaCount: ${{ values.replicas }}\n")},
		"config.schema.json": {Data: []byte(`{"properties": {"replicas": {"type": "integer", "default": 1}}}`)},
		"app.defaults.yaml":  {Data: []byte("image: {tag: \"4.5\"}\n")},
	})
	if err != nil {
		log.Fatal(err)
	}
	platform := map[string]any{"global": map[string]any{"okdp": map[string]any{"charts": map[string]any{
		"objects": map[string]any{"repository": "oci://registry.example.org/okdp", "chart": "okdp-objects", "version": "1.0.0"},
	}}}}
	res, err := compiler.Compile(def, compiler.Inputs{
		Charts: fstest.MapFS{ // the defaults of each upstream chart
			"charts.example.org/app-1.0.0/values.yaml":                 {Data: []byte("replicaCount: 1\nimage: {tag: latest}\n")},
			"registry.example.org/okdp/okdp-objects-1.0.0/values.yaml": {Data: []byte("objects: []\n")},
		},
		Contracts: fstest.MapFS{}, // no connection, no provides: no contract read
		Platform:  []map[string]any{platform},
		Values:    []map[string]any{{"replicas": 3}},
		Release:   "demo-app",
		Namespace: "demo",
	})
	if err != nil {
		log.Fatal(err)
	}
	files, err := compiler.Write(res, compiler.Output{}) // the values files, indented by compiler.DefaultIndent
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(files["app.values.yaml"]))
	// Output:
	// # Code generated by okdp-compile. DO NOT EDIT.
	// # Chart: app --repo https://charts.example.org --version 1.0.0
	// global:
	//   okdp:
	//     charts:
	//       objects:
	//         chart: okdp-objects
	//         repository: oci://registry.example.org/okdp
	//         version: 1.0.0
	// image:
	//   tag: "4.5"
	// replicaCount: 3
}

// The example compiled as a server compiles a package it does not review:
// cancelled with a context, bounded in CEL cost, without the okdp chart
// when it deploys OKDP's own objects (Result.Objects) otherwise.
func ExampleCompileContext() {
	def, in := example()
	ctx, cancel := context.WithCancel(context.Background())
	res, err := compiler.CompileContext(ctx, def, in, compiler.WithCostLimit(100), compiler.WithoutObjectsChart())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(slices.Sorted(maps.Keys(res.Charts)), res.Objects[0]["kind"])
	// Past its cost limit, or cancelled, a compile fails.
	_, err = compiler.CompileContext(ctx, def, in, compiler.WithCostLimit(10))
	fmt.Println(err)
	cancel()
	_, err = compiler.CompileContext(ctx, def, in)
	fmt.Println(errors.Is(err, context.Canceled))
	// Output:
	// [app] ConfigMap
	// let db: ${{ type(values.database) == map ? values.database : {} }}: the expressions of the compile exceed its CEL cost limit (10)
	// true
}

// TestDocPrograms: the Go programs of README.md and docs/01_usage.md are
// examples of example_test.go (Example, ExampleCompile, ExampleWrite):
// they compile, and print what the documentation shows. The body of a
// program's main, or a fragment but its import, is in the body of an
// example, line by line but for the indentation; a program imports the
// packages it uses; the text block that follows a program is the example's
// output.
func TestDocPrograms(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "example_test.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("example_test.go")
	if err != nil {
		t.Fatal(err)
	}
	packages := map[string]bool{} // the names of the packages example_test.go imports
	for _, s := range f.Imports {
		packages[importName(s)] = true
	}
	for _, md := range []string{"README.md", filepath.Join("docs", "01_usage.md")} {
		b, err := os.ReadFile(md)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range programs(string(b)) {
			code := p[0]
			if strings.HasPrefix(code, "package main") {
				pf, err := parser.ParseFile(fset, md, code, 0)
				if err != nil {
					t.Errorf("%s: %v", md, err)
					continue
				}
				used := map[string]bool{} // x of every x.y
				ast.Inspect(pf, func(n ast.Node) bool {
					if s, ok := n.(*ast.SelectorExpr); ok {
						if x, ok := s.X.(*ast.Ident); ok {
							used[x.Name] = true
						}
					}
					return true
				})
				imported := map[string]bool{}
				for _, s := range pf.Imports {
					if imported[importName(s)] = true; !used[importName(s)] {
						t.Errorf("%s: %s imported and not used", md, s.Path.Value)
					}
				}
				for x := range used {
					if packages[x] && !imported[x] {
						t.Errorf("%s: %s used and not imported", md, x)
					}
				}
				for _, d := range pf.Decls {
					if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "main" {
						code = code[fset.Position(fn.Body.Lbrace).Offset+1 : fset.Position(fn.Body.Rbrace).Offset]
					}
				}
			}
			var ex *doc.Example
			for _, e := range doc.Examples(f) {
				if body := src[fset.Position(e.Code.Pos()).Offset:fset.Position(e.Code.End()).Offset]; strings.Contains(lines(string(body)), lines(code)) {
					ex = e
				}
			}
			switch {
			case ex == nil:
				t.Errorf("%s: no example of example_test.go holds the program:\n%s", md, code)
			case p[1] != "" && strings.TrimSpace(ex.Output) != strings.TrimSpace(p[1]):
				t.Errorf("%s: Example%s prints\n%s\nthe documentation shows\n%s", md, ex.Name, ex.Output, p[1])
			}
		}
	}
}

// importName: the name a file refers to an imported package by.
func importName(s *ast.ImportSpec) string {
	if s.Name != nil {
		return s.Name.Name
	}
	p, _ := strconv.Unquote(s.Path.Value)
	return path.Base(p)
}

// programs: the go code blocks of a Markdown document, each with the text
// block that follows it, or "".
func programs(md string) [][2]string {
	var progs [][2]string
	ls := strings.Split(md, "\n")
	block := func(i int) (string, int) { // the block opened at line i, and the line after it
		end := i + 1
		for end < len(ls) && ls[end] != "```" {
			end++
		}
		return strings.Join(ls[i+1:end], "\n") + "\n", end + 1
	}
	for i := 0; i < len(ls); i++ {
		if ls[i] != "```go" {
			continue
		}
		var p [2]string
		p[0], i = block(i)
		next := i
		for next < len(ls) && strings.TrimSpace(ls[next]) == "" {
			next++
		}
		if next < len(ls) && ls[next] == "```text" {
			p[1], i = block(next)
		}
		progs = append(progs, p)
		i--
	}
	return progs
}

// lines: code without its import lines, its blank lines at either end and
// the indentation of each line, one line per line, between newlines.
func lines(code string) string {
	var out []string
	for l := range strings.SplitSeq(code, "\n") {
		if !strings.HasPrefix(l, "import ") {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return "\n" + strings.Trim(strings.Join(out, "\n"), "\n") + "\n"
}
