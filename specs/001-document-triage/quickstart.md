# Quickstart: Validating Document Triage

**Feature**: `001-document-triage` | **Plan**: [plan.md](./plan.md) | **Contracts**: [contracts/](./contracts/)

Runnable scenarios that prove the feature works end to end. Each maps to a success
criterion from the spec and states what to observe. This is a validation guide, not an
implementation guide — the code belongs in `tasks.md` and the implementation phase.

---

## Prerequisites

```sh
mise install                    # Go 1.26.1, task, golangci-lint, goreleaser, syft
task dev:install-pre-commit     # task refuses to run without the hook
```

Host tools, needed only by the scenarios that use them:

```sh
# macOS
brew install poppler
brew install --cask libreoffice     # only for scenario 8 (legacy .doc)

# Debian/Ubuntu
sudo apt install poppler-utils libreoffice-writer
```

A model endpoint. Any OpenAI-compatible `/chat/completions` will do; locally:

```sh
ollama serve
ollama pull qwen2.5vl:7b        # vision, for OCR
ollama pull qwen2.5:14b         # text, for analysis — must honour json_schema
```

Verify the analysis model actually honours schema-constrained output before anything else.
This is the one prerequisite that fails silently if you skip it:

```sh
curl -s http://localhost:11434/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen2.5:14b",
       "messages":[{"role":"user","content":"Alice is 30."}],
       "response_format":{"type":"json_schema","json_schema":{"name":"p","strict":true,
         "schema":{"type":"object","additionalProperties":false,
                   "required":["name","age"],
                   "properties":{"name":{"type":"string"},"age":{"type":"integer"}}}}}}' \
  | jq -r '.choices[0].message.content'
```

**Expected**: `{"name":"Alice","age":30}` — exactly those keys, no prose, no code fence. If
the reply is prose or JSON of a different shape, the endpoint does not honour the
constraint, and `tabularium` will (correctly) refuse it with exit **2** naming the endpoint.

---

## Setup

```sh
task build

mkdir -p ~/tab-demo/{scans,archive}
cp specs/001-document-triage/contracts/config.example.yaml ~/tab-demo/config.yaml
# edit archive_root to ~/tab-demo/archive
```

Put one scanned PDF (no text layer), one born-digital PDF, and one JPEG photo of a receipt
in `~/tab-demo/scans/`. A born-digital PDF is easy to make:

```sh
printf 'Facture 2025-03-14\nGarage Central\nTotal 384.50 EUR\n' > /tmp/f.txt
libreoffice --headless --convert-to pdf --outdir /tmp /tmp/f.txt
```

---

## Scenario 1 — See the plan without writing anything (US1, SC-009)

The irreducible value of the tool, and the first thing to build.

```sh
cd ~/tab-demo
find archive -type f | sort | xargs -r shasum > /tmp/before.txt

./tabularium --config config.yaml --dry-run scans/facture.pdf

find archive -type f | sort | xargs -r shasum > /tmp/after.txt
diff /tmp/before.txt /tmp/after.txt && echo "TREE UNCHANGED"
```

**Expect**: the recognised text, title, type, correspondent, tags, document date, the
computed filename, the destination path, and **the rule that produced it**. Exit `0`.
`diff` reports nothing — byte-for-byte identical (SC-009).

---

## Scenario 2 — A born-digital PDF costs zero model calls (US1 AS2, SC-001)

```sh
# watch the endpoint while the command runs
tail -f ~/.ollama/logs/server.log &
./tabularium --config config.yaml --dry-run /tmp/f.pdf
```

**Expect**: no vision request in the log. `--output=json` reports
`.text.origin == "text-layer"`. Compare with the scanned PDF, which reports `"vision"` and
does hit the endpoint once per page.

---

## Scenario 3 — The output contract holds under a strict parser (SC-005)

The one scenario to run against **every** supported document type.

```sh
for f in scans/*; do
  ./tabularium --config config.yaml --dry-run --output=json "$f" 2>/dev/null | jq -e . > /dev/null \
    && echo "OK   $f" || echo "FAIL $f"
done
```

