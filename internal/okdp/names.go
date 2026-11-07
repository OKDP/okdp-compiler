// Names: okdpSqlName and okdpObjectName, the names a provider derives from
// a claim's ID, one name per ID, so that two claimers' claims never share
// one: the database, the role or the keyspace of a claim, the object of a
// topic. A claim's ID is DNS-1123 labels joined by dots, and the
// replacement of its dots and dashes by one character loses it: a-b.c and
// a.b-c are both a_b_c.

package okdp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"

	"github.com/okdp/okdp-compiler/internal/compile"
)

const (
	// nameMax: the length okdpSqlName and okdpObjectName give a name by
	// default: PostgreSQL's identifiers and a DNS-1123 label.
	nameMax = 63
	// nameMaxMin, nameMaxMax: the bounds of the length a caller asks for.
	nameMaxMin, nameMaxMax = 24, 253
	// nameHash: the hexadecimal digits of the ID's SHA-256 a long name ends
	// with: 64 bits.
	nameHash = 16
	// systemEscape: what okdpSqlName puts before a name that starts with
	// system.
	systemEscape = "c___"
)

// dottedLabel: a DNS-1123 label, a part of a claim's ID.
var dottedLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// checkDotted: nil when id is DNS-1123 labels joined by dots, 253
// characters at most (a claim's ID, an instance's name): the form the
// names below are injective over. A label starts and ends with a letter or
// a digit, so that a dot is never next to a dash.
func checkDotted(fn, id string) error {
	if id == "" || len(id) > 253 {
		return fmt.Errorf("%s: %q is not a claim's ID: DNS-1123 labels joined by dots, 253 characters at most", fn, id)
	}
	for l := range strings.SplitSeq(id, ".") {
		if len(l) > 63 || !dottedLabel.MatchString(l) {
			return fmt.Errorf("%s: %q is not a claim's ID: DNS-1123 labels joined by dots, 253 characters at most", fn, id)
		}
	}
	return nil
}

// checkNameMax: nil when limit is a length a name may be asked to have.
func checkNameMax(fn string, limit int64) error {
	if limit < nameMaxMin || limit > nameMaxMax {
		return fmt.Errorf("%s: the length %d is out of %d to %d", fn, limit, nameMaxMin, nameMaxMax)
	}
	return nil
}

// boundName: name, the name of the ID id, as it is when it is shorter than
// limit; else its first limit-17 characters, "sep" and the first 16 digits of
// the SHA-256 of id: limit characters. A name that fits is shorter than limit,
// one cut is limit long, so that a cut name is no other ID's whole name, and
// the digits tell two IDs that share their start apart.
func boundName(id, name, sep string, limit int) string {
	if len(name) < limit {
		return name
	}
	sum := sha256.Sum256([]byte(id))
	return name[:limit-1-nameHash] + sep + hex.EncodeToString(sum[:])[:nameHash]
}

// sqlName: okdpSqlName, an identifier of [a-z0-9_], limit characters at
// most, one per claim ID (checkDotted): each dash two underscores, each dot
// one. The labels of an ID neither start nor end with a dash, so a dot is
// never next to one: a run of underscores is one dot (1) or dashes (2
// each), and two IDs never give one name. A name that starts with system
// (Cassandra's system, system_schema... keyspaces, PostgreSQL's system
// catalogs' prefix) gets systemEscape first: three underscores in a row
// are no ID's, so no other name is that one; then boundName.
func sqlName(id string, limit int64) (string, error) {
	if err := checkDotted("okdpSqlName", id); err != nil {
		return "", err
	}
	if err := checkNameMax("okdpSqlName", limit); err != nil {
		return "", err
	}
	name := strings.NewReplacer("-", "__", ".", "_").Replace(id)
	if strings.HasPrefix(name, "system") {
		name = systemEscape + name
	}
	return boundName(id, name, "_", int(limit)), nil
}

// objectName: okdpObjectName, a DNS-1123 label, limit characters at most,
// one per claim ID (checkDotted): each dash two dashes, each dot one. A dot
// is never next to a dash, so a run of dashes is one dot (1) or dashes (2
// each); then boundName.
func objectName(id string, limit int64) (string, error) {
	if err := checkDotted("okdpObjectName", id); err != nil {
		return "", err
	}
	if err := checkNameMax("okdpObjectName", limit); err != nil {
		return "", err
	}
	return boundName(id, strings.NewReplacer("-", "--", ".", "-").Replace(id), "-", int(limit)), nil
}

// nameFunctions: okdpSqlName(id), okdpSqlName(id, limit), okdpObjectName(id),
// okdpObjectName(id, limit), their calls counted in the session's cost. An
// argument of another type than a string and an int, which an expression
// of an unchecked type reaches, fails the call.
func nameFunctions() []cel.EnvOption {
	call := func(name string, f func(string, int64) (string, error), id, limit ref.Val) ref.Val {
		s, ok := id.(types.String)
		if !ok {
			return types.NewErr("%s: the ID is a string, got %s", name, id.Type().TypeName())
		}
		n := int64(nameMax)
		if limit != nil {
			m, ok := limit.(types.Int)
			if !ok {
				return types.NewErr("%s: the length is an integer, got %s", name, limit.Type().TypeName())
			}
			n = int64(m)
		}
		out, err := f(string(s), n)
		if err != nil {
			return types.WrapErr(err)
		}
		return types.String(out)
	}
	fn := func(name string, f func(string, int64) (string, error)) cel.EnvOption {
		one, two := name+"_string", name+"_string_int"
		return compile.Costed(cel.Function(name,
			cel.Overload(one, []*cel.Type{cel.StringType}, cel.StringType, cel.UnaryBinding(func(id ref.Val) ref.Val { return call(name, f, id, nil) })),
			cel.Overload(two, []*cel.Type{cel.StringType, cel.IntType}, cel.StringType, cel.BinaryBinding(func(id, limit ref.Val) ref.Val { return call(name, f, id, limit) }))), one, two)
	}
	return []cel.EnvOption{fn("okdpSqlName", sqlName), fn("okdpObjectName", objectName)}
}
