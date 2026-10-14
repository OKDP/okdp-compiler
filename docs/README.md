# okdp-compiler documentation

| Document | For | Content |
|---|---|---|
| [Usage](01_usage.md) | platform engineers, integrators | install, the `okdp-compile` command line, the Go library, CI and GitOps integration, troubleshooting |
| [Writing a package](02_writing_package.md) | package authors | a package, its upstream charts and its service definition, step by step; recipes and pitfalls from OKDP's packages; a checklist |
| [Service definition](specs/02_service_definition.md) | package authors (reference) | the package files: `Chart.yaml` (the package metadata, its upstream charts), `okdp.yaml` (requirements, OKDP's defaults, upstream values, the mapping's sections) and `values.cel.yaml` or `values.yaml` (the values of each chart); sections, expressions, the CEL context, errors |
| [Control flow](specs/03_control_flow.md) | package authors (reference) | the directives `$if`, `$value`, `$else`, `$for`, `$vars`, `$merge`, `$spread`: places, scope, evaluation, Helm equivalents, errors |
| [Functions](specs/04_functions.md) | package authors (reference) | what an expression calls: CEL's libraries at their pinned versions, the host functions (`str`, `toYaml`, `okdpFullname`, `okdpGeneratedSecret`...), Helm and sprig equivalents |
| [Inputs and outputs](specs/01_inputs_outputs.md) | everyone (reference) | the package files, the chart cache, parameters, connections, contracts, platform and instance values, the merge precedence, the outputs |
| [Architecture](00_architecture.md) | contributors | the compile pipeline, the source map, the design decisions, the tests, the limitations |
| [OKDP's own objects](03_okdp_objects.md) | contributors, platform engineers | why the descriptor, the credentials Secrets and the `objects` section reach the cluster through the okdp-objects chart, what reads the descriptor; removing either: pros and cons |