**Expect**: `OK` for every file. `jq -e` fails on any diagnostic leaking into stdout, which
is the point — `2>/dev/null` discards stderr, so anything `jq` chokes on was on the wrong
stream. Validate the shape against [`contracts/output.schema.json`](./contracts/output.schema.json).

---

## Scenario 4 — File it, first matching rule wins (US2, SC-002)

```sh
./tabularium --config config.yaml scans/facture.pdf
find archive -type f
```

**Expect**: `archive/factures/voiture/2025/2025-03-14-facture-voiture.pdf` — the date prefix
comes from the *document's* date, not today's. `scans/` no longer holds it (disposition
`move`). A `…pdf.tabularium.json` sits beside it; check it against
[`contracts/sidecar.schema.json`](./contracts/sidecar.schema.json).

Ordering: `factures-voiture` precedes `factures` in the config and both match, so the
narrower one decides (FR-037). Move it below `factures` and re-run on a fresh copy — the
destination changes to `factures/Garage Central/2025/`.

---

## Scenario 5 — Re-running is safe (SC-003, FR-049, FR-069)

```sh
cp archive/factures/voiture/2025/*.pdf scans/facture.pdf
./tabularium --config config.yaml scans/facture.pdf; echo "exit=$?"
find archive -name '*.pdf' | wc -l
```

**Expect**: `exit=0`, the duplicate reported **on stderr**, no second file. Then verify the
other branch — different bytes at the same destination get a suffix, not an overwrite:

```sh
printf 'different content' >> scans/facture.pdf     # same computed name, different bytes
./tabularium --config config.yaml scans/facture.pdf
ls archive/factures/voiture/2025/
```

**Expect**: both files present, the second suffixed `-2`. Nothing was ever overwritten.

---

## Scenario 6 — Resume a failed hand-off (US4, SC-004)

The scenario that justifies the sidecar. Use a fake archiver that records its argv:

```sh
cat > /tmp/fake-archiver <<'SH'
#!/bin/sh
printf '%s\n' "$@" >> /tmp/archiver-argv.txt
echo "token=${PAPERLESS_TOKEN:-unset}" >> /tmp/archiver-argv.txt
exit "${FAKE_EXIT:-0}"
SH
chmod +x /tmp/fake-archiver
# point config.yaml archive.command at /tmp/fake-archiver
```

Fail it, then fix it:

```sh
FAKE_EXIT=1 ./tabularium --config config.yaml scans/ticket.jpg; echo "exit=$?"   # expect 1
find archive -name 'ticket*'                                                      # filed anyway

: > /tmp/archiver-argv.txt
./tabularium --config config.yaml archive/.../ticket*.jpg; echo "exit=$?"         # expect 0
```

**Expect**: the first run exits `1` with the file **still filed** (FR-063). The second run
re-invokes only the archiver — no vision request in the endpoint log, no file moved
(SC-004). A third run exits `0` doing nothing at all: the sidecar records success.

**Also check**: `/tmp/archiver-argv.txt` shows each tag on its own line (FR-055) and
`token=` carrying the value from the environment, never from argv.

---

## Scenario 7 — Nothing partial survives an interrupt (SC-006)

```sh
./tabularium --config config.yaml scans/big.pdf & sleep 2; kill -INT %1; wait
find archive -name '.tabularium-*' -o -size 0
ls scans/                                     # source still there
```

**Expect**: no temp files, no zero-length files, no file under a final name, and the source
intact. Repeat killing during the model call and during the archiver call. The archiver
case also needs the group check — a grandchild must not survive:

```sh
# archive.command → a script that backgrounds `sleep 300`
pgrep -f 'sleep 300'      # expect nothing after the timeout fires
```

---

## Scenario 8 — Every failure lands on the right exit code (SC-007, FR-068)

