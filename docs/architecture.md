# Architecture

## System Overview

Tabularium is a single-purpose CLI: one document per invocation, in and out of a pipe.
Batching is `find | xargs -n1 tabularium`, parallelism is `xargs -P`. The exit code
therefore always describes exactly one document.

The processing pipeline has five stages, each independently disableable where it makes
sense:

```
document
   ├─ 1. sniff content type (never the extension)
   ├─ 2. extract text
   │      PDF with a text layer  → read it, zero model calls
   │      PDF without            → rasterise, one vision call per page
   │      raster image           → one vision call
   │      office / text          → extract locally, zero model calls
   ├─ 3. analyse   → one text call, schema-constrained JSON
   ├─ 4. compute name + path, then file it
   └─ 5. hand off to the external archiver
```

## Components

Target layout. None of it exists yet — the repository is at the specification stage.

- `cmd/tabularium/` — `main.go` only: wires the exit codes `0`/`1`/`2` and a
  `context.Context` cancelled on SIGINT/SIGTERM. Cobra returns an error and would exit 1
  for everything, so code `2` is mapped explicitly here.
- `internal/cli/` — thin cobra wrappers. Parse, validate, call, format. No business
  logic, so every rule below stays testable without a terminal.
- `internal/<domain>/` — the work itself, in packages named for the domain they serve.
  Constitution III forbids `utils`, `helpers`, `common` and `base`.

The domains the specification implies: content-type detection, text extraction, model
conversation, metadata analysis, filename derivation, rule evaluation, filesystem
filing, and external hand-off.

## Design Decisions

1. **Text layer before OCR.** A PDF born digital costs zero model calls. Verified as
   worthwhile: the alternative — always rasterising — spends several calls per document
   where `pdftotext` answers in milliseconds.

2. **Two model calls, not one.** Verbatim transcription and metadata judgement are
   separate concerns with separate failure modes, and they can target different models:
   a vision model locally for OCR, a text model anywhere for analysis. A single
   multimodal call would mix transcription with judgement and break on multi-page
   documents.

3. **Schema-constrained output, not prompt-requested JSON.** Measured against the local
   stack: with no constraint the model answers in prose; with "reply in JSON" it emits
   valid JSON with an invented structure; only a transmitted schema produces the asked-for
   fields. An endpoint that refuses the constraint is a configuration error, reported as
   such rather than left to fail intermittently.

4. **The model never names a folder.** It produces a `type` drawn from a closed
   vocabulary, plus tags. Every directory level is written literally in a rule's path
   template. Consequence: the complete set of destinations the tool can create is
   readable in the configuration, and a model mistake can only move a document between
   declared folders — never invent one.

5. **A closed vocabulary bounds validity, not correctness.** The same measurement showed
   the model returning an in-vocabulary but wrong value. Hence a mandatory fallback value,
   and `--dry-run` as the way to see the plan before anything moves.

6. **Local filing survives an external failure.** If filing succeeds and the archiver
   fails, the file stays filed, the error goes to stderr, and the exit code is 1. A
   sidecar records what was done, so re-running replays only the missing hand-off.

## Integration Points

- **Model endpoint** — any OpenAI-compatible chat-completions service. Local (Ollama,
  llama.cpp, vLLM) and hosted differ by configuration alone. Two independent
  configurations: one for OCR, one for analysis.
- **poppler-utils** — `pdftotext` and `pdftoppm`, required only when a PDF is processed.
  A runtime dependency, documented in `--help` and reported by name when missing.
- **External archiver** — an arbitrary command described entirely in configuration:
  binary plus templated arguments. Treated as a black box: its exit code decides, its
  output is recorded raw. No identifier is parsed and no format is presumed.

## Data Flow

Source file → sniffed type → extracted text (+ truncation flag) → schema-validated
metadata → sanitised filename with an optional `YYYY-MM-DD` prefix → first matching rule
→ destination path → move/copy/keep → sidecar → external command.

Collisions resolve on content: the same bytes already at the destination is a duplicate,
reported and exited 0; different bytes get a numeric suffix.
