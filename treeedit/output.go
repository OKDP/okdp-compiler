package treeedit

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing/fstest"

	compiler "github.com/okdp/okdp-compiler"
)

// The output of a compile in a tree a caller keeps (Git): where it is
// written and which targets write it.
const (
	// CompiledDir holds the output: the files of the Flux and Argo CD
	// targets, as they write them (compiled/projects/<project>/...).
	// Never edited by hand.
	CompiledDir = "compiled"
	// ArgoCDRootPath is the Application of the output that Argo CD syncs
	// first, written by hand (the engine's entry point): it says where
	// Argo CD reads the output from (spec.source.repoURL and
	// targetRevision), which the Application of each instance reads too
	// (compiler.ReadArgoRoot). An output without it is deployed by Flux
	// only: it has no Applications.
	ArgoCDRootPath = "argocd/root.yaml"
)

// OutputTargets are the targets of the compile of the tree r, for an
// output whose repository holds it under prefix (slashes at its ends
// ignored): the Flux target always, the Argo CD target when ArgoCDRootPath
// says where Argo CD reads the output from. The paths the engines read
// are under prefix/CompiledDir. A root that does not parse is an error
// naming it; a missing one is not.
func OutputTargets(r Reader, prefix string) ([]compiler.Target, error) {
	prefix = path.Join(strings.Trim(prefix, "/"), CompiledDir)
	targets := []compiler.Target{compiler.Flux{PathPrefix: prefix}}
	raw, err := r.ReadFile(ArgoCDRootPath)
	switch {
	case err == nil:
		argo, err := compiler.ReadArgoRoot(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ArgoCDRootPath, err)
		}
		targets = append(targets, compiler.ArgoCD{RepoURL: argo.RepoURL, Revision: argo.Revision, PathPrefix: prefix})
	case !errors.Is(err, ErrNotFound):
		return nil, err
	}
	return targets, nil
}

// WriteOutput rewrites the output of w: CompiledDir removed, then each
// of files (compiler.TreeFiles, by path under CompiledDir) written, in
// path order.
func WriteOutput(w Writer, files map[string][]byte) error {
	if err := w.Remove(CompiledDir); err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		if err := w.WriteFile(path.Join(CompiledDir, p), files[p]); err != nil {
			return err
		}
	}
	return nil
}

// OutputFS is the output of the tree r holds, CompiledDir, as an fs.FS,
// for the readers of an output (compiler.TreeFiles' previous,
// compiler.ReadCompiled, ReadCompiled, ListPublished): read as it is
// opened, a file whole, a directory of the names ReadDir gives, each
// read to know whether it is a file; a path that is, or goes through, a
// symbolic link refused (ErrSymlink). What it does not hold is an error
// wrapping fs.ErrNotExist and ErrNotFound. Nothing is read but what is
// opened.
func OutputFS(r Reader) fs.FS { return outputFS{r: r, dir: CompiledDir} }

type outputFS struct {
	r   Reader
	dir string
}

// Open opens name, a file or a directory of the output.
func (o outputFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	p := path.Join(o.dir, name)
	data, err := o.r.ReadFile(p)
	switch {
	case err == nil:
		base := path.Base(name)
		return fstest.MapFS{base: {Data: data, Mode: 0o444}}.Open(base)
	case errors.Is(err, ErrSymlink):
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	names, dirErr := o.r.ReadDir(p)
	if dirErr != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: dirErr}
	}
	if len(names) == 0 && name != "." {
		if errors.Is(err, ErrNotFound) {
			err = fmt.Errorf("%w: %w", fs.ErrNotExist, err)
		}
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	entries := fstest.MapFS{}
	for _, n := range names {
		data, err := o.r.ReadFile(path.Join(p, n))
		switch {
		case err == nil:
			entries[n] = &fstest.MapFile{Data: data, Mode: 0o444}
		case errors.Is(err, ErrSymlink):
			entries[n] = &fstest.MapFile{Mode: fs.ModeSymlink | 0o777}
		default:
			entries[n] = &fstest.MapFile{Mode: fs.ModeDir | 0o555}
		}
	}
	return entries.Open(".")
}
