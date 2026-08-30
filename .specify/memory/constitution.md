<!--
Sync Impact Report
==================
Version change: [CONSTITUTION_VERSION] (unfilled template) → 1.0.0
Bump rationale: MAJOR — first ratification. The template placeholders are replaced with
the project's governing principles; there is no prior ratified version to be compatible with.

Modified principles (template placeholder → ratified name):
- [PRINCIPLE_1_NAME] → I. Single Purpose, Composable
- [PRINCIPLE_2_NAME] → II. Pipe-Safe Output Contract (NON-NEGOTIABLE)
- [PRINCIPLE_3_NAME] → III. Thin Commands, Domain Packages
- [PRINCIPLE_4_NAME] → IV. Test-First (NON-NEGOTIABLE)
- [PRINCIPLE_5_NAME] → V. Interruptible and Bounded
- (added)            → VI. Standard Library First
- (added)            → VII. Reproducible, Self-Contained Binary

Added sections:
- Command-Line UX Requirements (was [SECTION_2_NAME])
- Code Quality Standards (was [SECTION_3_NAME])
- Development Workflow & Quality Gates (new third section; template shipped only two)

Removed sections: none.

Templates requiring updates:
- ✅ .specify/templates/plan-template.md — Constitution Check gate populated with the
  seven principle gates; Technical Context pre-seeded with the fixed Go/CLI stack.
- ✅ .specify/templates/spec-template.md — Success Criteria and Assumptions guidance
  aligned with the stdout/stderr contract and documented exit codes.
- ✅ .specify/templates/tasks-template.md — tests made mandatory (Principle IV) instead of
  optional; task categories extended for the output contract, cancellation, and lint/vuln gates.
- ✅ .claude/skills/speckit-*/SKILL.md — reviewed; agent-generic, no outdated
  agent-specific references found.
- ⚠ README.md — does not exist yet. Create it with the tool's purpose, --help contract,
  exit codes, and configuration precedence when the first feature lands.

Follow-up TODOs: none. RATIFICATION_DATE is the date of this first adoption.
-->

# Tabularium Constitution

## Core Principles

### I. Single Purpose, Composable

Tabularium is a single-purpose command-line tool: one focused job per binary, composable
with other tools through pipes. A binary that grows a second unrelated job MUST be split
rather than extended. Every feature MUST be justified as serving the one job; anything that
only makes sense as a second job belongs in a different binary.

Rationale: A tool with one job has an output that other tools can consume, a surface small
enough to test exhaustively, and a `--help` a reader finishes.

### II. Pipe-Safe Output Contract (NON-NEGOTIABLE)

- Stdout MUST carry data only, machine-parseable, selectable with `--output=text|json`.
- Stderr MUST carry logs, errors, and progress. Nothing diagnostic may reach stdout.
- Exit codes MUST be `0` success, `1` runtime failure, `2` usage error. They MUST be
  documented in `--help` and covered by a test.
- Cobra sets no exit code of its own — `Execute()` returns an error and the scaffolded
  `main` exits `1` for everything — so code `2` MUST be wired explicitly in `main`.

Rationale: The stdout/stderr split is what makes the tool safe inside a pipe; the exit code
is what makes it safe inside a shell script. Both are the tool's public contract and break
callers when changed.

### III. Thin Commands, Domain Packages

- Commands MUST be thin wrappers (cobra, or stdlib `flag` when there are no subcommands):
  parse, validate, call, format.
- Business logic MUST live in packages that import no CLI package, so it stays testable
  without a terminal.
- Packages MUST be named for the domain they serve. `utils`, `helpers`, `common`, and
  `base` packages are forbidden, because they attract unrelated code and grow into import
  cycles.

Rationale: Logic reachable only through a cobra command is logic that can only be tested
through a cobra command. Domain-named packages keep dependencies pointing one way.

### IV. Test-First (NON-NEGOTIABLE)

- Development MUST follow TDD: write the failing test, make it pass, then refactor. A
  behaviour change MUST arrive with the test that fails without it.
- Tests MUST live in `package <pkg>_test` (black box), so they exercise the API a consumer
  actually has. Internals a test needs are exposed through an `export_test.go` inside the
  package, never by moving the test back in.
- `task test` (`go test -count=2 -race ./...`) MUST pass. `-count=2` defeats result caching
  and surfaces state leaking between runs; `-race` is the only mechanical check for data
  races.
- Argument parsing, exit codes, and the stdout/stderr split MUST each be tested, and at
  least one test MUST invoke the built binary end to end.

Rationale: A test written after the code passes for reasons nobody verified. Black-box tests
are the only ones that fail when the exported API breaks.

### V. Interruptible and Bounded

- Every long-running operation MUST take a `context.Context` and cancel cleanly on `SIGINT`
  and `SIGTERM`, so the tool is always safe to interrupt.
- All I/O MUST have an explicit timeout.
- Retries MUST be bounded and backed off.

