# Implementation Plan: Tri, nommage et archivage d'un document

**Branch**: `001-document-triage` | **Date**: 2026-08-30 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/001-document-triage/spec.md`

## Summary

One document per invocation: sniff its type from content, extract its text (a PDF text
layer when there is one, a vision model when there is not, stdlib parsing for office and
plain text), infer metadata through a second schema-constrained model call, derive a
sanitised filename, file it by the first matching rule, and hand it to a configured
external command. 76 functional requirements, four prioritised user stories.

The technical approach that Phase 0 settled on, in one paragraph: **stdlib `flag`**, no
cobra, because there are no subcommands; a **hand-written `net/http` client** for the
OpenAI-compatible endpoints, because the tool makes two request shapes and reads one
response field; **hand-written validation** of a fixed, closed JSON schema, because
FR-025 requires the validator to be independent of the request builder and the schema has
no generality to exploit; **`os.Root`** for archive-root confinement, so FR-041 is enforced
by the kernel rather than by a string prefix check; and **two approved dependencies** —
`goccy/go-yaml` for configuration and `golang.org/x/text` for transliteration.

The design's load-bearing invariant is that **`Plan` is computed with no side effects**.
`--dry-run` stops there, so FR-043 and SC-009 hold structurally instead of depending on a
flag check inside the write path that someone could forget.

## Technical Context

**Language/Version**: Go 1.26.1, pinned in `mise.toml` and to be declared in `go.mod`.
Module path `github.com/sgaunet/tabularium`.

**Primary Dependencies**: two direct modules, both proposed to and approved by the author
before this plan (Principle VI):

| Module | Version | License | Purpose |
|---|---|---|---|
| `github.com/goccy/go-yaml` | v1.19.2 | MIT | configuration (FR-072) |
| `golang.org/x/text` | v0.41.0 | BSD-3-Clause | ASCII transliteration (FR-032) |

`gopkg.in/yaml.v3` was the alternative inside the same approval and is **rejected**: its
last release is v3.0.1 (2022) and the repository is archived, which fails Principle VI's
"actively maintained" clause. Everything else is standard library.

**Host tools** (runtime, not build): `pdftotext` and `pdftoppm` from poppler-utils for
PDFs; `libreoffice` for legacy `.doc`/`.xls`/`.ppt`. Each is looked up only when a document
of that kind is processed, reported by name when missing, and listed in `--help` (FR-013).

**Storage**: the local filesystem — an archive tree under a configured root, a JSON sidecar
beside each filed document, and one YAML config file. No database.

**Testing**: `go test -count=2 -race ./...` via `task test`; black-box `package <pkg>_test`
throughout; `cmd/tabularium/main_test.go` builds and invokes the real binary.

**Target Platform**: single static binary (`CGO_ENABLED=0`), linux/darwin/windows on
amd64/arm64 per `.goreleaser.yaml`. The archiver's process-group kill needs build-tagged
files so the `windows` targets still compile.

**Project Type**: single-purpose CLI.

**Performance Goals**: dominated by the model endpoint, not by this code. The one goal
worth stating is a negative: a PDF carrying a text layer must cost **zero** model calls
(SC-001), and a re-run on an already-filed document must cost zero model calls and zero
file moves (SC-004).

**Constraints**: every I/O carries an explicit timeout (FR-075); retries are bounded at 3
with 1s/2s/4s jittered backoff (FR-076); office extraction is bounded at 1024 entries,
64 MiB cumulative decompressed, depth 1 (FR-014); page processing is capped by
configuration and truncation is always reported (FR-008); the archiver's captured output is
truncated at 8 KiB (FR-057).

**Scale/Scope**: one document per process. Batching is `xargs -n1`, parallelism `xargs -P`
— safe by construction because destination names are claimed with `O_CREAT|O_EXCL`.

## Constitution Check

*GATE: evaluated before Phase 0 research, re-evaluated after Phase 1 design.*

**Initial evaluation (pre-Phase 0)**: PASS on I–V, VII, UX, Q. Gate VI was **BLOCKED**, not
failed: four questions — flag parser, config format, transliteration, and legacy office
support — each implied a dependency or a spec change that Principle VI and the plan
template forbid guessing at. They were put to the author, answered, and recorded in
[research.md](./research.md). Gate VI then passed.

**Post-Phase 1 re-evaluation**:

| # | Gate | Status | Notes |
|---|------|--------|-------|
| I | **Single purpose** | PASS | One job — one document in, filed and handed off. No subcommands. Batching and parallelism are delegated to `xargs`, so the exit code always describes exactly one document. |
| II | **Output contract** | PASS | stdout is data only, `--output=text\|json`; all diagnostics, the duplicate notice, and the truncation notice go to stderr. Enforced structurally: `cli.Run(ctx, args, stdout, stderr)` takes both writers, so no package can reach for `os.Stdout`. Exit codes 0/1/2 are mapped in `main` via `errors.As` on `*cli.UsageError`; `flag.ContinueOnError` is mandatory so `flag`'s own `os.Exit(2)` cannot bypass it. Contract: [contracts/cli.md](./contracts/cli.md). |
| III | **Thin commands** | PASS | `internal/cli` parses, validates, calls `triage.Run`, and formats. Eleven domain packages (`document`, `extract`, `chat`, `analysis`, `naming`, `rules`, `filing`, `sidecar`, `archiver`, `config`, `triage`) import no CLI package. No `utils`/`helpers`/`common`/`base`. |
| IV | **Test-first** | PASS | Every package is `package <pkg>_test`. `internal/rules`, `internal/naming`, and `internal/analysis` are pure functions and fully table-testable. Arg parsing, exit codes, and the stream split each get dedicated tests; `cmd/tabularium/main_test.go` builds the binary and asserts stdout, stderr, and the code together. `export_test.go` only where a black-box test genuinely needs an internal — expected in `extract` (the text-layer emptiness threshold) and `chat` (the retry classifier). |
| V | **Interruptible & bounded** | PASS | `signal.NotifyContext` in `main`; `ctx` is the first parameter of every function that does I/O. Timeouts: `http.Client.Timeout` per model call, `exec.Cmd` context plus `WaitDelay` for host tools and the archiver. Retries bounded at 3, backed off, and only on connection errors/429/5xx — a 4xx is a configuration error and is never retried. |
| VI | **Stdlib first** | PASS | Two direct dependencies, both author-approved, both permissively licensed. Five candidate dependencies were considered and rejected in favour of stdlib: cobra/pflag, an OpenAI SDK, a JSON Schema validator, a MIME sniffer, and a PDF library — each rejected in [research.md](./research.md) with its reason. |
| VII | **Reproducible binary** | PASS | `CGO_ENABLED=0 -trimpath` on the pinned toolchain; the starter config is embedded with `//go:embed`; targets stay in `.goreleaser.yaml`. **One defect to fix**: the ldflags there read `-X internal/cli.Version=…`, which is not a fully-qualified package path, so the linker silently ignores it and every release would report an empty `--version`. Must become `-X github.com/sgaunet/tabularium/internal/cli.Version=…` (research.md D17). |
| UX | **CLI behaviour** | PASS | `NO_COLOR` **and** non-TTY stdout both suppress colour and progress — conjunction, not disjunction. `--quiet`/`--verbose` present. Precedence is stated verbatim in `--help` and implemented with `FlagSet.Visit`, because reading flag variables directly would let unset flags overwrite configured values. Credentials come from the environment and every credential-bearing field is a distinct type whose `String`/`MarshalJSON`/`MarshalYAML` return `"[redacted]"`, so SC-008 is a property of the type rather than of reviewer discipline. **On `--yes`**: no confirmation gate is proposed, because this design contains no destructive action — an existing file is never overwritten (identical content is a duplicate and is skipped, different content gets a suffix — FR-048…FR-050), and a move unlinks the source only after the copy's digest is verified (FR-044). Gating the tool's sole purpose behind `--yes` would also break `xargs` composability, which Principle I and the governance tie-breaker both favour. |
| Q | **Quality gates** | PASS | `task test`, `task lint`, `go generate ./...` clean, and `govulncheck ./...` are the gate in [quickstart.md](./quickstart.md), alongside a `task snapshot` that must produce a binary printing a non-empty `--version`. |

