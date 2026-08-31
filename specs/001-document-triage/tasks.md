# Tasks: Tri, nommage et archivage d'un document

**Input**: Design documents from `/specs/001-document-triage/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md),
[data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: Tests are MANDATORY (Constitution Principle IV, Test-First). Every phase writes its
failing tests before its implementation tasks, and each test must be confirmed to fail for the
intended reason. Tests live in `package <pkg>_test`; internals a test needs are reached through
`export_test.go`, never by moving the test back into the package.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: `[US1]`…`[US4]`, mapping to the user stories in spec.md
- Every task names the exact file it touches

## Path Conventions

- **Entry point**: `cmd/tabularium/main.go` — exit codes 0/1/2 and the signal-cancelled context
- **Command layer**: `internal/cli/` — thin: parse, validate, call, format
- **Domain logic**: `internal/<domain>/` — imports no CLI package
- **Tests**: beside the code they test, in `package <pkg>_test`

Module path: `github.com/sgaunet/tabularium` (research.md D17).

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Bring the pre-implementation repository to a state where `task build`, `task test`
and `task lint` run against real Go code.

- [X] T001 Initialise the module in `go.mod`: `go mod init github.com/sgaunet/tabularium`, with the `go` and `toolchain` directives pinned to the 1.26.1 toolchain declared in `mise.toml` (Principle VII)
- [X] T002 [P] Add the two author-approved direct dependencies to `go.mod`/`go.sum`: `github.com/goccy/go-yaml@v1.19.2` (MIT) and `golang.org/x/text@v0.41.0` (BSD-3-Clause); no other direct dependency may be added without prior approval (Principle VI, research.md "Approved dependencies")
- [X] T003 [P] Create the package skeleton — `cmd/tabularium/` and `internal/{cli,config,document,extract,chat,analysis,naming,rules,filing,sidecar,archiver,triage}/` — each with a `doc.go` stating the package's one job; no `utils`/`helpers`/`common`/`base` (Principle III)
- [X] T004 [P] Fix the linker flags in `.goreleaser.yaml` to fully-qualified package paths: `-X github.com/sgaunet/tabularium/internal/cli.Version={{ .Version }}` and likewise for `Commit` and `Date`; the current `-X internal/cli.Version=…` is silently ignored and every release would print an empty `--version` (research.md D17)
- [X] T005 [P] Run `task dev:install-pre-commit` (per `.pre-commit-config.yaml`; `task` refuses to run without the hook) and confirm `task build`, `task test`, `task lint` are green on the skeleton
- [X] T006 [P] Create the shared document corpus in `testdata/` — a born-digital PDF with a text layer, a scanned PDF without one, a multi-page PDF above the page cap, a JPEG, a PNG, a little-endian and a big-endian TIFF, a `.docx`, an `.odt`, a legacy `.doc`, a Markdown file, and one unsupported binary — with a `testdata/README.md` recording how each was produced

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The CLI shell, the output contract, and configuration. Every user story runs through
`cli.Run` and reads a validated `config.Config`, so nothing below can start until this is done.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

### Tests for the foundation (MANDATORY — write first, watch them fail) ⚠️

- [X] T007 [P] End-to-end exit-code test in `cmd/tabularium/main_test.go`: build the real binary, assert `--help` → `0`, `--version` → `0` with non-empty output, no argument → `2`, two arguments → `2` (FR-001, FR-068; contracts/cli.md)
- [X] T008 [P] Flag-parsing test in `internal/cli/flags_test.go`: `flag.ContinueOnError` is set, both `-flag` and `--flag` and `--flag=value` parse, `flag.ErrHelp` maps to exit `0` not `2`, and `--quiet` with `--verbose` is a `*cli.UsageError` (contracts/cli.md "Interactions")
- [X] T009 [P] Help-text test in `internal/cli/usage_test.go`: the `--help` output contains the three exit codes, the verbatim precedence line `flags > environment > config file > defaults`, and the host tools `pdftotext`, `pdftoppm`, `libreoffice` (FR-013, FR-068, FR-072)
- [X] T010 [P] Stream-split test in `internal/cli/run_test.go`: `Run(ctx, args, stdout, stderr)` writes no diagnostic to the stdout writer, `log/slog` records land on the stderr writer, `--quiet` sets the level to Error and `--verbose` to Debug (FR-067, FR-071, SC-005)
- [X] T011 [P] Terminal-behaviour test in `internal/cli/term_test.go`: colour and progress are emitted only when stdout reports `ModeCharDevice` **and** `NO_COLOR` is unset — conjunction, not disjunction (FR-070)
- [X] T012 [P] Config-loading test in `internal/config/load_test.go`: `yaml.Strict()` turns an unknown key into a usage error at startup; resolution order defaults → file → env → flags; a flag left off the command line does not overwrite a configured value, because the flag layer is applied with `FlagSet.Visit` (FR-072, research.md D14)
- [X] T013 [P] Redaction test in `internal/config/redact_test.go`: every credential-bearing field returns `"[redacted]"` from `String()`, `MarshalJSON` and `MarshalYAML`, including when the whole `Config` is formatted with `%v` (FR-073, SC-008)
- [X] T014 [P] Config-validation test in `internal/config/validate_test.go`: the type vocabulary must declare a `fallback` that is a member of `values`; a second unconditional rule is a usage error; a malformed path template is a usage error at load, before any document is read (FR-029, FR-038, research.md D9)
- [X] T015 [P] Rule-matching table test in `internal/rules/rules_test.go`: first match wins in file order; `tags` is set containment, not equality; a rule with no condition is the default; `missingkey=error` and an empty rendered path segment both fail loudly rather than producing `factures//2025`; a document carrying the vocabulary's fallback type, or a tag phrased differently from what a rule expects, falls to the default rule and never to an invented one (FR-037, FR-038, FR-039, SC-013)

