# CLAUDE.md

This file provides guidance to Claude Code when working with this repository.

## Operating Guidelines

**Read `docs/operating-guidelines.md` at the start of every session.** It
defines how to plan, verify, and iterate in this repository: plan mode,
subagent strategy, verification gates, self-improvement loop, and the
communication contract. Treat it as load-bearing context.

## Behavioral Guidelines

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

### 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

### 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

### 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

### 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

## Repository Overview

Tabularium is a single-purpose Go CLI. It takes one document, extracts its text (OCR
through an OpenAI-compatible endpoint, local or remote by configuration alone), derives
tags and a more descriptive filename, files it into a local tree by ordered rules, then
hands it to a configurable external archiver.

Feature `001-document-triage` is implemented: all four user stories, `cmd/tabularium`
and twelve `internal/` packages, with the tests beside them. Work is driven by Spec Kit:
`.specify/memory/constitution.md` governs, and `specs/001-document-triage/spec.md` holds
the feature (76 requirements).

## Constitution — binding

`.specify/memory/constitution.md` **overrides your defaults**. A spec, plan, or patch
that violates a principle must be revised, or the conflict raised with the trade-off
stated. In practice:

- **Test-first, non-negotiable.** A behaviour change arrives with the test that failed
  without it. Tests are black box — `package <pkg>_test`; internals are reached through
  `export_test.go`, never by moving the test back into the package.
- **Pipe-safe output.** stdout carries data only, `--output=text|json`; logs, errors and
  progress go to stderr. Exit codes `0`/`1`/`2`, documented in `--help` and tested. A
  scaffolded `main` exits 1 for everything, so code `2` is wired in `main` explicitly.
- **Thin commands.** Business logic lives in domain-named packages that import no CLI
  package. `utils`, `helpers`, `common` and `base` are forbidden.
- **Stdlib first.** A new direct dependency needs the author's approval before it lands,
  and must be MIT, BSD or Apache-2.0.
- **Interruptible and bounded.** Long operations take `context.Context` and cancel on
  SIGINT/SIGTERM. Every I/O has an explicit timeout; retries bounded and backed off.

## Architecture

- `cmd/tabularium/` — `main.go` wires exit codes and the signal-cancelled context
- `internal/cli/` — thin stdlib `flag` wrappers: parse, validate, call, format
- `internal/<domain>/` — business logic, importing no CLI package

See `docs/architecture.md` for the pipeline and the design decisions behind it.

## Development Commands

```bash
task build      # CGO_ENABLED=0 go build -o tabularium ./cmd/tabularium
task test       # go test -count=2 -race ./...
task lint       # go generate ./... && golangci-lint run
task coverage   # print total coverage percentage
task snapshot   # goreleaser snapshot build
task release    # goreleaser release
```

The toolchain is pinned in `mise.toml` (Go 1.26.1, golangci-lint 2.12.1, goreleaser
2.16.0, syft 1.45.1). Run `task dev:install-pre-commit` once to wire test, lint and
build as a pre-commit hook — `task` refuses to run without it.

## Spec Kit workflow

Features go through `/speckit-specify` → `/speckit-plan` → `/speckit-tasks` →
`/speckit-implement`; `.specify/feature.json` points at the active feature. The plan's
Constitution Check gate must pass before Phase 0 and again after Phase 1 design.

## Code Quality Standards

**Linters configured** (do not duplicate their rules here):
- golangci-lint v2 — `.golangci.yml`, `default: all` minus an explicit disable list
- pre-commit — `.pre-commit-config.yaml`, runs test + lint + build

Conventions the linter does not enforce:
- Wrap errors with `fmt.Errorf("...: %w", err)`; never flatten one into a string
- Log with `log/slog`, on stderr, at a level the user controls
- Prefer concrete types; no generic before three concrete implementations exist
- Embed runtime assets with `//go:embed`; commit every generated file

## File Locations

- **Source**: `cmd/tabularium/`, `internal/`
- **Tests**: beside the code they test, in `package <pkg>_test`
- **Specs**: `specs/001-document-triage/`
- **Constitution**: `.specify/memory/constitution.md`
- **Docs**: `docs/`
- **CI**: `.github/workflows/` — lint, test, snapshot, release, each invoking `task`

## Documentation

- `docs/architecture.md`: pipeline, components, design decisions
- `docs/workflows.md`: Spec Kit flow, git workflow, release process
- `docs/patterns.md`: the Go patterns this project commits to
- `docs/operating-guidelines.md`: how to plan, verify, and iterate here