No gate requires justification. **Complexity Tracking is empty.**

## Project Structure

### Documentation (this feature)

```text
specs/001-document-triage/
├── plan.md                      # This file
├── spec.md                      # 76 requirements, 4 user stories
├── research.md                  # Phase 0 — 17 decisions, all unknowns resolved
├── data-model.md                # Phase 1 — entities, validation, state transitions
├── quickstart.md                # Phase 1 — 11 validation scenarios + the gate
├── contracts/                   # Phase 1
│   ├── cli.md                   # flags, exit codes, streams, --help, archiver invocation
│   ├── config.example.yaml      # the configuration contract (embedded as the starter)
│   ├── analysis.schema.json     # sent to the model AND re-validated locally
│   ├── output.schema.json       # --output=json
│   └── sidecar.schema.json      # <file>.tabularium.json
├── checklists/requirements.md
└── tasks.md                     # Phase 2 — /speckit-tasks, NOT created here
```

### Source Code (repository root)

```text
cmd/tabularium/
├── main.go                  # signal-cancelled ctx; exit 0/1/2 via errors.As
└── main_test.go             # end-to-end: builds the binary, asserts both streams + code

internal/cli/                # thin: parse, validate, call, format. No business logic.
├── run.go                   # Run(ctx, args, stdout, stderr) error
├── flags.go                 # flag.ContinueOnError; FlagSet.Visit for precedence
├── usage.go                 # UsageError + the hand-written --help (exit codes, precedence)
├── output_text.go           # text/tabwriter, borderless (FR-065)
├── output_json.go           # contracts/output.schema.json (FR-066)
└── *_test.go                # arg parsing, exit codes, the stdout/stderr split

internal/config/             # load, merge (defaults→file→env→flags), validate
├── config.go                # types; credential fields redact in String/Marshal*
├── load.go                  # goccy/go-yaml, yaml.Strict()
├── default.yaml             # //go:embed starter config
└── *_test.go

internal/document/           # the source file: sniff by content, hash, stat
├── document.go              # DetectContentType + TIFF magic + zip/OLE2 disambiguation
└── *_test.go

internal/extract/            # text out of any supported format
├── extract.go               # strategy by Kind; Text{Content, Origin, Truncation}
├── pdf.go                   # pdftotext, then pdftoppm + vision per page
├── office.go                # archive/zip + encoding/xml; libreoffice for OLE2; bounded
├── image.go                 # straight to the vision model
├── plain.go                 # text and Markdown
├── export_test.go           # the text-layer emptiness threshold
└── *_test.go

internal/chat/               # OpenAI-compatible /chat/completions over net/http
├── client.go                # timeout, bounded jittered retry, ctx-aware
├── schema.go                # response_format.json_schema construction
├── export_test.go           # the retry classifier
└── *_test.go                # httptest.Server throughout

internal/analysis/           # Metadata: prompt, decode, validate, normalise
├── analysis.go              # DisallowUnknownFields; vocabulary check; normalisation
├── metadata.go              # the entity; Date and decimal types
└── *_test.go                # pure — table-driven

internal/naming/             # the filename (FR-032…FR-036)
├── naming.go                # x/text NFD→strip marks→NFC, collapse, cap, date prefix
└── *_test.go                # pure — table-driven

internal/rules/              # ordered rules, first match wins; path templates
├── rules.go                 # Match; PathContext with no .Folder field
└── *_test.go                # pure, deterministic — table-driven

internal/filing/             # the write side
├── filing.go                # os.Root; temp+fsync+rename; EXDEV copy-verify-unlink
├── collision.go             # size guard → SHA-256 → duplicate or -N suffix via O_EXCL
└── *_test.go

internal/sidecar/            # <file>.tabularium.json (FR-051…FR-053)
├── sidecar.go               # read/write; digest mismatch ⇒ treated as absent
└── *_test.go

internal/archiver/           # the external command (FR-054…FR-060)
├── archiver.go              # templated argv, no shell; env-carried credentials
├── proc_unix.go             # //go:build unix   — Setpgid, kill(-pgid)
├── proc_windows.go          # //go:build windows — job object
└── *_test.go

internal/triage/             # the pipeline; owns Plan
├── triage.go                # sniff→extract→analyse→plan→file→archive
├── plan.go                  # Plan — computed with NO side effects
└── *_test.go
```