### Implementation for the foundation

- [X] T016 [P] `internal/cli/usage.go` — the `UsageError` type and the hand-written `--help` block reproduced verbatim from contracts/cli.md, including exit codes, precedence, and host tools
- [X] T017 [P] `internal/config/config.go` — `Config`, `Vocabulary{Values, Fallback}`, and a distinct credential type whose `String`/`MarshalJSON`/`MarshalYAML` return `"[redacted]"` (data-model.md; FR-073)
- [X] T018 [P] `internal/rules/rules.go` — `Rule{Name, Type, Tags, Correspondent, Path *template.Template}`, `PathContext` with exactly `.Type .Correspondent .Reference .Title .Year .Month .Day .Tags` and deliberately **no** `.Folder`, `Match` (linear scan, first match wins) and `Render` (`text/template`, `missingkey=error`, empty-segment check) — pure, no I/O, no clock (FR-039, SC-014)
- [X] T019 `internal/config/load.go` — `goccy/go-yaml` with `yaml.Strict()`, config path from `$XDG_CONFIG_HOME/tabularium/config.yaml` falling back to `os.UserConfigDir()`, and the four-layer merge with the flag layer applied via `FlagSet.Visit` (depends on T017)
- [X] T020 `internal/config/default.yaml` embedded with `//go:embed` in `internal/config/config.go` — the starter configuration, kept identical in substance to [contracts/config.example.yaml](./contracts/config.example.yaml) so the binary never depends on a file beside it (Principle VII)
- [X] T021 `internal/config/validate.go` — fallback membership, at most one default rule, every rule path template parsed at load, `archive_root` resolved; every failure is a `*cli.UsageError`-mapped error wrapped with `%w` (depends on T018, T019)
- [X] T022 `internal/cli/flags.go` — the `flag.FlagSet` with `ContinueOnError` and the eleven flags of contracts/cli.md, plus the `--quiet`/`--verbose` conflict check
- [X] T023 `internal/cli/run.go` — `Run(ctx context.Context, args []string, stdout, stderr io.Writer) error`; `slog.TextHandler` on the stderr writer at the level `--quiet`/`--verbose` select; the `NO_COLOR`-and-TTY gate; no package below reaches for `os.Stdout` (depends on T016, T022)
- [X] T024 `cmd/tabularium/main.go` — `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)`, `cli.Run(...)`, `errors.As(err, &*cli.UsageError)` → exit `2`, any other error → `1`, `nil` → `0`; the `Version`, `Commit`, `Date` vars the ldflags of T004 target live in `internal/cli` (FR-068, FR-074; depends on T023)

**Checkpoint**: the binary builds, parses flags, prints a contract-conforming `--help`, loads and
validates configuration, and exits with the right code. User stories can begin.

---

## Phase 3: User Story 1 — Comprendre un document et voir où il irait (Priority: P1) 🎯 MVP

**Goal**: `tabularium --dry-run <file>` reports the recognised text, the inferred metadata, the
computed filename, the destination path and the rule that produced it — with **zero bytes written**.

**Independent Test**: run it on a scanned PDF and on a born-digital PDF; both are described
correctly, the born-digital one costs zero vision calls, and `find … | xargs shasum` over the
archive tree is byte-identical before and after (spec US1 Independent Test, SC-001, SC-009).

