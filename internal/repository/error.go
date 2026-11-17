package repository

import (
	"slices"
	"strings"
)

// Error lists the problems of a tree that does not load, in order. A
// problem is a whole message, which may span several lines, and names its
// repository and path. Unwrap returns the errors behind the problems, so
// that errors.Is finds ErrNotInCatalog.
type Error struct {
	Problems []string
	errs     []error
}

func (e *Error) Error() string { return strings.Join(e.Problems, "\n") }

// Unwrap returns no error for an Error built from Problems only.
func (e *Error) Unwrap() []error { return e.errs }

// newError builds an *Error from errs. It drops the nil errors and lists
// one by one those errors.Join joined. It returns nil when none is left.
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

// JoinErrors is newError as an *Error: nil when errs holds no error.
func JoinErrors(errs []error) *Error {
	e, _ := newError(errs).(*Error)
	return e
}

// Added returns the problems of after that before does not have, in after's
// order. A nil before gives every problem of after.
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

// Select returns an Error with the problems of e that problems lists, in
// e's order. Each keeps its error when e has them, so that a caller can
// still map the problems to their fields (compiler.FieldErrors).
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

// unjoin returns the errors that errors.Join joined, else err alone.
func unjoin(err error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}
