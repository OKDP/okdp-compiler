package treeedit

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	compiler "github.com/okdp/okdp-compiler"
)

// Validate is the verdict of the compile on the edit e of the tree r
// holds, without writing it: e's change written over r in memory, and
// checked as Apply checks it, the tree compiled before and after it with
// compile (Check.Verify). A change the compile refuses, a
// *CompileError, is its field errors (compiler.FieldErrors): those of
// the instance it writes, or of the one it breaks; nil when it passes.
// The warnings are those of the instances it writes. The error is any
// other refusal: of e itself (ErrNotFound, ErrExists...), an *ErrInUse,
// an *ErrReleaseTaken, or a compile that fails. For a program that
// answers before it writes, a form checked field by field: a write's
// own *CompileError maps the same way.
func Validate(r Reader, e Edit, compile func(Reader) (*compiler.Tree, error)) ([]compiler.FieldError, []Warning, error) {
	_, warnings, err := Apply(newOverlay(r), e, compile)
	if ce, ok := errors.AsType[*CompileError](err); ok {
		return compiler.FieldErrors(ce.Err), warnings, nil
	}
	return nil, warnings, err
}

// ValidateInstance is Validate of the write of inst, its instance.yaml
// and its config.yaml: an update when the tree has its directory
// (UpdateInstance), else a creation (CreateInstance). The field errors
// are those of its compile, its parameters checked by its package's
// schema, its connection references resolved, its claims bound to their
// providers, each at its parameter path, with exactly the compile's
// rules.
func ValidateInstance(r Reader, compile func(Reader) (*compiler.Tree, error), inst Instance) ([]compiler.FieldError, []Warning, error) {
	var e Edit
	var err error
	if r.Exists(inst.Dir()) {
		e, err = UpdateInstance(inst.Project, inst.Name, func(i *Instance) error {
			i.Service, i.Version, i.Claimers, i.Values = inst.Service, inst.Version, inst.Claimers, inst.Values
			return nil
		})
	} else {
		e, err = CreateInstance(inst)
	}
	if err != nil {
		return nil, nil, err
	}
	return Validate(r, e, compile)
}

// ValidateClaim is Validate of the declaration of the named claim c of
// project, an update when the project declares it, else a creation
// (PutClaim): its contract, its class or its provider, its request, as
// the compile of the project's instances binds it, the field errors at
// claims.<name>. A project without instance binds none of its claims:
// the tree checks them at their declaration (its class, its request,
// its name), a problem of the tree that maps to claims.<name> too.
func ValidateClaim(r Reader, compile func(Reader) (*compiler.Tree, error), project string, c compiler.ClaimSpec) ([]compiler.FieldError, []Warning, error) {
	entries, _, err := readEntries(r, ProjectFilePath(project), claimsKey)
	if err != nil {
		return nil, nil, err
	}
	_, exists := entries[c.Name]
	e, err := PutClaim(project, c, !exists)
	if err != nil {
		return nil, nil, err
	}
	return Validate(r, e, compile)
}

// overlay: a Writer over a Reader, its writes and removals kept in
// memory, the Reader left as it is: the tree a change would make.
type overlay struct {
	base    Reader
	files   map[string][]byte // written, by path
	removed []string          // removed, files and directories, the base's hidden under them
}

func newOverlay(r Reader) *overlay { return &overlay{base: r, files: map[string][]byte{}} }

// under: p is dir or under it.
func under(p, dir string) bool {
	return p == dir || dir == "." || strings.HasPrefix(p, dir+"/")
}

// hidden: p is under a removed path.
func (o *overlay) hidden(p string) bool {
	return slices.ContainsFunc(o.removed, func(d string) bool { return under(p, d) })
}

func (o *overlay) ReadFile(p string) ([]byte, error) {
	p = path.Clean(p)
	if data, ok := o.files[p]; ok {
		return bytes.Clone(data), nil
	}
	if o.hidden(p) {
		return nil, fmt.Errorf("%s: %w", p, ErrNotFound)
	}
	return o.base.ReadFile(p)
}

func (o *overlay) ReadDir(p string) ([]string, error) {
	p = path.Clean(p)
	names := map[string]bool{}
	if !o.hidden(p) {
		base, err := o.base.ReadDir(p)
		if err != nil {
			return nil, err
		}
		for _, n := range base {
			if !o.hidden(path.Join(p, n)) {
				names[n] = true
			}
		}
	}
	for f := range o.files {
		rest, ok := strings.CutPrefix(f, p+"/")
		if p == "." {
			rest, ok = f, true
		}
		if ok {
			name, _, _ := strings.Cut(rest, "/")
			names[name] = true
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	return slices.Sorted(maps.Keys(names)), nil
}

func (o *overlay) Exists(p string) bool {
	p = path.Clean(p)
	for f := range o.files {
		if under(f, p) {
			return true
		}
	}
	if o.hidden(p) {
		return false
	}
	return o.base.Exists(p)
}

func (o *overlay) WriteFile(p string, data []byte) error {
	o.files[path.Clean(p)] = bytes.Clone(data)
	return nil
}

func (o *overlay) Remove(p string) error {
	p = path.Clean(p)
	for f := range o.files {
		if under(f, p) {
			delete(o.files, f)
		}
	}
	o.removed = append(o.removed, p)
	return nil
}