### Tests for User Story 1 (MANDATORY — write first, watch them fail) ⚠️

- [X] T025 [P] [US1] Content-sniffing table test in `internal/document/document_test.go`: PDF, PNG, JPEG, WebP, little- and big-endian TIFF, OOXML, ODF, legacy OLE2, plain text and Markdown are each detected from content and never from the extension; an unsupported file yields a usage error **naming the detected type**; a directory, a device and an unreadable file are usage errors; `Size` and `Digest` are correct (FR-002, FR-003, FR-004, FR-005, SC-007)
- [X] T026 [P] [US1] PDF extraction test in `internal/extract/pdf_test.go`: a text layer is used and no vision call is made; fewer than 64 non-whitespace runes (threshold reached through `internal/extract/export_test.go`) forces rasterisation; the page cap drops the excess and records a non-nil `Truncation`; a missing `pdftotext` or `pdftoppm` produces an error naming the binary and `poppler-utils` (FR-006, FR-007, FR-008, FR-013, SC-001, SC-010)
- [X] T027 [P] [US1] Office extraction test in `internal/extract/office_test.go`: text is recovered from `.docx`/`.xlsx`/`.pptx` and `.odt`/`.ods`/`.odp` with no vision call; an archive declaring more than 1024 entries is refused; a zip bomb aborts once the **cumulative bytes actually read** cross 64 MiB; a nested archive is never entered; a legacy OLE2 file routes to `libreoffice` and names it when absent (FR-010, FR-014)
- [X] T028 [P] [US1] Direct-path extraction tests in `internal/extract/plain_test.go` and `internal/extract/image_test.go`: text and Markdown are read directly with `Origin` `plain`; a raster image goes straight to the multimodal model with `Origin` `vision` (FR-009, FR-011)
- [X] T029 [P] [US1] Empty-extraction test in `internal/extract/extract_test.go`: a document from which no text was recovered is an explicit runtime failure and is not filed (FR-015)
- [X] T030 [P] [US1] HTTP client test in `internal/chat/client_test.go` against `httptest.Server`: the `Authorization` header appears only when a credential is configured; an image is sent as a `data:image/png;base64,…` `image_url` content part; `http.Client.Timeout` is set; cancelling the context aborts an in-flight request; retries are bounded at 3 with 1s/2s/4s jittered backoff on connection errors, 429 and 5xx, and **never** on any other 4xx (classifier reached through `internal/chat/export_test.go`) (FR-012, FR-075, FR-076)
- [X] T031 [P] [US1] Response-validation table test in `internal/analysis/analysis_test.go`: prose, a fenced code block, and an object with an unknown field are all rejected by `DisallowUnknownFields`; an absent required field is distinguished from an empty one; a `type` outside the configured vocabulary is rejected locally even when the transport accepted it; `tags` are checked when the configuration bounds them (FR-018, FR-022, FR-025, FR-026, FR-028, FR-030, SC-011, SC-012)
- [X] T032 [P] [US1] Normalisation table test in `internal/analysis/normalise_test.go`: a date not matching `YYYY-MM-DD` is dropped and never coerced; tags are lowercased and deduplicated with stable order; `amount` and `currency` are kept only as a pair and either alone drops both; the decimal-string constructor rejects anything but `-?\d+(\.\d+)?` (FR-020, FR-021, FR-031)
- [X] T033 [P] [US1] Schema-constraint test in `internal/analysis/schema_test.go` (with `internal/chat`): the request carries `response_format.json_schema` with `strict: true` and the configured vocabulary as the `type` enum; a server answering 400 mentioning `json_schema`, a 404/501, or 2xx prose on every bounded attempt each yield a **usage error naming the base URL and model**, not an intermittent runtime failure (FR-023, FR-024, FR-027, research.md D7)
- [X] T034 [P] [US1] Naming table test in `internal/naming/naming_test.go`: NFD → strip `unicode.Mn` → NFC transliteration, lowercasing, `[^a-z0-9]+` collapsed to `-`, trimmed, capped at 120 bytes on a rune boundary; the `YYYY-MM-DD-` prefix appears only when a document date survived validation and today's date is never substituted; the original extension is preserved; a proposal that sanitises to nothing keeps the original base name with `FromProposal=false`; `../../etc`, `/`, `\` and `.` cannot survive (FR-032, FR-033, FR-034, FR-035, FR-036)
- [X] T035 [P] [US1] Plan test in `internal/triage/plan_test.go`: building a `Plan` writes nothing — the tree is byte-identical before and after — and `Dest` is relative to the archive root, never absolute; no matching rule and no default rule is a runtime failure naming the document, with nothing moved (FR-040, FR-043, SC-009)
- [X] T036 [P] [US1] Output-contract test in `internal/cli/output_test.go`: text mode is a borderless `text/tabwriter` table with SOURCE, DEST, TYPE, TAGS; `--dry-run` additionally prints the applied rule and the full metadata; JSON mode emits one object validating against [contracts/output.schema.json](./contracts/output.schema.json) with `version: 1`, `truncation` present-and-null when nothing was dropped, and `SetEscapeHTML(false)` (FR-043, FR-064, FR-065, FR-066)
- [X] T037 [US1] End-to-end test in `cmd/tabularium/main_test.go`: `--dry-run` on a born-digital PDF exits `0`, stdout parses under a strict JSON decoder with nothing else on it, diagnostics are on stderr, and the archive tree is byte-identical afterwards (SC-005, SC-009)

