---

description: "Task list template for feature implementation"
---

# Tasks: [FEATURE NAME]

**Input**: Design documents from `/specs/[###-feature-name]/`

**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, contracts/

**Tests**: Tests are MANDATORY (Constitution Principle IV, Test-First). Every user story
gets its failing tests BEFORE its implementation tasks. Tests live in `package <pkg>_test`;
internals a test needs are exposed through `export_test.go`, never by moving the test back
into the package.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Entry point**: `cmd/tabularium/main.go` — wires exit codes 0/1/2 and the signal-cancelled context
- **Command layer**: `internal/cli/` — thin wrappers only: parse, validate, call, format
- **Domain logic**: `internal/<domain>/` — imports no CLI package; named for the domain it
  serves (`utils`, `helpers`, `common`, `base` are forbidden)
- **Tests**: alongside the code they test, in `package <pkg>_test`
- Adjust the concrete package names based on plan.md structure

<!--
  ============================================================================
  IMPORTANT: The tasks below are SAMPLE TASKS for illustration purposes only.

  The /speckit-tasks command MUST replace these with actual tasks based on:
  - User stories from spec.md (with their priorities P1, P2, P3...)
  - Feature requirements from plan.md
  - Entities from data-model.md
  - Endpoints from contracts/

  Tests are mandatory: every story's failing tests precede its implementation tasks.

  Tasks MUST be organized by user story so each story can be:
  - Implemented independently
  - Tested independently
  - Delivered as an MVP increment

  DO NOT keep these sample tasks in the generated tasks.md file.
  ============================================================================
-->

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and basic structure

- [ ] T001 Create package structure per implementation plan (domain packages import no CLI package)
- [ ] T002 Pin the Go toolchain in `go.mod` and `mise.toml`; confirm `CGO_ENABLED=0 -trimpath` builds
- [ ] T003 [P] Configure `.golangci.yml` so `task lint` passes
- [ ] T004 [P] Configure `task test` as `go test -count=2 -race ./...`
- [ ] T005 [P] Declare any generator as a Go tool dependency (`go get -tool <module>`)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

Examples of foundational tasks (adjust based on your project):

- [ ] T006 Wire `main` to exit `0` success / `1` runtime failure / `2` usage error, and document the codes in `--help`
- [ ] T007 Wire a `context.Context` cancelled on `SIGINT`/`SIGTERM` through to every long-running call
- [ ] T008 [P] Establish the stdout/stderr split and the `--output=text|json` writer
- [ ] T009 [P] Configure `log/slog` on stderr at a level `--quiet`/`--verbose` control
- [ ] T010 [P] Implement config resolution in precedence order flags > env > config file > defaults, stated in `--help`
- [ ] T011 [P] Honour `NO_COLOR` and suppress colour, spinners, and progress bars when stdout is not a TTY
- [ ] T012 Create the domain types all stories depend on, with errors wrapped using `%w`

**Checkpoint**: Foundation ready - user story implementation can now begin in parallel

---

## Phase 3: User Story 1 - [Title] (Priority: P1) 🎯 MVP

**Goal**: [Brief description of what this story delivers]

**Independent Test**: [How to verify this story works on its own]

### Tests for User Story 1 (MANDATORY — write first, watch them fail) ⚠️

> **NOTE: Write these tests FIRST and confirm they FAIL for the intended reason**

- [ ] T013 [P] [US1] Domain test for [behaviour] in internal/[domain]/[domain]_test.go (`package [domain]_test`)
- [ ] T014 [P] [US1] Command test for arg parsing and usage errors (exit `2`) in internal/cli/[cmd]_test.go
- [ ] T015 [P] [US1] Output-contract test: data on stdout, logs on stderr, `--output=text|json` in internal/cli/[cmd]_test.go
- [ ] T016 [US1] End-to-end test invoking the built binary in cmd/tabularium/main_test.go

### Implementation for User Story 1

- [ ] T017 [P] [US1] Create [Entity1] in internal/[domain]/[entity1].go (concrete types; no generic before 3 concrete implementations exist)
- [ ] T018 [P] [US1] Create [Entity2] in internal/[domain]/[entity2].go
- [ ] T019 [US1] Implement [operation] in internal/[domain]/[operation].go, taking `ctx context.Context` (depends on T017, T018)
- [ ] T020 [US1] Wire the thin command in internal/cli/[cmd].go: parse, validate, call, format
- [ ] T021 [US1] Add validation and wrap errors with `fmt.Errorf("...: %w", err)`
- [ ] T022 [US1] Add `log/slog` records for user story 1, on stderr, recording only what the reader can act on

**Checkpoint**: At this point, User Story 1 should be fully functional and testable independently

---

## Phase 4: User Story 2 - [Title] (Priority: P2)

**Goal**: [Brief description of what this story delivers]

**Independent Test**: [How to verify this story works on its own]

### Tests for User Story 2 (MANDATORY — write first, watch them fail) ⚠️

