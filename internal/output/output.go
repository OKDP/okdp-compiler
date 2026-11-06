// Package output: the output of a compiled tree for its deployers, the
// files of one or more targets, the projects that do not deploy kept as
// a previous output holds them (tree.Partial), their platform's part of
// today's settings, those of a release name two namespaces share too,
// every release of the output in one namespace for the targets whose
// objects share one; the release an instance would take of another's. A
// caller writes the files (the CLI to a directory, okdp-gitops to Git).
package output

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
	"github.com/okdp/okdp-compiler/internal/target/argocd"
	"github.com/okdp/okdp-compiler/internal/target/flux"
	"github.com/okdp/okdp-compiler/internal/tree"
)

// Files: the output of t, path -> content: the files each of targets
// writes for the projects tree.Partial writes, merged, a path two
// targets write differently an error; for each project kept, the files
// previous, the output written before (nil: none), holds under its
// target.ProjectDir, its instances' as they are, but its platform's part,
// which each target that is a target.Keeper writes of today's settings
// (rederive, tree.Kept.Rederived): its manifests (its Namespace, its
// identity, the roles of its groups, the control plane's) and its
// instances' identity (a Flux HelmRelease's ServiceAccount), so that a
// privilege revoked, a role taken back, a policy tightened, do not
// survive in a project kept by an instance refused. A Flux target lists
// in the root Kustomization (flux.Target.Keep) each kept project whose
// kustomization.yaml previous holds: the entry the root reads, which the
// Flux Kustomization of projects/ would prune otherwise. For a Flux or
// an Argo CD target, whose objects share one namespace, a release name
// two namespaces of t share (a-b/c and a/b-c), compiled or not, keeps
// each instance of it that compiled, its error wrapping a
// *target.ReleaseError (tree.PartialWith), and so their projects, the
// rest of t written; then every release of the projects written and of
// the kept projects' output in previous is in one namespace, else an
// error wrapping a *target.ReleaseError per release: two releases of one
// name are never written. A project kept whose namespace is none a
// project may have today, the compiler's reserved names or the platform's
// (global.okdp.policy.reserved), is dropped: nothing of it is kept nor
// written, Kept.Dropped says why, and the tree does not fail. An error of
// previous (a symbolic link, a file over its bounds) fails it. kept are
// the projects kept, and why.
func Files(t *tree.Tree, targets []target.Target, previous fs.FS) (map[string][]byte, []tree.Kept, error) {
	var shared []string // the objects named after the releases, of each target that has them
	for _, tg := range targets {
		if objects := releaseObjects(tg); objects != "" && !slices.Contains(shared, objects) {
			shared = append(shared, objects)
		}
	}
	var also map[*tree.Instance]error
	if len(shared) > 0 {
		also = taken(t, strings.Join(shared, " and "))
	}
	results, g, kept := tree.PartialWith(t, also)
	if g == nil {
		return nil, nil, errors.New("the deployment graph has a problem at no instance of the tree")
	}
	kepts := map[string][]byte{}
	var keep []string // the kept projects of an entry in the root Kustomization
	releases := map[[2]string]bool{}
	for _, res := range results {
		releases[[2]string{res.Namespace, res.Release}] = true
	}
	for i, k := range kept {
		files, err := read(previous, target.ProjectDir(k.Namespace))
		if err != nil {
			return nil, nil, fmt.Errorf("the output of the project kept %s: %w", k.Namespace, err)
		}
		previousFiles := files
		var dropped error
		files, kept[i].Rederived, dropped, err = rederive(t, targets, k.Namespace, files)
		if err != nil {
			return nil, nil, fmt.Errorf("the project kept %s: %w", k.Namespace, err)
		}
		if dropped != nil {
			// A namespace no project may have today: nothing of it kept.
			kept[i].Dropped = dropped.Error()
			continue
		}
		if _, ok := previousFiles[path.Join(target.ProjectDir(k.Namespace), flux.KustomizationFile)]; ok {
			keep = append(keep, k.Namespace)
		}
		for p := range previousFiles {
			if release, ok := instanceOf(k.Namespace, p); ok {
				releases[[2]string{k.Namespace, release}] = true
			}
		}
		maps.Copy(kepts, files)
	}
	out := map[string][]byte{}
	for _, tg := range targets {
		if objects := releaseObjects(tg); objects != "" {
			if err := unique(releases, objects); err != nil {
				return nil, nil, err
			}
		}
		switch t := tg.(type) {
		case flux.Target:
			t.Keep = append(slices.Clone(t.Keep), keep...)
			tg = t
		case *flux.Target:
			c := *t
			c.Keep = append(slices.Clone(c.Keep), keep...)
			tg = c
		}
		files, err := tg.Files(results, g)
		if err != nil {
			return nil, nil, err
		}
		for p, b := range files {
			if prev, ok := out[p]; ok && string(prev) != string(b) {
				return nil, nil, fmt.Errorf("%s: two targets write it differently", p)
			}
			out[p] = b
		}
	}
	maps.Copy(out, kepts)
	return out, kept, nil
}

