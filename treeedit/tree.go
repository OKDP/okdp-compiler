package treeedit

import (
	"errors"
	"fmt"
	"path"

	compiler "github.com/okdp/okdp-compiler"
	"github.com/okdp/okdp-compiler/internal/repository"
)

// Reader: a tree as a caller holds it, a checkout, a Git tree, a store's
// transaction, which the readers and the edits of this package read:
// paths slash-separated, relative to the root of the tree
// (compiler.PlatformFile, compiler.ProjectsDir...).
type Reader interface {
	// ReadFile returns the content of a file, or an error wrapping
	// ErrNotFound.
	ReadFile(p string) ([]byte, error)
	// ReadDir returns the sorted names of the entries of a directory, nil
	// when the directory does not exist.
	ReadDir(p string) ([]string, error)
	// Exists reports whether a file or a directory exists.
	Exists(p string) bool
}

// Writer: a Reader that writes, a store's transaction, its changes kept
// together or not at all: what Apply writes a Change to.
type Writer interface {
	Reader
	// WriteFile writes a file, its directories with it.
	WriteFile(p string, data []byte) error
	// Remove deletes a file or a whole directory. Removing nothing is
	// not an error.
	Remove(p string) error
}

// ErrNotFound: a file of the tree, or what an edit or a reader names (a
// project, an instance, a connection, a claim), that the tree does not
// have.
var ErrNotFound = errors.New("not found in the deployments repository")

// ErrExists: what an edit creates that the tree has already.
var ErrExists = errors.New("already exists in the deployments repository")

// The files of the tree beside compiler.PlatformFile that a program
// keeping the tree in Git reads: ProjectsFile, which the compile does not
// read (compiler.LoadRepository ignores it), and CatalogsFile, which it
// checks.
const (
	// CatalogsFile lists the catalog repositories a compile fetches,
	// repositories: [{name, url, ref}] (CatalogRepositories), and the
	// console's menu of their services (categories, ReadCatalog), which
	// the compile checks when a repository of the tree holds it
	// (ParseCatalogs, CheckCatalogs).
	CatalogsFile = repository.CatalogsFile
	// ProjectsFile lists the projects of a tree whose projects each have
	// a repository of their own: projects: [{name}] (ProjectsList).
	ProjectsFile = "projects.yaml"
)

// ProjectDir is projects/<project>, a project of the tree.
func ProjectDir(project string) string { return path.Join(compiler.ProjectsDir, project) }

// ProjectFilePath is projects/<project>/project.yaml.
func ProjectFilePath(project string) string {
	return path.Join(ProjectDir(project), compiler.ProjectFile)
}

// InstancesDir is projects/<project>/instances.
func InstancesDir(project string) string {
	return path.Join(ProjectDir(project), compiler.InstancesDir)
}

// InstanceDir is projects/<project>/instances/<instance>, an instance of
// the tree.
func InstanceDir(project, instance string) string {
	return path.Join(InstancesDir(project), instance)
}

// ValidateName refuses a name that okdp-compiler refuses
// (compiler.IsName), which cannot be a path segment and a Kubernetes
// object name at once: a project (a namespace), an instance, a
// connection, a claim.
func ValidateName(kind, name string) error {
	if !compiler.IsName(name) {
		return fmt.Errorf("invalid %s name %q: it must be a lowercase DNS label", kind, name)
	}
	return nil
}

// checkRef refuses a project or object name that cannot be one: joined
// into a path, ".." or "." would address another directory
// (InstanceDir(p, "..") is the project itself, and removing it removes
// every instance). Such a name is not found, the way ReadProject
// answers. A caller refuses these names first; this is the last line.
func checkRef(kind, project, name string) error {
	if ValidateName("project", project) != nil || ValidateName(kind, name) != nil {
		return fmt.Errorf("%s %s/%s: %w", kind, project, name, ErrNotFound)
	}
	return nil
}

// readValues reads a values file of the tree, nil without the file.
func readValues(r Reader, file string) (map[string]any, []byte, error) {
	raw, err := r.ReadFile(file)
	if errors.Is(err, ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	doc, err := DecodeValues(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	return doc, raw, nil
}
