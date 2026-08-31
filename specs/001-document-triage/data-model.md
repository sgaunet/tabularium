# Phase 1 Data Model: Document Triage

**Feature**: `001-document-triage` | **Date**: 2026-08-30 | **Plan**: [plan.md](./plan.md)

The entities from the specification, as Go types, with the package that owns each one and
the validation rules that govern it. Field types are given because the *choice* of type is
load-bearing here — a pointer versus a value decides whether "absent" and "empty" stay
distinguishable, which several requirements depend on.

Nothing in this document is a CLI type. Every package below imports no CLI package
(Principle III).

---

## Entity map

```
Source ──extract──▶ Text ──analyse──▶ Metadata ──derive──▶ Name
  │                                       │                  │
  │                                       └──match──▶ Rule ───┤
  │                                                           ▼
  └──────────────────────────────────────────────────────▶ Plan
                                                              │
                                                          file │
                                                              ▼
                                                          Outcome ──▶ Sidecar
                                                              ▲
                                                     ArchiveResult
```

`Plan` is the pivot: everything above it is computed, nothing has been written. `--dry-run`
stops exactly here (FR-043), which is what makes SC-009 — a byte-identical tree — a
structural property rather than a promise.

---

## `internal/document` — Source

The submitted file, identified by its content rather than its name.

| Field | Type | Notes |
|---|---|---|
| `Path` | `string` | absolute, as resolved from the argument |
| `Type` | `Kind` | sniffed from content, never the extension (FR-002) |
| `Size` | `int64` | from `os.Stat` |
| `Digest` | `[32]byte` | SHA-256 of the whole file |
| `Ext` | `string` | original extension, preserved verbatim (FR-036) |

```go
type Kind string   // "application/pdf", "image/tiff", "application/vnd.…wordprocessingml.document", …
```

`Kind` is the *detected MIME type*, kept as the detected string so FR-004 can name it in
the error. A supported-set lookup maps it to an extraction strategy; an unknown `Kind`
short-circuits to a usage error carrying the string.

**Validation**
- Must exist, be readable, and be a regular file — anything else is a usage error (FR-005).
  `Mode().IsRegular()` catches directories, devices, sockets, and FIFOs in one check.
- `Digest` is computed once and reused by the collision check (D11) and the sidecar (D12).

---

## `internal/extract` — Text

The extracted text plus the provenance the spec insists on carrying.

| Field | Type | Notes |
|---|---|---|
| `Content` | `string` | the text itself |
| `Origin` | `Origin` | how it was obtained |
| `Truncation` | `*Truncation` | `nil` when nothing was dropped |

```go
type Origin string
const (
    OriginTextLayer  Origin = "text-layer"   // PDF carried one (FR-006)
    OriginVision     Origin = "vision"       // rasterised and transcribed (FR-007, FR-009)
    OriginOffice     Origin = "office"       // OOXML/ODF/legacy (FR-010)
    OriginPlain      Origin = "plain"        // text or Markdown (FR-011)
)

type Truncation struct {
    PagesProcessed int
    PagesTotal     int
}
```

**Validation**
- Empty `Content` after extraction is an explicit runtime failure; the document is **not**
  filed (FR-015). A blank page must not become metadata invented from nothing.
- `Truncation` non-nil must be recorded in the sidecar *and* surfaced in the output
  (FR-008, SC-010). It is a pointer precisely so "no truncation" cannot be confused with
  "truncated to zero pages".

---

## `internal/analysis` — Metadata

What the model inferred, after local validation and normalisation. **No field names a
folder** (FR-016) — that is the invariant the whole filing design rests on.

| Field | Type | Required | Notes |
|---|---|---|---|
| `Title` | `string` | yes | free text |
| `Type` | `string` | yes | member of the closed vocabulary (FR-026) |
| `Correspondent` | `string` | no | |
| `Tags` | `[]string` | yes (may be empty) | lowercased, deduplicated, order-stable (FR-031) |
| `DocumentDate` | `*civil.Date` | no | `nil` when absent or rejected (FR-020) |
| `DueDate` | `*civil.Date` | no | same rule |
| `Reference` | `string` | no | |
| `Description` | `string` | no | |
| `Amount` | `*decimalString` | no | paired with `Currency` (FR-021) |
| `Currency` | `string` | no | ISO-4217, paired with `Amount` |
| `Filename` | `string` | yes | the model's *proposal*, never used raw (FR-032) |

