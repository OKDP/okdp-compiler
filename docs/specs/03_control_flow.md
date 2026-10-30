# Control flow

Normative reference of the directives of the mapping language: the keys
starting with `$` that shape the values a document computes, where a Helm
template writes `{{ if }}`, `{{ range }}`, `{{ with }}`, `merge` and
`concat`. A directive works on data, not on text: its result is a value of
the document at its place, never lines to indent (`toYaml | nindent` has no
equivalent, and no use).

Three kinds of document share the directives, the
[expressions](02_service_definition.md#expressions) and the
[functions](04_functions.md):

| Document | Directives |
|---|---|
| the mapping: the sections of `okdp.yaml` and the values file, `values.cel.yaml` ([service definition](02_service_definition.md#sections)) | all |
| the values of an OAuth client's chart, `internal/okdp/oidc-dcr.cel.yaml`, embedded in the compiler ([OAuth client charts](01_inputs_outputs.md#oauth-client-charts)) | all |
| the objects of [`okdpGeneratedSecret`](04_functions.md#okdpgeneratedsecret), `internal/okdp/generated-secret.cel.yaml`, embedded in the compiler | all |

## Directives

| Directive | In | Value | Result |
|---|---|---|---|
| [`$if`](#if) | a map | a condition | the map is left out when the condition is not true |
| [`$value`](#value) | a map | any node | the map is this value |
| [`$else`](#else) | a map with `$if` and `$value` | any node | the map is this value when the condition is not true |
| [`$for`](#for) with `$key`, `$value` | a map | `<name> in <expression>` | a map, one key per element |
| [`$for`](#for) with `$each` | a list item | `<name> in <expression>` | items per element |
| [`$vars`](#vars) | next to `$for` | a map of bindings | bindings per element |
| [`$merge`](#merge) | a map | an expression returning a map | merged over the map's keys |
| [`$spread`](#spread) | a list item | an expression returning a list | its items, spliced in |

What a Helm template writes, in the mapping:

| Helm template | Mapping |
|---|---|
| `{{- if .Values.x }}` around a key or an item | `$if: ${{ values.x }}` in its map |
| `{{ if c }}a{{ else }}b{{ end }}` | `{$if: "${{ c }}", $value: a, $else: b}`, or `"${{ c ? 'a' : 'b' }}"` ([`$else`](#else)) |
| `{{- with .Values.db }}`, then `{{ .host }}` | a [`vars`](02_service_definition.md#vars) binding `db`, `$if: ${{ db }}`, then `${{ db.host }}` |
| `{{- $x := ... }}` | a `vars` binding, or a [`$vars`](#vars) binding in a `$for` |
| `{{- range .Values.items }}` | `$for: i in values.items` with `$each` |
| `{{- range $k, $v := .Values.env }}` | `$for` over the sorted keys, `values.env[k]` ([iterating a map](#iterating-a-map)) |
| `{{- range $i, $e := .Values.l }}`, `until` | `$for: "i in lists.range(values.l.size())"` ([positions](#positions)) |
| `merge`, `mergeOverwrite` | `$merge` |
| `concat`, `append` | `${{ a + b }}`, or `$spread` |
| `{{- toYaml .Values.x \| nindent 4 }}` | `${{ values.x }}`: a lone expression keeps its value's type |
| `{{ include "chart.fullname" . }}` | a `vars` binding, or a [host function](04_functions.md#host-functions) (`okdpFullname`) |
| `{{ required "msg" .Values.x }}`, `{{ fail "msg" }}` | a rule of the [errors section](02_service_definition.md#the-errors-section) |
| `tpl` | none: a value holding `{{` is text |

## Loading and evaluation

A document is loaded before it is evaluated. Loading reads it as YAML
([YAML reading](02_service_definition.md#yaml-reading)), then builds it
into a tree: each map in the form its directives give it, each directive
checked against its [place](#places), each expression compiled once in the
[scope](#scope) of its place. Building is the check of the language: it
covers every branch, under a false `$if` and in a `$for` over no element
too, and the first mistake fails the load before any expression is
evaluated. Evaluation then runs the tree without parsing or compiling
anything: a `$for` over 5,000 elements runs its compiled programs 5,000
times.

Evaluation skips what is left out: the keys of a map whose `$if` is not
true, the branch of `$value` and `$else` not taken, the `$each` of a `$for`
over no element. An expression there may read what exists only when the
branch is taken:

```yaml
env:
  - $if: ${{ has(values.database) }}
    name: DB_HOST
    value: ${{ values.database.host }}   # evaluated only when database is set
```

It must still compile: a syntax error, a name out of scope, an unknown
function, literals of mismatched types (`'x' + 1`) fail the load whether an
instance takes the branch or not. A missing key (`no such key`) or a value
of the wrong type (`no such overload`) fails only when the expression is
evaluated: the variables are dynamic
([context](02_service_definition.md#context)).

A map is evaluated in this order: `$if`, and when it is not true, `$else`
or nothing; then `$for` (with `$key`, `$value`, `$vars`); else `$value`;
else each key in the order written, then `$merge`. A list's items are
evaluated in order; a list item that is a map: `$for` (with `$each`,
`$vars`); else `$spread`; else the item is evaluated as a map. The elements
of a `$for` are taken in order. Expressions have no side effects: the
order decides which error is reported, the first, and which of two
elements giving one key to a `$for` map wins, the later.

### Places

A key starting with `$` is a directive. Each must be one of its place, the
place taking its form from `$for`, `$spread` or `$value`; any other,
unknown (`$iff`) or misplaced, fails, `<where><key>: not allowed here
(allowed: <directives of the place>)`:

| Place | Holds |
|---|---|
| a map | keys, `$if`, `$value`, `$for`, `$merge` |
| a list item | keys, `$if`, `$value`, `$merge`, `$for`, `$spread` |
| a map with `$for` | `$if`, `$for`, `$key` and `$value` (required), `$vars` |
| a map with `$value` | `$if`, `$value`, `$else` (read with `$if` only) |
| a list item with `$for` | `$for`, `$each` (required), `$vars` |
| a list item with `$spread` | `$spread` |

The top level of a document holds its sections only: a directive there
fails (`<file>: <key>: unknown section (allowed: ...)`; `okdp.yaml`
refuses it when it is loaded, as any key it does not define). The values
file is one section whole: its top level is a map, and holds what a map
holds.

A key starting with `$` is written with one more `$`, as `$${{` writes a
literal `${{`: `$$schema: x` is the key `$schema`, `$$$x` the key `$$x`.
In a value, `$` is text (`$HOME`, `${VAR}`, `$$x`), `$${{` excepted.

## Scope

The names an expression may read, from the outermost:

1. The context variables of the document: those of the mapping
   ([context](02_service_definition.md#context)); a client template adds
   `client` and `names`; the generated Secret template has `args`,
   `release` and `labels` alone.
2. The `vars` bindings, in order, each seeing those before it.
3. In a `$for`, its variable, then its `$vars` bindings, in order. A `$for`
   in its `$each`, `$key` or `$value` adds its own.

A name shadows an outer one of the same name (a `vars` binding a context
variable, a `$for` variable a `vars` binding) and exists only inside: past
the `$for`, the outer name is back. A binding is a value, computed once (a
`vars` once per compile, a `$vars` once per element): nothing assigns or
changes it. The names are known when the document is loaded: an expression
reading a name out of its scope, a `$vars` binding outside its `$for`
included, fails the load, `undeclared reference to '<name>'`.

```yaml
# okdp.yaml
vars:
  suffix: ${{ global.okdp.ingress.suffix }}
```

```yaml
# values.cel.yaml
app:
  ingress:
    hosts:
      - $for: h in values.hosts
        $vars:
          fqdn: ${{ h }}.${{ suffix }}    # h, and the vars bindings
        $each: {host: "${{ fqdn }}"}
    tls: ${{ fqdn }}                       # fails: undeclared reference to 'fqdn'
```

The host functions do not see bindings: `okdpFullname`, `okdpLabels`,
`okdpSelectorLabels`, `okdpGeneratedSecret` and `oidcClientSecret` read the
compile's release, service and OAuth client mode
([functions](04_functions.md#properties)).

## `$if`

```yaml
metrics:
  $if: ${{ values.metrics.enabled }}      # metrics of the values file: the chart is not rendered
  target: http://${{ release.name }}-app:8080/metrics
env:
  - $if: ${{ db.size() > 0 }}             # the item is left out
    name: DB_DRIVER
    value: ${{ db.driver }}
labels:
  tier: {$if: "${{ values.replicas > 1 }}", $value: ha}
```

The value, usually one expression, `${{ <condition> }}`, is evaluated as
any node, then tested as a Helm condition: null, `false`, `0`, `""`, `[]`
and `{}` are false, anything else is true. `$if: ${{ values.extraEnv }}`
holds when the map is not empty. A literal is taken as it is (`$if: false`,
`no`); text around an expression is text, always true (`x${{ c }}`).

On a map, `$if` guards everything else (`$for`, `$value`, `$merge`, the
keys): none is evaluated when the condition is not true, and the map is
left out, or is the value of its [`$else`](#else). A map left out is:

| As | Result |
|---|---|
| a map value | the key is left out |
| a list item | the item is left out |
| an `$each` | no item for this element |
| a `$value` of a `$for` map | the key, with a null value |
| a `vars` or `$vars` value | the binding is null |
| a section (`instance`, ..., the values file) | the section is empty; the dependencies the values file writes are left out ([rendered dependencies](02_service_definition.md#rendered-dependencies)) |

On a list item carrying `$for` or `$spread`, `$if` fails: put the
condition in the expression (`$for: "s in (oidcOn ?
[platform.oidcClientSecret] : [])"`, `$spread: ${{ cond ? list : [] }}`).

## `$value`

The map stands for the value of `$value`, which need not be a map:
`{$if: cond, $value: x}` is a conditional scalar or list. The map holds
`$if`, `$value` and [`$else`](#else) only: any other key, `$merge`
included, fails. In a list, a `$value` list is one item, not spliced.

## `$else`

```yaml
secretKeyRef:
  $if: ${{ platform.oidc.dcr.enabled }}
  $value: {name: "${{ okdpFullname('hub') }}", key: hub.config.CryptKeeper.keys}
  $else: {name: "${{ platform.oidcClientSecret }}", key: JUPYTERHUB_CRYPT_KEY}
```

```yaml
# output, release demo, dcr mode
secretKeyRef: {name: demo-hub, key: hub.config.CryptKeeper.keys}
# otherwise
secretKeyRef: {name: creds-demo-oauth2, key: JUPYTERHUB_CRYPT_KEY}
```

In a map with `$if` and `$value`, `$else` is the map's value when the
condition is not true: the map is then not left out. Its value is any node,
as `$value`'s: a scalar, a list (one item in a list, not spliced), a map,
directives included. Both branches are compiled when the document is
loaded; only the one taken is evaluated, the other may read what exists
only when it is taken.

`$else` needs `$if` and `$value` beside it: without `$if`, it is not read
(`{$value: 1, $else: 2}` is 1). On a map of keys, the keys would be the
first branch and `$else` the second, at one level: an `$else` there fails,
as any key out of its place: write the keys under `$value`. A map with
`$for` holds no `$else` either.

There is no `$elif`: an `$else` holding a map with `$if` chains a third
branch. An `$else` left out by its own `$if` leaves the map out.

```yaml
size:
  $if: ${{ values.replicas > 10.0 }}
  $value: large
  $else:
    $if: ${{ values.replicas > 1.0 }}
    $value: medium
    $else: small
```

A choice among several values is clearer as a CEL map:
`resources: ${{ sizes[values.size] }}`, `sizes` a `vars` binding. A choice
between two scalars computed in expressions is a `?:`, which evaluates the
branch it takes only, as `$if` does:
`pullPolicy: "${{ values.image.tag == 'latest' ? 'Always' : 'IfNotPresent' }}"`.
`$else` is for a branch written as YAML: a map, a list, several
expressions under one condition.

## `$for`

`$for: <name> in <expression>`: the text before the first ` in `, trimmed,
is the variable; the rest is a CEL expression, written without `${{ }}`
(`c in ${{ list }}` fails). Quote the scalar when the expression holds
YAML characters: `$for: "k in ['a', 'b']"`. The expression must return a
list, or null (no element). Errors:
`$for: "<spec>" is not "<name> in <expression>"`, `$for "<spec>": not a list`
(a map is not a list: [iterating a map](#iterating-a-map)).

For each element, in order, the scope is the enclosing one, the variable
bound to the element, then the [`$vars`](#vars) bindings. The variable and
the bindings shadow the context and `vars`, and exist only inside the
`$for` ([scope](#scope)).

In a map, `$key` and `$value` give one entry per element:

```yaml
dags:
  gitSync:
    $for: e in gitSync                    # a vars binding: [{k, v}, ...]
    $key: ${{ e.k }}
    $value: ${{ e.v }}
```

`$key` is converted to text ([`str`](04_functions.md#str-and-first)); a
later element with the same key wins. No element gives `{}`. The map holds
`$if`, `$for`, `$key`, `$value` and `$vars` only: any other key, `$merge`
included, fails; without `$key` or `$value`:
`$for: in a map, needs $key and $value`.

In a list item, `$each` gives the items of each element:

```yaml
env:
  - name: FIXED
    value: "1"
  - $for: "k in ['HTTPS_PROXY', 'HTTP_PROXY', 'NO_PROXY'].filter(p, p in platform.proxyEnv)"
    $each:                                # a list: spliced, two items per element
      - {name: "${{ k }}", value: "${{ str(platform.proxyEnv[k]) }}"}
      - {name: "${{ k.lowerAscii() }}", value: "${{ str(platform.proxyEnv[k]) }}"}
```

A list value of `$each` (a YAML list, or an expression returning a list) is
spliced: its items are added; any other value is one item; a value left out
by `$if` adds nothing. A list as one item is nested: `$each: [[a, b]]`.
The item holds `$for`, `$each` and `$vars` only: any other key, `$if`
included, fails; without `$each`: `$for: in a list item, needs $each`.

A `$for` over a list of zero or one element gives a group of items on one
condition, the element binding a value the group uses:
[conditional items](../02_writing_package.md#conditional-items-maps-from-lists).

### Iterating a map

`$for` takes a list. Over a map, iterate its keys, sorted: CEL iterates a
map (`m.map(k, k)`, `m.filter(...)`, `transformList`) in no fixed order,
neither the order written nor one order from a compile to the next. A list
built from a map without sorting changes the output for the same inputs.

```yaml
env:
  - $for: "k in values.extraEnv.map(k, k).sort()"
    $each: {name: "${{ k }}", value: "${{ str(values.extraEnv[k]) }}"}
# or in one expression, the items sorted by name:
env2:
  - $spread: "${{ values.extraEnv.transformList(k, v, {'name': k, 'value': str(v)}).sortBy(e, e.name) }}"
```

A map built from a map (`$key`, or `transformMap`) needs no sorting: a map
has no order.

### Positions

`lists.range(n)` gives the positions `0` to `n - 1`, CEL `int`s:

```yaml
peers:
  - $for: "i in lists.range(int(values.replicas))"    # a number of values is a double
    $each: ${{ release.name }}-${{ i }}.${{ release.name }}-headless
```

With a list, `$for: "i in lists.range(l.size())"` reads the element as
`l[i]`.

### Filtering and nesting

A condition on the elements goes in the expression, not in a `$if` of the
item: `$for: "c in values.catalogs.filter(c, c.enabled)"`. A `$for` in an
`$each` runs for every element of the outer one, whose variable it sees;
its items are spliced:

```yaml
grants:
  - $for: u in values.users
    $each:
      - $for: "db in ['raw', 'curated']"
        $each: {user: "${{ u }}", database: "${{ db }}"}   # every user, every database
```

## `$vars`

```yaml
pyspark:
  - $for: name in pysparkNames
    $vars:
      matches: ${{ sparkConnections.filter(c, c.?name.orValue('') == name) }}
      c: "${{ matches.size() > 0 ? matches[matches.size() - 1] : {} }}"
      prefix: ${{ name.replace('-', '_').upperAscii() }}
    $each:
      name: ${{ prefix }}
      value: ${{ str(c.?properties.orValue('')) }}
```

With `$for` only: a map of bindings, evaluated for each element, in order,
after the variable; each sees the enclosing scope, the variable and the
bindings before it. Values are nodes (directives included; left out by
`$if`, null). Names are taken as written, as in
[vars](02_service_definition.md#vars): of a name bound twice, the later
binding wins. A `$vars` without `$for` fails, and so does one that
is neither a map nor null: `$vars: must be a map of bindings, got <value>`.
An error is reported as `$vars <name>: <error>`.

## `$merge`

```yaml
ingress:
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: 64m
    $merge: ${{ platform.ingressAnnotations }}  # the platform's annotations win
spark-defaults:
  $merge: ${{ values['spark-defaults'] }}
```

In a map, after its other keys are evaluated, the map the expression
returns is merged over them, wherever `$merge` is written, first or last:
maps merge key by key at any depth; lists, scalars and nulls replace
(Helm's merge of `-f` files). A null merged over a key sets it to null,
which Helm then gives its meaning ([nulls](01_inputs_outputs.md#nulls)).
`$merge` takes any node, usually one expression; a literal YAML map merges
as it is. A value that is not a map (null included) merges nothing.

```yaml
# values.config: {a: {b: 2, l: [2]}}
config:
  a: {c: 1, l: [1]}
  $merge: ${{ values.config }}     # {a: {b: 2, c: 1, l: [2]}}
```

Defaults under a user's map: write the defaults as keys and `$merge` the
user's map, as above. A map has one `$merge` (a second `$merge` key
replaces the first, as any key given twice): two computed maps are not
merged in one place. A map with `$for` or `$value` holds no `$merge`:
compute that map in `vars`, and `$merge` it into a map of keys.

## `$spread`

A list item `{$spread: ${{ <list> }}}` is replaced by the items of the list
the expression returns, spliced in at its place, between the items written
before and after it:

```yaml
# values.extraArgs: [--verbose, --port=8080]
args:
  - serve
  - $spread: ${{ values.extraArgs }}
  - --config=/etc/app.yaml
```

```yaml
# output
args:
  - serve
  - --verbose
  - --port=8080
  - --config=/etc/app.yaml
```

Without `$spread`, the list is one item, or the whole value:

| Mapping | Output |
|---|---|
| `args: [serve, {$spread: "${{ values.extraArgs }}"}]` | `[serve, --verbose, --port=8080]`: the items, spliced |
| `args: [serve, "${{ values.extraArgs }}"]` | `[serve, [--verbose, --port=8080]]`: one item, a list |
| `args: ${{ values.extraArgs }}` | `[--verbose, --port=8080]`: the list replaces `serve` |
| `args: "${{ ['serve'] + values.extraArgs }}"` | `[serve, --verbose, --port=8080]`: the same as `$spread`, in one expression |

What the expression returns, and the items it adds:

| Value | Items added |
|---|---|
| `[a, b]` | `a`, `b` |
| `[]` | none |
| `[[a, b]]` | one, the list `[a, b]`: one level is spliced |
| null, a string, a number, a map | none, without error |

A value that is not a list adds nothing rather than failing: null is the
usual "nothing to add", but a string or a map is likely a mistake
(`$spread: ${{ values.extraEnv }}` of a map adds no item; spread its
entries: [iterating a map](#iterating-a-map)). `$spread` takes any node,
usually one expression; a literal YAML list is spliced as it is (rather
write its items in the list). The item holds `$spread` only: any other
key, `$if` included, fails; the condition goes in the expression:

```yaml
args:
  - serve
  - $spread: "${{ values.debug ? ['--log-level=debug', '--trace'] : [] }}"
# output, values.debug true:  [serve, --log-level=debug, --trace]
#         values.debug false: [serve]
```

A function returning several objects is spliced the same way.
[`okdpGeneratedSecret`](04_functions.md#okdpgeneratedsecret) returns a
`Password` per key, then the `ExternalSecret`, for the `objects` list; as
one item, the list would fail as an object without a `kind`:

```yaml
# release demo
objects:
  - apiVersion: v1
    kind: ConfigMap
    metadata: {name: "${{ okdpFullname('settings') }}"}
  - $spread: >-
      ${{ okdpGeneratedSecret({'name': okdpFullname('session'),
          'keys': [{'key': 'session-key'}, {'key': 'csrf-key'}]}) }}
```

```yaml
# output
objects:
  - {apiVersion: v1, kind: ConfigMap, metadata: {name: demo-settings}}
  - {apiVersion: generators.external-secrets.io/v1alpha1, kind: Password, metadata: {name: demo-session-session-key, ...}, ...}
  - {apiVersion: generators.external-secrets.io/v1alpha1, kind: Password, metadata: {name: demo-session-csrf-key, ...}, ...}
  - {apiVersion: external-secrets.io/v1, kind: ExternalSecret, metadata: {name: demo-session, ...}, ...}
```

The `$each` of a `$for` splices a list the same way, once per element
([`$for`](#for)).

## Cost and cancellation

Every expression the directives evaluate counts in the compile's CEL cost,
an expression in a `$for` once per element: once the total passes
`Inputs.CostLimit` (checked after each expression), the compile fails with
`the expressions of the compile exceed its CEL cost limit (<n>)`
([usage](../01_usage.md)). The context of `Compile` is checked
before each expression and every 100 iterations of a comprehension. The
language has no recursion and no include, and every loop (`$for`, CEL's
macros) runs over a list or a map it is given: the work is bounded by the
document and the size of the data it iterates.

## Errors

The directives of a document are checked when it is loaded, in every
branch; the first mistake fails:

| Mistake | Error |
|---|---|
| an unknown directive (`$iff`, `$Each`, `$elif`), a key out of its place, `$else` without `$value` | `<where><key>: not allowed here (allowed: <directives>)` |
| `$for` without its keys | `<where>$for: in a map, needs $key and $value`, `<where>$for: in a list item, needs $each` |
| `$for` not `<name> in <expression>` | `<where>$for: "<spec>" is not "<name> in <expression>"` |
| a `$vars` that is not a map | `<where>$vars: must be a map of bindings, got <value>` |
| an expression that does not compile, or reads a name out of scope | `<where>${{ <expression> }}: ERROR: <input>:<line>:<column>: <message>` |

Evaluation fails on the first error: an expression's
(`<where>${{ <expression> }}: <cause>`), or a `$for` over a value that is
not a list, `<where>$for "<spec>": not a list`. `<where>` is the
document's file, then the path to the map or item holding the directive,
`values.cel.yaml: app: env[2]: `, where a list position counts the
items as written: an item carrying `$for` or `$spread` is one position,
whatever it gives
([compile errors](02_service_definition.md#compile-errors)).