```sh
./tabularium --config config.yaml                       ; echo "no args      → $?"   # 2
./tabularium --config config.yaml a.pdf b.pdf           ; echo "two args     → $?"   # 2
./tabularium --config config.yaml /nonexistent          ; echo "missing      → $?"   # 2
./tabularium --config config.yaml /tmp                  ; echo "directory    → $?"   # 2
./tabularium --config config.yaml /bin/ls               ; echo "unsupported  → $?"   # 2, naming the type
./tabularium --config config.yaml blank-page.pdf        ; echo "no text      → $?"   # 1
./tabularium --config /dev/null blank.pdf               ; echo "bad config   → $?"   # 2
./tabularium --help                                     ; echo "help         → $?"   # 0
```

**Expect**: the codes above. The unsupported-type message must **name the detected type**
(FR-004) — never a generic refusal. `--help` must print the exit codes and the precedence
order, which is asserted by a test, not left to convention.

Legacy office support, if `libreoffice` is installed:

```sh
libreoffice --headless --convert-to doc --outdir /tmp /tmp/f.txt
./tabularium --config config.yaml --dry-run /tmp/f.doc      # exit 0, text extracted
```

---

## Scenario 9 — No credential is ever printed (SC-008)

```sh
export TABULARIUM_ANALYSIS_API_KEY='sk-canary-9f2c0e4b'
./tabularium --config config.yaml --verbose --dry-run scans/facture.pdf > /tmp/out 2> /tmp/err
grep -c 'sk-canary-9f2c0e4b' /tmp/out /tmp/err     # expect 0 in both
```

**Expect**: zero matches across both streams at the most verbose level. Also confirm it
never reaches an argv while the archiver runs:

```sh
./tabularium --config config.yaml scans/facture.pdf &
ps -Ao args | grep -c 'sk-canary'                   # expect 0
```

---

## Scenario 10 — The tree can only grow where the config says (SC-012, SC-014)

The invariant the whole design rests on. Stub the analysis endpoint to return a `type`
outside the vocabulary — `"type": "cryptomonnaie"` — and run:

```sh
find archive -type d | sort > /tmp/dirs-before.txt
./tabularium --config config.yaml scans/facture.pdf ; echo "exit=$?"    # expect 1
find archive -type d | sort > /tmp/dirs-after.txt
diff /tmp/dirs-before.txt /tmp/dirs-after.txt && echo "NO NEW BRANCH"
```

**Expect**: exit `1` after the bounded retries, and **no new directory** — the value is
rejected locally even though the transport accepted it (FR-028, SC-012).

Then the subtler case: a value that is *in* the vocabulary but wrong, or a tag phrased
differently from what a rule expects. The document must land under the **default** rule,
visibly, never in an invented folder (SC-013):

```sh
# stub returns type=facture but tags=["automobile"] — no rule expects "automobile"
./tabularium --config config.yaml --dry-run scans/facture.pdf
```

**Expect**: rule `factures` or `divers`, never `factures/voiture`. Misfiled, but visibly
and recoverably — which is the guarantee the closed vocabulary actually provides.

Finally, confirm SC-014 by eye: every directory under `archive/` must correspond to a
literal path segment in `config.yaml`. There is no template field that could produce one
that does not.

---

## Scenario 11 — Truncation is never silent (SC-010, FR-008)

```sh
# max_pages: 20 in config; use a 200-page PDF
./tabularium --config config.yaml --dry-run --output=json big.pdf | jq .truncation
```

**Expect**: `{"pages_processed": 20, "pages_total": 200}`, and a matching notice on stderr.
The field is present-and-null when nothing was dropped, never omitted — a consumer can tell
"not truncated" from "the tool forgot to say".

---

## Gate before calling it done

```sh
task test        # go test -count=2 -race ./...
task lint        # go generate ./... && golangci-lint run
go generate ./... && git diff --exit-code      # generated files committed
govulncheck ./...
task snapshot    # goreleaser builds all targets, including windows
./tabularium --version                          # NOT empty — see research.md D17
```