Two type choices worth stating:

- **Dates are a date, not a `time.Time`.** A `time.Time` carries a clock and a zone that a
  document date does not have, and formatting one back out invites a timezone shift across
  a day boundary. A small `Date{Year, Month, Day int}` value type parses via
  `time.Parse(time.DateOnly, …)` and formats via `time.DateOnly`, and its zero value is
  meaningfully invalid. Pointer-typed so "no date" is distinct from "year zero" — FR-035
  turns on exactly that distinction.
- **Amount is not a `float64`.** Money in binary floating point is a defect waiting for the
  first `19.99`. The value is carried as a validated decimal string (digits, one optional
  `.`, optional leading `-`); the tool never does arithmetic on it, only records and
  templates it, so a string with a validating constructor is sufficient and exact.

**Validation** (all applied locally, after the response is decoded — FR-025)
1. `Type` ∈ vocabulary, else reject (FR-028). Never creates a new tree branch (SC-012).
2. `Tags` ∈ vocabulary when the configuration bounds them; free otherwise (FR-030).
3. Dates matching `YYYY-MM-DD` or dropped — never coerced (FR-020).
4. `Amount` and `Currency` are kept only as a pair; either alone drops both (FR-021).
5. Tags lowercased and deduplicated before any rule sees them (FR-031).

---

## `internal/config` — Vocabulary

The closed set a field may take, declared in configuration.

| Field | Type | Notes |
|---|---|---|
| `Values` | `[]string` | the accepted values, order preserved for the prompt |
| `Fallback` | `string` | required for types (FR-029), unused for tags |

**Validation**
- The type vocabulary MUST declare a `Fallback` and it MUST be a member of `Values`. A
  missing or non-member fallback is a usage error at config load — caught before any
  document is read, because FR-029 exists to keep a misunderstood document *recognisable*
  rather than plausibly misfiled.
- The tag vocabulary is optional; absent means tags are free (FR-030).
- Membership is tested against a `map[string]struct{}` built once at load.

---

## `internal/rules` — Rule

A condition plus a path template. Ordered; first match wins (FR-037).

| Field | Type | Notes |
|---|---|---|
| `Name` | `string` | for reporting which rule applied (FR-043, FR-066) |
| `Type` | `string` | equality; empty means unconstrained |
| `Tags` | `[]string` | **all** must be present — containment, not equality |
| `Correspondent` | `string` | equality; empty means unconstrained |
| `Path` | `*template.Template` | parsed at load, not at match time |

A rule with `Type`, `Tags`, and `Correspondent` all empty is the **default rule** (FR-038).

**Template context** — exactly these fields, and deliberately no `.Folder` (FR-039):

```go
type PathContext struct {
    Type, Correspondent, Reference, Title string
    Year, Month, Day                      string   // "2025", "03", "14"; empty when no date
    Tags                                  []string
}
```

Date parts are strings, not ints, because a template must render `03` and not `3` for a
path to sort correctly, and `printf "%02d"` in every template is a trap waiting to be
forgotten once.

**Validation**
- Every `Path` parses at config load; a bad template is a usage error before any I/O.
- At render time, no path segment may be empty — a rule referencing `.Correspondent` on a
  document that has none fails loudly rather than producing `factures//2025`.
- At most one default rule; a second is unreachable and is a usage error.

---

## `internal/naming` — Name

| Field | Type | Notes |
|---|---|---|
| `Base` | `string` | sanitised, date-prefixed, extension appended |
| `FromProposal` | `bool` | false when the model's proposal sanitised to nothing (FR-033) |

Derived per D8. `FromProposal` exists so the fallback path is visible in the output and in
tests, rather than being an invisible branch.

