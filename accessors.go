package compiler

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/repository"
	"github.com/okdp/okdp-compiler/internal/value"
)

// The accessors of the compile's rules, for a program that answers
// before it writes (a control plane): each the compile's own, none
// written again.

// FieldError: a problem of a compile, at the field of an instance's
// values it is about, for a form that shows it beside the field
// (FieldErrors).
type FieldError struct {
	// Path is the field as the compile's messages name it, keys joined by
	// dots, an index in brackets, a key with a dot in brackets and quotes:
	// catalogs[1].storage, claims.analytics, config["spark.ui.port"]; ""
	// for the instance as a whole.
	Path string `json:"path"`
	// Pointer is the same field, a JSON pointer (RFC 6901):
	// /catalogs/1/storage; "" for the instance as a whole.
	Pointer string `json:"pointer"`
	// Message is the compile's: its error, or the part of it about the
	// field, the path at its head, but a violation of the parameter
	// schema's, its line ("at '/replicas': got string, want integer").
	Message string `json:"message"`
}

// FieldErrors: err, the error of a compile (an instance's, Outcome.Err,
// TreeInstance.Err, or what wraps it, a *treeedit.CompileError), as the
// problems of its fields: each violation of the parameter schema at its
// place, each connection reference or claim that does not resolve or
// bind at its parameter path, each named claim of the context at
// claims.<name>, every other problem, a tree that does not load among
// them (a *LoadError, a problem each), of the instance as a whole (Path
// ""). In the order of the error; nil for nil. The messages are the
// compile's: nothing is checked again.
func FieldErrors(err error) []FieldError {
	if err == nil {
		return nil
	}
	var out []FieldError
	fieldErrors(err, &out)
	return out
}

// fieldErrors adds the problems of err to out (FieldErrors): a tagged
// error, its field's; a join, each of its errors'; an error that wraps a
// tagged one, the tagged ones', else one of its own.
func fieldErrors(err error, out *[]FieldError) {
	switch e := err.(type) {
	case *okdp.FieldError:
		*out = append(*out, FieldError{Path: e.Field, Pointer: value.Pointer(value.Split(e.Field)), Message: e.Error()})
		return
	case *chart.ValuesError:
		for _, v := range e.Violations {
			*out = append(*out, FieldError{Path: value.Join(v.Location), Pointer: value.Pointer(v.Location), Message: v.Message})
		}
		return
	case *repository.Error:
		if len(e.Unwrap()) == 0 {
			for _, p := range e.Problems {
				*out = append(*out, FieldError{Message: p})
			}
			return
		}
	}
	switch u := err.(type) {
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			fieldErrors(e, out)
		}
		return
	case interface{ Unwrap() error }:
		if inner := u.Unwrap(); inner != nil && tagged(inner) {
			fieldErrors(inner, out)
			return
		}
	}
	*out = append(*out, FieldError{Message: err.Error()})
}

// tagged: err holds a problem of a field, a tagged error or a schema's
// violations.
func tagged(err error) bool {
	var fe *okdp.FieldError
	var ve *chart.ValuesError
	return errors.As(err, &fe) || errors.As(err, &ve)
}

// ConnectionRef: a connection reference of a package's parameter schema
// (x-okdp-connection-ref), where the compile finds it: its parameter
// path ([] for every item of a list, catalogs[].storage), its place in
// the schema, its contract, provision, request and require as written,
// whether its parent requires it, its property's schema whole.
type ConnectionRef = okdp.ConnectionRef

// ConnectionRefs: the connection references of the parameter schema
// schema (a package's config.schema.json, decoded), as the compile reads
// them: through properties and items from the root, in key order, not
// under a combinator (allOf, anyOf, oneOf), a definition ($defs, $ref)
// or additionalProperties, where the compile refuses one; the
// compile's error when one of them cannot be read.
func ConnectionRefs(schema map[string]any) ([]ConnectionRef, error) {
	v, err := value.Normalize(schema)
	if err != nil {
		return nil, err
	}
	return okdp.ConnectionRefs(value.AsMap(v))
}

// ContractDescriptor: a contract, <name>.schema.json of the contracts, as
// the compile reads it (ReadContract): its name, its label
// (x-okdp-contract), its title and description, its fields in the order
// of the file, the required ones, its claim schema (x-okdp-claim), the
// document as written for the keywords the compile does not read.
type ContractDescriptor = okdp.ContractDescriptor

// ContractField: a field of a contract: secret, required, its default,
// derived (x-okdp-derived), its condition (x-ui-condition), its
// property's schema as written.
type ContractField = okdp.ContractField

// ReadContract: the contract name of contracts (Inputs.Contracts, the
// contracts/ of a tree's repositories), read and checked as a compile
// reads it, its error when it cannot.
func ReadContract(contracts fs.FS, name string) (*ContractDescriptor, error) {
	return okdp.ReadContract(contracts, name)
}

// ReadContracts: every contract of contracts, by name (ReadContract),
// every one that cannot be read at once.
func ReadContracts(contracts fs.FS) ([]ContractDescriptor, error) {
	return okdp.ReadContracts(contracts)
}

// MergeValues: the values layers merged in order, as the compile merges
// an instance's (Inputs.Values) and as Helm merges its -f files: maps key
// by key, lists and scalars replaced, a null kept. The layers are not
// changed.
func MergeValues(layers ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, l := range layers {
		out = value.Merge(out, l)
	}
	return out
}

// CoalesceValues: values over a chart's defaults, as Helm gives them to
// the chart's templates and the compile checks a schema's defaults:
// maps key by key; a null of values deletes the default; the null
// defaults dropped. Neither is changed.
func CoalesceValues(values, defaults map[string]any) map[string]any {
	return chart.Coalesce(defaults, values)
}

// NamedClaimID: the ID of the named claim name of project, the name of
// the output its provider publishes for it: <project>.<name>.
func NamedClaimID(project, name string) string { return okdp.NamedClaimID(project, name) }

// IsClaimID: name is of the form of a claim's ID, DNS-1123 labels joined
// by dots (<namespace>.<name>, <namespace>.<instance>.<field>), the name
// of the output a provider publishes for a claim, named or not; an output
// named after its instance, a label, is not. Which outputs of a provider
// are its claims' is its compile's (TreeInstance.Provided).
func IsClaimID(name string) bool { return okdp.IsClaimID(name) }

// CheckProvider: nil when provider may name a claim's or a class's
// provider, <instance>, <release>, <namespace>/<instance> or
// <namespace>/<release>; the compile's error otherwise, at where.
func CheckProvider(where, provider string) error { return okdp.CheckProvider(where, provider) }

// CheckConnection: nil when the connection c of project ("" for the
// platform's, platform.yaml) holds at its declaration, as the compile of
// the tree checks every connection of an instance's context, referenced
// or not: its name, its contract one of contracts, its fields, none
// secret, its required fields after completion, its types, its
// secretRef (a remoteKey of the store of platform, the platform's values,
// a project's under <project>/), no "{{". The compile's error otherwise,
// at connections.<name> (FieldErrors).
func CheckConnection(contracts fs.FS, project string, c Connection, platform map[string]any) error {
	v, err := value.Normalize(platform)
	if err != nil {
		return fmt.Errorf("the platform's values (%s): %w", PlatformFile, err)
	}
	level := uint(0)
	if project != "" {
		level = 1
	}
	return okdp.CheckConnection(contracts, c.Name, c.Entry(), level, project, value.Str(value.Dig(value.AsMap(v), "global.okdp.secrets.store")))
}
