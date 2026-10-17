# M1 repository, tooling, CI and gates register

**Source:** M1 roadmap epic "Repository, tooling, CI and gates" (R2, R3, R4)
**Covers:** docs/superpowers/specs/2026-10-17-m1-repository-tooling-design.md

| # | Item | Assigned | Acceptance | State | Note |
|---|---|---|---|---|---|
| 1 | R2 one Go module builds static linux/amd64 and linux/arm64 binaries with the web UI embedded | - | make build verify-build passes; file reports two static ELF binaries embedding web/dist/index.html | open | - |
| 2 | R2 one documented build command | - | README and CONTRIBUTING document make build | open | - |
| 3 | R3 CI runs Go and TypeScript lint, type checks, unit tests and both builds on every push | - | ci.yml jobs adr, go, web, build call make targets; CI run on pushed main concludes success | open | - |
| 4 | R3 LICENSE (MIT), CONTRIBUTING.md and SECURITY.md | - | files exist with the content the spec section 3 names | open | - |
| 5 | R4 docs/superpowers/gates.md declares the project's gates | - | gates 1-6 in running-gates format; every gate command passes | open | - |
| 6 | Development host toolchain: Go 1.27.2 and golangci-lint v2.15.0 installed system-wide | - | go version and golangci-lint version print the pinned versions | open | - |
