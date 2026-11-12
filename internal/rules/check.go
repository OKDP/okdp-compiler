// The checks of a rule set: documents of the mapping language that say what
// a walker of the compiler refuses. Each is prepared once, in a session of
// the rule set's own: a compile pays neither for building them nor in its
// cost, which bounds what a package's expressions compute.

package rules

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"runtime"
	"slices"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"github.com/okdp/cel2yaml"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/value"
)

// The checks a rule set holds, every one of them.
const (
	CheckNamespaceRule  = "namespace"
	CheckPlatformNSRule = "platformNamespace"
	CheckReleaseRule    = "release"
	CheckObjectRule     = "object"
	CheckUpstreamKey    = "upstreamKey"
	CheckPlainSecret    = "plainSecret"
	CheckHost           = "host"
	CheckContextLayer   = "contextLayer"
	CheckValuesLayer    = "valuesLayer"
)

// ruleChecks: the names of the checks of a rule set, sorted.
var ruleChecks = []string{CheckContextLayer, CheckHost, CheckNamespaceRule, CheckObjectRule, CheckPlainSecret, CheckPlatformNSRule, CheckReleaseRule, CheckUpstreamKey, CheckValuesLayer}

// ruleInputs: what the checks may read, besides their sets and tables.
var ruleInputs = []string{"added", "arg", "at", "declared", "field", "host", "items", "key", "layer", "level", "listed", "name", "ns", "object", "platform", "raw", "release", "sets", "settings", "suffix", "svc", "tables", "value"}

// checker: the checks prepared in one session, which is not safe for
// concurrent use: a rule set keeps those that are free.
type checker map[string]*compile.Prepared

// prepare: the documents of sources, by name, built in a new session.
func prepare(sources map[string]compile.Source) (checker, error) {
	c := checker{}
	s := cel2yaml.NewSession(context.Background(), 0, ruleFunctions(c)...)
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		p, err := compile.Prepare(s, ruleInputs, []string{"values"}, sources[name])
		if err != nil {
			return nil, err
		}
		c[name] = p
	}
	return c, nil
}