Rationale: A CLI is run interactively and killed interactively. Unbounded I/O turns a
transient network fault into a hung terminal, and unbounded retries turn it into an outage
for whatever is on the other end.

### VI. Standard Library First

- The standard library MUST be the first choice.
- Adding a direct module dependency MUST be proposed to the author and approved before it
  lands, so the dependency surface stays small enough for one person to audit.
- A new dependency MUST be MIT, BSD, or Apache-2.0 licensed and actively maintained;
  copyleft or unlicensed modules are refused.

Rationale: Every direct dependency is code the author is accountable for and cannot review.
The standard library is already audited, already vendored, and already compatible.

### VII. Reproducible, Self-Contained Binary

- The tool MUST build as a single static binary with `CGO_ENABLED=0`, so it runs on any host
  with no runtime dependency. A default build is not static: `net` and `os/user` link cgo
  unless it is disabled.
- Builds MUST pass `-trimpath` and use the toolchain pinned in `go.mod` (`go` and
  `toolchain` directives) and `mise.toml`, so rebuilding a tag produces the same binary.
- Runtime assets (templates, default configs, static files) MUST be embedded with
  `//go:embed`, so the binary never depends on files sitting next to it.
- Releases MUST go through goreleaser v2 (`task release`) and publish SHA-256 checksums and
  an SBOM. Build targets live in `.goreleaser.yml`, never in ad-hoc commands.

Rationale: A binary a user can copy onto a host and run is the whole distribution story. A
build that cannot be reproduced from a tag cannot be audited after the fact.

## Command-Line UX Requirements

- The tool MUST respect `NO_COLOR`, and MUST NOT emit colour, spinners, or progress bars
  when stdout is not a TTY.
- The tool MUST support `--quiet` and `--verbose`.
- Configuration precedence MUST be flags > environment > config file > defaults, and MUST be
  stated in `--help`.
- Destructive actions MUST require `--yes` or an interactive confirmation, so a mistyped
  command cannot delete anything.
- Secrets MUST NOT appear in the repository, in logs, or in error messages; configuration
  comes from the environment.

## Code Quality Standards

- Code MUST prefer concrete types. A generic MUST NOT be introduced before the same concrete
  implementation exists for three or more types, because generics cost readability and most
  Go code never needs them.
- Errors MUST be wrapped with `fmt.Errorf("...: %w", err)` and never flattened into a
  string, so callers can still use `errors.Is` and `errors.As`.
- Logging MUST use `log/slog` at a level the user controls, and MUST record only what the
  reader can act on.
- `task lint` (golangci-lint v2) MUST pass. "Idiomatic" means whatever the linter accepts;
  disagreements are settled by editing `.golangci.yml`, not by arguing in review.
- Generators MUST be declared as Go tool dependencies (`go get -tool <module>`, Go 1.24+
  `tool` directive) rather than installed globally, so every checkout runs the same versions.
- Each generator MUST be invoked by a `//go:generate go tool <name> ...` directive placed
  next to the file it produces, and the whole tree MUST regenerate with `go generate ./...`.
- Generated files MUST be committed, so a clean checkout builds without running any
  generator.

## Development Workflow & Quality Gates

Every change passes these gates before it lands:

1. The failing test exists and fails for the intended reason (Principle IV).
2. `task test` (`go test -count=2 -race ./...`) passes.
3. `task lint` (golangci-lint v2) passes.
4. `go generate ./...` produces no diff, and any regenerated file is committed.
5. `govulncheck ./...` passes in CI.
6. Any new direct dependency carries the author's prior approval and an approved license.

Dependabot MUST watch `gomod`, `docker`, and `github-actions` monthly. Releases are cut with
`task release` (goreleaser v2) and never by hand.

## Governance

These principles override the assistant's defaults. A spec, plan, or patch that violates one
MUST be revised to comply, or the conflict MUST be raised to the author with the trade-off
stated.

When two principles conflict, composability and a stable output contract win: breaking a
pipe is worse than an awkward internal design.

When a case is genuinely unaddressed here, the assistant MUST ask rather than guess, and
SHOULD choose the simpler design, with fewer dependencies and more tests.

**Amendment procedure**: Amendments are proposed to the author as a diff to this file,
together with the version bump and its rationale, and land only with the author's approval.
An amendment that changes a gate MUST update `.specify/templates/` in the same change.

**Versioning policy**: This constitution is versioned MAJOR.MINOR.PATCH. MAJOR for
backward-incompatible governance changes — a principle removed or redefined such that
existing code now violates it. MINOR for a new principle or section, or materially expanded
guidance. PATCH for clarifications, wording, and non-semantic refinements.

**Compliance review**: The Constitution Check in `.specify/templates/plan-template.md` is
evaluated before Phase 0 research and re-evaluated after Phase 1 design. Violations that
cannot be removed MUST be recorded in that plan's Complexity Tracking table with the
simpler alternative that was rejected and why. An unjustified violation blocks the plan.

**Version**: 1.0.0 | **Ratified**: 2026-08-30 | **Last Amended**: 2026-08-30
