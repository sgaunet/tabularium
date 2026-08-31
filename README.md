# Tabularium

Hand it one scanned document. It reads the text, works out what the document is, gives it
a name that says so, files it where it belongs, and passes it on to your archive.

```console
$ tabularium ~/Scans/facture.pdf
SOURCE       DEST                                              TYPE     TAGS
facture.pdf  factures/voiture/2025/2025-03-14-facture-voiture.pdf  facture  voiture, entretien
```

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

## Output and exit codes

stdout carries data only — the table above, or one JSON object with `--output=json`.
Logs, errors, progress, and the duplicate notice all go to stderr, so
`tabularium --output=json f.pdf | jq` is reliable on every supported document type.

| Code | Meaning |
|---|---|
| `0` | success, including a detected duplicate and a completed `--dry-run` |
| `1` | runtime failure — no text recovered, no matching rule, a write failed, the archiver refused the document |
| `2` | usage error — wrong argument count, an unreadable or unsupported file, invalid configuration, an endpoint that will not honour schema-constrained output |

A duplicate is reported on stderr and still exits `0`: the document is already archived,
which is what you wanted.

## Configuration

### Where the file goes

One YAML file. Tabularium looks for it in this order:

1. the path given to `--config` — if you name one and it is not there, that is an error,
   not a fallback;
2. `$XDG_CONFIG_HOME/tabularium/config.yaml`, when that variable is set;
3. `~/.config/tabularium/config.yaml`.

The same rule on every platform, deliberately not the platform's own configuration
directory: that would be `~/Library/Application Support` on macOS and `%AppData%` on
Windows, and neither is where anyone keeps a YAML file they edit by hand.

Finding nothing at step 3 is a usage error naming the path it wanted, because
`archive_root` has no default — a run without a configuration file could never have
succeeded.

The file is decoded **strictly**: a key Tabularium does not recognise is a usage error at
startup, not a setting that silently does nothing. That turns a typo into a message
naming the key, rather than into a rule that never fires and never explains why.

The quickest start is to copy the commented example, which documents every setting inline:

```sh
mkdir -p ~/.config/tabularium
cp specs/001-document-triage/contracts/config.example.yaml ~/.config/tabularium/config.yaml
```

The same file is embedded in the binary, so a release you downloaded carries its own
reference copy.

### The smallest thing that works

```yaml
archive_root: ~/Documents/Archive

analysis:
  base_url: http://localhost:11434/v1
  model: qwen2.5:14b

types:
  fallback: autre
  values: [facture, releve-bancaire, contrat, courrier, autre]

rules:
  - name: divers
    path: "divers/{{.Year}}"
```

That is enough to file any document that carries its own text — a born-digital PDF, an
office document, a text file. Add an `ocr:` section when you want to hand it a scan or a
photograph; until then nothing needs one, and a document that does will say so by name:

```console
$ tabularium scan.pdf
tabularium: scan.pdf needs a vision model to be read, and none is configured: set ocr.base_url and ocr.model
$ echo $?
2
```

### Where documents go

```yaml
archive_root: ~/Documents/Archive   # required; `~` is expanded
disposition: move                   # move (default) | copy | keep
```

`archive_root` must already exist. Tabularium will not create a tree at a path you may
have mistyped, and every write is confined inside it by the kernel rather than by a string
comparison — a rule that rendered a traversal, or a symlink pointing out of the tree,
fails at the syscall.

`disposition` decides what becomes of the file you handed in:

| Value | The archive | Your scan box |
|---|---|---|
| `move` (default) | gets the document | the original is removed, but only after the copy's SHA-256 has been verified |
| `copy` | gets the document | the original stays |
| `keep` | untouched | the original stays — nothing is written at all |

`--dry-run` overrides all three unconditionally.

### The two models

OCR and analysis are configured separately, so you can run a vision model on your own
machine and send the text somewhere else — or use one endpoint for both.

```yaml
ocr:                                    # only needed for scans and images
  base_url: http://localhost:11434/v1
  model: qwen2.5vl:7b
  timeout: 120s                         # default 120s
  max_pages: 20                         # default 20; pages beyond this are dropped
  dpi: 200                              # default 200, the usual floor for 10pt text
  api_key_env: TABULARIUM_OCR_API_KEY

analysis:                               # always needed, unless --no-analysis
  base_url: http://localhost:11434/v1
  model: qwen2.5:14b
  timeout: 120s                         # default 120s
  retries: 3                            # default 3, backed off 1s/2s/4s with jitter
  api_key_env: TABULARIUM_ANALYSIS_API_KEY
```

