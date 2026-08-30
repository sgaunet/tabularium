# Implementation Plan: [FEATURE]

**Branch**: `[###-feature-name]` | **Date**: [DATE] | **Spec**: [link]

**Input**: Feature specification from `/specs/[###-feature-name]/spec.md`

**Note**: This template is filled in by the `/speckit-plan` command; its definition describes the execution workflow.

## Summary

[Extract from feature spec: primary requirement + technical approach from research]

## Technical Context

<!--
  ACTION REQUIRED: Replace the content in this section with the technical details
  for the project. The structure here is presented in advisory capacity to guide
  the iteration process.
-->

**Language/Version**: Go, at the version pinned by the `go`/`toolchain` directives in `go.mod` and by `mise.toml`

**Primary Dependencies**: standard library first (Principle VI). List any direct module
dependency this feature needs, its license, and whether the author has approved it — an
unapproved dependency is a Constitution Check FAIL, not a NEEDS CLARIFICATION.

**Storage**: [if applicable, e.g., files on disk, embedded assets, or N/A]

**Testing**: `go test -count=2 -race ./...` via `task test`; black-box `package <pkg>_test`

**Target Platform**: single static binary (`CGO_ENABLED=0`), goreleaser v2 targets in `.goreleaser.yml`

**Project Type**: single-purpose CLI

**Performance Goals**: [domain-specific, e.g., 1000 req/s, 10k lines/sec, 60 fps or NEEDS CLARIFICATION]

**Constraints**: [domain-specific, e.g., <200ms p95, <100MB memory, offline-capable or NEEDS CLARIFICATION]

**Scale/Scope**: [domain-specific, e.g., 10k users, 1M LOC, 50 screens or NEEDS CLARIFICATION]

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Mark each gate PASS / FAIL / N-A. Any FAIL must be justified in Complexity Tracking or the
plan must be revised. See `.specify/memory/constitution.md`.

| # | Gate | Status | Notes |
|---|------|--------|-------|
| I | **Single purpose** — the feature serves the binary's one job; it does not add a second, unrelated job | | |
| II | **Output contract** — stdout is data only and `--output=text\|json`; logs, errors and progress go to stderr; exit codes 0/1/2 documented in `--help` and tested (code `2` wired explicitly in `main`) | | |
| III | **Thin commands** — command layer only parses, validates, calls, formats; logic sits in domain-named packages importing no CLI package; no `utils`/`helpers`/`common`/`base` package | | |
| IV | **Test-first** — failing test precedes implementation; tests in `package <pkg>_test`; internals exposed via `export_test.go`; arg parsing, exit codes and the stdout/stderr split each covered; one end-to-end test invokes the built binary | | |
| V | **Interruptible & bounded** — long-running work takes `context.Context` and cancels on `SIGINT`/`SIGTERM`; every I/O has an explicit timeout; retries bounded and backed off | | |
| VI | **Stdlib first** — no new direct module dependency, or one proposed to and approved by the author with an MIT/BSD/Apache-2.0 license | | |
| VII | **Reproducible binary** — builds with `CGO_ENABLED=0 -trimpath` on the pinned toolchain; runtime assets embedded with `//go:embed`; release targets live in `.goreleaser.yml` | | |
| UX | **CLI behaviour** — honours `NO_COLOR` and non-TTY stdout; supports `--quiet`/`--verbose`; config precedence flags > env > file > defaults stated in `--help`; destructive actions gated behind `--yes` or confirmation; no credentials in the repo, in logs, or in error messages | | |
| Q | **Quality gates** — `task test`, `task lint`, `go generate ./...` (no diff, generated files committed) and `govulncheck ./...` all pass | | |

## Project Structure

### Documentation (this feature)

```text
specs/[###-feature]/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output (/speckit-plan command)
├── data-model.md        # Phase 1 output (/speckit-plan command)
├── quickstart.md        # Phase 1 output (/speckit-plan command)
├── contracts/           # Phase 1 output (/speckit-plan command)
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)
<!--
  ACTION REQUIRED: Replace the tree below with the concrete layout for this
  feature, using real package paths. Keep the separation the constitution
  requires: domain logic in packages that import no CLI package (Principle III).
-->

```text
# Replace the domain package names below with the real ones for this feature.
# Packages are named for the domain they serve; utils/helpers/common/base are forbidden.

cmd/tabularium/
├── main.go              # wires exit codes 0/1/2 and the signal-cancelled context
└── main_test.go         # end-to-end: invokes the built binary

internal/<domain>/       # business logic; imports no CLI package
├── <domain>.go
├── export_test.go       # only if a black-box test needs an internal
└── <domain>_test.go     # package <domain>_test

internal/cli/            # thin cobra/flag wrappers: parse, validate, call, format
├── root.go
└── root_test.go         # arg parsing, stdout/stderr split, exit codes
```

**Structure Decision**: [Document the selected structure and reference the real
directories captured above]

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| [e.g., new direct dependency] | [current need] | [why the standard library is insufficient] |
| [e.g., generic type parameter] | [specific problem] | [why it is needed before 3 concrete implementations exist] |