// rederive: the files of the project kept namespace of t, previous those
// the output written before holds of it (none: none written), its
// platform's part of today's settings (D-5): the project as its context
// decides it now (okdp.ProjectOf of its first instance, or of its own
// context when it does not load: privileged or not, its secret store, the
// roles of its groups, the control plane, its network policy, the
// claimers of every instance's claims its own), written by each of
// targets that is a target.Keeper (Kept), a path two write differently an
// error, in place of every file previous holds under its
// target.ProjectManifests, its instances' files kept as they are but
// those Kept gives; previous as it is when no target is a Keeper. A
// privilege revoked, a role taken back, a tightened policy, reach the
// kept project's namespace and identity at once, whatever its instances:
// the instances kept are applied with today's identity, which refuses
// what they hold that it no longer allows. An access the compile refuses
// gives no group a role; the platform's settings refused fail it. A
// namespace that is none a project may have today, reserved by the
// compiler since the output was written or by the platform
// (global.okdp.policy.reserved), drops the project: dropped says why,
// and nothing of it is kept, nor re-derived (okdp.CheckProject, the
// compile's own view). And the paths written of today's settings, in
// path order.
func rederive(t *tree.Tree, targets []target.Target, namespace string, previous map[string][]byte) (files map[string][]byte, rederived []string, dropped, err error) {
	inputs, unloaded, own, ok := projectInputs(t, namespace)
	if !ok || len(previous) == 0 {
		return previous, nil, nil, nil
	}
	if err := okdp.CheckProject(inputs); err != nil {
		switch {
		case errors.As(err, new(*okdp.NamespaceError)):
			return nil, nil, err, nil
		case unloaded:
			// The context of a project that does not load may be what it
			// does not load for: its output is kept, not the tree's
			// failure.
			return previous, nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("its manifests of today's settings: %w", err)
	}
	var keepers []target.Keeper
	for _, tg := range targets {
		if k, ok := tg.(target.Keeper); ok {
			keepers = append(keepers, k)
		}
	}
	if len(keepers) == 0 || unloaded && !own {
		return previous, nil, nil, nil
	}
	p, err := okdp.ProjectOf(inputs)
	if err != nil && !errors.As(err, new(*okdp.AccessError)) {
		return nil, nil, nil, fmt.Errorf("its manifests of today's settings: %w", err)
	}
	// The network policy's claimers: those of the claims bound to every
	// instance of the project, as the compile bound them, compiled or not.
	for _, ti := range t.Instances {
		if ti.Namespace() == namespace {
			for _, pr := range ti.Provided {
				target.AddClaimers(&p, pr.Claimer.Namespace())
			}
		}
	}
	written := map[string][]byte{}
	for _, k := range keepers {
		files, err := k.Kept(p, previous)
		if err != nil {
			return nil, nil, nil, err
		}
		for f, b := range files {
			if prev, ok := written[f]; ok && string(prev) != string(b) {
				return nil, nil, nil, fmt.Errorf("%s: two targets write it differently", f)
			}
			written[f] = b
		}
	}
	out := map[string][]byte{}
	manifests := target.ProjectManifests(namespace) + "/"
	for f, b := range previous {
		if !strings.HasPrefix(f, manifests) {
			out[f] = b
		}
	}
	maps.Copy(out, written)
	return out, slices.Sorted(maps.Keys(written)), nil, nil
}

// projectInputs: the inputs the project namespace of t is read from, those
// of its first instance, else its own context when it does not load
// (tree.Refused, unloaded, own when its project.yaml read); none for a
// project of t that has neither.
func projectInputs(t *tree.Tree, namespace string) (in okdp.Inputs, unloaded, own, ok bool) {
	for _, ti := range t.Instances {
		if ti.Namespace() == namespace {
			return ti.Inputs, false, true, true
		}
	}
	for _, r := range t.Refused {
		if r.Namespace == namespace {
			return r.Inputs, true, r.Own, true
		}
	}
	return okdp.Inputs{}, false, false, false
}

// releaseObjects: the objects of tg named after the releases, which share
// one namespace, "" when it has none (the helm target).
func releaseObjects(tg target.Target) string {
	switch tg.(type) {
	case flux.Target, *flux.Target:
		return flux.ReleaseObjects
	case argocd.Target, *argocd.Target:
		return argocd.ReleaseObjects
	}
	return ""
}

// taken: each instance of t that compiled whose release name an instance
// of another namespace of t has too, compiled or not, and why, a
// *target.ReleaseError, objects needing distinct names.
func taken(t *tree.Tree, objects string) map[*tree.Instance]error {
	namespaces := map[string][]string{}
	for _, ti := range t.Instances {
		if ns := namespaces[ti.Release()]; !slices.Contains(ns, ti.Namespace()) {
			namespaces[ti.Release()] = append(ns, ti.Namespace())
		}
	}
	also := map[*tree.Instance]error{}
	for _, ti := range t.Instances {
		ns := namespaces[ti.Release()]
		if len(ns) < 2 || ti.Outcome.Err != nil {
			continue
		}
		re := &target.ReleaseError{Release: ti.Release(), Namespaces: slices.Sorted(slices.Values(ns))}
		also[ti] = fmt.Errorf("%w: %s need distinct names", re, objects)
	}
	return also
}

// unique: target.UniqueReleases of the releases, namespace and release
// each, the objects named after them objects.
func unique(releases map[[2]string]bool, objects string) error {
	var results []*okdp.Result
	for _, r := range slices.SortedFunc(maps.Keys(releases), func(a, b [2]string) int {
		return strings.Compare(a[0]+"/"+a[1], b[0]+"/"+b[1])
	}) {
		results = append(results, &okdp.Result{Namespace: r[0], Release: r[1]})
	}
	return target.UniqueReleases(results, objects)
}

// read: the files under dir of fsys, by path; none when fsys is nil or
// has no dir.
func read(fsys fs.FS, dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	if fsys == nil {
		return files, nil
	}
	if _, err := fs.Stat(fsys, dir); errors.Is(err, fs.ErrNotExist) {
		return files, nil
	}
	err := fs.WalkDir(fsys, dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		files[p] = b
		return err
	})
	return files, err
}

