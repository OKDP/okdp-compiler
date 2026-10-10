package chart

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// tgz: a chart archive, as helm package writes it.
func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var entries []tarEntry
	for name, content := range files {
		entries = append(entries, tarEntry{tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}, content})
	}
	return tgzEntries(t, entries...)
}

// tarEntry: an entry of a test archive, its header and its content.
type tarEntry struct {
	header  tar.Header
	content string
}

// tgzEntries: an archive of entries, in order.
func tgzEntries(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&e.header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestReadArchive: the two files of a chart archive, read as Helm reads
// them: written on Windows (names split on \), by git archive (a global
// header, directory entries), a byte order mark stripped, its own files
// only, a path Helm refuses skipped.
func TestReadArchive(t *testing.T) {
	gitArchive := tgzEntries(t,
		tarEntry{tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "ce92112"}}, ""},
		tarEntry{tar.Header{Name: "app/", Mode: 0o755, Typeflag: tar.TypeDir}, ""},
		tarEntry{tar.Header{Name: "app/values.yaml", Mode: 0o644, Size: 5, Typeflag: tar.TypeReg}, "a: 1\n"},
	)
	for name, tc := range map[string]struct {
		archive []byte
		want    map[string]string
	}{
		"helm package":    {tgz(t, map[string]string{"app/Chart.yaml": "name: app\n", "app/values.yaml": "a: 1\n", "app/charts/sub/values.yaml": "b: 2\n"}), map[string]string{"values.yaml": "a: 1\n"}},
		"on Windows":      {tgz(t, map[string]string{`app\Chart.yaml`: "name: app\n", `app\values.yaml`: "a: 1\n", `app\values.schema.json`: "{}", `app\charts\sub\values.yaml`: "b: 2\n"}), map[string]string{"values.yaml": "a: 1\n", "values.schema.json": "{}"}},
		"git archive":     {gitArchive, map[string]string{"values.yaml": "a: 1\n"}},
		"byte order mark": {tgz(t, map[string]string{"app/values.yaml": "\uFEFFa: 1\n", "app/values.schema.json": "\uFEFF{}"}), map[string]string{"values.yaml": "a: 1\n", "values.schema.json": "{}"}},
		"a refused path":  {tgz(t, map[string]string{"app/values.yaml": "a: 1\n", "app/../../values.schema.json": "{}"}), map[string]string{"values.yaml": "a: 1\n"}},
	} {
		files, err := readArchive(bytes.NewReader(tc.archive))
		got := map[string]string{}
		for file, b := range files {
			got[file] = string(b)
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q %v, want %q", name, got, err, tc.want)
		}
	}
}

// TestChartSize: an archive whose tar stream is over maxChartSize, here
// 2048 bytes (a header, a block of content and the end blocks of one small
// file), fails; a directory is read whole.
func TestChartSize(t *testing.T) {
	defer func(n int64) { maxChartSize = n }(maxChartSize)
	maxChartSize = 2048
	cache := fstest.MapFS{
		"at-1.0.0.tgz":          {Data: tgz(t, map[string]string{"at/values.yaml": "a: 1\n"})},
		"over-1.0.0.tgz":        {Data: tgz(t, map[string]string{"over/values.yaml": "a: 1\n", "over/README.md": "x"})},
		"dir-1.0.0/values.yaml": {Data: []byte(strings.Repeat("a: 1\n", 500))},
	}
	for entry, want := range map[string]string{
		"at-1.0.0":   "",
		"over-1.0.0": "over-1.0.0.tgz: unexpected EOF (a chart is at most 2048 bytes decompressed)",
		"dir-1.0.0":  "",
	} {
		got := ""
		if _, err := readEntry(cache, entry); err != nil {
			got = err.Error()
		}
		if got != want {
			t.Errorf("%s: got error %q, want %q", entry, got, want)
		}
	}
}

// TestRefCacheDir: a repository's directory of the chart cache, a path
// inside it or none.
func TestRefCacheDir(t *testing.T) {
	for repository, want := range map[string]string{
		"oci://quay.io/adaltas":             "quay.io/adaltas",
		"oci://quay.io/adaltas/":            "quay.io/adaltas",
		"https://charts.example.org":        "charts.example.org",
		"http://localhost:8080/charts/":     "localhost:8080/charts",
		"https://":                          "",
		"https:///charts":                   "",
		"https://charts.example.org//x":     "",
		"https://charts.example.org/./x":    "",
		"https://charts.example.org/../../": "",
		"oci://..":                          "",
		"oci://.":                           "",
		`https://charts.example.org\x`:      "",
		"file:///charts":                    "",
		"charts.example.org":                "",
	} {
		if got := (Ref{Repository: repository}).cacheDir(); got != want {
			t.Errorf("%s: got %q, want %q", repository, got, want)
		}
	}
}