Local and hosted differ by `base_url` alone; no behaviour branches on it. Pages beyond
`max_pages` are dropped, and the fact is always reported — on stderr, and as
`truncation` in `--output=json`.

### What the model is allowed to say

```yaml
types:
  fallback: autre        # required, and must be one of `values`
  values:
    - facture
    - releve-bancaire
    - contrat
    - autre

tags:                    # optional — omit it entirely to leave tags free
  values: [voiture, entretien, sante, impots]
```

`types` is a closed set. It is sent to the model as the schema's `enum`, *and* checked
again locally when the answer comes back — because a service honouring the constraint is
never assumed. A type outside the set is rejected, which is what stops a model mistake
from creating a new branch of your tree.

`fallback` is what a document the model cannot place is filed as. It exists so such a
document stays *recognisable as unplaced* rather than being filed somewhere plausible and
wrong.

Bounding `tags` is optional but worth doing as soon as a rule conditions on one: a free
tag phrased differently from what a rule expects does not misfile the document, but it
does make it fall through to the default rule, and those accumulate quietly.

### Rules — where each document lands

Rules are **ordered**, and the first one that matches decides the path alone.

```yaml
rules:
  - name: factures-voiture
    type: facture               # equality
    tags: [voiture]             # ALL listed tags must be present
    path: "factures/voiture/{{.Year}}"

  - name: contrats-axa
    type: contrat
    correspondent: axa          # equality
    path: "contrats/axa"

  - name: factures
    type: facture
    path: "factures/{{.Correspondent}}/{{.Year}}"

  - name: divers                # no condition: the default rule
    path: "divers/{{.Year}}"
```

A rule with no `type`, `tags` or `correspondent` is the **default rule**, and there may be
at most one — a second would sit unreachable behind the first. Without a default, a
document matching nothing is a runtime failure naming the document, and nothing moves.

**The model never names a folder.** It produces a bounded type and some tags; every
directory level is literal text in a rule. That is the invariant the whole design rests
on: a model mistake can move a document between two folders you declared, but it can never
invent a third. Reading your `rules:` tells you the complete set of destinations.