### Implementation for User Story 1

- [X] T038 [P] [US1] `internal/document/document.go` — `Source{Path, Type Kind, Size, Digest [32]byte, Ext}`; `http.DetectContentType` over the first 512 bytes, preceded by the TIFF magic check (`II*\x00`, `MM\x00*`) that the stdlib table lacks, then ZIP disambiguation by opening the central directory (`mimetype` first member ⇒ ODF, `[Content_Types].xml` ⇒ OOXML, neither ⇒ unsupported) and the OLE2/CFB signature check; `Mode().IsRegular()` guard (research.md D2)
- [X] T039 [P] [US1] `internal/extract/extract.go` — `Text{Content, Origin, Truncation *Truncation}`, the four `Origin` constants, and strategy dispatch on `document.Kind`
- [X] T040 [US1] `internal/extract/pdf.go` with `internal/extract/export_test.go` — `pdftotext -q -l <cap> <src> -`, the 64-non-whitespace-rune emptiness threshold exported for the test, `pdftoppm -png -r <dpi> -f N -l N` per page into a scratch directory, one vision call per page concatenated in page order, the page cap recorded as `Truncation`, and `exec.LookPath` before first use (depends on T039, T043)
- [X] T041 [P] [US1] `internal/extract/office.go` — `archive/zip` + `encoding/xml` streaming `word/document.xml`, `xl/sharedStrings.xml` and `xl/worksheets/*.xml`, `ppt/slides/slide*.xml`, and ODF `content.xml`; the three independent bounds (1024 entries, 64 MiB cumulative on bytes *read* rather than declared, depth fixed at 1); `libreoffice --headless --convert-to txt:Text` for OLE2 (research.md D4; depends on T039)
- [X] T042 [P] [US1] `internal/extract/plain.go` and `internal/extract/image.go` — direct read for text and Markdown; base64 `image_url` submission for raster images (depends on T039, T043)
- [X] T043 [P] [US1] `internal/chat/client.go` with `internal/chat/export_test.go` — `POST {base_url}/chat/completions` over an `*http.Client` with an explicit `Timeout`, `ctx` threaded through, bounded jittered retry, and the retry classifier exported for the test; two independently constructed clients so OCR and analysis are configured separately (FR-012, FR-017)
- [X] T044 [P] [US1] `internal/chat/schema.go` — `response_format.json_schema` construction with `strict: true` and the configured vocabulary substituted into the `type` enum, per [contracts/analysis.schema.json](./contracts/analysis.schema.json)
- [X] T045 [US1] `internal/analysis/metadata.go` — `Metadata` with pointer-typed `DocumentDate`/`DueDate`/`Amount` so absent stays distinct from empty, a `Date{Year, Month, Day}` value type parsed with `time.Parse(time.DateOnly, …)`, and a validated decimal-string type for money — never a `float64`; the eleven fields of contracts/analysis.schema.json and not one of them names a folder (FR-016, data-model.md)
- [X] T046 [US1] `internal/analysis/analysis.go` — the text-only prompt, `json.Decoder` with `DisallowUnknownFields`, presence checks, vocabulary checks, the normalisation pipeline, bounded retries, and the usage error for an endpoint that will not honour the constraint (depends on T043, T044, T045)
- [X] T047 [P] [US1] `internal/naming/naming.go` — `Name{Base, FromProposal}` built by the `golang.org/x/text` pipeline of research.md D8
- [X] T048 [US1] `internal/triage/plan.go` and `internal/triage/triage.go` — sniff → extract → analyse → name → match, producing a `Plan{Source, Text, Metadata, Name, Rule, Dest, Disposition}` with **no side effects**; `--dry-run` stops here, which is what makes FR-043 structural (depends on T038, T039, T046, T047, T018)
- [X] T049 [US1] `internal/cli/output_text.go` — the borderless `text/tabwriter` table and the `--dry-run` detail block naming the applied rule (depends on T048)
- [X] T050 [US1] `internal/cli/output_json.go` — the single object of contracts/output.schema.json with `SetEscapeHTML(false)` (depends on T048)
- [X] T051 [US1] `internal/cli/run.go` — wire `--dry-run`, `--no-analysis` (text only, implying no filing and no archiving), `--output`, and map each failure to a usage error or a runtime error per contracts/cli.md; errors wrapped with `fmt.Errorf("…: %w", err)` and logged on stderr (FR-019, FR-064, FR-067, FR-068; depends on T049, T050)

