# okdp-compiler

Compiles an OKDP service instance into the values of its upstream charts
with a YAML + CEL mapping.

Two ways to use it:

- **as a Go library** (`github.com/okdp/okdp-compiler/compiler`): no binary to call,
  everything is read through `fs.FS` and in-memory values;
- **as a binary** (`okdp-compile`), for CI and/or scripts.

## Library

```go
import "github.com/okdp/okdp-compiler/compiler"

//TODO

def, err := compiler.LoadDefinition(serviceFS)
res, err := compiler.Compile(def, compiler.Inputs{
	//TODO
})
// res.Values:   chart -> final values of the upstream chart
// res.Computed: chart -> what the mapping computed, before the merges
// res.Objects:  OKDP's own objects (the descriptor ConfigMap)
// res.Dependencies: instances the connection references point to (Flux dependsOn, Argo sync waves)
```

## Binary

```bash
make build    # bin/okdp-compile
#TODO
```

## Layout

| Path | Description |
|---|---|
| `compiler/compile.go` | public API |
| `cmd/okdp-compile/main.go` | Binary entrypoint |
| `example/` | a complete example service, `webapp`: `service.yaml` and `mapping.cel.yaml` (the service definition), `webapp/` (its wrapper chart and vendored chart defaults), `contracts/`, `platform-values.yaml`, `values/` (instances) and `compiled/` (their compiled output, checked by `go test`; `go test ./compiler -update` rewrites it) |
| `testdata/` | instance values that must fail on the example, each with its expected error on the first line (`# error: ...`): malformed connections (wrong contract, unknown or secret field, no secretRef), mapping error, protected upstream path, missing platform value |