**Structure Decision**: `cmd/tabularium` holds only exit-code and signal wiring.
`internal/cli` is the thin layer Principle III demands — it parses, validates, calls
`triage.Run`, and formats, and formatting is explicitly within what the principle allows a
command layer to do. Everything else is a domain package importing no CLI package, which
makes the mechanical test in `docs/patterns.md` hold: no package below `internal/cli`
needs a `cobra.Command` — or now, a `flag.FlagSet` — to be exercised.

Three packages are deliberately pure — `rules`, `naming`, `analysis` (validation and
normalisation) — with no I/O, no clock, and no network. That is what makes the spec's
determinism assumption testable, and it puts the requirements most likely to be wrong
(FR-020, FR-021, FR-031…FR-040) under exhaustive table tests rather than integration tests.

`filing` and `archiver` are separated from `triage` so that FR-063's partial-failure
behaviour — filed, archiver failed, exit 1, file stays put — is a composition decision in
one readable place rather than an error path threaded through the write logic.

## Follow-ups for `/speckit-tasks`

Not changed by this phase; recorded so they are not lost:

1. **`.goreleaser.yaml` ldflags** use `-X internal/cli.Version=…`, which the linker
   silently ignores. Must be fully qualified with the module path (research.md D17).
2. **Documentation drift**: `CLAUDE.md`, `docs/architecture.md`, and `docs/patterns.md`
   each state that commands are cobra wrappers. Phase 0 chose stdlib `flag` under the
   carve-out in Principle III. Principle II's *rationale* also names Cobra; its normative
   requirement is unaffected, but the constitution's wording deserves a PATCH amendment
   when the code lands.
3. **README** claims poppler as the only host tool. `libreoffice` joins it for legacy
   office formats (research.md D4).

## Complexity Tracking

No Constitution Check gate failed, and no violation needs justification. This table is
intentionally empty.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| *(none)* | | |
