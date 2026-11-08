package treeedit

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// linkTree is a memTree some of whose paths are symbolic links, which it
// refuses as a store does: a read of a link, or through one, is an
// ErrSymlink.
type linkTree struct {
	memTree
	links map[string]bool
}

func (l linkTree) through(p string) (string, bool) {
	for q := clean(p); q != "." && q != "/"; q = clean(q + "/..") {
		if l.links[q] {
			return q, true
		}
	}
	return "", false
}

func (l linkTree) ReadFile(p string) ([]byte, error) {
	if q, ok := l.through(p); ok {
		return nil, fmt.Errorf("%s: %w", q, ErrSymlink)
	}
	return l.memTree.ReadFile(p)
}

func (l linkTree) ReadDir(p string) ([]string, error) {
	if q, ok := l.through(p); ok {
		return nil, fmt.Errorf("%s: %w", q, ErrSymlink)
	}
	return l.memTree.ReadDir(p)
}

// ReadSources reads platform.yaml, catalogs.yaml and projects/, and
// nothing else of the tree, at their paths.
func TestReadSourcesChoosesTheFiles(t *testing.T) {
	tree := memTree{
		"platform.yaml":                             []byte("global: {}\n"),
		"catalogs.yaml":                             []byte("repositories: []\n"),
		"projects.yaml":                             []byte("projects: []\n"),
		"README.md":                                 []byte("# not read\n"),
		"compiled/projects/demo/Chart.yaml":         []byte("not read\n"),
		"projects/demo/project.yaml":                []byte("a: 1\n"),
		"projects/demo/instances/app/instance.yaml": []byte("service: app\n"),
		"projects/demo/instances/app/values.yaml":   []byte("x: 1\n"),
	}
	got, err := ReadSources(tree)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for p, f := range got {
		if !f.Mode.IsDir() {
			files = append(files, p)
		}
	}
	slices.Sort(files)
	want := []string{
		"catalogs.yaml",
		"platform.yaml",
		"projects/demo/instances/app/instance.yaml",
		"projects/demo/instances/app/values.yaml",
		"projects/demo/project.yaml",
	}
	if !slices.Equal(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}
	if string(got["platform.yaml"].Data) != "global: {}\n" {
		t.Errorf("platform.yaml = %q", got["platform.yaml"].Data)
	}
	for _, d := range []string{"projects/demo", "projects/demo/instances", "projects/demo/instances/app"} {
		if f, ok := got[d]; !ok || !f.Mode.IsDir() {
			t.Errorf("%s: %v, %v", d, f, ok)
		}
	}

	// A tree with nothing of the three is an empty one, not an error.
	if got, err := ReadSources(memTree{}); err != nil || len(got) != 0 {
		t.Errorf("an empty tree: %v, %v", got, err)
	}
}

// A symbolic link in the sources, or on the way to them, fails the read
// and names the link.
func TestReadSourcesRefusesALink(t *testing.T) {
	base := memTree{
		"platform.yaml": []byte("global: {}\n"),
		"projects/demo/instances/app/config.yaml": []byte("a: 1\n"),
		"projects/demo/instances/app/other.yaml":  []byte("b: 1\n"),
	}
	for _, link := range []string{
		"projects/demo/instances/app/config.yaml",
		"projects/demo/instances/app",
		"projects/demo",
	} {
		_, err := ReadSources(linkTree{memTree: base, links: map[string]bool{link: true}})
		if !errors.Is(err, ErrSymlink) || !strings.HasPrefix(err.Error(), link+": ") {
			t.Errorf("link %s: %v", link, err)
		}
	}
}

// The compile is given a tree within bounds: a file of MaxTreeFileSize at
// most, MaxTreeSize together.
func TestReadSourcesIsBounded(t *testing.T) {
	big := strings.Repeat("#", MaxTreeFileSize+1)
	_, err := ReadSources(memTree{
		"platform.yaml": []byte("global: {}\n"),
		"projects/demo/instances/app/config.yaml": []byte(big),
	})
	if !errors.Is(err, ErrTooLarge) || !strings.HasPrefix(err.Error(), "projects/demo/instances/app/config.yaml is 4194305 bytes, more than the 4194304") {
		t.Errorf("a big file: %v", err)
	}

	files := memTree{}
	for i := range MaxTreeSize/MaxTreeFileSize + 1 {
		files["projects/demo/instances/app/"+string(rune('a'+i))+".yaml"] = []byte(big[1:])
	}
	_, err = ReadSources(files)
	if !errors.Is(err, ErrTooLarge) || err.Error() != "the tree holds more than 67108864 bytes" {
		t.Errorf("a big tree: %v", err)
	}
}

// A reader that fails a read otherwise is not taken for a directory.
func TestReadSourcesReturnsAnErrorOfTheReader(t *testing.T) {
	boom := errors.New("boom")
	_, err := ReadSources(failing{memTree: memTree{"platform.yaml": []byte("x")}, err: boom})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

type failing struct {
	memTree
	err error
}

func (f failing) ReadDir(string) ([]string, error) { return nil, f.err }