// loadChecks: the checks of a rule set, their files read from fsys,
// prepared once to fail on one that does not compile, the session kept for
// the first compile.
func (r *RuleSet) loadChecks(fsys fs.FS, files, documents M) error {
	sources := map[string]compile.Source{}
	read := func(what, name string, file any) error {
		b, err := fs.ReadFile(fsys, path.Clean(value.Str(file)))
		if err != nil {
			return fmt.Errorf("%s: %s: %s: %w", RulesFile, what, name, err)
		}
		sources[name] = compile.Source{Name: path.Clean(value.Str(file)), Data: b}
		return nil
	}
	for _, name := range ruleChecks {
		if err := read("checks", name, files[name]); err != nil {
			return err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(documents)) {
		if _, taken := sources[name]; taken {
			return fmt.Errorf("%s: documents: %s: the name of a check", RulesFile, name)
		}
		if err := read("documents", name, documents[name]); err != nil {
			return err
		}
	}
	first, err := prepare(sources)
	if err != nil {
		return fmt.Errorf("%s: checks: %w", RulesFile, err)
	}
	r.sources = sources
	r.free = make(chan checker, runtime.GOMAXPROCS(0))
	r.free <- first
	return nil
}

// get: a checker of the rule set, one that is free, else a new one: a pool
// the collector would empty rebuilds them at every compile.
func (r *RuleSet) get() checker {
	select {
	case c := <-r.free:
		return c
	default:
	}
	c, err := prepare(r.sources)
	if err != nil {
		panic(err) // compiled once already, in LoadRules
	}
	return c
}

// put: c free again, when the rule set keeps fewer than it can use at once.
func (r *RuleSet) put(c checker) {
	select {
	case r.free <- c:
	default:
	}
}

// eval: the document on input, with the sets and tables of the rule set:
// the first of its messages, "" when it has none, and its values.
func (r *RuleSet) eval(document string, input M) (compile.Problem, M, error) {
	if len(r.sources) == 0 { // Empty: nothing to refuse, nothing computed
		return compile.Problem{}, M{}, nil
	}
	c := r.get()
	defer r.put(c)
	scope := make(M, len(input)+2)
	maps.Copy(scope, input)
	scope["sets"], scope["tables"] = r.sets, r.lists
	sections, problems, err := c[document].Eval(scope)
	if err != nil {
		return compile.Problem{}, nil, fmt.Errorf("the rule set: %w", err)
	}
	if len(problems) > 0 {
		return problems[0], nil, nil
	}
	return compile.Problem{}, value.AsMap(sections["values"]), nil
}

// problem: the first message of the check on input, "" when it passes. A
// check that fails to evaluate is an error: never a rule passed.
func (r *RuleSet) Problem(check string, input M) (string, error) {
	p, _, err := r.eval(check, input)
	return p.Message, err
}

// values: the values of the document on input; its first message is an
// error.
func (r *RuleSet) Values(document string, input M) (M, error) {
	p, v, err := r.eval(document, input)
	if err == nil {
		err = problem(p).err()
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// refuse: the error of the check's first message on input, at its field
// when it names one; nil when it passes.
func (r *RuleSet) Refuse(check string, input M) error {
	p, _, err := r.eval(check, input)
	if err != nil {
		return err
	}
	return problem(p).err()
}

// problem: a message of a document, as an error.
type problem compile.Problem

// err: the error of p, nil without a message, at its field when it names
// one.
func (p problem) err() error {
	switch {
	case p.Message == "":
		return nil
	case p.Path != "":
		return value.AtField(p.Path, errors.New(p.Message))
	}
	return errors.New(p.Message)
}

// toData: a CEL value as plain data.
func toData(v ref.Val) (any, error) {
	pb, err := v.ConvertToNative(types.JSONValueType)
	if err != nil {
		return nil, err
	}
	return pb.(*structpb.Value).AsInterface(), nil
}

// ruleFunctions: what the documents call besides the language: reading the
// walkers' values as they are, what a message or a heuristic needs, and
// rule, the values of another document of c, those of the session.
func ruleFunctions(c checker) []cel.EnvOption {
	native := func(name string, f func(any) any) cel.EnvOption {
		return cel.Function(name, cel.Overload(name+"_dyn", []*cel.Type{cel.DynType}, cel.DynType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				var arg any
				if v.Type() != types.NullType {
					arg = v.Value()
				}
				return types.DefaultTypeAdapter.NativeToValue(f(arg))
			})))
	}
	binary := func(name string, f func(a, b ref.Val) (any, error)) cel.EnvOption {
		return cel.Function(name, cel.Overload(name+"_dyn_dyn", []*cel.Type{cel.DynType, cel.DynType}, cel.DynType,
			cel.BinaryBinding(func(a, b ref.Val) ref.Val {
				v, err := f(a, b)
				if err != nil {
					return types.WrapErr(err)
				}
				return types.DefaultTypeAdapter.NativeToValue(v)
			})))
	}
	return []cel.EnvOption{
		binary("dig", func(m, p ref.Val) (any, error) { return value.Dig(value.AsMap(m.Value()), value.Str(p.Value())), nil }),
		binary("pathKey", func(p, k ref.Val) (any, error) { return value.Key(value.Str(p.Value()), value.Str(k.Value())), nil }),
		binary("merge", func(a, b ref.Val) (any, error) {
			x, err := toData(a)
			if err != nil {
				return nil, err
			}
			y, err := toData(b)
			if err != nil {
				return nil, err
			}
			out := maps.Clone(value.AsMap(x))
			maps.Copy(out, value.AsMap(y))
			return out, nil
		}),
		binary("rule", func(name, input ref.Val) (any, error) {
			in, err := toData(input)
			if err != nil {
				return nil, err
			}
			p, ok := c[value.Str(name.Value())]
			if !ok {
				return nil, fmt.Errorf("rule: no document %s", value.Str(name.Value()))
			}
			sections, problems, err := p.Eval(value.AsMap(in))
			if err != nil {
				return nil, err
			}
			if len(problems) > 0 {
				return nil, errors.New(problems[0].Message)
			}
			return sections["values"], nil
		}),
		native("asList", func(v any) any { return value.AsList(v) }),
		native("asMap", func(v any) any { return value.AsMap(v) }),
		native("truthy", func(v any) any { return value.Truthy(v) }),
		native("keys", func(v any) any {
			out := []any{}
			for _, k := range slices.Sorted(maps.Keys(value.AsMap(v))) {
				out = append(out, k)
			}
			return out
		}),
		native("sensitive", func(k any) any { return value.Sensitive(value.Str(k)) }),
		native("credentialReference", func(k any) any { return value.CredentialReference(value.Str(k)) }),
		native("credentialLiteral", func(v any) any { return value.CredentialLiteral(v) }),
	}
}
