# Security policy

okdp-compiler compiles what a platform's contributors write, the
packages of its catalog and the values of its projects, into what its
deployer installs: a flaw in it can give a project more than its
platform grants, or leak a credential. What the compiler enforces, and
what it leaves to the platform, is in [security
boundaries](docs/00_architecture.md#security-boundaries), and by author,
with what the compiler cannot enforce, in the [threat
model](docs/04_threat_model.md).

## Reporting a vulnerability

Do not open a public issue. Write to **security@okdp.io**, or open a
private report with GitHub's
[security advisories](https://github.com/OKDP/okdp-compiler/security/advisories/new),
with:

- the version (`okdp-compile --version`, or the module version of the
  library), and how the compiler is run (the CLI, okdp-gitops, a
  control plane);
- what an attacker controls (a package, an instance's `config.yaml`, a
  project's `project.yaml`, the platform values) and what it gains;
- a tree that reproduces it, the smallest you can, and its output.

We acknowledge a report within 3 working days, and tell you within 10
whether we accept it, and how severe we rate it.

## Supported versions

| Version | Supported |
|---|---|
| the latest minor release, `0.y.z` | yes: fixes are released as its next patch |
| older releases | no: upgrade to the latest |
| `main` | fixed before a release |

Before 1.0 a minor release may break the inputs or outputs
([CHANGELOG.md](CHANGELOG.md)); a security fix is not backported to an
older minor release.

## Disclosure

1. We confirm the flaw, and agree with you on its severity and on a
   disclosure date, 90 days after the report at the latest.
2. We fix it in a private fork, with a test, and prepare a GitHub
   security advisory, a CVE requested through GitHub when it warrants
   one.
3. We release the fix, then publish the advisory, crediting you unless
   you ask us not to; the projects that embed the compiler (okdp-gitops,
   the OKDP control plane) are told before the advisory is public.

A flaw already exploited, or already public, is fixed and disclosed as
soon as a release is ready.

## Verifying a release

Each release's checksums file is signed keyless with cosign, each
archive has an SBOM and an attested build provenance: [release
archives](docs/01_usage.md#release-archives) shows how to verify them.
