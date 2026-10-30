# Contributing

Issues and pull requests are welcome.

## Requirements

- Go 1.27 or later (`go 1.27.0` in `go.mod`). `go test ./...` runs
  offline: nothing else is needed to build, test and lint.
- golangci-lint is pinned in a separate modfile, `tools/go.mod`, and run
  with `go tool -modfile=tools/go.mod golangci-lint`: nothing to install.
- Helm only to regenerate the [Helm fixtures](#helm-fixtures), with python3
  and PyYAML and the deployers' Helm binaries.

## Make targets

| Target | Does |
|---|---|
| `make build` | `bin/okdp-compile`, its `--version` from `git describe` |
| `make test` | `go test ./...` |
| `make race` | the tests with the race detector |
| `make cover` | the tests with a coverage profile |
| `make lint` | golangci-lint, at the version of `tools/go.mod` |
| `make fmt` | formats the sources (gofmt) |
| `make tidy` | `go mod tidy`, for `go.mod` and `tools/go.mod` |
| `make example` | rewrites the outputs of `examples/` from the current compiler |
| `make fixtures` | regenerates `testdata/helm` with `testdata/helm/generate.sh` (`UMBRELLA_HELMS`) |
| `make fuzz` | each fuzz test in turn, `FUZZTIME` (30s) each ([fuzz tests](docs/00_architecture.md#fuzz-tests)) |
| `make check` | what CI runs on a pull request: the format check, `go vet`, lint, `go mod tidy -diff`, the race tests |
| `make help` | lists the targets |

Run `make check` before pushing. CI (`.github/workflows/ci.yml`) runs the
same checks on every pull request, `.github/workflows/helm-fixtures.yml`
regenerates the Helm fixtures and fails on a diff.

## Tests and test data

- A source file's tests are in its `_test.go`, in memory (`fstest.MapFS`)
  where they can be; what the compiler copies from Helm is tested against
  Helm (`helm_test.go`).
- Test data lives at the repository root, in `testdata/`, never under
  `internal/`. Keep a fixture small, the least input that shows the
  behaviour, and add a case only for what no other case covers.
- `testdata/<case>.yaml` (instance values) and
  `testdata/context/<level>/<case>.yaml` (context values of that level, 0
  the platform's) must fail on the
  example: the first line is `# error: <message>`, the message the compile
  must report (`TestExampleErrors`,
  [failure cases](docs/00_architecture.md#failure-cases)).
- The outputs of each example of `examples/`, a repository tree
  (`<example>/output/projects/<project>/instances/<instance>/`), are
  compared byte for byte with a compile of the instance of its `input/db/`
  (`TestExample`).
  A change that changes the output runs `make example` and commits the diff:
  it is the change's effect, reviewed as such.

## Helm fixtures

`testdata/helm` holds what `helm template` computes on the same inputs:
values coalescing, schema reports, the functions copied from Helm, the
umbrella as each deployer's Helm renders it. The fixtures are committed, so
that the tests need no Helm. Regenerate them when a case is added or
changed, or when Flux or Argo CD embeds another Helm version:

```bash
UMBRELLA_HELMS="/path/to/helm-4.x /path/to/helm-3.19" make fixtures
```

- `UMBRELLA_HELMS`: the Helm binaries the deployers embed, separated by
  spaces; how to download and check them:
  [Helm fixtures](docs/00_architecture.md#helm-fixtures).
- Helm 4 on the `PATH`, python3 with PyYAML.

`testdata/helm/SOURCE` records what the fixtures were generated from (the
Helm versions): commit it with them. Review the diff of a regeneration as a
change of Helm's behaviour; a new
difference between Helm versions goes in `helmFinalKnown`
(`internal/chart/helm_test.go`) for the values cases, or `umbrellaRenders`
(`internal/target/helm/helm_test.go`) for the umbrella, and in the spec
([equivalence with Helm](docs/specs/01_inputs_outputs.md#equivalence-with-helm),
[umbrella chart](docs/specs/01_inputs_outputs.md#umbrella-chart)).

## Documentation

`docs/` is the reference, updated in the same commit as the code: a
behaviour, a flag, an input, an output or an error message that changes
changes its doc. [docs/README.md](docs/README.md) says which doc is for
what:

- the package files (`Chart.yaml`, `okdp.yaml`, `values.cel.yaml`) and the
  mapping language:
  [docs/specs/02_service_definition.md](docs/specs/02_service_definition.md),
  its directives in
  [docs/specs/03_control_flow.md](docs/specs/03_control_flow.md), its
  functions in [docs/specs/04_functions.md](docs/specs/04_functions.md);
- the inputs, the outputs, the checks and their messages:
  [docs/specs/01_inputs_outputs.md](docs/specs/01_inputs_outputs.md);
- the command line, the library, CI and GitOps, the troubleshooting table
  (each error, its cause, its fix): [docs/01_usage.md](docs/01_usage.md);
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
command line, the package files, the mapping language or the outputs: a minor
version before 1.0, a major one after.

## CEL libraries

The mapping language enables CEL extensions at pinned versions
(`Session.env` in `internal/compile/cel.go`), so that a cel-go update
does not change what a mapping may call. A cel-go update keeps them as they
are; a version is raised on purpose, only together with the "CEL libraries"
table of
[docs/specs/04_functions.md](docs/specs/04_functions.md#cel-libraries)
(the version, the functions it adds, the cel-go version) and with tests of
the functions it adds.