// instanceOf: the release of p, a path under target.InstanceDir of a
// release of namespace.
func instanceOf(namespace, p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, path.Join(target.ProjectDir(namespace), "instances")+"/")
	if !ok {
		return "", false
	}
	release, _, ok := strings.Cut(rest, "/")
	return release, ok
}

// ReleaseTaken: the instance of t of another namespace than namespace
// whose release the instance name of namespace has too, nil when none:
// the release names of the compiled instances, as target.Releases finds
// them in two namespaces (a-b/c and a/b-c are both a-b-c), which the Flux
// and Argo CD targets refuse.
func ReleaseTaken(t *tree.Tree, namespace, name string) *tree.Instance {
	ti := t.Instance(namespace, name)
	if ti == nil {
		return nil
	}
	var results []*okdp.Result
	for _, ti := range t.Instances {
		if ti.Outcome.Result != nil {
			results = append(results, ti.Outcome.Result)
		}
	}
	err := target.Releases(results)
	if err == nil {
		return nil
	}
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		re, ok := errors.AsType[*target.ReleaseError](e)
		if !ok || re.Release != ti.Release() {
			continue
		}
		for _, ns := range re.Namespaces {
			if other := t.Release(ns, re.Release); ns != namespace && other != nil {
				return other
			}
		}
	}
	return nil
}
