# okdp-compiler documentation

| Document | For | Content |
|---|---|---|
| [Usage](01_usage.md) | platform engineers, integrators | the Go library, the tree a compile reads, how to give it a rule set, the `okdp-compile` command line, troubleshooting |
| [Writing a package](02_writing_package.md) | package authors | a package, its upstream charts and its service definition, step by step; recipes and pitfalls from OKDP's packages; a checklist |
| [Service definition](specs/02_service_definition.md) | package authors (reference) | the package files: `Chart.yaml` (the package metadata, its upstream charts), `okdp.yaml` (requirements, OKDP's defaults, upstream values, the mapping's sections) and `values.cel.yaml` or `values.yaml` (the values of each chart); sections, expressions, the CEL context, errors |
| [Control flow](specs/03_control_flow.md) | package authors (reference) | the directives `$if`, `$value`, `$else`, `$for`, `$vars`, `$merge`, `$spread`: places, scope, evaluation, Helm equivalents, errors |
| [Functions](specs/04_functions.md) | package authors (reference) | what an expression calls: CEL's libraries at their pinned versions, the host functions (`str`, `toYaml`, `okdpFullname`, `okdpGeneratedSecret`...), a rule set's functions, Helm and sprig equivalents |
| [Inputs and outputs](specs/01_inputs_outputs.md) | everyone (reference) | the package files, the chart cache of a host's checks, parameters, connections, contracts, platform and instance values, the merge precedence, the outputs |
| [The rule set](specs/05_rules.md) | platform engineers, package authors, contributors (reference) | the compiler's mandatory rule set, which every compile applies: its tables, checks, schemas, functions and documents, where each applies, how a set added to it extends it (schemas, scopes, tables, hooks, sections), what a platform may add, how to change a rule |
| [Architecture](00_architecture.md) | contributors | the compile pipeline, the source map, the design decisions, the tests, the limitations |
| [OKDP's own objects](03_okdp_objects.md) | contributors, platform engineers | how the credentials Secrets and the `objects` section reach the cluster, through the `okdp-objects` chart, and why so; other vehicles: pros and cons |
| [Threat model](04_threat_model.md) | platform engineers, reviewers, contributors | who writes what (the platform, a project, an instance, a catalog), what each may reach, what the compiler refuses of each and the code and fixture that prove it, what it leaves to a host; the engines, the secret store and the runner; what cannot be enforced |
| [Ideas](IDEAS.md) | contributors, platform engineers | what the compiler could do in the future, not decided |

The hosts a project serves, the OAuth clients, the hardened pod and the
policy on upstream values are the security rule set's, documented with
it: [okdp-compiler-security](https://github.com/OKDP/okdp-compiler-security/blob/main/docs/README.md).
