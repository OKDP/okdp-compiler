package treeedit

import (
	"errors"
	"fmt"
	"strings"

	compiler "github.com/okdp/okdp-compiler"
)

// Edit: an edit of a tree, an instance, a connection, a claim, a project
// or the catalog menu created, updated or removed (CreateInstance,
// PutConnection...): what it is named after, and its change of the tree
// as a Reader holds it (Change). The edit is pure: it reads the tree and
// returns what to write, which the caller writes (Apply: a store's
// transaction, its commit named after Action and Target).
type Edit struct {
	// Action and Target name the edit, "deploy" "demo/trino", "create
	// connection" "platform/pg": what a commit of it says it does.
	Action string
	Target string

	change func(Reader) (*Change, error)
}

// Change is the change of e to the tree r holds, or why e refuses that
// tree: an error wrapping ErrNotFound or ErrExists, or one of a file
// that does not parse. It may run more than once, on the tree as it is
// each time (a store's transaction replayed).
func (e Edit) Change(r Reader) (*Change, error) { return e.change(r) }

// Change: the files an Edit writes and removes, in order, and how the
// compile of the tree checks it (Check, nil: unchecked).
type Change struct {
	Files []File
	// Check is the check of the change by the compile of the tree before
	// and after it (Check.Verify); nil when the change needs none (a
	// project's description, the catalog menu).
	Check *Check
	// Instance is the instance an edit of an instance writes, as written
	// (UpdateInstance's change applied); nil for another edit.
	Instance *Instance
}

// File: a file a Change writes, Data, or a file or a directory it
// removes (Remove), everything under it.
type File struct {
	Path   string
	Data   []byte
	Remove bool
}

// write adds the writing of data at p.
func (c *Change) write(p string, data []byte) {
	c.Files = append(c.Files, File{Path: p, Data: data})
}

// remove adds the removal of p.
func (c *Change) remove(p string) { c.Files = append(c.Files, File{Path: p, Remove: true}) }

