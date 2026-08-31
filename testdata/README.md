# Test corpus

Every file here is produced by [`gen/main.go`](gen/main.go). Regenerate the whole corpus
from the repository root with:

```sh
go run ./testdata/gen
```

The generator is deterministic — zip modification times are pinned, images are computed
from a fixed function, and nothing is random — so a regenerated corpus is a byte-identical
corpus. That is what lets a reviewer confirm a fixture was not hand-edited.

It is deliberately **not** wired to `go:generate`. The `go` tool excludes `testdata` from
`./...`, so `go generate ./...` never visits it and Gate 4 ("no diff") stays about the
source tree.

## What each file is for

| File | Detected as | Exercises |
|---|---|---|
| `born-digital.pdf` | `application/pdf` | A real text layer: `pdftotext` recovers ~200 non-whitespace runes, well over the 64-rune threshold, so the run costs **zero** model calls (FR-006, SC-001). |
| `scanned.pdf` | `application/pdf` | One JPEG image XObject and no text operator at all. `pdftotext` returns a bare form feed, so rasterisation must take over (FR-007). |
| `multipage.pdf` | `application/pdf` | 25 pages of text — above any sane `max_pages` — so the page cap drops the excess and records a `Truncation` (FR-008, SC-010). |
| `receipt.jpg` | `image/jpeg` | The direct-to-vision path (FR-009). Also embedded inside `scanned.pdf`. |
| `receipt.png` | `image/png` | Same path, second raster format. |
| `little-endian.tiff` | `image/tiff` | TIFF is **absent** from the standard library's sniff table, so detection needs the explicit `II*\0` magic check (FR-003, research.md D2). |
| `big-endian.tiff` | `image/tiff` | The `MM\0*` order, which is the half people forget. |
| `letter.docx` | OOXML | `archive/zip` + `encoding/xml` over `word/document.xml`; disambiguated from ODF by `[Content_Types].xml` (FR-010). |
| `letter.odt` | ODF | Disambiguated by a **stored, first-member** `mimetype` entry; text read from `content.xml` (FR-010). |
| `plain.zip` | `application/zip` | An ordinary archive: neither marker present, so it must be refused as unsupported (FR-004). |
| `legacy.doc` | OLE2 / CFB | The `D0 CF 11 E0 A1 B1 1A E1` signature, which sniffs as `application/octet-stream` and must route to `libreoffice` — and name it when absent (FR-010, FR-013). |
| `notes.md` | `text/plain` | Markdown read directly, origin `plain` (FR-011). |
| `notes.txt` | `text/plain` | Plain text on the same path. |
| `unsupported.bin` | `application/octet-stream` | No known magic and deliberately not OLE2: the error must **name the detected type** (FR-004). |

## What is not here

Two fixtures are built inside the tests that need them rather than committed:

- an archive declaring more than 1024 entries, and
- a zip bomb whose cumulative decompressed size crosses 64 MiB.

Both are large, both are hostile input, and both are one loop to construct — committing
them would put a decompression bomb in the repository for no gain (FR-014).

`legacy.doc` carries a valid Compound File header but no Word stream. Producing a real
`.doc` needs `libreoffice`, which is exactly the host tool under test; the fixture only
has to be *detected* as OLE2 and routed, and the routing is what the tests assert.
