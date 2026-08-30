# Tabularium

Hand it one scanned document. It reads the text, works out what the document is, gives it
a name that says so, files it where it belongs, and passes it on to your archive.

```console
$ tabularium ~/Scans/facture.pdf
SOURCE       DEST                                              TYPE     TAGS
facture.pdf  factures/voiture/2025/2025-03-14-facture-voiture.pdf  facture  voiture, entretien
```

> **Status: specification.** The tooling is scaffolded; there is no implementation yet.
> The behaviour above is what
> [`specs/001-document-triage/spec.md`](specs/001-document-triage/spec.md) requires — 76
> requirements, four prioritised user stories.

## How it works

One document per invocation, composable through pipes. Batch with
`find ~/Scans -name '*.pdf' | xargs -n1 tabularium`, parallelise with `xargs -P4`.

1. **Read it.** A PDF that already carries a text layer costs nothing. One that does not
   is rasterised and transcribed by a vision model. Office documents and plain text are
   read locally.
2. **Understand it.** A second, text-only call returns schema-constrained JSON: title,
   type, correspondent, tags, dates, reference, amount.
3. **Name it.** `2025-03-14-facture-voiture.pdf` — the document's own date first, so a
   directory listing sorts chronologically. No date extracted means no prefix; the current
   date would lie about the document.
4. **File it.** Ordered rules, first match wins. The model never names a folder: it
   produces a bounded type and tags, and every directory level is written literally in a
   rule. The full set of destinations is readable in your configuration.
5. **Archive it.** An arbitrary command described in configuration, treated as a black
   box.

Both filing and archiving are on by default when configured, and each can be turned off.
`--dry-run` shows the whole plan and writes nothing.

## The model

Any OpenAI-compatible chat-completions endpoint. Local and hosted differ by configuration
alone, and OCR and analysis are configured independently — vision model on your own
machine, analysis wherever you like.

The analysis model must support schema-constrained output. This is a requirement, not a
preference: without the constraint a model answers in prose, and asked merely for "JSON"
it returns valid JSON with a structure of its own invention.

## Requirements

- `poppler-utils` (`pdftotext`, `pdftoppm`) — only when processing PDFs
- An OpenAI-compatible model endpoint

The binary itself is static and cgo-free.

## Development

```sh
task test    # go test -count=2 -race ./...
task lint    # go generate ./... && golangci-lint run
task build   # CGO_ENABLED=0 go build -o tabularium ./cmd/tabularium
```

The toolchain is pinned in `mise.toml`. Run `task dev:install-pre-commit` first.

Development is governed by [`.specify/memory/constitution.md`](.specify/memory/constitution.md)
and driven by Spec Kit; see [`docs/workflows.md`](docs/workflows.md).

## Licence

MIT — see [LICENSE](LICENSE).
