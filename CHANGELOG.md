# Changelog

## [0.5.0](https://github.com/OKDP/okdp-compiler/compare/v0.4.0...v0.5.0) (2026-10-26)


### Features

* --repository and --target ([c813881](https://github.com/OKDP/okdp-compiler/commit/c8138810db605ed8c10313466766af49629b95aa))
* CompileAll, the instances of a namespace compiled together ([dae29fa](https://github.com/OKDP/okdp-compiler/commit/dae29fa8feaab3708e14ee0a0a964ff705bcade1))
* the deployment graph of compiled instances ([6c084a4](https://github.com/OKDP/okdp-compiler/commit/6c084a4c72de3a3f6e81dae08c3a51769963e4fd))
* the helm, flux and argocd targets ([496648e](https://github.com/OKDP/okdp-compiler/commit/496648e73d657cd1b8c8947cdb2e7cb15ce25127))

## [0.4.0](https://github.com/OKDP/okdp-compiler/compare/v0.3.0...v0.4.0) (2026-10-16)


### ⚠ BREAKING CHANGES

* okdp.info.yaml, what an instance publishes
* no named output of a contract with a naming convention
* no okdp.dependencies.yaml, the dependencies printed on stderr
* chart defaults out of the compiled values, the chart cache for checks only
* no empty okdp.instance.yaml, okdp.dependencies.yaml or keys
* okdp.instance.yaml replaces the descriptor ConfigMap
* OKDP's objects in the umbrella, no okdp chart
* every dependency is rendered unless the values leave it out
* Chart.yaml, okdp.yaml and values.cel.yaml replace service.yaml
* an instance's connections are a context layer of its level
* a missing config.schema.json is an empty schema
* context levels replace --platform and --project
* $else beside $if and $value
* --project values that add to the platform's, examples/03_full
* name the upstream chart in each <key>.values.yaml
* rename charts.<key>.base to defaults, <key>.defaults.yaml by convention
* mark generated files with a DO NOT EDIT line
* replace okdp.charts.yaml with install.sh (--mkinstall)
* write okdp.dependencies.yaml only with --mkdag
* let a require entry of service.yaml take a condition, $if, evaluated on the mapping's context
* take global from the platform's values layers only (--platform, Inputs.Platform)
* customizable indent
* **compiler:** add the okdp chart to every compile and take the platform's charts from global.okdp.charts
* **compiler:** name each upstream chart by its source and read it from a chart cache
* **compiler:** read the package's metadata from service.yaml and its parameters from the package

### Features

* --contracts only when a contract is read ([0fb70e2](https://github.com/OKDP/okdp-compiler/commit/0fb70e2096b832604366e0c408e453c77fdfa3d3))
* --mkinstall with --umbrella, an install.sh for the umbrella chart ([4577ee5](https://github.com/OKDP/okdp-compiler/commit/4577ee5833cc7848be26b08a3c37c416dea38cbb))
* --project values that add to the platform's, examples/03_full ([91b4f11](https://github.com/OKDP/okdp-compiler/commit/91b4f11b4c0ae208da3476d60fa60896d012eeff))
* $else beside $if and $value ([f91d187](https://github.com/OKDP/okdp-compiler/commit/f91d187d27331444e2e08090661068dbebb3ad5c))
* a missing config.schema.json is an empty schema ([6493e89](https://github.com/OKDP/okdp-compiler/commit/6493e898b8dd314c74bd952fbf90c8fed246289f))
* a reference names an output another instance of the namespace publishes ([bb65ba2](https://github.com/OKDP/okdp-compiler/commit/bb65ba2d6f3e8ed7211da2084c4a2909841075ac))
* an instance's connections are a context layer of its level ([9406cce](https://github.com/OKDP/okdp-compiler/commit/9406cceea0bb3ff0dc669c12e86b9d50034999be))
* chart defaults out of the compiled values, the chart cache for checks only ([4bda6a1](https://github.com/OKDP/okdp-compiler/commit/4bda6a1fc109221218c4cea3c20ed21ca6c6408c))
* Chart.yaml, okdp.yaml and values.cel.yaml replace service.yaml ([83148f0](https://github.com/OKDP/okdp-compiler/commit/83148f0bb59927b3e3a4ffbcd400c9edeb36a1a7))
* check external connections and provides against their contract's schema, apply contract defaults until stable, report an unreadable contract ([46eae24](https://github.com/OKDP/okdp-compiler/commit/46eae24415f9a3c4c6ba6e4a292f23fef7144bf9))
* check the types of provides secrets, okdpGeneratedSecret arguments and instance, report every missing contract field ([37671fb](https://github.com/OKDP/okdp-compiler/commit/37671fb51726128ddd2ba044c6e4948482b07345))
* check the umbrella with the Helm versions Flux and Argo CD embed, Helm 3.19 differences documented ([9108aa5](https://github.com/OKDP/okdp-compiler/commit/9108aa508a900d7d9237f7214cc939a989e68fcb))
* **cmd:** add the okdp-compile CLI ([3fc5444](https://github.com/OKDP/okdp-compiler/commit/3fc54446b383ee608deba1bcdbd4634d9c77692f))
* compile an empty mapping as one without sections ([26b3fbe](https://github.com/OKDP/okdp-compiler/commit/26b3fbed97be88d0335f1ea5fc1a648740347969))
* **compiler:** accept bracketed literal keys and globs in protect and append paths ([4bccd14](https://github.com/OKDP/okdp-compiler/commit/4bccd1478a84c90ae68da04df516525164bfc4f8))
* **compiler:** add the oidc-dcr chart of each OAuth client without a service.yaml entry ([b55364e](https://github.com/OKDP/okdp-compiler/commit/b55364e7aaa9ad67080daae1ee2a0ceb6d5f1f8a))
* **compiler:** add the okdp chart to every compile and take the platform's charts from global.okdp.charts ([f8d4c63](https://github.com/OKDP/okdp-compiler/commit/f8d4c6305624f16daf2d87ca6e3f1ba7d5d9d91e))
* **compiler:** add the sha256, toJson, toPrettyJson, toYaml, quote and okdpFullname CEL functions ([c4777a4](https://github.com/OKDP/okdp-compiler/commit/c4777a4b3ca779bd2bbf70dae19d1bed6dde2d92))
* **compiler:** add the YAML + CEL mapping engine ([3e4599b](https://github.com/OKDP/okdp-compiler/commit/3e4599b2f1990ad438159009d36ff077ce18f631))
* **compiler:** compare a compile with a wrapper chart render ([6c09201](https://github.com/OKDP/okdp-compiler/commit/6c092012941e6cfd681e68acae4183e4248747d6))
* **compiler:** compile a service instance into upstream chart values ([346b213](https://github.com/OKDP/okdp-compiler/commit/346b213ddaa1fa21e533b4839fcd5aa20b9dc46b))
* **compiler:** compile the OAuth clients of an oidcClients section from an oidc-dcr template ([ac45eca](https://github.com/OKDP/okdp-compiler/commit/ac45eca23ce5fe202444d68e9449edb883582640))
* **compiler:** give the mapping its chart metadata and labels ([24dc815](https://github.com/OKDP/okdp-compiler/commit/24dc815b1dfcb9940e84061769c74909213d4fee))
* **compiler:** let a chart of service.yaml name the vendored chart it compiles from ([4d49c9c](https://github.com/OKDP/okdp-compiler/commit/4d49c9c7bd0b414104cc85c4a8732a71c06aebf2))
* **compiler:** let a chart receive the upstream values of another chart with upstream.from ([5a75046](https://github.com/OKDP/okdp-compiler/commit/5a75046894d7491e94fe97f209c4c025a530df9d))
* **compiler:** name each upstream chart by its source and read it from a chart cache ([a17ca37](https://github.com/OKDP/okdp-compiler/commit/a17ca37f1de02d5d41a3c4b1119c9710c434f55b))
* **compiler:** read and merge values as Helm does ([18d64f5](https://github.com/OKDP/okdp-compiler/commit/18d64f5747140c295f3801f0341b22871c3bb941))
* **compiler:** read and merge values exactly as Helm does ([b4ef002](https://github.com/OKDP/okdp-compiler/commit/b4ef00288492c74c7ca9c399fbde9427a5baa16f))
* **compiler:** read the instance upstream values from files ([d25ed1d](https://github.com/OKDP/okdp-compiler/commit/d25ed1d10ddade5ef3b7f4a7358e1de22e6952ca))
* **compiler:** read the oidc-dcr Job settings from the platform values ([33c499d](https://github.com/OKDP/okdp-compiler/commit/33c499d31468e1dd610fa00e132125573a7bd200))
* **compiler:** read the package's metadata from service.yaml and its parameters from the package ([b8fc940](https://github.com/OKDP/okdp-compiler/commit/b8fc940f1557893c1a224d6ea2c58446fd338447))
* **compiler:** refuse unknown keys under global.okdp.charts ([b2bf8d0](https://github.com/OKDP/okdp-compiler/commit/b2bf8d0befa3bae2833994a5659126c53f4c4255))
* **compiler:** rename the fixed values of a chart to base ([f10317e](https://github.com/OKDP/okdp-compiler/commit/f10317ec40f31467937806f9a63aff857bfeda08))
* **compiler:** render OKDP's own objects and feed them to the okdp chart ([606faa0](https://github.com/OKDP/okdp-compiler/commit/606faa0f9759d90f1a07167e9fa99359fcd16b6f))
* **compiler:** report the instances an instance depends on ([c63136e](https://github.com/OKDP/okdp-compiler/commit/c63136ec29f10abb7c1009495ad42ca9f347e4dc))
* **compiler:** set the oidc-dcr 0.5.0 values in okdpDcr ([b7d9ece](https://github.com/OKDP/okdp-compiler/commit/b7d9ece13065aa4781f876ea9e3a46b63558ed76))
* **compiler:** show the mapping the upstream values files merged with the inline values ([c34c33a](https://github.com/OKDP/okdp-compiler/commit/c34c33a97dc2ef95f8dbb6d230ab53d9dd2bba7d))
* **compiler:** validate the parameters and the compiled values against their schemas ([a961dc9](https://github.com/OKDP/okdp-compiler/commit/a961dc967f4fba546e34477210aa26cb99fc524b))
* context levels replace --platform and --project ([3c6b7b0](https://github.com/OKDP/okdp-compiler/commit/3c6b7b0e126fc18ebc7c08fe8b12898b1eed7d60))
* convert CEL values to data recursively and refuse what values cannot hold ([8ad89bf](https://github.com/OKDP/okdp-compiler/commit/8ad89bffb9bdc330b5923670a2d3391f727cd5f6))
* customizable indent ([f2dce7c](https://github.com/OKDP/okdp-compiler/commit/f2dce7c9f9ce339775453cafbb6858ed3c5bf2ed))
* decode service.yaml strictly and require chart keys as DNS labels ([f9e1a15](https://github.com/OKDP/okdp-compiler/commit/f9e1a15b963d83060c1c0984f7d88652aeb27847))
* do not require the secret fields of a contract ([f9191dc](https://github.com/OKDP/okdp-compiler/commit/f9191dcfaea27cde3bef2a9b38edb8004ed95ef5))
* emit the instance as an umbrella chart ([d414017](https://github.com/OKDP/okdp-compiler/commit/d414017262c102524a416240b41011792247dfe3))
* every dependency is rendered unless the values leave it out ([ad6d6ef](https://github.com/OKDP/okdp-compiler/commit/ad6d6eff622d2a4adf3228133378f1f63a867536))
* **example:** add basic target configure example ([7f29cbc](https://github.com/OKDP/okdp-compiler/commit/7f29cbc67295828b679e8d9b1796cf84c3356a96))
* **example:** add the catalog annotations to the webapp chart ([75800c6](https://github.com/OKDP/okdp-compiler/commit/75800c6a9451b2fd51b236dbb8837f09ae231d52))
* **example:** add the webapp chart and contracts, test the example ([9fd35e5](https://github.com/OKDP/okdp-compiler/commit/9fd35e5eff43e068422c5f819da27ba774aa765d))
* find the }} of an expression past raw and triple-quoted strings and comments, refuse mapping keys Helm reads at random, read the mapping's lines and YAML 1.1 scalars as Helm, fuzz the scanner, paths, values and the mapping reader ([e09a264](https://github.com/OKDP/okdp-compiler/commit/e09a264039986050c80970d6083066da9b95baf9))
* key the chart cache by repository, &lt;repository&gt;/&lt;chart&gt;-&lt;version&gt;, the flat entry kept unless two repositories share it ([c7c1a43](https://github.com/OKDP/okdp-compiler/commit/c7c1a43f1aeb4319c448257e7dca2c213f6ffaf8))
* let a require entry of service.yaml take a condition, $if, evaluated on the mapping's context ([cf6095b](https://github.com/OKDP/okdp-compiler/commit/cf6095b1c4c7a2ef5e56902ae2b36dbbe70057b9))
* make DcrSource a function, add CompileContext with a CEL cost limit ([3e8d2e1](https://github.com/OKDP/okdp-compiler/commit/3e8d2e19232fd4245f4da1bd3abce85e4292ff7d))
* mark generated files with a DO NOT EDIT line ([c735d84](https://github.com/OKDP/okdp-compiler/commit/c735d844e65ff0243d97fd94f391e202af88d177))
* name the platform values input in the errors of a missing platform value, add CONTRIBUTING.md and a license section ([bfe1717](https://github.com/OKDP/okdp-compiler/commit/bfe171774402c05276ffdab15cb4a51779c5db4d))
* name the upstream chart in each &lt;key&gt;.values.yaml ([b4c2996](https://github.com/OKDP/okdp-compiler/commit/b4c299672388af2ae20cf26c0a329486c3f67fcb))
* no empty okdp.instance.yaml, okdp.dependencies.yaml or keys ([8a2f8f3](https://github.com/OKDP/okdp-compiler/commit/8a2f8f38d4781491e4e12aef932aa08911f3b159))
* no named output of a contract with a naming convention ([1cab6c4](https://github.com/OKDP/okdp-compiler/commit/1cab6c49ac187b918ed9f5d856930adff2948f54))
* no okdp.dependencies.yaml, the dependencies printed on stderr ([06954e6](https://github.com/OKDP/okdp-compiler/commit/06954e613761829786a3d1bb62f046a4d88d004b))
* normalize Go-typed values layers as Helm values, refuse what values cannot hold ([3f038a1](https://github.com/OKDP/okdp-compiler/commit/3f038a1e151857c248aa1b9e63c5f973eac2affb))
* okdp.info.yaml, what an instance publishes ([ad6a137](https://github.com/OKDP/okdp-compiler/commit/ad6a137aa1cd25b65f4b3a9d7f91e2007efd0403))
* okdp.instance.yaml replaces the descriptor ConfigMap ([731eb36](https://github.com/OKDP/okdp-compiler/commit/731eb3689b429e3b4eab25ffaf2455af909835d3))
* OKDP's objects in the umbrella, no okdp chart ([a802f92](https://github.com/OKDP/okdp-compiler/commit/a802f92b6b99795420e32e4b6906966fcc641222))
* read chart archives as Helm does, backslash names, paths out of the chart refused and a 100 MiB cap ([047a10c](https://github.com/OKDP/okdp-compiler/commit/047a10c4438b66c3f1a721a39a0b09fddf6b53b5))
* refuse a YAML 1.1 boolean as upstream in service.yaml instead of taking it as a file name ([3c6f09c](https://github.com/OKDP/okdp-compiler/commit/3c6f09c95d02aa9b45cafaf69c3b57ee23fa6b06))
* refuse a YAML alias inside its own anchor and excessive aliasing ([291fa3b](https://github.com/OKDP/okdp-compiler/commit/291fa3b89c034907456c9c3113b4d2be32c6320e))
* refuse mistakes in the mapping language and resolve YAML merge keys as Helm ([0236fa5](https://github.com/OKDP/okdp-compiler/commit/0236fa5b0271dc1a9c34bc43349d1ea8b8b3a2fc))
* refuse positional arguments, check the input directories, write every output or none, keep the output directories to their files, require --release and --namespace ([33a6e4c](https://github.com/OKDP/okdp-compiler/commit/33a6e4ccf3f742ce1662036c80b8cf17919f8056))
* refuse sections and platform settings of the wrong shape ([667cba1](https://github.com/OKDP/okdp-compiler/commit/667cba1272b61576641d0d5653e096877638a72b))
* refuse template code in instance parameters and connections, and a connection reference that is not a string or not a release name ([004c863](https://github.com/OKDP/okdp-compiler/commit/004c8638b8beb4627614543001021b824b06aaec))
* release the okdp-compile binaries with goreleaser, print the module version without ldflags ([ce2f1ba](https://github.com/OKDP/okdp-compiler/commit/ce2f1ba4da68350a2e03c94a0ca00f1ebfc0990e))
* rename charts.&lt;key&gt;.base to defaults, &lt;key&gt;.defaults.yaml by convention ([510cb24](https://github.com/OKDP/okdp-compiler/commit/510cb24bd4a4d6da5f71c38f27010db1842afa8c))
* replace okdp.charts.yaml with install.sh (--mkinstall) ([328cf6e](https://github.com/OKDP/okdp-compiler/commit/328cf6e415c29673079111a5ec780fafabf85fb2))
* report connection errors at once and in a stable order ([6c289d3](https://github.com/OKDP/okdp-compiler/commit/6c289d391ceb62c062514bbf8f035373de25a97a))
* take global from the platform's values layers only (--platform, Inputs.Platform) ([5f6a468](https://github.com/OKDP/okdp-compiler/commit/5f6a468855a419a1da5769dec9a1dc534ca6549e))
* validate the release and namespace, name the descriptor with fullname, refuse colliding OAuth clients and objects and a secretRef without a name ([ef77c56](https://github.com/OKDP/okdp-compiler/commit/ef77c56cfcabc1f2ab54b2100aff68d3f2be81ec))
* values.yaml, the charts' values without expressions ([80704bf](https://github.com/OKDP/okdp-compiler/commit/80704bf939388e098f8f3249f6ff2c2e05c200bc))
* write okdp.dependencies.yaml only with --mkdag ([01f4e89](https://github.com/OKDP/okdp-compiler/commit/01f4e89d1d980a5aae235dd9c85371496f07c556))
* write the errors in CEL.YAML instead of CEL ([b53c504](https://github.com/OKDP/okdp-compiler/commit/b53c504161dcad663d00b4d0c7702a43b4b4688a))

## [0.3.0](https://github.com/alliage-io/okdp-compiler/compare/v0.2.0...v0.3.0) (2026-10-14)


### ⚠ BREAKING CHANGES

* okdp.instance.yaml replaces the descriptor ConfigMap
* OKDP's objects in the umbrella, no okdp chart
* every dependency is rendered unless the values leave it out
* Chart.yaml, okdp.yaml and values.cel.yaml replace service.yaml
* an instance's connections are a context layer of its level
* a missing config.schema.json is an empty schema
* context levels replace --platform and --project
* $else beside $if and $value
* --project values that add to the platform's, examples/03_full

### Features

* --project values that add to the platform's, examples/03_full ([91b4f11](https://github.com/alliage-io/okdp-compiler/commit/91b4f11b4c0ae208da3476d60fa60896d012eeff))
* $else beside $if and $value ([f91d187](https://github.com/alliage-io/okdp-compiler/commit/f91d187d27331444e2e08090661068dbebb3ad5c))
* a missing config.schema.json is an empty schema ([6493e89](https://github.com/alliage-io/okdp-compiler/commit/6493e898b8dd314c74bd952fbf90c8fed246289f))
* an instance's connections are a context layer of its level ([9406cce](https://github.com/alliage-io/okdp-compiler/commit/9406cceea0bb3ff0dc669c12e86b9d50034999be))
* Chart.yaml, okdp.yaml and values.cel.yaml replace service.yaml ([83148f0](https://github.com/alliage-io/okdp-compiler/commit/83148f0bb59927b3e3a4ffbcd400c9edeb36a1a7))
* context levels replace --platform and --project ([3c6b7b0](https://github.com/alliage-io/okdp-compiler/commit/3c6b7b0e126fc18ebc7c08fe8b12898b1eed7d60))
* every dependency is rendered unless the values leave it out ([ad6d6ef](https://github.com/alliage-io/okdp-compiler/commit/ad6d6eff622d2a4adf3228133378f1f63a867536))
* okdp.instance.yaml replaces the descriptor ConfigMap ([731eb36](https://github.com/alliage-io/okdp-compiler/commit/731eb3689b429e3b4eab25ffaf2455af909835d3))
* OKDP's objects in the umbrella, no okdp chart ([a802f92](https://github.com/alliage-io/okdp-compiler/commit/a802f92b6b99795420e32e4b6906966fcc641222))
* values.yaml, the charts' values without expressions ([80704bf](https://github.com/alliage-io/okdp-compiler/commit/80704bf939388e098f8f3249f6ff2c2e05c200bc))

## [0.2.0](https://github.com/alliage-io/okdp-compiler/compare/v0.1.0...v0.2.0) (2026-10-06)


### ⚠ BREAKING CHANGES

* name the upstream chart in each <key>.values.yaml
* rename charts.<key>.base to defaults, <key>.defaults.yaml by convention
* mark generated files with a DO NOT EDIT line
* replace okdp.charts.yaml with install.sh (--mkinstall)
* write okdp.dependencies.yaml only with --mkdag
* let a require entry of service.yaml take a condition, $if, evaluated on the mapping's context
* take global from the platform's values layers only (--platform, Inputs.Platform)
* customizable indent
* **compiler:** add the okdp chart to every compile and take the platform's charts from global.okdp.charts
* **compiler:** name each upstream chart by its source and read it from a chart cache
* **compiler:** read the package's metadata from service.yaml and its parameters from the package

### Features

* --mkinstall with --umbrella, an install.sh for the umbrella chart ([4577ee5](https://github.com/alliage-io/okdp-compiler/commit/4577ee5833cc7848be26b08a3c37c416dea38cbb))
* check external connections and provides against their contract's schema, apply contract defaults until stable, report an unreadable contract ([46eae24](https://github.com/alliage-io/okdp-compiler/commit/46eae24415f9a3c4c6ba6e4a292f23fef7144bf9))
* check the types of provides secrets, okdpGeneratedSecret arguments and instance, report every missing contract field ([37671fb](https://github.com/alliage-io/okdp-compiler/commit/37671fb51726128ddd2ba044c6e4948482b07345))
* check the umbrella with the Helm versions Flux and Argo CD embed, Helm 3.19 differences documented ([9108aa5](https://github.com/alliage-io/okdp-compiler/commit/9108aa508a900d7d9237f7214cc939a989e68fcb))
* **cmd:** add the okdp-compile CLI ([3fc5444](https://github.com/alliage-io/okdp-compiler/commit/3fc54446b383ee608deba1bcdbd4634d9c77692f))
* compile an empty mapping as one without sections ([26b3fbe](https://github.com/alliage-io/okdp-compiler/commit/26b3fbed97be88d0335f1ea5fc1a648740347969))
* **compiler:** accept bracketed literal keys and globs in protect and append paths ([4bccd14](https://github.com/alliage-io/okdp-compiler/commit/4bccd1478a84c90ae68da04df516525164bfc4f8))
* **compiler:** add the oidc-dcr chart of each OAuth client without a service.yaml entry ([b55364e](https://github.com/alliage-io/okdp-compiler/commit/b55364e7aaa9ad67080daae1ee2a0ceb6d5f1f8a))
* **compiler:** add the okdp chart to every compile and take the platform's charts from global.okdp.charts ([f8d4c63](https://github.com/alliage-io/okdp-compiler/commit/f8d4c6305624f16daf2d87ca6e3f1ba7d5d9d91e))
* **compiler:** add the sha256, toJson, toPrettyJson, toYaml, quote and okdpFullname CEL functions ([c4777a4](https://github.com/alliage-io/okdp-compiler/commit/c4777a4b3ca779bd2bbf70dae19d1bed6dde2d92))
* **compiler:** add the YAML + CEL mapping engine ([3e4599b](https://github.com/alliage-io/okdp-compiler/commit/3e4599b2f1990ad438159009d36ff077ce18f631))
* **compiler:** compare a compile with a wrapper chart render ([6c09201](https://github.com/alliage-io/okdp-compiler/commit/6c092012941e6cfd681e68acae4183e4248747d6))
* **compiler:** compile a service instance into upstream chart values ([346b213](https://github.com/alliage-io/okdp-compiler/commit/346b213ddaa1fa21e533b4839fcd5aa20b9dc46b))
* **compiler:** compile the OAuth clients of an oidcClients section from an oidc-dcr template ([ac45eca](https://github.com/alliage-io/okdp-compiler/commit/ac45eca23ce5fe202444d68e9449edb883582640))
* **compiler:** give the mapping its chart metadata and labels ([24dc815](https://github.com/alliage-io/okdp-compiler/commit/24dc815b1dfcb9940e84061769c74909213d4fee))
* **compiler:** let a chart of service.yaml name the vendored chart it compiles from ([4d49c9c](https://github.com/alliage-io/okdp-compiler/commit/4d49c9c7bd0b414104cc85c4a8732a71c06aebf2))
* **compiler:** let a chart receive the upstream values of another chart with upstream.from ([5a75046](https://github.com/alliage-io/okdp-compiler/commit/5a75046894d7491e94fe97f209c4c025a530df9d))
* **compiler:** name each upstream chart by its source and read it from a chart cache ([a17ca37](https://github.com/alliage-io/okdp-compiler/commit/a17ca37f1de02d5d41a3c4b1119c9710c434f55b))
* **compiler:** read and merge values as Helm does ([18d64f5](https://github.com/alliage-io/okdp-compiler/commit/18d64f5747140c295f3801f0341b22871c3bb941))
* **compiler:** read and merge values exactly as Helm does ([b4ef002](https://github.com/alliage-io/okdp-compiler/commit/b4ef00288492c74c7ca9c399fbde9427a5baa16f))
* **compiler:** read the instance upstream values from files ([d25ed1d](https://github.com/alliage-io/okdp-compiler/commit/d25ed1d10ddade5ef3b7f4a7358e1de22e6952ca))
* **compiler:** read the oidc-dcr Job settings from the platform values ([33c499d](https://github.com/alliage-io/okdp-compiler/commit/33c499d31468e1dd610fa00e132125573a7bd200))
* **compiler:** read the package's metadata from service.yaml and its parameters from the package ([b8fc940](https://github.com/alliage-io/okdp-compiler/commit/b8fc940f1557893c1a224d6ea2c58446fd338447))
* **compiler:** refuse unknown keys under global.okdp.charts ([b2bf8d0](https://github.com/alliage-io/okdp-compiler/commit/b2bf8d0befa3bae2833994a5659126c53f4c4255))
* **compiler:** rename the fixed values of a chart to base ([f10317e](https://github.com/alliage-io/okdp-compiler/commit/f10317ec40f31467937806f9a63aff857bfeda08))
* **compiler:** render OKDP's own objects and feed them to the okdp chart ([606faa0](https://github.com/alliage-io/okdp-compiler/commit/606faa0f9759d90f1a07167e9fa99359fcd16b6f))
* **compiler:** report the instances an instance depends on ([c63136e](https://github.com/alliage-io/okdp-compiler/commit/c63136ec29f10abb7c1009495ad42ca9f347e4dc))
* **compiler:** set the oidc-dcr 0.5.0 values in okdpDcr ([b7d9ece](https://github.com/alliage-io/okdp-compiler/commit/b7d9ece13065aa4781f876ea9e3a46b63558ed76))
* **compiler:** show the mapping the upstream values files merged with the inline values ([c34c33a](https://github.com/alliage-io/okdp-compiler/commit/c34c33a97dc2ef95f8dbb6d230ab53d9dd2bba7d))
* **compiler:** validate the parameters and the compiled values against their schemas ([a961dc9](https://github.com/alliage-io/okdp-compiler/commit/a961dc967f4fba546e34477210aa26cb99fc524b))
* convert CEL values to data recursively and refuse what values cannot hold ([8ad89bf](https://github.com/alliage-io/okdp-compiler/commit/8ad89bffb9bdc330b5923670a2d3391f727cd5f6))
* customizable indent ([f2dce7c](https://github.com/alliage-io/okdp-compiler/commit/f2dce7c9f9ce339775453cafbb6858ed3c5bf2ed))
* decode service.yaml strictly and require chart keys as DNS labels ([f9e1a15](https://github.com/alliage-io/okdp-compiler/commit/f9e1a15b963d83060c1c0984f7d88652aeb27847))
* do not require the secret fields of a contract ([f9191dc](https://github.com/alliage-io/okdp-compiler/commit/f9191dcfaea27cde3bef2a9b38edb8004ed95ef5))
* emit the instance as an umbrella chart ([d414017](https://github.com/alliage-io/okdp-compiler/commit/d414017262c102524a416240b41011792247dfe3))
* **example:** add basic target configure example ([7f29cbc](https://github.com/alliage-io/okdp-compiler/commit/7f29cbc67295828b679e8d9b1796cf84c3356a96))
* **example:** add the catalog annotations to the webapp chart ([75800c6](https://github.com/alliage-io/okdp-compiler/commit/75800c6a9451b2fd51b236dbb8837f09ae231d52))
* **example:** add the webapp chart and contracts, test the example ([9fd35e5](https://github.com/alliage-io/okdp-compiler/commit/9fd35e5eff43e068422c5f819da27ba774aa765d))
* find the }} of an expression past raw and triple-quoted strings and comments, refuse mapping keys Helm reads at random, read the mapping's lines and YAML 1.1 scalars as Helm, fuzz the scanner, paths, values and the mapping reader ([e09a264](https://github.com/alliage-io/okdp-compiler/commit/e09a264039986050c80970d6083066da9b95baf9))
* key the chart cache by repository, &lt;repository&gt;/&lt;chart&gt;-&lt;version&gt;, the flat entry kept unless two repositories share it ([c7c1a43](https://github.com/alliage-io/okdp-compiler/commit/c7c1a43f1aeb4319c448257e7dca2c213f6ffaf8))
* let a require entry of service.yaml take a condition, $if, evaluated on the mapping's context ([cf6095b](https://github.com/alliage-io/okdp-compiler/commit/cf6095b1c4c7a2ef5e56902ae2b36dbbe70057b9))
* make DcrSource a function, add CompileContext with a CEL cost limit ([3e8d2e1](https://github.com/alliage-io/okdp-compiler/commit/3e8d2e19232fd4245f4da1bd3abce85e4292ff7d))
* mark generated files with a DO NOT EDIT line ([c735d84](https://github.com/alliage-io/okdp-compiler/commit/c735d844e65ff0243d97fd94f391e202af88d177))
* name the platform values input in the errors of a missing platform value, add CONTRIBUTING.md and a license section ([bfe1717](https://github.com/alliage-io/okdp-compiler/commit/bfe171774402c05276ffdab15cb4a51779c5db4d))
* name the upstream chart in each &lt;key&gt;.values.yaml ([b4c2996](https://github.com/alliage-io/okdp-compiler/commit/b4c299672388af2ae20cf26c0a329486c3f67fcb))
* normalize Go-typed values layers as Helm values, refuse what values cannot hold ([3f038a1](https://github.com/alliage-io/okdp-compiler/commit/3f038a1e151857c248aa1b9e63c5f973eac2affb))
* read chart archives as Helm does, backslash names, paths out of the chart refused and a 100 MiB cap ([047a10c](https://github.com/alliage-io/okdp-compiler/commit/047a10c4438b66c3f1a721a39a0b09fddf6b53b5))
* refuse a YAML 1.1 boolean as upstream in service.yaml instead of taking it as a file name ([3c6f09c](https://github.com/alliage-io/okdp-compiler/commit/3c6f09c95d02aa9b45cafaf69c3b57ee23fa6b06))
* refuse a YAML alias inside its own anchor and excessive aliasing ([291fa3b](https://github.com/alliage-io/okdp-compiler/commit/291fa3b89c034907456c9c3113b4d2be32c6320e))
* refuse mistakes in the mapping language and resolve YAML merge keys as Helm ([0236fa5](https://github.com/alliage-io/okdp-compiler/commit/0236fa5b0271dc1a9c34bc43349d1ea8b8b3a2fc))
* refuse positional arguments, check the input directories, write every output or none, keep the output directories to their files, require --release and --namespace ([33a6e4c](https://github.com/alliage-io/okdp-compiler/commit/33a6e4ccf3f742ce1662036c80b8cf17919f8056))
* refuse sections and platform settings of the wrong shape ([667cba1](https://github.com/alliage-io/okdp-compiler/commit/667cba1272b61576641d0d5653e096877638a72b))
* refuse template code in instance parameters and connections, and a connection reference that is not a string or not a release name ([004c863](https://github.com/alliage-io/okdp-compiler/commit/004c8638b8beb4627614543001021b824b06aaec))
* release the okdp-compile binaries with goreleaser, print the module version without ldflags ([ce2f1ba](https://github.com/alliage-io/okdp-compiler/commit/ce2f1ba4da68350a2e03c94a0ca00f1ebfc0990e))
* rename charts.&lt;key&gt;.base to defaults, &lt;key&gt;.defaults.yaml by convention ([510cb24](https://github.com/alliage-io/okdp-compiler/commit/510cb24bd4a4d6da5f71c38f27010db1842afa8c))
* replace okdp.charts.yaml with install.sh (--mkinstall) ([328cf6e](https://github.com/alliage-io/okdp-compiler/commit/328cf6e415c29673079111a5ec780fafabf85fb2))
* report connection errors at once and in a stable order ([6c289d3](https://github.com/alliage-io/okdp-compiler/commit/6c289d391ceb62c062514bbf8f035373de25a97a))
* take global from the platform's values layers only (--platform, Inputs.Platform) ([5f6a468](https://github.com/alliage-io/okdp-compiler/commit/5f6a468855a419a1da5769dec9a1dc534ca6549e))
* validate the release and namespace, name the descriptor with fullname, refuse colliding OAuth clients and objects and a secretRef without a name ([ef77c56](https://github.com/alliage-io/okdp-compiler/commit/ef77c56cfcabc1f2ab54b2100aff68d3f2be81ec))
* write okdp.dependencies.yaml only with --mkdag ([01f4e89](https://github.com/alliage-io/okdp-compiler/commit/01f4e89d1d980a5aae235dd9c85371496f07c556))
* write the errors in CEL.YAML instead of CEL ([b53c504](https://github.com/alliage-io/okdp-compiler/commit/b53c504161dcad663d00b4d0c7702a43b4b4688a))