All six must pass. The `--version` check is there because the ldflags in `.goreleaser.yaml`
currently use a package path the linker silently ignores; a snapshot binary printing an
empty version means that fix has not landed.

---

## Result of the walk

All eleven scenarios were walked end to end against the built binary on 2026-08-31
(darwin/arm64, Go 1.26.1, poppler 25.x), with a stub OpenAI-compatible endpoint standing
in for the model so the run is deterministic and needs no network. **67 checks, 67
passed.**

| # | Scenario | Result |
|---|---|---|
| 1 | The plan without writing anything (SC-009) | pass — tree byte-identical, rule and metadata reported, source untouched |
| 2 | A born-digital PDF costs zero OCR calls (SC-001) | pass — 0 vision calls, `origin: text-layer`; the scanned PDF costs exactly 1 and reports `vision` |
| 3 | The output contract under a strict parser (SC-005) | pass — `jq -e` succeeds on all eleven supported document types |
| 4 | File it, first matching rule wins (SC-002) | pass — filed at `factures/voiture/2025/2025-03-14-…`, scan box emptied, sidecar conforms |
| 5 | Re-running is safe (SC-003, FR-049, FR-069) | pass — duplicate on stderr with exit 0 and no second file; different bytes get `-2`, the original untouched |
| 6 | Resume a failed hand-off (SC-004) | pass — first run exits 1 with the file still filed; the re-run costs 0 model calls and only runs the archiver; a third run does nothing |
| 7 | Nothing partial survives an interrupt (SC-006) | pass — no temp files, no zero-length files, the document either intact at the source or filed byte-identically |
| 8 | Every failure on the right exit code (SC-007, FR-068) | pass — all ten cases, and the unsupported-type message names `application/octet-stream` |
| 9 | No credential is ever printed (SC-008) | pass — zero matches on both streams at `--verbose`, and none in any `tabularium` argv |
| 10 | The tree grows only where the config says (SC-012) | pass — a type outside the vocabulary exits 1 and creates **no** new directory; an unexpected tag falls to a declared rule |
| 11 | Truncation is never silent (SC-010) | pass — `{"pages_processed":20,"pages_total":25}` with a matching stderr notice; present-and-null otherwise |

Two checks failed on the first attempt and both were faults in the walk script, not in the
tool: scenario 7 looked for the filed document under a name the stub never produced (the
stub returns identical metadata for every document, so every one computes the same name),
and scenario 9's `ps -Ao args | grep -c sk-canary` counted **grep's own argv**, which
contains the string it is searching for. Both were corrected and both then passed.

### The gate

| Gate | Result |
|---|---|
| `task test` | pass — `go test -count=2 -race ./...`, thirteen packages |
| `task lint` | pass — golangci-lint v2, 0 issues |
| `go generate ./...` clean | pass — the tree carries no `go:generate` directive, and nothing changed |
| `govulncheck ./...` | **FAIL** — 15 standard-library advisories, none in this code or its two dependencies; every one is fixed by a toolchain patch release. See below. |
| `task snapshot` | pass — all six targets including `windows_amd64` and `windows_arm64` |
| `./tabularium --version` | pass — `tabularium 0.0.0-SNAPSHOT-99f6ff2 (99f6ff2…, 2026-08-31T06:50:18Z)`, non-empty, which is what proves the research.md D17 ldflags fix landed |

**govulncheck**: every finding is in the standard library of the pinned Go 1.26.1 —
`crypto/x509`, `crypto/tls`, `net/http`, `net/url`, `net/textproto`, `encoding/xml`,
`encoding/asn1`, `net`, `os`. Nothing in `internal/`, nothing in `goccy/go-yaml` or
`golang.org/x/text`. The fix is a toolchain bump: the highest fixed-in version among the
findings is **go1.26.6**, and 1.26.7 is available. That means editing the pin in
`mise.toml` and the `go` directive in `go.mod`, which is a deliberate, Principle
VII-governed choice recorded in `plan.md` — so it is left to the author rather than
changed here.
