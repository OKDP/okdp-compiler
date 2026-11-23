// The documents of a rule set are documents of the mapping language. Each
// is prepared once, in a session that belongs to the rule set. A compile
// therefore does not pay for building them, nor count them in its cost.

package rules

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/value"
)

// ruleInputs lists what the documents may read: what the compiler gives
// them (the hooks', the scopes', the functions' and the sections' inputs),
// and the sets and tables of the rule set. A set declares the names of the
// inputs its documents give one another through rule (inputs in
// rules.yaml): every document may read them.
var ruleInputs = []string{"allowPlainSecrets", "arg", "field", "items", "key", "ns", "object", "platform", "release", "sets", "settings", "svc", "tables", "value"}

// checker holds the documents prepared in one session. A session is not safe
// for concurrent use, so a rule set keeps the checkers that are free. ctx
// is the context of the evaluation that holds the checker, nil while it is
// free: the rule function needs it, because a CEL binding takes no context.
type checker struct {
	docs map[string]*compile.Prepared
	ctx  context.Context
}

func prepare(sources map[string]compile.Source, inputs []string) (*checker, error) {
	c := &checker{docs: map[string]*compile.Prepared{}}
	s := cel2yaml.NewSession(0, ruleFunctions(c)...)
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		p, err := compile.Prepare(s, inputs, []string{"values"}, sources[name])
		if err != nil {
			return nil, err
		}
		c.docs[name] = p
	}
	return c, nil
}

// loadDocuments reads the documents of a set, of names no set read before
// gives. ready prepares them.
func (r *RuleSet) loadDocuments(fsys fs.FS, documents M) error {
	for _, name := range slices.Sorted(maps.Keys(documents)) {
		if _, ok := r.sources[name]; ok {
			return r.taken("documents", name)
		}
		file := path.Clean(value.Str(documents[name]))
		b, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf("%s: documents: %s: %w", RulesFile, name, err)
		}
		r.sources[name] = compile.Source{Name: file, Data: b}
	}
	return nil
}

// get returns a free checker, else a new one. A channel keeps them rather
// than a sync.Pool, which the garbage collector would empty.
func (r *RuleSet) get() (*checker, error) {
	select {
	case c := <-r.free:
		return c, nil
	default:
	}
	return prepare(r.sources, r.inputs)
}

// put frees c, or drops it when the rule set keeps as many as it can use.
func (r *RuleSet) put(c *checker) {
	select {
	case r.free <- c:
	default:
	}
}

// Values evaluates the document on input, with the sets and tables of the
// rule set, and returns its values. Its first message is an error (a
// *Failure); the first message of a document it calls through rule is its
// own, as written. A document r does not hold fails.
func (r *RuleSet) Values(ctx context.Context, document string, input M) (M, error) {
	if !r.Loaded() {
		return nil, fmt.Errorf("the rule set: %w", ErrNotLoaded)
	}
	if _, ok := r.sources[document]; !ok {
		return nil, fmt.Errorf("the rule set: no document %s", document)
	}
	c, err := r.get()
	if err != nil {
		return nil, fmt.Errorf("the rule set: %w", err)
	}
	c.ctx = ctx
	defer func() { c.ctx = nil; r.put(c) }()
	scope := make(M, len(input)+2)
	maps.Copy(scope, input)
	scope["sets"], scope["tables"] = r.sets, r.lists
	sections, problems, err := c.docs[document].Eval(ctx, scope)
	if f, ok := errors.AsType[*Failure](err); ok {
		return nil, f // a document that rule calls refused: its message is this one's
	}
	if err != nil {
		return nil, fmt.Errorf("the rule set: %w", err)
	}
	if len(problems) > 0 {
		return nil, failure(problems[0])
	}
	return value.AsMap(sections["values"]), nil
}

// Refuse evaluates the document of any set, by name, on input, as a
// check. It returns nil when the document passes, and its first message
// as a *Failure. A document that fails to evaluate gives another error: it
// never counts as a rule passed.
func (r *RuleSet) Refuse(ctx context.Context, document string, input M) error {
	_, err := r.Values(ctx, document, input)
	return err
}

// ErrRefused is what a *Failure matches (errors.Is): a rule of the rule set
// refused, as opposed to a document that could not run.
var ErrRefused = errors.New("refused by the rule set")

// ErrNotLoaded is the error of a document or a hook run on the zero value
// of RuleSet.
var ErrNotLoaded = errors.New("no rule set was loaded (LoadRules)")

// Failure is a rule that refused. Path is the field the message is about,
// when it names one: value.FieldError then wraps the Failure.
type Failure struct {
	Path, Message string
}

// Error returns the message.
func (f *Failure) Error() string { return f.Message }

// Is makes a *Failure match ErrRefused.
func (f *Failure) Is(target error) bool { return target == ErrRefused }

// failure turns the message p into an error, nil without a message.
func failure(p compile.Problem) error {
	if p.Message == "" {
		return nil
	}
	f := &Failure{Path: p.Path, Message: p.Message}
	if p.Path != "" {
		return value.AtField(p.Path, f)
	}
	return f
}

// ruleFunctions returns the functions the documents call besides the
// language. rule evaluates another document of c, in the same session.
func ruleFunctions(c *checker) []cel.EnvOption {
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
			x, err := cel2yaml.ToData(a)
			if err != nil {
				return nil, err
			}
			y, err := cel2yaml.ToData(b)
			if err != nil {
				return nil, err
			}
			out := maps.Clone(value.AsMap(x))
			maps.Copy(out, value.AsMap(y))
			return out, nil
		}),
		binary("rule", func(name, input ref.Val) (any, error) {
			in, err := cel2yaml.ToData(input)
			if err != nil {
				return nil, err
			}
			p, ok := c.docs[value.Str(name.Value())]
			if !ok {
				return nil, fmt.Errorf("rule: no document %s", value.Str(name.Value()))
			}
			sections, problems, err := p.Eval(c.ctx, value.AsMap(in))
			if err != nil {
				return nil, err
			}
			if len(problems) > 0 {
				return nil, failure(compile.Problem{Message: problems[0].Message})
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
		cel2yaml.Func("leaves", cel.DynType, func(x any) (any, error) { return leaves(x), nil }),
		binary("fields", func(s, seps ref.Val) (any, error) { return fields(value.Str(s.Value()), value.Str(seps.Value())), nil }),
		native("urlParse", func(s any) any { return urlParse(value.Str(s)) }),
	}
}
