# Phase 0 Research: Document Triage

**Feature**: `001-document-triage` | **Date**: 2026-08-30 | **Plan**: [plan.md](./plan.md)

Every NEEDS CLARIFICATION raised by the Technical Context is resolved below. Four of them
were dependency or scope questions the constitution forbids guessing at (Principle VI,
and FR-003's scope); they were put to the author and are recorded here as **approved**.

> **Language note.** The specification is written in French because the feature was
> described in French. These engineering artifacts are in English, matching the
> constitution, `docs/`, and the README. Identifiers, error messages, and `--help` text
> are English; the *contents* the tool produces (tags, titles) follow the document's own
> language, per the spec's assumptions.

---

## Approved dependencies

The whole feature lands on **two** direct module dependencies. Both were proposed to and
approved by the author before this plan was written, as Principle VI requires.

| Module | Version | License | Approved | Used for |
|---|---|---|---|---|
| `github.com/goccy/go-yaml` | v1.19.2 | MIT | yes | configuration file (FR-072) |
| `golang.org/x/text` | v0.41.0 | BSD-3-Clause | yes | ASCII transliteration (FR-032) |

Everything else — HTTP, JSON, zip, XML, templating, hashing, tabular output, process
control, path confinement — is standard library.

---

## D1. Flag parsing: standard library `flag`

**Decision**: `flag.NewFlagSet` in `internal/cli`. No cobra, no pflag. Zero dependencies.

**Rationale**: Tabularium has no subcommands — one positional argument and roughly a
dozen flags. Principle III names this case explicitly: "cobra, or stdlib `flag` when there
are no subcommands". Stdlib `flag` accepts both `-flag` and `--flag` spellings and
`--output=text`, which covers the whole surface FR-064/FR-071/FR-072 asks for. `--help`
is written by hand, which is a feature here: FR-068 and FR-072 require the exit codes and
the configuration precedence to be *stated* in it, and a hand-written usage block states
them where a generated one would not.

**Alternatives considered**: cobra + pflag (Apache-2.0). Rejected — two dependencies bought
for subcommand routing and completions that a single-purpose binary never uses.

**Consequence — documentation drift**: `CLAUDE.md`, `docs/architecture.md`,
`docs/patterns.md`, and Principle II's *rationale* all say "cobra". The principle's
normative requirement is unaffected (code `2` is still wired explicitly in `main`, because
`flag` also has no notion of an exit code beyond its own `ExitOnError`), but the prose must
be corrected when the code lands. `flag.ContinueOnError` is mandatory — `ExitOnError`
calls `os.Exit(2)` itself and would bypass the classification in `main`, and
`flag.ErrHelp` must be mapped to exit `0`, not `2`.

---

## D2. Content-type detection by content, never extension (FR-002)

**Decision**: `net/http.DetectContentType` over the first 512 bytes, then two refinements:

1. **TIFF is not in the stdlib sniff table** — verified against the Go 1.26.1 source, which
   covers PDF, PNG, JPEG, GIF, WebP, BMP, ICO, and ZIP but no TIFF. FR-003 requires TIFF,
   so a magic-number check for `II\x2a\x00` (little-endian) and `MM\x00\x2a` (big-endian)
   runs first.
2. **`application/zip` is ambiguous** — OOXML, ODF, and an ordinary archive all sniff as
   ZIP. Disambiguate by opening the central directory with `archive/zip`: ODF declares a
   stored, uncompressed `mimetype` entry as its *first* member; OOXML carries
   `[Content_Types].xml`. Neither present means a plain archive, which is unsupported.
3. **Legacy office files** sniff as `application/octet-stream`. Check for the OLE2/CFB
   signature `D0 CF 11 E0 A1 B1 1A E1`.

**Rationale**: FR-004 requires the error to *name the detected type*, so detection has to
produce a name even in the unsupported case — which `DetectContentType` always does,
falling back to `application/octet-stream`.

**Alternatives considered**: `github.com/gabriel-vasile/mimetype` (MIT) — a good library,
but it buys detection of ~170 formats where this tool supports about a dozen, and the two
refinements above are a few dozen lines of stdlib.

---

## D3. PDF: text layer first, rasterise only when it is empty (FR-006, FR-007, FR-013)

**Decision**: shell out to poppler, invoked only when a PDF is actually processed.

- Text layer: `pdftotext -q -l <cap> <src> -` on stdout.
- Emptiness test: strip Unicode whitespace and the form feeds `pdftotext` emits per page;
  fewer than **64 remaining runes across the whole document** counts as no usable text
  layer. A scanned PDF typically yields zero or a stray ligature; a born-digital page
  yields hundreds.
- Rasterise: `pdftoppm -png -r 200 -f N -l N` per page into a scratch directory, one vision
  call per page, concatenated in page order.

**Rationale**: The spec's assumptions already fix this ("Les outils de traitement des PDF
sont fournis par l'hôte et non embarqués"), and `docs/architecture.md` Decision 1 measured
it: a born-digital PDF costs zero model calls. 200 DPI is the usual floor for reliable OCR
of 10pt text without inflating the image past what a local vision model will accept.

**Missing-binary handling (FR-013)**: `exec.LookPath` before the first use, and the error
names the binary and the package that provides it. Both `pdftotext` and `pdftoppm` are
listed in `--help`.

**Alternatives considered**: a pure-Go PDF library (`ledongthuc/pdf`, `pdfcpu`). Rejected —
text extraction from real-world PDFs is where these libraries are weakest, rasterisation
needs a full renderer, and both mean a dependency for something the host already has.

---

## D4. Office documents: stdlib for open formats, host tool for legacy (FR-010, FR-014)

**Decision** — approved by the author:

- **OOXML** (`.docx`/`.xlsx`/`.pptx`): `archive/zip` + `encoding/xml`, streaming the
  relevant parts — `word/document.xml`, `xl/sharedStrings.xml` plus `xl/worksheets/*.xml`,
  `ppt/slides/slide*.xml` — and collecting character data from the text runs.
- **ODF** (`.odt`/`.ods`/`.odp`): same, reading `content.xml`.
- **Legacy OLE2** (`.doc`/`.xls`/`.ppt`): `libreoffice --headless --convert-to txt:Text
  --outdir <tmp> <src>`, invoked only when an OLE2 file is actually seen.

**Rationale**: The legacy branch mirrors the precedent FR-013 already sets for poppler — a
host tool, named in `--help`, reported by name when missing, never bundled. It costs zero
Go dependencies and keeps the common paths (OOXML, ODF) pure stdlib.

**Zip-bomb bounding (FR-014)**: three independent limits, because any one alone is
defeatable. (a) Refuse an archive declaring more than **1024** entries. (b) Wrap every
entry reader in an `io.LimitedReader` and abort when the *cumulative* decompressed total
crosses **64 MiB** — a limit on each entry alone is defeated by many entries. (c) Never
recurse into a nested archive: depth is fixed at one. `archive/zip` reports
`File.UncompressedSize64` from the header, which a malicious archive controls, so the
limit is enforced on bytes actually read, not on the declared size.

**Alternatives considered**: a Go OLE2/CFB parser (`richardlehane/mscfb`). Rejected —
lightly maintained, and `.xls`/`.ppt` coverage is materially worse than `.doc`.
`libreoffice` handles all three and is already present on most machines that hold such
files. Dropping legacy support outright was offered and declined; FR-003 stands as written.

---

## D5. OpenAI-compatible client: `net/http` by hand (FR-012, FR-017)

**Decision**: a small `internal/chat` client speaking `POST {base_url}/chat/completions`
with `encoding/json` over an `*http.Client` with an explicit `Timeout`. Two independently
constructed clients — one for OCR, one for analysis — each with its own base URL, model,
credential, and timeout.

**Rationale**: The tool uses exactly two shapes of request (multimodal user message with a
base64 `image_url`; text-only user message with a `response_format`) and reads exactly one
field from the response (`choices[0].message.content`). That is well under a hundred lines
of stdlib. The official SDK models the entire API surface including streaming, batching,
assistants, and embeddings — none of which this tool touches — and its OpenAI-specific
behaviours can get in the way of the local endpoints FR-012 must support unchanged.

**Local/remote parity (FR-012)**: nothing branches on locality. Ollama, llama.cpp's server,
vLLM, and the hosted APIs all expose `/v1/chat/completions`; the only difference is the
base URL and whether an `Authorization` header is sent. Image payloads use
`data:image/png;base64,...` in an `image_url` content part, which every one of them accepts.

**Bounding (FR-075, FR-076)**: `http.Client.Timeout` per call, `context.Context` threaded
from `main` so SIGINT aborts an in-flight request, and a bounded retry — **3 attempts**,
exponential backoff `1s, 2s, 4s` with full jitter — on connection errors, 429, and 5xx
only. A 4xx other than 429 is a configuration error and is never retried.

**Alternatives considered**: `github.com/openai/openai-go`, `sashabaranov/go-openai`. Both
rejected on Principle VI for the reason above.

---

## D6. Schema-constrained generation and local re-validation (FR-022 … FR-028)

**Decision**: transmit the schema in `response_format`, and re-validate what comes back
with a hand-written checker — no JSON Schema library.

Request:

```json
"response_format": {
  "type": "json_schema",
  "json_schema": {"name": "document_metadata", "strict": true, "schema": { ... }}
}
```

Response handling, in order:

1. Decode with `json.Decoder` + **`DisallowUnknownFields`**. Prose, a fenced code block, or
   an object of a different shape all fail here — which is exactly FR-022's requirement.
2. Check required fields are present and non-empty. Distinguishing "absent" from "empty"
   needs pointer fields or `json.RawMessage`; plain zero values conflate the two and would
   let a missing `type` pass as `""`.
3. Check `type` against the configured closed vocabulary, and `tags` too when the
   configuration bounds them (FR-026, FR-028, FR-030).
4. Normalise: dates parsed with `time.Parse(time.DateOnly, ...)` and **discarded** rather
   than coerced if they do not match `YYYY-MM-DD` (FR-020, FR-031); tags lowercased and
   deduplicated; `amount` and `currency` dropped as a pair unless both are present
   (FR-021).

Any failure in 1–3 is a malformed response: retry up to **3 attempts**, then fail. Nothing
is filed on that basis (FR-018).

**Rationale**: FR-025 requires local validation precisely *because* the service's
compliance cannot be assumed, so the validator must not be the same code path that built
the request. The schema is fixed and closed — one object, eleven scalar-or-array fields, no
nesting, no `$ref`, no composition — so a general JSON Schema engine would be a dependency
whose generality is entirely unused. The vocabularies are the only dynamic part, and they
are just string-set membership.

**Alternatives considered**: `santhosh-tekuri/jsonschema` (Apache-2.0), `xeipuuv/gojsonschema`.
Rejected as above. Prompt-only "reply in JSON" is rejected by FR-023 and by
`docs/architecture.md` Decision 3, which measured it producing valid JSON with an invented
structure.

---

## D7. Detecting an endpoint that will not honour the schema (FR-024)

**Decision**: classify at first use, and report it as a **usage error (exit 2)**, not a
runtime failure.

Three distinguishable signals, all seen from real endpoints:

| Signal | Meaning |
|---|---|
| HTTP 400 whose body mentions `response_format` / `json_schema` | the server rejects the parameter outright |
| HTTP 2xx, but the content is prose or non-conforming JSON, on **every** attempt | the server accepted and ignored the parameter |
| HTTP 404 / 501 on the endpoint | not an OpenAI-compatible service at all |

The first and third are unambiguous on the first response. The second is only
distinguishable from an ordinary bad generation after the bounded retries of D6 are
exhausted — so when all attempts fail schema validation, the resulting error names the
configured base URL and model and says the endpoint does not honour schema-constrained
output. FR-024's point is that this must not surface as an intermittent runtime fault, and
exit `2` with the endpoint named is what makes it a configuration problem the user can fix.

**Alternatives considered**: a preflight probe on a throwaway prompt before every run.
Rejected — it doubles the request count on every invocation to detect a
configuration error that is stable across runs, and a probe that passes gives no guarantee
the real call will.

---

## D8. Filename sanitisation and transliteration (FR-032 … FR-036)

**Decision**: `golang.org/x/text` (approved), then a fixed pipeline.

```
norm.NFD → runes.Remove(runes.In(unicode.Mn)) → norm.NFC   // "Réf. n°42" → "Ref. n42"
→ lowercase
→ [^a-z0-9]+ collapsed to a single "-"
→ trim leading/trailing "-"
→ truncate to 120 bytes on a rune boundary
→ prepend "YYYY-MM-DD-" when a document date survived validation
→ append the original extension
```

**Rationale**: NFD-decompose-and-strip-marks is the correct general answer for Latin
scripts and degrades predictably elsewhere. The 120-byte cap leaves room under the common
255-byte `NAME_MAX` for the date prefix, the extension, and a `-2` collision suffix.

**Safety (FR-033, FR-041)**: sanitisation is what makes the model's proposed name safe,
and the `[^a-z0-9]+` collapse removes `/`, `\`, and `.` as a side effect — so `../../etc`
cannot survive, and neither can `.` or `..` as a whole name. This is defence in depth, not
the primary control: D10's `os.Root` is what actually enforces confinement.

**Empty result (FR-033)**: if nothing survives, keep the original base name — never
substitute a generated one.

**No date means no prefix (FR-035)**: today's date is never substituted. A wrong date in a
filename is worse than no date, because it sorts wrong forever and looks authoritative.

---

## D9. Rules and path templates (FR-037 … FR-040)

**Decision**: an ordered slice, first match wins, with `text/template` rendering the path.

- **Matching**: a rule may constrain `type` (equality), `tags` (all listed tags must be
  present — set containment, not equality), and `correspondent` (equality). A rule with no
  condition is the default rule. Evaluation is a linear scan; the first match returns.
- **Template fields**: `.Type`, `.Correspondent`, `.Reference`, `.Title`, `.Year`,
  `.Month`, `.Day`, `.Tags`. There is deliberately **no** `.Folder` or `.Path` field
  (FR-039) — every directory level is literal text in the template, which is what makes
  SC-014 checkable by reading the config.
- **Missing values**: `template.Option("missingkey=error")` plus a render-time check that
  no path segment came out empty, so a blank `.Correspondent` produces an error rather than
  a `factures//2025` collapse.
- **No match and no default (FR-040)**: fail, naming the document. Nothing moves.

**Rationale**: Templates are parsed once at config load, so a malformed template is a
usage error caught before any document is touched. `text/template`, not `html/template` —
this is a filesystem path, and HTML escaping would corrupt it.

**Determinism**: matching and rendering are pure functions of the metadata and the config.
No I/O, no clock, no network — which is what the spec's determinism assumption requires,
and it makes the whole of `internal/rules` table-testable.

---

## D10. Root confinement, atomic write, cross-device move (FR-041, FR-044 … FR-047)

**Decision**: `os.Root` for confinement, temp-file-plus-rename for atomicity.

**Confinement (FR-041)**: `os.OpenRoot(archiveRoot)` once, and every subsequent write goes
through `(*os.Root).MkdirAll`, `Create`, `Rename`. Verified present in Go 1.26.1. This
resolves paths with `openat2`-style semantics inside the kernel, so a traversal or a
symlink pointing outside the root fails at the syscall — not at a `strings.HasPrefix` check
that a symlink or a race can defeat. `filepath.Clean` plus a prefix comparison is the
usual approach and it is not equivalent: it validates a string, while `os.Root` validates
the actual resolution.

**Atomicity (FR-046)**: write to `.tabularium-<random>.tmp` **in the destination
directory** — same filesystem, so the final `rename(2)` is atomic — `Sync()`, `Close()`,
then rename onto the final name. No partial file is ever visible under its final name.

**Cross-device (FR-044)**: try `Rename` first; on `EXDEV` (surfaced as an `*os.LinkError`,
matched with `errors.Is(err, syscall.EXDEV)`) fall back to copy-to-temp → `Sync` → verify
the SHA-256 of what was written → rename → and only then remove the source. The source is
never removed before the copy is verified, which is FR-044 verbatim.

**Interrupt safety (FR-047)**: a `defer` removes the temp file on any error path including
context cancellation. The window where a crash leaks a file is the temp name only, never
the final name — and the temp name is prefixed and greppable.

---

## D11. Collisions (FR-048 … FR-050)

**Decision**: SHA-256 of both files, compared, with a cheap guard in front.

Compare `Size()` first — different sizes cannot be identical content, and that skips
hashing a large file in the common case. On equal sizes, hash both with
`io.Copy(sha256.New(), f)`.

- Identical → duplicate: nothing is written, the fact is reported on stderr, exit **0**
  (FR-049, FR-069).
- Different → append `-2`, `-3`, … before the extension, probing with
  `O_CREAT|O_EXCL` so the probe and the claim are one atomic step (FR-050).

**Rationale**: SHA-256 is the constitution-compatible choice (`crypto/sha256`, stdlib) and
the spec says "empreinte cryptographique" explicitly. It is also the same hash the sidecar
records, so it is computed once per file and reused.

---

## D12. Sidecar and resume (FR-051 … FR-053, FR-057)

**Decision**: `<archived-filename>.tabularium.json`, written beside the archived file.

JSON rather than YAML here, deliberately: the sidecar is machine-written and
machine-read, never hand-edited, so `encoding/json` costs nothing and avoids a YAML
round-trip the user never sees. The config file is the opposite case, which is why D14 goes
the other way.

Contents: schema version, source path, source SHA-256, the validated metadata, the applied
rule, the filing timestamp (RFC 3339), the truncation record, and the archiver result
(exit code plus raw output truncated to **8 KiB**, per FR-057).

**Resume (FR-052)**: when the argument is a file that already has a sidecar whose recorded
SHA-256 matches the file's own bytes, skip extraction, analysis, and filing entirely and
replay only a missing archiver hand-off. If the sidecar records the archiver already
succeeded, do nothing and exit **0** (FR-052, SC-004).

**Corrupt or inconsistent sidecar (FR-053)**: unparseable, wrong schema version, or a
hash that does not match the file it sits beside — all treated as *absent*. Log the anomaly
on stderr and reprocess from the start. Never trust a sidecar over the bytes.

**Writing**: same temp-plus-rename dance as D10. A half-written sidecar would be
indistinguishable from a corrupt one on the next run, which is survivable but noisy.

---

## D13. External archiver (FR-054 … FR-060)

**Decision**: `exec.CommandContext` with a templated `argv`, never a shell.

- **argv (FR-055)**: each argument is its own `text/template`, rendered separately, and
  passed as a distinct element of `exec.Cmd.Args`. No shell, no string concatenation, no
  quoting rules to get wrong. A multi-valued field like tags expands to one argument per
  tag via a repeated template entry, satisfying FR-055's "chaque tag lui parvient comme un
  argument distinct".
- **Timeout and process-group kill (FR-056)**: `Setpgid: true` in `SysProcAttr`, and
  `Cmd.Cancel` sends `SIGKILL` to the *negated* PID so the whole group dies, not just the
  direct child. `Cmd.WaitDelay` bounds the wait on a child that ignores the signal or holds
  the pipes open. Both `Cancel` and `WaitDelay` verified present in Go 1.26.1. This needs
  build-tagged files: `archiver_unix.go` and `archiver_windows.go` (which uses a job object
  via `CREATE_NEW_PROCESS_GROUP`), because `Setpgid` does not exist on Windows and the
  package must still compile for the `windows` targets in `.goreleaser.yaml`.
- **Credentials (FR-058, FR-073)**: passed in `Cmd.Env`, never in `Args` — an argv is
  world-readable in `/proc` and in `ps` output. The environment is built explicitly from
  `os.Environ()` plus the configured values rather than inherited wholesale.
- **Black box (FR-057, FR-059)**: a non-zero exit code is the failure signal; stdout and
  stderr are captured, truncated to 8 KiB, and recorded verbatim. No parsing, no identifier
  extraction, no format presumed.
- **Missing binary (FR-060)**: `exec.LookPath` first, and the error names the command.

---

## D14. Configuration: YAML, with flags > env > file > defaults (FR-072)

**Decision**: `github.com/goccy/go-yaml` (approved), one file at
`$XDG_CONFIG_HOME/tabularium/config.yaml` (falling back to `~/.config/...`, and
`os.UserConfigDir()` on Darwin/Windows).

**Module choice within the approval**: the author approved YAML with `gopkg.in/yaml.v3` and
`goccy/go-yaml` both named. `yaml.v3` last released **v3.0.1 in 2022** and its repository is
archived; Principle VI requires a dependency to be *actively maintained*, and
`goccy/go-yaml` is at **v1.19.2** with ongoing releases. The approval therefore resolves to
`goccy/go-yaml`. It is MIT and has no transitive dependencies outside `golang.org/x`.

**Precedence mechanics (FR-072)**: the ordering only works if "was this flag set?" is
distinguishable from "is this flag at its zero value". Resolve in the order
defaults → file → environment → flags, and apply the flag layer using `FlagSet.Visit`,
which iterates **only flags actually present on the command line**. Reading the flag
variables directly would let an unset `--output` overwrite a configured value with `"text"`.
The resulting order is stated verbatim in `--help`.

**Strictness**: decode with `yaml.Strict()` so a typo'd key is a usage error at startup
rather than a silently ignored setting — the failure mode where a user's rule never
matches and nothing explains why.

**Credential handling (FR-073)**: API keys come from the environment
(`TABULARIUM_OCR_API_KEY`, `TABULARIUM_ANALYSIS_API_KEY`, and the archiver's configured
names). The config file *may* carry them, but every such field is a distinct
type whose `String()`, `MarshalJSON`, and `MarshalYAML` return `"[redacted]"` — so a
credential cannot reach a log line or an error message even when a whole config struct is
formatted with `%v`. That is what makes SC-008 a property of the type rather than a
discipline.

**Embedded default config (Principle VII)**: a commented starter config is embedded with
`//go:embed` and written on demand, so the binary never depends on a file next to it.

---

## D15. Output: `text/tabwriter` and `encoding/json` (FR-064 … FR-071)

**Decision**: stdout is `io.Writer`-injected everywhere, never `os.Stdout` reached for
directly, so both streams are assertable in a test.

- **Text (FR-065)**: `text/tabwriter` with `padding=2` and no border characters, four
  columns — SOURCE, DEST, TYPE, TAGS.
- **JSON (FR-066)**: one object, `SetEscapeHTML(false)`, containing source, destination,
  applied rule, metadata, truncation, and archiver result. A single object rather than a
  stream, because one invocation handles exactly one document.
- **Diagnostics (FR-067)**: `slog` to stderr with a `TextHandler`; `--quiet` sets the level
  to `Error`, `--verbose` to `Debug`. Nothing diagnostic is ever written to stdout — SC-005
  depends on it.
- **TTY and colour (FR-070)**: colour and progress require *both* `os.Stdout.Stat()` to
  report `ModeCharDevice` **and** `NO_COLOR` to be unset. Stdlib only; no `isatty`
  dependency.

---

## D16. Concurrent invocations targeting one destination

**Decision**: rely on `O_CREAT|O_EXCL` at the point of claiming a name, not on a lock file.

The collision probe of D11 already opens the destination with `O_EXCL`; that is the atomic
claim. Two simultaneous runs racing for `.../2025-03-14-facture.pdf` therefore resolve to
one winner and one `-2`, with no lost file and no lock to leak if a process is killed.
`xargs -P` — the parallelism story in the README — is safe by construction.

**Alternatives considered**: an advisory lock file per destination directory. Rejected — a
lock file survives `SIGKILL` and turns a crash into a wedged tree, to prevent a race that
`O_EXCL` already resolves correctly.

---

## D17. Module path, build metadata, and a scaffolding defect

**Module path**: `github.com/sgaunet/tabularium`, from the `origin` remote.

**Defect found in `.goreleaser.yaml`** — the ldflags are:

```yaml
- -X internal/cli.Version={{ .Version }}
```

`-X` takes a *fully-qualified* package path. `internal/cli` is not one, so the linker
silently does nothing and `--version` would report an empty string in every release build.
It must become `-X github.com/sgaunet/tabularium/internal/cli.Version=...` for all three
variables. Recorded here so `/speckit-tasks` picks it up; not changed by this phase.

---

## Resolved unknowns

| Unknown | Resolution |
|---|---|
| Flag parser | stdlib `flag` — D1 |
| Config format and module | YAML via `goccy/go-yaml` — D14 |
| Transliteration | `golang.org/x/text` — D8 |
| Legacy `.doc`/`.xls`/`.ppt` | `libreoffice --headless`, as a host tool — D4 |
| TIFF detection | explicit magic check; absent from the stdlib sniff table — D2 |
| "No usable text layer" threshold | < 64 non-whitespace runes — D3 |
| Zip-bomb bounds | 1024 entries, 64 MiB cumulative, depth 1 — D4 |
| Retry policy | 3 attempts, 1s/2s/4s with jitter, 429 and 5xx only — D5 |
| JSON Schema validation | hand-written; schema is fixed and closed — D6 |
| Endpoint schema non-compliance | exit 2 naming the endpoint, after retries — D7 |
| Filename length cap | 120 bytes, rune-aligned — D8 |
| Path confinement | `os.Root`, not string prefixing — D10 |
| Cross-device move | `EXDEV` → copy, verify, rename, then unlink — D10 |
| Sidecar name and format | `<file>.tabularium.json` — D12 |
| Archiver output cap | 8 KiB — D12/D13 |
| Process-group kill on Windows | build-tagged file, job object — D13 |
| Precedence implementation | `FlagSet.Visit`, not flag zero values — D14 |
| Concurrency | `O_CREAT\|O_EXCL`, no lock file — D16 |
| Module path | `github.com/sgaunet/tabularium` — D17 |
