# Contributing

Issues and pull requests are welcome.

## Requirements

- Go 1.27.3 or later (`go 1.27.3` in `go.mod`). `go test ./...` runs
  offline: nothing else is needed to build, test and lint.
- golangci-lint is pinned in a separate modfile, `tools/go.mod`, and run
  with `go tool -modfile=tools/go.mod golangci-lint`: nothing to install.

## Make targets

| Target | Does |
|---|---|
| `make test` | `go test ./...` |
| `make race` | the tests with the race detector |
| `make cover` | the tests with a coverage profile |
| `make lint` | golangci-lint, at the version of `tools/go.mod` |
| `make fmt` | formats the sources (gofmt) |
| `make tidy` | `go mod tidy`, for `go.mod` and `tools/go.mod` |
| `make fuzz` | each fuzz test in turn, `FUZZTIME` (30s) each ([fuzz tests](docs/00_architecture.md#fuzz-tests)) |
| `make check` | what CI runs on a pull request: the format check, `go vet`, lint, `go mod tidy -diff`, the race tests |
| `make help` | lists the targets |

Run `make check` before pushing. CI (`.github/workflows/ci.yml`) runs the
same checks on every pull request.

## Tests and test data

- A source file's tests are in its `_test.go`, in memory (`fstest.MapFS`)
  where they can be.
- Test data lives at the repository root, in `testdata/`, never under
  `internal/`. Keep a fixture small, the least input that shows the
  behaviour, and add a case only for what no other case covers.
- `testdata/rules` is a copy of okdp-gitops' standard rule set
  (`rules/v1`), which this module cannot import: its tests compile with it
  (`internal/testrules`). A rule changes in both, and okdp-gitops'
  `TestCompilerCopy` fails while they differ. A compile with no rule set
  is tested too (`TestCompileWithoutRules`).
- A boundary of the [threat model](docs/04_threat_model.md), what an
  author may not do, has one case that must fail: a unit test here, or a
  fixture of okdp-gitops, where a whole tree compiles. A new check gets
  its case, its row in the threat model and in the [security
  boundaries](docs/00_architecture.md#security-boundaries), named in
  both.
- What `Write` gives of each instance of `examples/` is compared byte for
  byte with its `output/` (`TestExample`). A change that changes the
  output runs `go test . -run TestExample -update` and commits the diff:
  it is the change's effect, reviewed as such.

## Helm fixtures

What the compiler copies from Helm is compared with what `helm template`
computes in okdp-gitops, whose `testdata/helm` holds the fixtures, with
each deployer's Helm. A change of the merge (`internal/merge`, `chart/`)
is checked there too.

## Documentation

`docs/` is the reference, updated in the same commit as the code: a
behaviour, an input, an output or an error message that changes changes
its doc. [docs/README.md](docs/README.md) says which doc is for
what:

- the package files (`Chart.yaml`, `okdp.yaml`, `values.cel.yaml`) and the
  mapping language:
  [docs/specs/02_service_definition.md](docs/specs/02_service_definition.md),
  its directives in
  [docs/specs/03_control_flow.md](docs/specs/03_control_flow.md), its
  functions in [docs/specs/04_functions.md](docs/specs/04_functions.md);
- the inputs, the outputs, the checks and their messages:
  [docs/specs/01_inputs_outputs.md](docs/specs/01_inputs_outputs.md);
- the library, the troubleshooting table (each error, its cause, its
  fix): [docs/01_usage.md](docs/01_usage.md);
- the pipeline, the source map (a new file or a moved function), the
  design decisions, the tests:
  [docs/00_architecture.md](docs/00_architecture.md).

An error message is quoted in tests, in `testdata/` first lines and in the
docs: grep for it when you change it.

## Commits and releases

Commits follow [Conventional Commits](https://www.conventionalcommits.org):
a subject line, `<type>: <what changes>`, lowercase, without a body. The
subject is the line of the changelog: a fix to a commit not yet merged is
folded into it, not a commit of its own. release-please
(`.github/workflows/release-please.yml`) reads them on `main`: it keeps a
release pull request with the next version and `CHANGELOG.md`, and merging
it tags `vX.Y.Z` and publishes the GitHub release. Do not edit
`CHANGELOG.md` or `.release-please-manifest.json` by hand.

| Type | For | Release |
|---|---|---|
| `feat:` | a new behaviour, a new input, output or function | a minor version |
| `fix:` | a correction, a clearer error | a patch |
| `deps:` | a dependency or Go update | a patch |
| `docs:`, `test:`, `ci:`, `chore:`, `refactor:` | no change of behaviour | none by itself |

`!` after the type (`feat!:`) marks a breaking change of the library, the
package files, the mapping language or the outputs: a minor version before
1.0, a major one after.

## CEL libraries

The mapping language enables CEL extensions at pinned versions
(`Session.compile` in github.com/okdp/cel2yaml, `session.go`), so that a
cel-go update does not change what a mapping may call. A cel-go update
keeps them as they are; a version is raised on purpose, only together
with the "CEL libraries" table of
[docs/specs/04_functions.md](docs/specs/04_functions.md#cel-libraries)
(the version, the functions it adds, the cel-go version) and with tests of
the functions it adds.
