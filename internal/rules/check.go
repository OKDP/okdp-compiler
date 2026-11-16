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
// concurrent use: a rule set keeps those that are free. ctx: the context of
// the evaluation that holds the checker (RuleSet.eval), for the rule
// function, a CEL binding, which takes none; nil while it is free.
type checker struct {
	docs map[string]*compile.Prepared
	ctx  context.Context
}

// prepare: the documents of sources, by name, built in a new session.
func prepare(sources map[string]compile.Source) (*checker, error) {
	c := &checker{docs: map[string]*compile.Prepared{}}
	s := cel2yaml.NewSession(0, ruleFunctions(c)...)
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		p, err := compile.Prepare(s, ruleInputs, []string{"values"}, sources[name])
		if err != nil {
			return nil, err
		}
		c.docs[name] = p
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
	r.free = make(chan *checker, runtime.GOMAXPROCS(0))
	r.free <- first
	return nil
}

// get: a checker of the rule set, one that is free, else a new one: a pool
// the collector would empty rebuilds them at every compile.
func (r *RuleSet) get() (*checker, error) {
	select {
	case c := <-r.free:
		return c, nil
	default:
	}
	return prepare(r.sources)
}

// put: c free again, when the rule set keeps fewer than it can use at once.
func (r *RuleSet) put(c *checker) {
	select {
	case r.free <- c:
	default:
	}
}

// eval: the document on input, with the sets and tables of the rule set,
// cancelled with ctx: the first of its messages, "" when it has none, and
// its values. A rule set no LoadRules made (the zero value) fails.
func (r *RuleSet) eval(ctx context.Context, document string, input M) (compile.Problem, M, error) {
	if !r.Loaded() {
		return compile.Problem{}, nil, fmt.Errorf("the rule set: %w", ErrNotLoaded)
	}
	if len(r.sources) == 0 { // Empty: nothing to refuse, nothing computed
		return compile.Problem{}, M{}, nil
	}
	c, err := r.get()
	if err != nil {
		return compile.Problem{}, nil, fmt.Errorf("the rule set: %w", err)
	}
	c.ctx = ctx
	defer func() { c.ctx = nil; r.put(c) }()
	scope := make(M, len(input)+2)
	maps.Copy(scope, input)
	scope["sets"], scope["tables"] = r.sets, r.lists
	sections, problems, err := c.docs[document].Eval(ctx, scope)
	if err != nil {
		return compile.Problem{}, nil, fmt.Errorf("the rule set: %w", err)
	}
	if len(problems) > 0 {
		return problems[0], nil, nil
	}
	return compile.Problem{}, value.AsMap(sections["values"]), nil
}

// Values: the values of the document on input, cancelled with ctx; its
// first message is an error (a *Failure).
func (r *RuleSet) Values(ctx context.Context, document string, input M) (M, error) {
	p, v, err := r.eval(ctx, document, input)
	if err == nil {
		err = failure(document, p)
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Refuse: the check on input, cancelled with ctx: nil when it passes; its
// first message as a *Failure, at its field when it names one; any other
// error, a check that fails to evaluate, never a rule passed.
func (r *RuleSet) Refuse(ctx context.Context, check string, input M) error {
	p, _, err := r.eval(ctx, check, input)
	if err != nil {
		return err
	}
	return failure(check, p)
}

// ErrRefused: what a *Failure is (errors.Is): a rule of the rule set
// refused, as opposed to a check that could not run.
var ErrRefused = errors.New("refused by the rule set")

// ErrNotLoaded: what a check of the zero value of RuleSet fails with: none
// was read (LoadRules, Empty).
var ErrNotLoaded = errors.New("no rule set was loaded (LoadRules)")

// Failure: a rule that refused, the message of a check or a document of
// the rule set, whole (its Error), and the document's name; Path, the field
// it is about (value.FieldError wraps it), when it names one.
type Failure struct {
	Check, Path, Message string
}

// Error: the message.
func (f *Failure) Error() string { return f.Message }

// Is: a *Failure is ErrRefused.
func (f *Failure) Is(target error) bool { return target == ErrRefused }

// failure: the error of the message p of the document name, nil without a
// message, at its field when it names one.
func failure(name string, p compile.Problem) error {
	if p.Message == "" {
		return nil
	}
	f := &Failure{Check: name, Path: p.Path, Message: p.Message}
	if p.Path != "" {
		return value.AtField(p.Path, f)
	}
	return f
}

// ruleFunctions: what the documents call besides the language: reading the
// walkers' values as they are, what a message or a heuristic needs, and
// rule, the values of another document of c, those of the session.
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
				return nil, failure(value.Str(name.Value()), compile.Problem{Message: problems[0].Message})
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