// Write writes the files of c to w, in order.
func (c *Change) Write(w Writer) error {
	for _, f := range c.Files {
		var err error
		if f.Remove {
			err = w.Remove(f.Path)
		} else {
			err = w.WriteFile(f.Path, f.Data)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Apply writes the change of e to the tree w holds (Edit.Change), checked
// by compile when the change asks for it (Change.Check): the tree
// compiled before the change is written and after, Check.Verify the
// verdict; the warnings of the instances it writes. compile is the
// compile of the tree a Reader holds (compiler.Build of its parts and of
// its catalogs, a *compiler.LoadError when it does not load); nil, the
// change is not checked. On an error, w holds the change or part of it:
// the caller drops it (a transaction that fails commits nothing).
func Apply(w Writer, e Edit, compile func(Reader) (*compiler.Tree, error)) (*Change, []Warning, error) {
	change, err := e.Change(w)
	if err != nil {
		return nil, nil, err
	}
	if compile == nil || change.Check == nil {
		return change, nil, change.Write(w)
	}
	before, err := StateOf(compile(w))
	if err != nil {
		return nil, nil, err
	}
	if err := change.Write(w); err != nil {
		return nil, nil, err
	}
	after, err := StateOf(compile(w))
	if err != nil {
		return nil, nil, err
	}
	warnings, err := change.Check.Verify(before, after)
	if err != nil {
		return nil, warnings, err
	}
	return change, warnings, nil
}

// State: the compile of a tree as Check.Verify reads it, its Tree, or
// why it does not load (Load); the zero State: none, unknown.
type State struct {
	Tree *compiler.Tree
	Load *compiler.LoadError
}

// StateOf is the State of a compile, compiler.Build's tree and error: a
// *compiler.LoadError is the State's, any other error is returned.
func StateOf(tree *compiler.Tree, err error) (State, error) {
	if load, ok := errors.AsType[*compiler.LoadError](err); ok {
		return State{Load: load}, nil
	}
	if err != nil {
		return State{}, err
	}
	return State{Tree: tree}, nil
}

// Check: how the compile of the tree checks a change, the compile of the
// tree before it and after it (Verify): the change may not add a problem
// to the tree.
type Check struct {
	// Subjects are the instances the change writes (their Project and
	// Name): each must compile after it, and the tree must load.
	Subjects []Instance
	// Created: the subjects are new, the release of each may not be
	// another instance's (*ErrReleaseTaken).
	Created bool
	// Removed is what the change removes of Project, "instance web",
	// "connection pg", "claim analytics", "project lab": an instance
	// that compiled before and does not after uses it (*ErrInUse). ""
	// for a change that removes nothing: such an instance is the
	// change's fault (*CompileError).
	Removed string
	Project string
}

// Warning: a warning of the compile of an instance a change writes.
type Warning struct {
	Project  string
	Instance string
	Message  string
}

// Verify is the verdict of c on a change, the tree compiled before it and
// after it: a *CompileError when the change adds a problem to the tree,
// the tree does not load after it and did, or with a problem of its own,
// or it does not load and the change writes an instance; an instance the
// change writes (Subjects) does not compile; an instance that compiled
// before does not after (*ErrInUse naming every such instance for a
// removal, Removed); a *ErrReleaseTaken when a new subject's release is
// another instance's (Created). The warnings are the subjects' compile's.
func (c *Check) Verify(before, after State) ([]Warning, error) {
	strict := len(c.Subjects) > 0
	if after.Load != nil {
		added := compiler.AddedProblems(before.Load, after.Load)
		if len(added) == 0 && !strict {
			return nil, nil
		}
		if len(added) == 0 {
			added = after.Load.Problems
		}
		ce := &CompileError{Err: after.Load.Select(added)}
		if strict {
			ce.Project, ce.Instance = c.Subjects[0].Project, c.Subjects[0].Name
		}
		return nil, ce
	}
	tree := after.Tree
	var warnings []Warning
	for _, s := range c.Subjects {
		ti := tree.Instance(s.Project, s.Name)
		if ti == nil {
			continue
		}
		if ti.Outcome.Result != nil {
			for _, w := range ti.Outcome.Result.Warnings {
				warnings = append(warnings, Warning{Project: ti.Namespace(), Instance: ti.Name, Message: w})
			}
		}
		if err := ti.Err(); err != nil {
			return warnings, &CompileError{Project: s.Project, Instance: s.Name, Err: err}
		}
	}
	if before.Tree != nil {
		var users []string
		for _, ti := range compiler.Regressions(before.Tree, tree) {
			if c.Removed == "" {
				return warnings, &CompileError{Project: ti.Namespace(), Instance: ti.Name, Err: ti.Err()}
			}
			user := ti.Name
			if ti.Namespace() != c.Project {
				user = ti.Namespace() + "/" + ti.Name
			}
			users = append(users, user)
		}
		if len(users) > 0 {
			return warnings, &ErrInUse{What: c.Removed, Users: users}
		}
	}
	if c.Created {
		for _, s := range c.Subjects {
			if other := compiler.ReleaseTaken(tree, s.Project, s.Name); other != nil {
				return warnings, &ErrReleaseTaken{Release: other.Release(), Project: other.Namespace(), Instance: other.Name}
			}
		}
	}
	return warnings, nil
}

// CompileError is a change the compile of the tree refuses: an instance
// it writes, or one that deployed before it, does not compile after it
// (its parameters, its connections, its claims...), or the tree does not
// load (Instance empty), or a new project's name is one the compile
// refuses (CheckProjectName, Instance empty). It is the change's fault,
// not the platform's: a control plane answers 400 with the compiler's
// message.
type CompileError struct {
	Project  string
	Instance string
	Err      error
}

func (e *CompileError) Error() string {
	switch {
	case e.Instance == "" && e.Project != "":
		// A project's name, the compiler's message naming it.
		return e.Err.Error()
	case e.Instance == "":
		return fmt.Sprintf("the deployments do not compile: %v", e.Err)
	}
	return fmt.Sprintf("instance %s/%s does not compile: %v", e.Project, e.Instance, e.Err)
}

func (e *CompileError) Unwrap() error { return e.Err }

// ErrReleaseTaken: a new instance's release <p>-<i> is already another
// instance's, of another project, which the Flux and Argo CD targets
// refuse (compiler.ReleaseTaken): project a-b instance c and project a
// instance b-c are both release a-b-c. It is not ErrExists: the instance
// itself is new, its name only collides.
type ErrReleaseTaken struct {
	Release  string
	Project  string
	Instance string
}

func (e *ErrReleaseTaken) Error() string {
	return fmt.Sprintf("release name '%s' is already used by instance '%s' of project '%s'", e.Release, e.Instance, e.Project)
}

// ErrInUse: a removal of what other instances still use, which would not
// compile without it.
type ErrInUse struct {
	What  string
	Users []string
}

func (e *ErrInUse) Error() string {
	return fmt.Sprintf("%s is still used by %s", e.What, strings.Join(e.Users, ", "))
}
