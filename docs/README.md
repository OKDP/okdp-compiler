# okdp-compiler documentation

| Document | For | Content |
|---|---|---|
| [Usage](01_usage.md) | platform engineers, integrators | install, the `okdp-compile` command line, the Go library, CI and GitOps integration, troubleshooting |
| [Writing a package](02_writing_package.md) | package authors | a package, its upstream charts and its service definition, step by step; recipes and pitfalls from OKDP's packages; a checklist |
| [Service definition](specs/02_service_definition.md) | package authors (reference) | `service.yaml` (the package metadata, the charts and their sources) and `values.cel.yaml`: sections, expressions, directives, the CEL context, the functions, errors |
| [Inputs and outputs](specs/01_inputs_outputs.md) | everyone (reference) | the package files, the chart cache, parameters, connections, contracts, platform and instance values, the merge precedence, the outputs |
| [Architecture](00_architecture.md) | contributors | the compile pipeline, the source map, the design decisions, the tests, the limitations |
| [OKDP's own objects](03_okdp_objects.md) | contributors, platform engineers | why the descriptor, the credentials Secrets and the `objects` section reach the cluster through the okdp-objects chart, what reads the descriptor; removing either: pros and cons |