// TestChartCache: an upstream chart in its repository's directory,
// unpacked or as its archive, its own defaults and schema (not its
// subcharts'); the helm command of a chart the directory does not hold,
// also when the root of the cache does; a repository without a directory.
func TestChartCache(t *testing.T) {
	cache := fstest.MapFS{
		"charts.example.org/app-1.0.0/values.yaml":        {Data: []byte("a: 1\n")},
		"charts.example.org/app-1.0.0/values.schema.json": {Data: []byte("{}")},
		"charts.example.org/app-1.0.0/templates/x.yaml":   {Data: []byte("{{ .Values.a }}")},
		"charts.example.org/nodefaults-1.0.0/Chart.yaml":  {Data: []byte("name: nodefaults\n")},
		"charts.example.org/packed-2.0.0.tgz": {Data: tgz(t, map[string]string{
			"packed/Chart.yaml":                    "name: packed\n",
			"packed/values.yaml":                   "b: on\n",
			"packed/charts/sub/values.yaml":        "c: 3\n",
			"packed/charts/sub/values.schema.json": "false",
		})},
		"quay.io/adaltas/oidc-dcr-0.5.0.tgz":          {Data: tgz(t, map[string]string{"oidc-dcr/values.yaml": "enabled: true\n"})},
		"rooted-1.0.0/values.yaml":                    {Data: []byte("a: 0\n")},
		"charts.example.org/broken-1.0.0/values.yaml": {Data: []byte("a: [\n")},
		"charts.example.org/corrupt-1.0.0.tgz":        {Data: []byte("not gzip")},
	}
	ref := func(chart, version string) Ref {
		return Ref{Repository: "https://charts.example.org", Chart: chart, Version: version}
	}
	c := newChartCache(cache)
	for name, tc := range map[string]struct {
		ref      Ref
		entry    string
		defaults M
		schema   string
	}{
		"unpacked":         {ref("app", "1.0.0"), "charts.example.org/app-1.0.0", M{"a": float64(1)}, "{}"},
		"archive":          {ref("packed", "2.0.0"), "charts.example.org/packed-2.0.0", M{"b": true}, ""},
		"no values.yaml":   {ref("nodefaults", "1.0.0"), "charts.example.org/nodefaults-1.0.0", M{}, ""},
		"read once, again": {ref("app", "1.0.0"), "charts.example.org/app-1.0.0", M{"a": float64(1)}, "{}"},
		"a trailing /":     {Ref{Repository: "https://charts.example.org/", Chart: "app", Version: "1.0.0"}, "charts.example.org/app-1.0.0", M{"a": float64(1)}, "{}"},
		"an OCI archive":   {Ref{Repository: "oci://quay.io/adaltas", Chart: "oidc-dcr", Version: "0.5.0"}, "quay.io/adaltas/oidc-dcr-0.5.0", M{"enabled": true}, ""},
	} {
		u, err := c.get(tc.ref)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if u.entry != tc.entry || !reflect.DeepEqual(u.defaults, tc.defaults) || string(u.schema) != tc.schema {
			t.Errorf("%s: got %s %#v %q", name, u.entry, u.defaults, u.schema)
		}
	}
	for name, tc := range map[string]struct {
		ref  Ref
		want string
	}{
		"not in the cache": {ref("nope", "1.0.0"), "chart nope-1.0.0 is not in the chart cache: mkdir -p <chart cache>/charts.example.org && helm pull nope --repo https://charts.example.org --version 1.0.0 -d <chart cache>/charts.example.org"},
		"an OCI chart":     {Ref{Repository: "oci://quay.io/adaltas/", Chart: "nope", Version: "1.0.0"}, "chart nope-1.0.0 is not in the chart cache: mkdir -p <chart cache>/quay.io/adaltas && helm pull oci://quay.io/adaltas/nope --version 1.0.0 -d <chart cache>/quay.io/adaltas"},
		"at the root only": {ref("rooted", "1.0.0"), "chart rooted-1.0.0 is not in the chart cache: mkdir -p <chart cache>/charts.example.org && "},
		"no directory":     {Ref{Repository: "file:///charts", Chart: "app", Version: "1.0.0"}, `chart app-1.0.0: repository "file:///charts" has no directory in the chart cache`},
		"bad values.yaml":  {ref("broken", "1.0.0"), "charts.example.org/broken-1.0.0/values.yaml: "},
		"bad archive":      {ref("corrupt", "1.0.0"), "charts.example.org/corrupt-1.0.0.tgz: "},
	} {
		if _, err := c.get(tc.ref); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error starting with %q", name, err, tc.want)
		}
	}
}