A `path` is a [`text/template`](https://pkg.go.dev/text/template) with exactly these
fields available — and deliberately no `.Folder`:

| Field | Value |
|---|---|
| `.Type` | the document type, from your vocabulary |
| `.Correspondent` | who it is from |
| `.Reference` | invoice or contract number |
| `.Title` | the inferred title |
| `.Year` `.Month` `.Day` | zero-padded — `2025`, `03`, `14` — so paths sort correctly |
| `.Tags` | the tag list |

Values are sanitised before they reach a path, so a correspondent of `Garage Central`
becomes `garage-central` and one containing a `/` collapses to a hyphen rather than
inventing a directory level. A field that is empty where a rule needs it fails loudly:
`factures/{{.Correspondent}}/{{.Year}}` on a document with no correspondent is an error,
never `factures//2025`.

Templates are compiled when the configuration loads, so a malformed one is caught before
any document is read.

### The external archiver

Optional. Omit the section and the step never runs; `--no-archive` disables it for one run.

```yaml
archive:
  command: paperless-cli
  args:
    - documents
    - upload
    - "--title"
    - "{{.Title}}"
    - "{{.Path}}"
  args_each_tag: ["--tag", "{{.}}"]   # repeated once per tag
  timeout: 60s                        # default 60s
  env: [PAPERLESS_URL, PAPERLESS_TOKEN]
```

Each entry in `args` is its own template and becomes one distinct argument. **No shell is
involved**, so there are no quoting rules to get wrong and a title containing `;` or
`$(…)` is just a title. `args_each_tag` is rendered once per tag, with `{{.}}` bound to
the tag, so every tag arrives as its own argument.

Templates here may use `.Path` (the archived file, absolute), `.Title`, `.Type`,
`.Correspondent`, `.Reference`, `.Description`, `.Amount`, `.Currency`, `.Tags`,
`.DocumentDate` and `.DueDate`.

`env` names variables to forward. Credentials travel this way and never through `argv`,
which is world-readable through `ps` and `/proc`.

The command is a black box: a non-zero exit is the failure signal, its output is kept
verbatim for you to read, and nothing is parsed out of it. If it fails, the document
**stays filed** and the run exits `1` — re-running on the filed document replays only the
hand-off.

Everything the command writes is echoed on stderr as it arrives, each line named by the
command it came from, so a refusal explains itself:

```console
$ tabularium facture.pdf
archivis| archivis: unknown flag: --created
SOURCE       DEST                           TYPE     TAGS
facture.pdf  factures/edf/2025/…            facture  energie

archiver failed (exit 2)
tabularium: the document is filed at …, but the archiver did not accept it: archivis exited 2.
```

`--quiet` keeps a successful archiver silent, but still prints what a failing one said.

### Environment variables

Every setting below can be overridden without touching the file:

| Variable | Overrides |
|---|---|
| `TABULARIUM_ARCHIVE_ROOT` | `archive_root` |
| `TABULARIUM_DISPOSITION` | `disposition` |
| `TABULARIUM_OCR_BASE_URL` | `ocr.base_url` |
| `TABULARIUM_OCR_MODEL` | `ocr.model` |
| `TABULARIUM_OCR_TIMEOUT` | `ocr.timeout` |
| `TABULARIUM_OCR_MAX_PAGES` | `ocr.max_pages` |
| `TABULARIUM_OCR_DPI` | `ocr.dpi` |
| `TABULARIUM_ANALYSIS_BASE_URL` | `analysis.base_url` |
| `TABULARIUM_ANALYSIS_MODEL` | `analysis.model` |
| `TABULARIUM_ANALYSIS_TIMEOUT` | `analysis.timeout` |
| `TABULARIUM_ANALYSIS_RETRIES` | `analysis.retries` |
| `TABULARIUM_ARCHIVE_COMMAND` | `archive.command` |
| `TABULARIUM_ARCHIVE_TIMEOUT` | `archive.timeout` |

A value that will not parse — `TABULARIUM_OCR_MAX_PAGES=many` — is an error naming the
variable.

### Credentials

API keys come from the environment, never from the file:

```sh
export TABULARIUM_ANALYSIS_API_KEY='sk-…'
export TABULARIUM_OCR_API_KEY='sk-…'
```

Those two names work with no configuration at all. To keep a key under a name of your own,
point `api_key_env` at it:

```yaml
analysis:
  api_key_env: MY_OPENAI_KEY
```

No credential is ever printed, logged, or placed in an argv — not even at `--verbose`, and
not even when an error formats the whole configuration. That is a property of the type
that holds them, so it does not depend on anyone remembering.

### Precedence

Highest first:

```
flags > environment > config file > defaults
```

Only flags you actually type count. Leaving `--disposition` off does not overwrite your
configured value with the flag's own default:

```sh
# config.yaml says `disposition: copy`
tabularium doc.pdf                       # copy   — the file wins
TABULARIUM_DISPOSITION=keep tabularium doc.pdf   # keep — the environment wins
tabularium --disposition move doc.pdf    # move   — the flag wins
```

### What is checked before any document is read

Configuration errors are found at startup and exit `2`, so you hear about them once rather
than on the day a particular document happens to trip over one:

- an unknown key, anywhere in the file;
- `archive_root` unset, or a `disposition` that is not `move`, `copy` or `keep`;
- `types.values` empty, `types.fallback` unset, or a fallback that is not one of the values;
- a rule with no name, no path, or a name another rule already has;
- a second rule with no condition, which would be unreachable;
- a rule conditioning on a `type` that is not in `types.values`, which could never match;
- a malformed `path` or archiver-argument template.

To see what a configuration actually does without writing anything:

```sh
tabularium --dry-run --output=json ~/Scans/facture.pdf | jq '{rule, destination, metadata}'
```

## Batching

One document per invocation, so the exit code always describes exactly one document.

```sh
find ~/Scans -type f | xargs -n1 tabularium          # one at a time
find ~/Scans -type f | xargs -P4 -n1 tabularium      # four at a time
```

Parallel runs are safe by construction: destination names are claimed with
`O_CREAT|O_EXCL`, so two runs racing for the same name resolve to one winner and one
`-2`, with no lost file and no lock to leak if a process is killed.

## Requirements

Host tools, needed only for the formats that use them:

| Tool | Needed for |
|---|---|
| `pdftotext`, `pdftoppm` (`poppler-utils`) | PDFs |
| `libreoffice` | legacy `.doc`, `.xls`, `.ppt` |

Each is looked up only when a document of that kind is actually processed — a PDF-free
run never touches poppler — and a missing one is reported by name.

You also need an OpenAI-compatible model endpoint. The binary itself is static and
cgo-free.

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