- [ ] T023 [P] [US2] Domain test for [behaviour] in internal/[domain]/[domain]_test.go (`package [domain]_test`)
- [ ] T024 [P] [US2] Command test for arg parsing, exit codes, and the stdout/stderr split in internal/cli/[cmd]_test.go

### Implementation for User Story 2

- [ ] T025 [P] [US2] Create [Entity] in internal/[domain]/[entity].go
- [ ] T026 [US2] Implement [operation] in internal/[domain]/[operation].go, taking `ctx context.Context`
- [ ] T027 [US2] Wire the thin command in internal/cli/[cmd].go
- [ ] T028 [US2] Integrate with User Story 1 components (if needed)

**Checkpoint**: At this point, User Stories 1 AND 2 should both work independently

---

## Phase 5: User Story 3 - [Title] (Priority: P3)

**Goal**: [Brief description of what this story delivers]

**Independent Test**: [How to verify this story works on its own]

### Tests for User Story 3 (MANDATORY — write first, watch them fail) ⚠️

- [ ] T029 [P] [US3] Domain test for [behaviour] in internal/[domain]/[domain]_test.go (`package [domain]_test`)
- [ ] T030 [P] [US3] Command test for arg parsing, exit codes, and the stdout/stderr split in internal/cli/[cmd]_test.go

### Implementation for User Story 3

- [ ] T031 [P] [US3] Create [Entity] in internal/[domain]/[entity].go
- [ ] T032 [US3] Implement [operation] in internal/[domain]/[operation].go, taking `ctx context.Context`
- [ ] T033 [US3] Wire the thin command in internal/cli/[cmd].go

**Checkpoint**: All user stories should now be independently functional

---

[Add more user story phases as needed, following the same pattern]

---

## Phase N: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories

- [ ] TXXX [P] Document exit codes and configuration precedence in `--help` and README.md
- [ ] TXXX [P] Embed any runtime asset with `//go:embed` so the binary stands alone
- [ ] TXXX Run `go generate ./...`, confirm no diff, and commit every generated file
- [ ] TXXX Verify `task lint` (golangci-lint v2) passes
- [ ] TXXX Verify `task test` (`go test -count=2 -race ./...`) passes
- [ ] TXXX Verify `govulncheck ./...` passes
- [ ] TXXX Confirm a `CGO_ENABLED=0 -trimpath` build produces a single static binary
- [ ] TXXX Confirm destructive actions refuse to run without `--yes` or an interactive confirmation
- [ ] TXXX Confirm no credentials appear in the repository, in logs, or in error messages
- [ ] TXXX Run quickstart.md validation

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3+)**: All depend on Foundational phase completion
  - User stories can then proceed in parallel (if staffed)
  - Or sequentially in priority order (P1 → P2 → P3)
- **Polish (Final Phase)**: Depends on all desired user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational (Phase 2) - No dependencies on other stories
- **User Story 2 (P2)**: Can start after Foundational (Phase 2) - May integrate with US1 but should be independently testable
- **User Story 3 (P3)**: Can start after Foundational (Phase 2) - May integrate with US1/US2 but should be independently testable

### Within Each User Story

- Tests MUST be written and MUST FAIL before implementation (Principle IV, non-negotiable)
- Domain types before domain operations
- Domain operations before the command that calls them
- Core implementation before integration
- Story complete before moving to next priority

### Parallel Opportunities

- All Setup tasks marked [P] can run in parallel
- All Foundational tasks marked [P] can run in parallel (within Phase 2)
- Once Foundational phase completes, all user stories can start in parallel (if team capacity allows)
- All tests for a user story marked [P] can run in parallel
- Models within a story marked [P] can run in parallel
- Different user stories can be worked on in parallel by different team members

---

## Parallel Example: User Story 1

```bash
# Launch all tests for User Story 1 together (they must all fail first):
Task: "Domain test for [behaviour] in internal/[domain]/[domain]_test.go"
Task: "Command test for arg parsing and usage errors in internal/cli/[cmd]_test.go"
Task: "Output-contract test for the stdout/stderr split in internal/cli/[cmd]_test.go"

# Launch all domain types for User Story 1 together:
Task: "Create [Entity1] in internal/[domain]/[entity1].go"
Task: "Create [Entity2] in internal/[domain]/[entity2].go"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL - blocks all stories)
3. Complete Phase 3: User Story 1
4. **STOP and VALIDATE**: Test User Story 1 independently
5. Deploy/demo if ready

### Incremental Delivery

1. Complete Setup + Foundational → Foundation ready
2. Add User Story 1 → Test independently → Deploy/Demo (MVP!)
3. Add User Story 2 → Test independently → Deploy/Demo
4. Add User Story 3 → Test independently → Deploy/Demo
5. Each story adds value without breaking previous stories

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup + Foundational together
2. Once Foundational is done:
   - Developer A: User Story 1
   - Developer B: User Story 2
   - Developer C: User Story 3
3. Stories complete and integrate independently

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Verify tests fail before implementing — a test that never failed proves nothing
- Tests are black box (`package <pkg>_test`); reach internals through `export_test.go` only
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Avoid: vague tasks, same file conflicts, cross-story dependencies that break independence
