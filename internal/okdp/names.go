// Names: okdpSqlName and okdpObjectName, the names a provider derives
// from a claim's ID: a database, a role, a keyspace, the object of a
// topic. No two IDs give the same name, so the claims of two claimers
// never share one. Replacing the dots and dashes of an ID by a single
// character would lose that: a-b.c and a.b-c both become a_b_c.

package okdp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/chart"
)

const (
	nameMax                = 63      // the default limit: a PostgreSQL identifier, a DNS-1123 label
	nameMaxMin, nameMaxMax = 24, 253 // bound the limit a caller may ask for
	nameHash               = 16      // hex digits of the ID's SHA-256 that end a long name: 64 bits
	systemEscape           = "c___"  // what okdpSqlName puts before a name that starts with system
)

// checkDotted returns nil when id is a claim's ID. The names below are
// injective over that form: a label starts and ends with a letter or a
// digit, so a dot is never next to a dash. fn names the caller in the
// error.
func checkDotted(fn, id string) error {
	if !chart.IsClaimID(id) {
		return fmt.Errorf("%s: %q is not a claim's ID: DNS-1123 labels joined by dots, 63 characters at most", fn, id)
	}
	return nil
}

func checkNameMax(fn string, limit int64) error {
	if limit < nameMaxMin || limit > nameMaxMax {
		return fmt.Errorf("%s: the length %d is out of %d to %d", fn, limit, nameMaxMin, nameMaxMax)
	}
	return nil
}

// boundName returns name, derived from id, when it is shorter than
// limit. Otherwise it returns exactly limit characters: the start of
// name, then sep, then the first 16 hexadecimal digits of the SHA-256 of
// id. A cut name is limit long and a name that fits is shorter, so a cut
// name is never the whole name of another ID.
func boundName(id, name, sep string, limit int) string {
	if len(name) < limit {
		return name
	}
	sum := sha256.Sum256([]byte(id))
	return name[:limit-1-nameHash] + sep + hex.EncodeToString(sum[:])[:nameHash]
}

// sqlName implements okdpSqlName. It returns an identifier of [a-z0-9_]:
// each dash becomes two underscores and each dot one. A dot is never
// next to a dash, so a run of underscores reads back as one dot (1) or
// dashes (2 each), and two IDs never give one name.
//
// A name that starts with system gets systemEscape first. It would
// otherwise collide with Cassandra's system keyspaces or with the prefix
// of PostgreSQL's system catalogs. No ID gives three underscores in a
// row, so the escaped name is no other ID's.
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

// objectName implements okdpObjectName. It returns a DNS-1123 label:
// each dash becomes two dashes and each dot one, which reads back the
// same way.
func objectName(id string, limit int64) (string, error) {
	if err := checkDotted("okdpObjectName", id); err != nil {
		return "", err
	}
	if err := checkNameMax("okdpObjectName", limit); err != nil {
		return "", err
	}
	return boundName(id, strings.NewReplacer("-", "--", ".", "-").Replace(id), "-", int(limit)), nil
}

// nameFunctions returns okdpSqlName and okdpObjectName, each taking an
// ID and an optional limit. An expression of an unchecked type can pass
// an argument that is not a string or an int: the call then fails.
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
		return cel2yaml.Costed(cel.Function(name,
			cel.Overload(one, []*cel.Type{cel.StringType}, cel.StringType, cel.UnaryBinding(func(id ref.Val) ref.Val { return call(name, f, id, nil) })),
			cel.Overload(two, []*cel.Type{cel.StringType, cel.IntType}, cel.StringType, cel.BinaryBinding(func(id, limit ref.Val) ref.Val { return call(name, f, id, limit) }))), one, two)
	}
	return []cel.EnvOption{fn("okdpSqlName", sqlName), fn("okdpObjectName", objectName)}
}
