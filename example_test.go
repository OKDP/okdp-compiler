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

// The program of README.md, on the advanced example's tree.
func Example() {
	treeFS := os.DirFS("examples/02_advanced/input/db")

	instances, err := compiler.LoadRepository([]compiler.Repository{
		{Name: "db", FS: treeFS}, // the tree, in one or more repositories: platform.yaml, contracts/, catalog/, projects/
	})
	if err != nil {
		log.Fatal(err) // every problem of the tree, each naming its path
	}
	var results []*compiler.Result
	for i, o := range compiler.CompileAll(context.Background(), instances) { // each after those whose outputs it references, each claim bound to its provider
		if o.Err != nil {
			log.Fatalf("%s: %v", instances[i].Inputs.Release, o.Err) // the whole report, e.g. every schema violation
		}
		results = append(results, o.Result)
	}
	g, err := compiler.NewGraph(results) // every dependency checked, the releases ordered
	if err != nil {
		log.Fatal(err)
	}
	// res.Values:   chart -> final values of the upstream chart (helm template <res.Charts[chart]> -f them)
	// res.Charts:   chart -> the upstream chart that renders them (compiler.ChartRef: repository, chart, version):
	//               its Chart.yaml dependency, the platform's oidc-dcr chart for the OAuth clients (oidcClients)
	// res.Objects:  OKDP's own objects: the credentials Secrets of the provided connections,
	//               the mapping's objects section (okdp.objects.yaml)
	// res.Info: what the instance publishes: its url, its usage, the connections it provides (okdp.info.yaml)
	// res.Dependencies: the instances the connection references point to (compiler.Dependency): the graph's edges

	// What okdp-compile -o <dir> writes, path -> content: each instance's umbrella chart
	// (compiler.Flux{}, compiler.ArgoCD{RepoURL: ...}: with the engine's objects).
	files, err := compiler.Helm{}.Files(results, g)
	if err != nil {
		log.Fatal(err)
	}
	web := results[slices.IndexFunc(results, func(r *compiler.Result) bool { return r.Release == "demo-webapp" })]
	fmt.Println(slices.Sorted(maps.Keys(web.Values)))
	fmt.Println(web.Values["app"]["resources"])
	fmt.Println(web.Dependencies)
	fmt.Println(len(files), "files")
	// Output:
	// [app oidc-dcr]
	// map[limits:map[memory:512Mi] requests:map[cpu:100m memory:128Mi]]
	// [{demo-db  database database demo.demo-webapp.database}]
	// 8 files
}

// The first program of docs/01_usage.md: the example, from a checkout.
func ExampleCompile() {
	def, err := compiler.LoadDefinition(os.DirFS("examples/02_advanced/input/db/catalog/webapp/1.0.0")) // the package
	if err != nil {
		log.Fatal(err)
	}
	in := compiler.Inputs{
		Contracts: os.DirFS("examples/02_advanced/input/db/contracts"),
		Release:   "demo-webapp",
		Namespace: "demo",
		// The claims of the instance, by parameter, each bound to its provider:
		// database's to demo-db, with the output demo-db publishes for it (its
		// okdp.info.yaml).
		Bindings: map[string]compiler.Binding{"database": {
			Namespace: "demo", Release: "demo-db", Contract: "database", ID: "demo.demo-webapp.database",
			Output: &compiler.Output{
				Name: "demo.demo-webapp.database", Contract: "database",
				Values:    map[string]any{"host": "demo-db-db.demo.svc", "dbName": "demo_demo_webapp_database"},
				SecretRef: &compiler.SecretRef{Name: "demo.demo-webapp.database-credentials"},
			},
		}},
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
	in.Context = []compiler.ContextLayer{{Values: read("examples/02_advanced/input/db/platform.yaml")}}
	in.Values = []map[string]any{read("examples/02_advanced/input/db/projects/demo/instances/demo-webapp/config.yaml")}
	res, err := compiler.Compile(context.Background(), def, in)
	if err != nil {
		log.Fatal(err) // the whole report, e.g. every schema violation
	}
	fmt.Println("charts:", len(res.Values), "outputs:", len(res.Info.Provides))
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
	// charts: 2 outputs: 1
	// replicaCount: 2
	// app: https://charts.example.org app 1.8.0
	// oidc-dcr: oci://quay.io/adaltas oidc-dcr 0.5.0
	// dependency: database -> demo-db (database)
}

// The second program of docs/01_usage.md: a package and its inputs in
// memory, written as okdp-compile -o <dir> writes them.
func ExampleWrite() {
	def, err := compiler.LoadDefinition(fstest.MapFS{
		"Chart.yaml": {Data: []byte(`apiVersion: v2
name: demo
version: 1.2.3
dependencies:
  - {name: app, repository: https://charts.example.org, version: 1.0.0}
`)},
		"okdp.yaml":          {Data: []byte("defaults: {app: app.defaults.yaml}\n")},
		"values.cel.yaml":    {Data: []byte("app:\n  replicaCount: ${{ values.replicas }}\n")},
		"config.schema.json": {Data: []byte(`{"properties": {"replicas": {"type": "integer", "default": 1}}}`)},
		"app.defaults.yaml":  {Data: []byte("image: {tag: \"4.5\"}\n")},
	})
	if err != nil {
		log.Fatal(err)
	}
	res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
		Contracts: fstest.MapFS{},                    // no connection, no provides: no contract read
		Values:    []map[string]any{{"replicas": 3}}, // no context: the package requires no platform value
		Release:   "demo-app",
		Namespace: "demo",
	})
	if err != nil {
		log.Fatal(err)
	}
	files, err := compiler.Write(res) // the umbrella chart
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(files["Chart.yaml"]))
	// Output:
	// apiVersion: v2
	// name: demo
	// version: 1.2.3
	// dependencies:
	//   - name: app
	//     repository: https://charts.example.org
	//     version: 1.0.0
}

// The example compiled as a server compiles a package it does not review:
// cancelled with a context, bounded in CEL cost.
func ExampleCompile_server() {
	def, in := example()
	in.CostLimit = 1000
	ctx, cancel := context.WithCancel(context.Background())
	res, err := compiler.Compile(ctx, def, in)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(slices.Sorted(maps.Keys(res.Charts)), res.Info.URL)
	// Past its cost limit, or cancelled, a compile fails.
	in.CostLimit = 10
	_, err = compiler.Compile(ctx, def, in)
	fmt.Println(err)
	cancel()
	_, err = compiler.Compile(ctx, def, in)
	fmt.Println(errors.Is(err, context.Canceled))
	// Output:
	// [app oidc-dcr] https://demo-webapp.okdp.example.org
	// okdp.yaml: vars db: ${{ type(values.database) == map ? values.database : {} }}: the expressions of the compile exceed its CEL cost limit (10)
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
