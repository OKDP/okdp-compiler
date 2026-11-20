# Functions

Normative reference of the functions an expression may call: CEL's
standard library, the CEL libraries the language enables, each at a pinned
version, and the host functions, the language's own (Helm's and sprig's
`sha256sum`, `toJson`, `toYaml`, `quote`...) and OKDP's (`okdpFullname`,
`okdpLabels`, `okdpGeneratedSecret`...).

Every expression may call them: those of the mapping and of the rule
set's templates, the generated Secret's among them
([control flow](03_control_flow.md)). Nothing else is callable: no
template of a chart (`include`, `tpl`), no Go template function that is not
below.

## Properties

The host functions are plain Go, and share these properties:

- **Deterministic and hermetic.** A function reads its arguments and, for
  OKDP's, the compile's release and service; no clock,
  no randomness, no file, no network, no environment variable. The same
  inputs compile to the same outputs. There is no `now`, `randAlphaNum`,
  `uuidv4`, `lookup` or `env`: a random value is generated in the cluster
  ([`okdpGeneratedSecret`](#okdpgeneratedsecret)), a value of the cluster
  or of the platform is an input.
- **Dynamic arguments.** Each takes its argument as `dyn`, of any type
  (`okdpSqlName` and `okdpObjectName`: a string, and a whole number;
  `okdpGeneratedSecret` and a rule set's fragments: a map); the
  number of arguments is checked when the document is loaded. A call
  matching no overload fails the load, in any branch:
  `found no matching overload for '<function>' applied to '(<types>)'`.
- **The argument as data.** The argument is converted to data, maps with
  string keys, lists, numbers, strings, booleans and null, as an
  expression's value is ([lone
  expressions](02_service_definition.md#lone-expressions-and-interpolation)),
  but keeps what the values cannot hold: `str` prints `NaN`, a key `1` as
  `"1"`, a type as its name. A number is a float64, as Helm reads a values
  number ([numbers](02_service_definition.md#numbers)).
- **Errors fail.** A function that cannot compute its result fails the
  expression, where Helm would often print `""`:
  `<where>${{ <expression> }}: <message>`
  ([compile errors](02_service_definition.md#compile-errors)).
- **No bindings.** OKDP's functions read the compile's `release` and
  `service`, not the expression's scope: a `vars` binding named
  `release` does not change `okdpFullname`
  ([context](02_service_definition.md#context)).
- **Helm's output.** A function reproducing a Helm or sprig one is compared
  with what `helm template` prints for the same inputs (`testdata/helm`):
  `testdata/helm/functions` of okdp-gitops for `sha256`, `toJson`,
  `toPrettyJson`, `toYaml` and `quote`
  ([testing](../00_architecture.md#helm-fixtures)).

Adding a function: [extension points](../00_architecture.md#extension-points).

## CEL

### Standard library

Operators (`+`, `-`, `*`, `/`, `%`, comparisons, `&&`, `||`, `!`, `?:`,
`in`, indexing), `has`, `size`, `type`, `matches` (RE2), `contains`,
`startsWith`, `endsWith`; conversions `int`, `uint`, `double`, `string`,
`bool`, `bytes`, `dyn`, `duration`, `timestamp`; the macros `all`,
`exists`, `exists_one`, `map`, `filter`. `&&` and `||` absorb an error
when the other side decides: `has(m.k) && m.k > 1` is `false`, without
error, when `k` is missing; `?:` evaluates the branch it takes only.

### CEL libraries

Besides the standard library, the environment enables these libraries of
cel-go v0.33.2, each at a pinned version: a cel-go upgrade does not change
what a mapping may call until the compiler raises a version.

| Library | Version | Functions |
|---|---|---|
| strings (`ext.Strings`) | 5 | `charAt`, `indexOf`, `lastIndexOf`, `lowerAscii`, `upperAscii`, `replace`, `split`, `substring`, `trim`, `join`, `format` (a precision of 100 at most), `reverse`, `strings.quote` |
| lists (`ext.Lists`) | 3 | `lists.range`, `slice`, `flatten`, `distinct`, `reverse`, `sort`, `sortBy` |
| bindings (`ext.Bindings`) | 1 | `cel.bind(name, value, expr)` |
| two-variable comprehensions (`ext.TwoVarComprehensions`) | 0 | `all(k, v, ...)`, `exists(k, v, ...)`, `existsOne`, `transformList`, `transformMap`, `transformMapEntry` |
| encoders (`ext.Encoders`) | 0 | `base64.encode`, `base64.decode` |
| optional types (`cel.OptionalTypes`) | 2 | `x.?field`, `m[?key]`, `l[?index]`, `orValue`, `hasValue`, `value`, `or`, `optional.of`, `optional.none`, `optional.ofNonZeroValue`, `optFlatMap`, `l.first()` and `l.last()` (optionals: not the host function `first(list)`), optional entries `{?'k': opt}` |

The math, sets and regex extensions are not enabled.

CEL iterates a map in no fixed order: a list built from a map (`m.map(k, k)`,
`m.filter(...)`, `transformList`) is sorted before it is written
([iterating a map](03_control_flow.md#iterating-a-map)).

## Host functions

`okdpGeneratedSecret` is a fragment of the compiler's mandatory rule set
([rule set](01_inputs_outputs.md#rule-set)): every compile has the
functions below. A rule set added to it declares functions of its own,
which its compiles have besides these:
[a rule set's functions](#a-rule-sets-functions).

| Function | Returns | Helm equivalent |
|---|---|---|
| `str(x)` | string | printing `{{ x }}` |
| `first(list)` | any | `a \| default b` |
| `sha256(x)` | string | `sha256sum` |
| `toJson(x)` | string | `toJson` |
| `json.encode(x)` | string | `toJson` |
| `toPrettyJson(x)` | string | `toPrettyJson` |
| `toYaml(x)` | string | `toYaml` |
| `quote(x)` | string | `quote` |
| `okdpFullname()`, `okdpFullname(suffix)` | string | none |
| `okdpLabels(component)` | map | none |
| `okdpSelectorLabels(component)` | map | none |
| `okdpGeneratedSecret(args)` | list of objects | none |
| `okdpSqlName(id)`, `okdpSqlName(id, max)` | string | none |
| `okdpObjectName(id)`, `okdpObjectName(id, max)` | string | none |

Each takes one argument (`okdpFullname`: zero or one; `okdpSqlName` and
`okdpObjectName`: a string, and a whole number; `okdpGeneratedSecret`: a
map). `str`, `first`, `sha256`, `toJson`, `json.encode`, `toPrettyJson`,
`toYaml` and `quote` belong to the language (github.com/okdp/cel2yaml);
the `okdp` functions to OKDP (the root package, `compiler`), which adds
them to the compile's session.

### `str` and `first`

- `str(x)`: `x` as text, as Go templates print it: null is `""`, a string
  itself, bytes their base64, a number as a float64
  ([numbers](02_service_definition.md#numbers)), `true`, a list `[a b]`, a
  map `map[k:v]`. Interpolation converts every expression's value with it.
  Unlike sprig's `toString`, null is `""`, not `<nil>`.
- `first(list)`: the first element that is not empty (null, `false`, `0`,
  `""`, `[]`, `{}`), else `""`. `first([x, d])` is Helm's `x | default d`,
  `first([a, b, c])` sprig's `coalesce a b c`. A value that is not a list
  gives `""`.

### `sha256`, `toJson`, `toPrettyJson`, `toYaml`, `quote`

| Function | Result |
|---|---|
| `sha256(x)` | hex SHA-256 of `str(x)`; null is hashed as `""` (Helm hashes `<nil>`) |
| `toJson(x)` | Go's `encoding/json`: compact, keys sorted, `<`, `>`, `&`, U+2028 and U+2029 escaped |
| `json.encode(x)` | the same as `toJson` |
| `toPrettyJson(x)` | `toJson`'s, indented by two spaces |
| `toYaml(x)` | sigs.k8s.io/yaml: keys sorted, YAML 1.1 quoting (`"yes"`, `"1.0"`), no final newline |
| `quote(x)` | Go's `%q` of `str(x)`: `"a\"b"`; null gives `""`, nothing rather than two quotes |

An encoding that fails (a string `toYaml` cannot write) fails the compile
with the encoder's error (Helm would print `""`).

```yaml
podAnnotations:
  checksum/settings: ${{ sha256(toJson(settings)) }}   # restarts the pods on a change
```

A value is written in the values as data: `config: ${{ settings }}` is a
map, where a template writes `{{ toYaml .settings | nindent 2 }}`. The
encoders are for a value that must be text: a configuration file in a
ConfigMap, an annotation, a hash.

### `okdpFullname`

`okdpFullname(suffix)`: `<release>-<suffix>`, cut to 63 bytes, without a
trailing `-`. `okdpFullname()` and `okdpFullname('')` give the release name
alone, cut the same way. The suffix is converted with `str`. Named
`okdpFullname`, not `fullname`, so that a mapping can bind its own
`fullname`.

### `okdpLabels` and `okdpSelectorLabels`

`okdpLabels(component)`, `okdpSelectorLabels(component)`: the
[labels](02_service_definition.md#context) of the context, with
`app.kubernetes.io/component` set to `str(component)`, left out when it is
`''`.

### `okdpSqlName` and `okdpObjectName`

The name a provider derives from a claim's ID: one name per ID, so that
two claimers' claims never share one. A claim's ID is DNS-1123 labels
joined by dots (`<namespace>.<name>`); replacing its dots and dashes by
one character loses it, `a-b.c` and `a.b-c` both become `a_b_c`, and the
claimer sorted last would own the role, the database or the keyspace of
the other. Both functions refuse (fail the expression) an `id` that is not
DNS-1123 labels joined by dots (63 characters at most), and a `max` out
of 24 to 253.

| Function | Result |
|---|---|
| `okdpSqlName(id)`, `okdpSqlName(id, max)` | an identifier of `[a-z0-9_]`, `max` characters at most (63 by default, PostgreSQL's limit; Cassandra's keyspaces take 48): each dash two underscores, each dot one |
| `okdpObjectName(id)`, `okdpObjectName(id, max)` | a DNS-1123 label, `max` characters at most (63 by default): each dash two dashes, each dot one |

The mapping is injective: a label neither starts nor ends with a dash, so
a dot is never next to one, and a run of underscores (dashes) is one dot
(a run of one) or dashes (a run of two each); `team.analytics` is
`team_analytics` and `team-analytics`, `team-a.analytics` is
`team__a_analytics` and `team--a-analytics`. A name shorter than `max`
is kept. One that is not is cut: its first `max` - 17 characters, a
`_` (`-`), and the first 16 hexadecimal digits of the SHA-256 of the ID,
`max` characters in all; a name that fits is shorter than `max`, one that
is cut is as long, so a cut name is no ID's whole name, and the digits
tell two IDs that share their start apart (two IDs would collide on
a SHA-256 prefix of 64 bits only).

`okdpSqlName` never gives a name that starts with `system`, Cassandra's
`system`, `system_schema`, `system_auth`... keyspaces and the like: such a
name gets `c___` first (`system.x` is `c___system_x`). Not `c_`: that is
the name of `c.system.x`. Three underscores in a row are no ID's, so the
prefix makes a name no other ID has. The prefix counts in `max`.

```yaml
claimed:
  - $for: c in claims
    $each:
      keyspace: ${{ okdpSqlName(c.id, 48) }}   # cassandra: 48 at most
      topic: ${{ okdpObjectName(c.id) }}       # an object name
```

Two objects of one kind, namespace and name in an instance are refused
([objects section](01_inputs_outputs.md#objects-section)): a provider
that derived a name another way fails the compile of the instance, where
it would have given two claimers one object.

### `okdpGeneratedSecret`

The objects of a Secret whose random values External Secrets Operator
generates in the cluster, for
`$spread` in [`objects`](02_service_definition.md#objects). The
`provides[].secret` `generate` form uses it too
([secret](02_service_definition.md#secret)).

| Key of `args` | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | required | name of the Secret, and of the ExternalSecret writing it |
| `keys` | list | `[]` | the generated keys, below |
| `stringData` | map of strings, numbers or booleans | `{}` | static keys, as text (`str`), not credentials: a literal at a sensitive key (`password`, `token`, `...Key`) is refused unless `Inputs.AllowPlainSecrets` ([objects section](01_inputs_outputs.md#objects-section)); a value containing `{{` is written as a template string literal |
| `labels` | map of strings | `{}` | extra labels of the ExternalSecret and of the Secret, over [`labels`](02_service_definition.md#context) |
| `annotations` | map of strings | `{}` | annotations of every object |
| `type` | string | none | type of the Secret |
| `namespace` | string | `release.namespace` | namespace of every object; another than the release's, a privileged project's only (`global.okdp.policy.privileged`, [objects section](01_inputs_outputs.md#objects-section)) |
| `refreshInterval` | string | `"0"` | refresh interval of the ExternalSecret, a duration (`1h`): `"0"` generates once |
| `apiVersion` | string | `external-secrets.io/v1` | apiVersion of the ExternalSecret |

| Key of `keys[]` | Type | Default | Meaning |
|---|---|---|---|
| `key` | string | required | the Secret's key |
| `length` | whole number >= 0 | `32` | length of the generated value |
| `digits` | whole number >= 0 | `6` | number of digits; `0` when given as `0` |
| `symbols` | whole number >= 0 | `0` | number of symbols |
| `noUpper` | `true` or `false` | `false` | no upper-case letters |
| `transform` | string | none | `sha256` (the key holds the value's `sha256sum`) or `b64enc` |

An empty value (`""`, `0`, `false`) takes the default, as Helm's
`default` does, except a `digits` of `0`.

Every argument and key field is checked for its type when set, against
the mapping schema (`okdpGeneratedSecret`, and `generatedKey` for a key:
[sections](02_service_definition.md#evaluation-order)): a value of
another type, a null included, fails rather than reads as empty or is printed (a
`stringData` value that is a list or a map, which Helm prints as Go does;
`length: '32'` or `noUpper: 'true'`, text where Helm would print the
number or the boolean), and so does an argument or a key field the
function does not know: a typo would otherwise be ignored (`lenght`).
Every violation is reported at once.

It returns one `generators.external-secrets.io/v1alpha1` `Password` per
key, in order, then the `ExternalSecret`. A Password is named
`<name>-<key>`, the key lower-cased with `_` and `.` as `-`; a name longer
than 63 characters is cut to 54, without a trailing `-`, and suffixed with
`-` and 8 hex characters of the key's SHA-256. Passwords carry
[`labels`](02_service_definition.md#context). The Secret's data are the
`stringData` keys, then each generated key; a generated key replaces a
`stringData` key of the same name.

Errors, an argument that is not a map, the schema's, then the keys of
one generator name, one line per name:

```text
okdpGeneratedSecret: the arguments are a map, got <value>
okdpGeneratedSecret: the arguments don't meet the specifications of mapping.schema.json:
- at '/okdpGeneratedSecret': missing property 'name'
- at '/okdpGeneratedSecret/keys': validation failed
  - at '/okdpGeneratedSecret/keys/0': additional properties 'lenght' not allowed
  - at '/okdpGeneratedSecret/keys/1/transform': value must be one of '', 'sha256', 'b64enc'
okdpGeneratedSecret: <name>: keys "<a>" and "<b>" give the same generator name "<n>"
```

### A rule set's functions

A rule set declares the functions mappings call besides these, in
`rules.yaml` `functions` ([functions](05_rules.md#functions)); a compile
given the set has them, one without it fails at load on a call of one,
in any branch: `undeclared reference to '<function>'`. Each takes one
argument and is one of two kinds.

- **A fragment** is a template of the set in the mapping language,
  evaluated at each call on its argument, a map, which the mapping schema
  checks under the function's name (every violation at once,
  `<function>: the arguments don't meet the specifications of
  mapping.schema.json:`). It sees `args`, `release`, `labels` (OKDP's,
  without component), `platform` and `global`, and returns its values,
  or the one `result` names. Its cost counts in the compile's total.
- **A document** computes the result from the argument of the call,
  `arg`, `release` and `platform`, and returns the value `result` names.

The objects a fragment returns are the package's own, in `objects` with
`$spread`: the compile's checks apply to them as to any object (the
namespace, no cluster-scoped kind, no Secret in clear, the set's own
hooks). The arguments are data, not templates: no `{{`, no function of
the fragment in a string. They are CEL, in git with the set: nothing to
publish, release or pin apart, and the compile sees them.

The security set's functions, the hardened pod and the workloads built
on it, and the Secret of an OAuth client: [its
functions](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/specs/03_functions.md).

## From Helm

A Helm template function, and what an expression calls instead. A number
of the values is a double: `int(x)` where an `int` is needed
([numbers](02_service_definition.md#numbers)).

| Helm, sprig | Expression |
|---|---|
| `default d x`, `coalesce a b c` | `first([x, d])`, `first([a, b, c])`; a key that may be absent: `m.?k.orValue(d)` |
| `dig "a" "b" d m` | `m.?a.?b.orValue(d)` |
| `hasKey m "k"`, `has x l` | `'k' in m` or `has(m.k)`, `x in l` |
| `ternary a b c` | `c ? a : b` |
| `if x`, `if not (empty x)` | `$if: ${{ x }}`: `$if` tests a value as Helm does ([`$if`](03_control_flow.md#if)) |
| `toString x` | `str(x)` (null is `""`) |
| `squote x` | `"'" + str(x) + "'"` |
| `printf "%s-%d" a b` | `'%s-%d'.format([a, b])` |
| `upper`, `lower` | `upperAscii()`, `lowerAscii()`: ASCII letters only |
| `trim`, `replace "a" "b" s`, `contains`, `hasPrefix`, `hasSuffix` | `s.trim()`, `s.replace('a', 'b')`, `s.contains(x)`, `s.startsWith(p)`, `s.endsWith(p)` |
| `trimPrefix p s`, `trimSuffix p s` | `s.startsWith(p) ? s.substring(p.size()) : s`, `s.endsWith(p) ? s.substring(0, s.size() - p.size()) : s` |
| `substr a b s`, `trunc n s` | `s.substring(a, b)`, `s.size() > n ? s.substring(0, n) : s` (code points, where sprig counts bytes) |
| `splitList "." s`, `join "," l` | `s.split('.')`, `l.join(',')` (a list of strings: `l.map(x, str(x)).join(',')`) |
| `regexMatch r s` | `s.matches(r)` (RE2) |
| `b64enc s`, `b64dec s` | `base64.encode(bytes(s))`, `string(base64.decode(s))` |
| `sha256sum`, `toJson`, `toPrettyJson`, `toYaml`, `quote` | `sha256`, `toJson`, `toPrettyJson`, `toYaml`, `quote` |
| `list a b`, `dict "k" v` | `[a, b]`, `{'k': v}` |
| `concat a b`, `append l x` | `a + b`, `l + [x]` |
| `first l`, `last l`, `len l` | `l[0]`, `l[l.size() - 1]`, `l.size()` |
| `uniq`, `sortAlpha`, `reverse` | `distinct()`, `sort()`, `reverse()` |
| `until n` | `lists.range(n)` |
| `keys m`, `values m` | `m.map(k, k).sort()`, `m.map(k, k).sort().map(k, m[k])` |
| `kindIs "map" x`, `kindIs "slice" x`, `kindIs "string" x` | `type(x) == map`, `type(x) == list`, `type(x) == string` |
| `int`, `float64`, `atoi` | `int(x)`, `double(x)`, `int(s)` |
| `merge`, `mergeOverwrite` | the [`$merge`](03_control_flow.md#merge) directive |
| `include "<chart>.fullname" .`, `include "<chart>.labels" .` | `okdpFullname()`, `okdpLabels(component)`, or a `vars` binding |
| `required`, `fail` | the [errors section](02_service_definition.md#the-errors-section) |
| `toYaml x \| nindent n`, `indent` | the value itself, `${{ x }}` |
| `fromJson`, `fromYaml`, `tpl`, `lookup`, `now`, `randAlphaNum`, `uuidv4`, `genCA`, `env`, `semverCompare`, `sha1sum` | none ([properties](#properties)) |