**Checkpoint**: User Story 1 is fully functional — the tool identifies any supported document and
prints its plan without touching the filesystem. This is the MVP.

---

## Phase 4: User Story 2 — Classer le document dans l'arborescence (Priority: P2)

**Goal**: the document is moved into the archive tree at the path the first matching rule dictates,
atomically, with a sidecar recording what happened.

**Independent Test**: configure two rules and a default, run on three documents of different types,
and verify each lands under the path the first matching rule dictates and the scan box is empty
(spec US2 Independent Test, SC-002).

**Depends on**: User Story 1 — a `Plan` is what filing executes (spec: "Il dépend entièrement de
l'histoire 1").

### Tests for User Story 2 (MANDATORY — write first, watch them fail) ⚠️

- [X] T052 [P] [US2] Confinement and atomicity test in `internal/filing/filing_test.go`: a `..` traversal and a symlink pointing outside the root both fail at the `os.Root` operation, not at a string check; intermediate directories are created; the write is temp-then-`Sync`-then-`Rename` so no partial file is ever visible under its final name (FR-041, FR-045, FR-046, SC-012)
- [X] T053 [P] [US2] Cross-device test in `internal/filing/exdev_test.go`: a `Rename` returning `EXDEV` (matched with `errors.Is(err, syscall.EXDEV)`) falls back to copy → `Sync` → SHA-256 verification → rename, and the source is unlinked **only after** the copy verifies (FR-044)
- [X] T054 [P] [US2] Collision test in `internal/filing/collision_test.go`: an occupied destination is compared by size first and then by SHA-256; identical content is a duplicate — nothing written, reported on stderr, exit `0`; different content gets `-2`, `-3`, … claimed with `O_CREAT|O_EXCL`; two concurrent claims on the same name resolve to one winner and one `-2` with no lost file (FR-048, FR-049, FR-050, FR-069, SC-003, research.md D16)
- [X] T055 [P] [US2] Interrupt test in `internal/filing/interrupt_test.go`: cancelling the context mid-copy leaves no `.tabularium-*` temp file, no file under the final name, and the source intact (FR-047, FR-074, SC-006)
- [X] T056 [P] [US2] Sidecar-write test in `internal/sidecar/write_test.go`: the written `<file>.tabularium.json` validates against [contracts/sidecar.schema.json](./contracts/sidecar.schema.json), is produced on **every** successful filing rather than only on failure, and is itself written temp-then-rename (FR-051)
- [X] T057 [P] [US2] Disposition test in `internal/triage/file_test.go`: `move` is the default, `copy` leaves the source, `keep` writes nothing at the source end, and `--dry-run` overrides all three unconditionally (FR-042, FR-043)
- [X] T058 [US2] End-to-end test in `cmd/tabularium/main_test.go`: a filed document lands at the rule's path with the date prefix taken from the *document's* date, the source is gone, the sidecar sits beside it, and JSON output reports `action: "filed"` with the rule name (FR-034, FR-037, FR-066, SC-002)

### Implementation for User Story 2

- [X] T059 [P] [US2] `internal/filing/filing.go` — `os.OpenRoot(archiveRoot)` once, then `(*os.Root).MkdirAll`/`Create`/`Rename` only; write to `.tabularium-<random>.tmp` **in the destination directory**, `Sync`, `Close`, rename; a `defer` removes the temp file on every error path including cancellation (research.md D10)
- [X] T060 [US2] `internal/filing/filing.go` — the `EXDEV` branch: copy to temp, `Sync`, verify the SHA-256 of what was written, rename, and only then remove the source (depends on T059)
- [X] T061 [P] [US2] `internal/filing/collision.go` — the size guard, then `io.Copy(sha256.New(), f)` on both files, then duplicate-or-suffix with the `O_CREAT|O_EXCL` probe that claims the name atomically (research.md D11)
- [X] T062 [P] [US2] `internal/sidecar/sidecar.go` — the `Sidecar` type of data-model.md and `Write`, using the same temp-plus-rename dance (FR-051, FR-057)
- [X] T063 [US2] `internal/triage/triage.go` — execute a `Plan`: apply the disposition, file through `internal/filing`, write the sidecar, and return the outcome; filing stays a separate package so the partial-failure composition of US3 remains one readable decision (depends on T059, T060, T061, T062)
- [X] T064 [US2] `internal/cli/run.go`, `internal/cli/output_text.go` and `internal/cli/output_json.go` — the `filed`, `duplicate` and `not-filed` actions in both formats, filing on by default whenever it is configured and `--no-file` honoured, and the duplicate notice on **stderr** while the exit code stays `0` (FR-049, FR-061, FR-062, FR-069; depends on T063)

**Checkpoint**: User Stories 1 and 2 both work. Documents are named, filed and traced.

---

## Phase 5: User Story 3 — Transmettre le document à un archivage externe (Priority: P3)

**Goal**: after filing, the inferred metadata and the archived path are handed to a configured
external command, which the tool treats as a black box.

**Independent Test**: point `archive.command` at a script that records its argv, run on one
document, and verify each metadata field and each tag arrives as its own argument and the result is
recorded (spec US3 Independent Test).

**Depends on**: User Story 2 — the hand-off carries the archived path.

### Tests for User Story 3 (MANDATORY — write first, watch them fail) ⚠️

- [X] T065 [P] [US3] Argv test in `internal/archiver/archiver_test.go`: each configured argument is its own `text/template`, rendered separately and passed as a distinct element of `Cmd.Args` with no shell anywhere; `args_each_tag` expands to one argument per tag; a command not on `PATH` produces an error **naming it** (FR-054, FR-055, FR-060)
- [X] T066 [P] [US3] Credential test in `internal/archiver/env_test.go`: configured credentials appear in `Cmd.Env` — built explicitly from `os.Environ()` plus the named variables — and never in `Cmd.Args` (FR-058, FR-073, SC-008)
- [X] T067 [P] [US3] Process-group test in `internal/archiver/timeout_unix_test.go` (`//go:build unix`): on timeout the whole process group is killed, so a backgrounded grandchild does not survive; `Cmd.WaitDelay` bounds a child that holds the pipes open (FR-056)
- [X] T068 [P] [US3] Result test in `internal/archiver/result_test.go`: a non-zero exit code is a failure of the step; combined stdout and stderr are captured verbatim and truncated at 8 KiB; nothing is parsed for an identifier and no output format is presumed (FR-057, FR-059)
- [X] T069 [P] [US3] Partial-failure test in `internal/triage/archive_test.go`: a successful filing followed by an archiver failure leaves the file where it was filed, logs the error on stderr, and returns a runtime failure (exit `1`); `--no-archive` skips the step entirely and reports `archive: null` (FR-062, FR-063)
- [X] T070 [US3] End-to-end test in `cmd/tabularium/main_test.go` with a fake archiver script recording its argv: every tag arrives on its own line, the token arrives from the environment and never from argv, and a failing archiver exits `1` with the document still filed

### Implementation for User Story 3

- [X] T071 [P] [US3] `internal/archiver/archiver.go` — `ArchiveResult{Attempted, Success, ExitCode, Output, Err}`, `exec.LookPath`, `exec.CommandContext`, per-argument template rendering, `Cmd.Env` assembled explicitly, and 8 KiB output truncation (data-model.md; research.md D13)
- [X] T072 [P] [US3] `internal/archiver/proc_unix.go` — `//go:build unix`: `SysProcAttr{Setpgid: true}`, a `Cmd.Cancel` sending `SIGKILL` to the negated PID, and `Cmd.WaitDelay`
- [X] T073 [P] [US3] `internal/archiver/proc_windows.go` — `//go:build windows`: a job object via `CREATE_NEW_PROCESS_GROUP`, so the `windows` targets in `.goreleaser.yaml` still compile
- [X] T074 [US3] `internal/sidecar/sidecar.go` — record the `archive` object: `attempted`, `success`, `exit_code`, `output`, `error`, `attempted_at`, per contracts/sidecar.schema.json (depends on T071)
- [X] T075 [US3] `internal/triage/triage.go` — compose filing and archiving so FR-063 is one readable decision: file, hand off, and on hand-off failure return a runtime error without unwinding the filing (depends on T071, T074)
- [X] T076 [US3] `internal/cli/output_json.go` and `internal/cli/output_text.go` — the `archive` object in JSON output, null when the archiver is unconfigured or disabled (FR-066; depends on T075)

**Checkpoint**: all three primary stories work; the hand-off is recorded whether it succeeded or not.

---

## Phase 6: User Story 4 — Rattraper un archivage externe qui a échoué (Priority: P4)

**Goal**: re-running on an already-filed document replays only the missing hand-off — no OCR, no
analysis, no move.

**Independent Test**: fail the archiver deliberately, confirm the document is filed and the exit
code is `1`, then re-run with a working archiver and confirm no recognition is redone and no file
moves (spec US4 Independent Test, SC-004).

**Depends on**: User Story 3 — the sidecar's archive state is what makes re-entry decidable.

### Tests for User Story 4 (MANDATORY — write first, watch them fail) ⚠️

- [X] T077 [P] [US4] Sidecar-read test in `internal/sidecar/read_test.go`: a sidecar whose `source_digest` matches the accompanying file's bytes is honoured; an unparseable file, an unknown `version`, and a digest mismatch are each treated as **absent**, with the anomaly logged on stderr (FR-053)
- [X] T078 [P] [US4] Re-entry test in `internal/triage/resume_test.go`: with a matching sidecar recording a failed hand-off, extraction, analysis and filing are all skipped and only the archiver runs; with success recorded, nothing runs at all and the exit code is `0` (FR-052, SC-004)
- [X] T079 [US4] End-to-end test in `cmd/tabularium/main_test.go`: fail the archiver, re-run on the filed document and assert zero model requests, zero file moves and exit `0`; a third run does nothing at all

### Implementation for User Story 4

- [X] T080 [US4] `internal/sidecar/sidecar.go` — `Read`: parse, check the schema version, and compare `source_digest` against the accompanying file's bytes; the bytes always win over the sidecar (depends on T062)
- [X] T081 [US4] `internal/triage/triage.go` — the re-entry branch taken before sniffing: honour a valid sidecar, replay only a missing hand-off, and reprocess from the start when the sidecar is treated as absent (depends on T080, T075)
- [X] T082 [US4] `internal/cli/run.go` — report the replay-only outcome in both output formats, and log the sidecar anomaly on stderr when one is discarded (depends on T081)

**Checkpoint**: all four user stories are independently functional.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: documentation the code now contradicts, and the constitution's quality gate.

- [X] T083 [P] Update `README.md` — add `libreoffice` beside `poppler-utils` in the host-tool list (research.md D4), and document the exit codes, the configuration precedence, and `xargs -n1` batching
- [X] T084 [P] Correct the cobra references to stdlib `flag` in `CLAUDE.md`, `docs/architecture.md` and `docs/patterns.md`, which the Phase 0 decision D1 has made wrong
- [X] T085 [P] Propose to the author a PATCH amendment to `.specify/memory/constitution.md` — Principle II's *rationale* names Cobra; the normative requirement is unaffected, so this is a wording change requiring approval, not a unilateral edit (plan.md follow-up 2)
- [X] T086 [P] Confirm `internal/config/default.yaml` still matches [contracts/config.example.yaml](./contracts/config.example.yaml) in substance, and that every comment explaining a requirement survived the embedding
- [X] T087 Run `go generate ./...` and `git diff --exit-code`: no diff, every generated file committed (Gate 4)
- [X] T088 Verify `task lint` (golangci-lint v2, per `.golangci.yml`) passes with no new disables (Gate 3)
- [X] T089 Verify `task test` (`go test -count=2 -race ./...`, per `Taskfile.yml`) passes (Gate 2)
- [ ] T090 Verify `govulncheck ./...` passes (Gate 5) — **does not pass**: 15 advisories, all in the standard library of the pinned Go 1.26.1, none in this code or its two dependencies. Fixed by a toolchain bump to go1.26.6 or later; the pin is the author's to change (Principle VII). Recorded in [quickstart.md](./quickstart.md#the-gate).
- [X] T091 Verify `task snapshot` builds every target in `.goreleaser.yaml` including `windows`, and that the resulting binary prints a **non-empty** `--version` — the check that proves T004 landed
- [X] T092 Walk the eleven scenarios of [quickstart.md](./quickstart.md) end to end and record the result
- [X] T093 Confirm SC-008 with the canary of quickstart.md scenario 9: `TABULARIUM_ANALYSIS_API_KEY='sk-canary-…'`, `--verbose`, zero matches across both streams, and zero matches in `ps -Ao args`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies — start immediately
- **Foundational (Phase 2)**: depends on Setup — **blocks every user story**
- **US1 (Phase 3)**: depends on Foundational. The MVP
- **US2 (Phase 4)**: depends on US1 — filing executes a `Plan`
- **US3 (Phase 5)**: depends on US2 — the hand-off carries the archived path
- **US4 (Phase 6)**: depends on US3 — re-entry is decided from the sidecar's archive state
- **Polish (Phase 7)**: depends on every story you intend to ship

The story chain is real, not incidental: the spec states US2 "dépend entièrement de l'histoire 1",
and US4 exists only to recover the failure mode US3 introduces. Each story is still independently
*testable* at its own checkpoint.

### Within Each Phase

- Tests are written first and confirmed to fail for the intended reason (Principle IV)
- Domain types before domain operations; domain operations before the CLI that calls them
- `export_test.go` only where a black-box test genuinely needs an internal — expected in
  `internal/extract` (the emptiness threshold) and `internal/chat` (the retry classifier)

### Key Cross-Package Dependencies

- T040, T042, T046 depend on T043 (`internal/chat`) — extraction and analysis both call the model
- T048 depends on T018 (`internal/rules`) and T038/T039/T046/T047
- T021 depends on T018 — config load parses and validates every rule template
- T063 depends on T059–T062; T075 depends on T071 and T074; T081 depends on T080 and T075

### Parallel Opportunities

- Every `[P]` task in Phase 1 after T001
- T007–T015: the whole foundational test suite in parallel (nine different files)
- T016, T017, T018 in parallel; then T019–T024 in dependency order
- T025–T036: twelve US1 test files in parallel — the largest parallel block in the plan
- T038, T039, T041, T042, T043, T044, T047 in parallel once T039's types exist
- T052–T057 in parallel; T065–T069 in parallel; T077–T078 in parallel
- T083–T086 in parallel

---

## Parallel Example: User Story 1

```bash
# All twelve US1 test files at once — they must all fail first:
Task: "Content-sniffing table test in internal/document/document_test.go"
Task: "PDF extraction test in internal/extract/pdf_test.go"
Task: "Office extraction test in internal/extract/office_test.go"
Task: "Direct-path extraction tests in internal/extract/plain_test.go and image_test.go"
Task: "Empty-extraction test in internal/extract/extract_test.go"
Task: "HTTP client test in internal/chat/client_test.go"
Task: "Response-validation table test in internal/analysis/analysis_test.go"
Task: "Normalisation table test in internal/analysis/normalise_test.go"
Task: "Schema-constraint test in internal/analysis/schema_test.go"
Task: "Naming table test in internal/naming/naming_test.go"
Task: "Plan test in internal/triage/plan_test.go"
Task: "Output-contract test in internal/cli/output_test.go"

# Then the independent domain implementations together:
Task: "internal/document/document.go — content sniffing"
Task: "internal/chat/client.go — the OpenAI-compatible client"
Task: "internal/naming/naming.go — the sanitisation pipeline"
Task: "internal/extract/office.go — OOXML/ODF with the three bounds"
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1 Setup — the module exists and the toolchain gates run
2. Phase 2 Foundational — **blocks everything**; do not start a story before its checkpoint
3. Phase 3 User Story 1
4. **STOP and VALIDATE**: quickstart.md scenarios 1, 2, 3 and 8
5. At this point the tool already earns its keep: it identifies a pile of anonymous scans and
   lets the rules be tuned before anything moves

### Incremental Delivery

1. Setup + Foundational → the shell is contract-conforming
2. + US1 → **MVP**: `--dry-run` describes any supported document (quickstart 1–3, 8)
3. + US2 → documents are filed and traced (quickstart 4, 5, 7, 10, 11)
4. + US3 → the external hand-off runs (quickstart 6, 9)
5. + US4 → a failed hand-off is recoverable with one re-run (quickstart 6)
6. + Polish → the six-command gate in quickstart.md all green

### Parallel Team Strategy

The story chain limits parallelism across stories, but not within them. With more than one pair of
hands, split Phase 3 by package — `document`+`extract` on one side, `chat`+`analysis` on the
other, `naming`+`rules`+output on a third — since those are seven independent packages behind one
`triage` integration point (T048).

---

## Notes

- `[P]` = different files, no dependency on an incomplete task
- Verify each test fails before implementing it — a test that never failed proves nothing
- Tests are black box (`package <pkg>_test`); reach internals through `export_test.go` only
- Wrap every error with `fmt.Errorf("…: %w", err)`; never flatten one into a string
- Every function doing I/O takes `ctx context.Context` as its first parameter
- No new direct dependency lands without the author's prior approval (Principle VI)
- Commit after each task or logical group; the pre-commit hook runs test, lint and build
- Stop at any checkpoint to validate the story independently
