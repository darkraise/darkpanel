# Gates

What proves a change to Darkpanel works, in the order it must run. The
dr-superpowers running-gates skill reads this file. Every gate's command must
also exit 0.

## 1. ADR records
Command: `bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh --complete`
Green: the `check-adrs tests:` line reports `0 failed`, and no `check-adrs:` error line follows it
Evidence: output
Applies: always
Why: the ADRs are the decision record every later epic builds on; the checker catches a missing section or an untraced requirement

## 2. Lint
Command: `make lint`
Green: golangci-lint prints `0 issues.`, and ESLint and `tsc --noEmit` print no findings
Evidence: output
Applies: always
Why: CI runs the same lint targets; a finding caught here does not cost a CI round

## 3. Tests
Command: `make test`
Green: every Go package line starts with `ok`, the Vitest `Tests` line has no `failed`, and the ADR checker reports `0 failed`
Evidence: output
Applies: always
Why: the unit tests for the CLI, the embedded UI package, the web app and the ADR checker

## 4. Build
Command: `make build verify-build`
Green: `ok  	github.com/darkraise/darkpanel/web` after the `file` and UI-marker checks
Evidence: output
Applies: always
Why: the Go linker drops an embed.FS that no code reads, so a build can succeed with no UI in it; verify-build checks each binary for the UI

## 5. Vulnerabilities
Command: `make vuln`
Green: `No vulnerabilities found.`
Evidence: output
Applies: always
Why: govulncheck reports known vulnerabilities in the code the binaries actually reach

## 6. CI green
Command: `timeout 60 gh run list --workflow ci.yml --branch "$(git branch --show-current)" --commit "$(git rev-parse HEAD)" --json conclusion --jq '.[0].conclusion'`
Green: `success`
Evidence: output
Applies: once the branch is pushed to origin
Why: only a clean runner shows that the build does not depend on the development host; the CI run on main is M1's evidence for "CI and gates green"
