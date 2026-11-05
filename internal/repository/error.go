package repository

import (
	"slices"
	"strings"
)

// Error: the problems of a tree that does not load (Load), each naming its
// repository and path, in their order; one problem a whole message, which
// may span lines (a report). Error gives them one per line, Unwrap the
// errors they are of (errors.Is ErrNotInCatalog).
type Error struct {
	Problems []string
	errs     []error
}

func (e *Error) Error() string { return strings.Join(e.Problems, "\n") }

// Unwrap: the errors of the problems, none for an Error built of Problems
// only.
func (e *Error) Unwrap() []error { return e.errs }

// newError: the *Error of errs, the nil ones left out and those
// errors.Join joined listed one by one; nil when there is none.
func newError(errs []error) error {
	var flat []error
	var add func(err error)
	add = func(err error) {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, e := range joined.Unwrap() {
				add(e)
			}
		} else if err != nil {
			flat = append(flat, err)
		}
	}
	for _, err := range errs {
		add(err)
	}
	if flat == nil {
		return nil
	}
	e := &Error{errs: flat}
	for _, err := range flat {
		e.Problems = append(e.Problems, err.Error())
	}
	return e
}

// Added: the problems of after that before has not, in after's order;
// every one of after's when before is nil: what a change of the tree
// added to why it does not load.
func Added(before, after *Error) []string {
	if after == nil {
		return nil
	}
	if before == nil {
		return slices.Clone(after.Problems)
	}
	var out []string
	for _, p := range after.Problems {
		if !slices.Contains(before.Problems, p) {
			out = append(out, p)
		}
	}
	return out
}

// Select: the Error of the problems of e that problems lists, in e's
// order, each with its error when e has them (Unwrap): a change's added
// problems (Added), their fields kept for a caller that maps them
// (compiler.FieldErrors).
func (e *Error) Select(problems []string) *Error {
	out := &Error{}
	for i, p := range e.Problems {
		if !slices.Contains(problems, p) {
			continue
		}
		out.Problems = append(out.Problems, p)
		if len(e.errs) == len(e.Problems) {
			out.errs = append(out.errs, e.errs[i])
		}
	}
	return out
}

// unjoin: the errors errors.Join joined, err alone otherwise, none for
// nil.
func unjoin(err error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}
