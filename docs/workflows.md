# Development Workflows

## Feature Development

This repository is driven by Spec Kit, so a feature starts as a document, not as code.

1. `/speckit-specify` — write `specs/NNN-name/spec.md`. What and why, never how.
   Creates `specs/NNN-name/checklists/requirements.md` and points `.specify/feature.json`
   at the directory.
2. `/speckit-clarify` — optional, resolves any `[NEEDS CLARIFICATION]` markers left in
   the spec.
3. `/speckit-plan` — the technical plan. Its **Constitution Check** gate must pass before
   Phase 0 research and again after Phase 1 design. A violation that cannot be removed is
   recorded in the plan's Complexity Tracking table with the rejected simpler alternative;
   an unjustified one blocks the plan.
4. `/speckit-tasks` — a dependency-ordered `tasks.md`, grouped by user story. Tests are
   mandatory and precede implementation in every story.
5. `/speckit-implement` — execute the tasks.

Branch from `main` before committing. Commits and pushes happen only when asked.

## Code Review Process

There is no `CONTRIBUTING.md`; the constitution is the review standard.

- Every gate in `docs/workflows.md` § Quality Gates passes before a change lands
- A new direct dependency needs the author's prior approval and an MIT/BSD/Apache-2.0
  licence — this is a review blocker, not a preference
- "Idiomatic" means whatever `.golangci.yml` accepts. Disagreements are settled by editing
  that file, not by arguing in review

## Testing Strategy

Test-first is non-negotiable (Constitution IV). Write the failing test, watch it fail for
the intended reason, make it pass, then refactor.

- **Location**: beside the code, in `package <pkg>_test` — black box, so tests exercise
  the API a consumer actually has
- **Internals**: exposed through an `export_test.go` inside the package, never by moving
  the test back in
- **Command**: `task test` → `go test -count=2 -race ./...`. `-count=2` defeats result
  caching and surfaces state leaking between runs; `-race` is the only mechanical check
  for data races
- **Mandatory coverage**: argument parsing, exit codes, and the stdout/stderr split each
  get a test, and at least one test invokes the built binary end to end
- **Coverage number**: `task coverage` prints the total, excluding `cmd/`

## Quality Gates

Every change clears all six before it lands:

1. The failing test exists and failed for the intended reason
2. `task test` passes
3. `task lint` passes (`go generate ./...` then `golangci-lint run`)
4. `go generate ./...` produces no diff, and generated files are committed
5. `govulncheck ./...` passes — **not yet wired into CI**
6. Any new direct dependency carries prior approval and an approved licence

`task check-before-commit` runs test, snapshot and lint together.
`task dev:install-pre-commit` installs the hook that runs test, lint and build; the
default `task` target refuses to run until that hook exists.

## CI

Four GitHub Actions workflows, each installing the pinned toolchain with
`jdx/mise-action` and then delegating to `task` — so CI and a local run execute the same
commands:

| Workflow | Runs |
|---|---|
| `test.yml` | `task test` |
| `linter.yml` | `task lint` |
| `snapshot.yml` | `task snapshot` |
| `release.yml` | `task release` |

Dependabot watches `gomod` weekly (one PR per module) and `github-actions` monthly
(batched).

## Release Process

Automated through goreleaser v2, driven by `release.yml`. `.goreleaser.yaml` holds every
build target — never an ad-hoc command line. Builds are `CGO_ENABLED=0` with `-trimpath`,
and the release publishes SHA-256 checksums and an SBOM (syft, pinned in `mise.toml`).

`task snapshot` produces the same artefacts locally without publishing.
