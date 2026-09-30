# okdp-compiler documentation

| Document | For | Content |
|---|---|---|
| [Usage](01_usage.md) | platform engineers, integrators | install, the `okdp-compile` command line, the Go library, CI and GitOps integration, troubleshooting |
| [Writing a package](02_writing_package.md) | package authors | a wrapper chart and its service definition, step by step; recipes and pitfalls from OKDP's packages; a checklist |
| [Service definition](specs/02_service_definition.md) | package authors (reference) | `service.yaml` and `mapping.cel.yaml`: sections, expressions, directives, the CEL context, the functions, errors |
| [Inputs and outputs](specs/01_inputs_outputs.md) | everyone (reference) | the wrapper chart, parameters, connections, contracts, platform and instance values, the merge precedence, the outputs, `Compare` |
| [Architecture](00_architecture.md) | contributors | the compile pipeline, the source map, the design decisions, the tests, the limitations |

The two specification files are the reference: the other documents link to
them rather than repeat them.
