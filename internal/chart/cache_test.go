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

// TestReadArchive: a chart archive read as Helm reads it: written on
// Windows (names split on \), by git archive (a global header, directory
// entries), a byte order mark stripped, its own files only; the paths Helm
// refuses; a file over 100 MiB, refused from its header (the archive ends
// there).
func TestReadArchive(t *testing.T) {
	var huge bytes.Buffer
	gz := gzip.NewWriter(&huge)
	if err := tar.NewWriter(gz).WriteHeader(&tar.Header{Name: "app/values.yaml", Mode: 0o644, Size: 100<<20 + 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	gitArchive := tgzEntries(t,
		tarEntry{tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "ce92112"}}, ""},
		tarEntry{tar.Header{Name: "app/", Mode: 0o755, Typeflag: tar.TypeDir}, ""},
		tarEntry{tar.Header{Name: "app/values.yaml", Mode: 0o644, Size: 5, Typeflag: tar.TypeReg}, "a: 1\n"},
	)
	for name, tc := range map[string]struct {
		archive []byte
		want    map[string]string
		err     string
	}{
		"helm package":           {tgz(t, map[string]string{"app/Chart.yaml": "name: app\n", "app/values.yaml": "a: 1\n", "app/charts/sub/values.yaml": "b: 2\n"}), map[string]string{"values.yaml": "a: 1\n"}, ""},
		"on Windows":             {tgz(t, map[string]string{`app\Chart.yaml`: "name: app\n", `app\values.yaml`: "a: 1\n", `app\values.schema.json`: "{}", `app\charts\sub\values.yaml`: "b: 2\n"}), map[string]string{"values.yaml": "a: 1\n", "values.schema.json": "{}"}, ""},
		"git archive":            {gitArchive, map[string]string{"values.yaml": "a: 1\n"}, ""},
		"byte order mark":        {tgz(t, map[string]string{"app/values.yaml": "\uFEFFa: 1\n", "app/values.schema.json": "\uFEFF{}"}), map[string]string{"values.yaml": "a: 1\n", "values.schema.json": "{}"}, ""},
		"a file at the root":     {tgz(t, map[string]string{"values.yaml": "a: 1\n"}), nil, `chart illegally contains content outside the base directory: "values.yaml"`},
		"an absolute path":       {tgz(t, map[string]string{"app//etc/values.yaml": ""}), nil, "chart illegally contains absolute paths"},
		"absolute, on Windows":   {tgz(t, map[string]string{`app\\etc\values.yaml`: ""}), nil, "chart illegally contains absolute paths"},
		"out of the chart":       {tgz(t, map[string]string{"app/../../values.yaml": ""}), nil, "chart illegally references parent directory"},
		"out, on Windows":        {tgz(t, map[string]string{`app\templates\..\..\..\x.yaml`: ""}), nil, "chart illegally references parent directory"},
		"a drive":                {tgz(t, map[string]string{`app\C:\values.yaml`: ""}), nil, "chart contains illegally named files"},
		"a file over 100 MiB":    {huge.Bytes(), nil, "decompressed chart is larger than the maximum size 104857600"},
		"refused, though unread": {tgz(t, map[string]string{"app/values.yaml": "a: 1\n", "app/templates/../../x.yaml": ""}), nil, "chart illegally references parent directory"},
	} {
		files, err := readArchive(bytes.NewReader(tc.archive))
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("%s: got %v, want %q", name, err, tc.err)
			}
			continue
		}
		got := map[string]string{}
		for file, b := range files {
			got[file] = string(b)
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q %v, want %q", name, got, err, tc.want)
		}
	}
}

// TestChartSize: a chart over maxChartSize, here 10 bytes: an archive
// counts all its files and fails at the cap, as Helm 4 loads an archive; a
// directory the files read, and fails over the cap, as Helm 4 loads a
// directory.
func TestChartSize(t *testing.T) {
	defer func(n int64) { maxChartSize = n }(maxChartSize)
	maxChartSize = 10
	cache := fstest.MapFS{
		"under-1.0.0.tgz":                  {Data: tgz(t, map[string]string{"under/values.yaml": "a: 1\n", "under/README.md": "four"})},
		"at-1.0.0.tgz":                     {Data: tgz(t, map[string]string{"at/values.yaml": "a: 1\n", "at/README.md": "five!"})},
		"over-1.0.0.tgz":                   {Data: tgz(t, map[string]string{"over/values.yaml": "a: 1\n", "over/README.md": "a README"})},
		"dir-1.0.0/values.yaml":            {Data: []byte("a: 1\n")},
		"dir-1.0.0/values.schema.json":     {Data: []byte(`"abc"`)},
		"dir-1.0.0/README.md":              {Data: []byte("not read, not counted")},
		"dirover-1.0.0/values.yaml":        {Data: []byte("a: 1\n")},
		"dirover-1.0.0/values.schema.json": {Data: []byte(`{"a":1}`)},
		"big-1.0.0/values.yaml":            {Data: []byte("a: 1\nb: 22\n")},
	}
	for entry, want := range map[string]string{
		"under-1.0.0":   "",
		"at-1.0.0":      "at-1.0.0.tgz: decompressed chart is larger than the maximum size 10",
		"over-1.0.0":    "over-1.0.0.tgz: decompressed chart is larger than the maximum size 10",
		"dir-1.0.0":     "",
		"dirover-1.0.0": "dirover-1.0.0/values.schema.json: chart exceeds maximum decompressed size of 10 bytes",
		"big-1.0.0":     "big-1.0.0/values.yaml: chart exceeds maximum decompressed size of 10 bytes",
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