---

## `internal/triage` — Plan

The complete result of computation, before a single byte is written. This is what
`--dry-run` prints (FR-043).

| Field | Type | Notes |
|---|---|---|
| `Source` | `document.Source` | |
| `Text` | `extract.Text` | |
| `Metadata` | `analysis.Metadata` | |
| `Name` | `naming.Name` | |
| `Rule` | `rules.Rule` | the one that matched |
| `Dest` | `string` | relative to the archive root — never absolute |
| `Disposition` | `Disposition` | `move` (default) \| `copy` \| `keep` (FR-042) |

`Dest` is **relative to the archive root** by construction, because that is what
`(*os.Root)` operations take (D10). Storing it as an absolute path would invite the string
concatenation the `os.Root` design exists to eliminate.

**Validation**
- Producing a `Plan` must be free of side effects. This is the testable form of FR-043 and
  SC-009: if constructing a `Plan` cannot write, `--dry-run` cannot write either — no flag
  check inside the write path can be forgotten.

---

## `internal/archiver` — ArchiveResult

| Field | Type | Notes |
|---|---|---|
| `Attempted` | `bool` | false when disabled by flag or not configured (FR-062) |
| `Success` | `bool` | |
| `ExitCode` | `int` | the command's own code (FR-059) |
| `Output` | `string` | stdout+stderr, verbatim, truncated to 8 KiB (FR-057) |
| `Err` | `string` | why it failed, when it failed before producing an exit code |

Deliberately **not** parsed for an identifier: the archiver is a black box and its output
is kept for a human to read (FR-057).

---

## `internal/sidecar` — Sidecar

The trace deposited beside the archived file, `<file>.tabularium.json` (FR-051).

| Field | Type | Notes |
|---|---|---|
| `Version` | `int` | schema version; a mismatch means "absent" (FR-053) |
| `Source` | `string` | the original path |
| `SourceDigest` | `string` | hex SHA-256 — the consistency check (FR-053) |
| `Metadata` | `analysis.Metadata` | as validated |
| `Rule` | `string` | the rule name that applied |
| `FiledAt` | `time.Time` | RFC 3339 |
| `Truncation` | `*extract.Truncation` | `nil` when none |
| `Archive` | `ArchiveResult` | the external step's outcome |

**Validation**
- Unparseable, wrong `Version`, or a `SourceDigest` that does not match the file it sits
  beside → treated as **absent**, the anomaly logged on stderr, the document reprocessed
  from the start (FR-053). The bytes always win over the sidecar.
- Written on every successful filing, not only on failure — it is a trace, not an error log
  (spec assumption).

---

## State transitions

A run occupies one of these states; the sidecar is what makes the second entry point
possible (FR-052).

```
                    ┌────────────── sidecar present, digest matches ──────────────┐
                    │                                                             ▼
  argument ──▶ sniff ──▶ extract ──▶ analyse ──▶ plan ──▶ file ──▶ archive ──▶ done
                                                    │                  │
                                              --dry-run           failure
                                                    ▼                  ▼
                                                 print            filed, exit 1
                                                 exit 0           (FR-063)
```

| From | To | Trigger |
|---|---|---|
| sniff | usage error (2) | unsupported type, named (FR-004) |
| extract | runtime error (1) | no text recovered (FR-015) |
| analyse | runtime error (1) | schema-invalid after bounded retries (FR-018) |
| analyse | usage error (2) | endpoint refuses schema constraint (FR-024, D7) |
| plan | done (0) | `--dry-run` (FR-043) |
| plan | runtime error (1) | no rule matched and no default (FR-040) |
| file | done (0) | duplicate detected (FR-049, FR-069) |
| archive | filed, runtime error (1) | archiver failed; file stays filed (FR-063) |
| *(re-entry)* | done (0) | sidecar records archiver already succeeded (FR-052) |
| *(re-entry)* | archive | sidecar records the hand-off still missing (FR-052) |

The re-entry rows are what SC-004 measures: a single re-run finishes the job without
redoing extraction, analysis, or filing.
