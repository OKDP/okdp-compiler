package compiler

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/value"
)

// This file exposes the compile's own rules to a program that must answer
// before it writes. Each accessor calls the compile's code.

// FieldError is a problem of a compile, at the field of an instance's
// values it is about, for a form to show beside that field. Path and
// Pointer are "" for the instance as a whole.
type FieldError struct {
	// Path is the field as the compile's messages name it:
	// catalogs[1].storage, config["spark.ui.port"].
	Path    string `json:"path"`
	Pointer string `json:"pointer"` // the same field as a JSON pointer (RFC 6901)
	// Message is the compile's message, which starts with the path. For a
	// violation of the parameter schema, it is the violation's line.
	Message string `json:"message"`
}

// FieldErrors splits err, the error of a compile (Outcome.Err,
// or what wraps it), into the problems of its fields,
// in the order of the error. A violation of the parameter schema, a
// reference or a claim that does not resolve, and a named claim are
// placed at their field. Any other problem is at the
// instance as a whole. The messages are the compile's.
func FieldErrors(err error) []FieldError {
	if err == nil {
		return nil
	}
	var out []FieldError
	fieldErrors(err, &out)
	return out
}

// fieldErrors appends the problems of err to out. It descends into a
// join, and into an error that wraps a tagged one.
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

// tagged reports whether err holds a tagged error or a schema's violations.
func tagged(err error) bool {
	var fe *okdp.FieldError
	var ve *chart.ValuesError
	return errors.As(err, &fe) || errors.As(err, &ve)
}

// ConnectionRef is a connection reference of a package's parameter
// schema (x-okdp-connection-ref), where the compile finds it. Its path
// has [] for every item of a list: catalogs[].storage.
type ConnectionRef = okdp.ConnectionRef

// ConnectionRefs returns the connection references of schema, a package's
// decoded config.schema.json, as the compile reads them. It walks
// properties and items in key order, and does not look under a
// combinator, a definition or additionalProperties, where the compile
// refuses a reference.
func ConnectionRefs(schema map[string]any) ([]ConnectionRef, error) {
	v, err := value.Normalize(schema)
	if err != nil {
		return nil, err
	}
	return okdp.ConnectionRefs(value.AsMap(v))
}

// ContractDescriptor is a contract, <name>.schema.json of the contracts,
// as the compile reads it: its fields in the order of the file, its
// claim schema (x-okdp-claim) and the document as written.
type ContractDescriptor = okdp.ContractDescriptor

// ContractField is a field of a contract, with the schema of its
// property as written.
type ContractField = okdp.ContractField

// ReadContract reads and checks the contract name of contracts
// (Inputs.Contracts), as a compile does.
func ReadContract(contracts fs.FS, name string) (*ContractDescriptor, error) {
	return okdp.ReadContract(contracts, name)
}

// ReadContracts returns every contract of contracts, by name. Its error
// covers every contract that cannot be read.
func ReadContracts(contracts fs.FS) ([]ContractDescriptor, error) {
	return okdp.ReadContracts(contracts)
}

// MergeValues merges the values layers in order, as the compile merges
// those of an instance (Inputs.Values) and as Helm merges its -f files.
// Maps merge key by key, lists and scalars are replaced, and a null is
// kept. It does not change the layers.
func MergeValues(layers ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, l := range layers {
		out = value.Merge(out, l)
	}
	return out
}

// NamedClaimID returns <project>.<name>, the ID of a named claim and the
// name of the output that its provider publishes for it.
func NamedClaimID(project, name string) string { return okdp.NamedClaimID(project, name) }

// IsClaimID reports whether name has the form of a claim's ID: labels
// joined by dots (<namespace>.<name>). An output named after its instance
// is a single label, so it is not one. The form alone does not tell which
// outputs of a provider are those of its claims (TreeInstance.Provided).
func IsClaimID(name string) bool { return okdp.IsClaimID(name) }

// CheckProvider returns nil when provider may name the provider of a
// claim or of a class: an instance or a release, with or without its
// namespace (<namespace>/<instance>). where places the error.
func CheckProvider(where, provider string) error { return okdp.CheckProvider(where, provider) }

// CheckClaimers returns nil when claimers, the namespaces that may claim
// of a provider from another namespace, are each a namespace name or "*",
// listed once. name is what the error calls the list.
func CheckClaimers(name string, claimers []string) error { return okdp.CheckClaimers(name, claimers) }

// CheckDeclarations checks the declarations of values that no instance
// compiles (in.Namespace, in.Contracts, in.Values), as CompileAll checks
// those of an instance: the layers, the settings, every connection and
// every named claim.
func CheckDeclarations(ctx context.Context, in Inputs) error { return okdp.CheckDeclarations(ctx, in) }

// CheckConnection returns nil when the connection c of namespace holds
// at its declaration, as the compile checks every connection of the
// values, referenced or not. The contract must be one of contracts, the
// fields must fit it and hold no secret, and a remoteKey must belong to
// the store that values give (global.okdp.secrets), never under platform/
// when they give the store's provider. The error is placed at
// connections.<name>.
func CheckConnection(rules *RuleSet, contracts fs.FS, namespace string, c Connection, values map[string]any) error {
	v, err := value.Normalize(values)
	if err != nil {
		return fmt.Errorf("the values: %w", err)
	}
	secrets := value.AsMap(value.Dig(value.AsMap(v), "global.okdp.secrets"))
	return okdp.CheckConnection(rules, contracts, c.Name, c.Entry(), namespace, value.Str(secrets["store"]), len(value.AsMap(secrets["provider"])) > 0)
}
