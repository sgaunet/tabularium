# Contract: Command-Line Interface

**Feature**: `001-document-triage` | **Status**: design | **Plan**: [../plan.md](../plan.md)

This is the tool's public contract. Principle II makes the stdout/stderr split and the exit
codes breaking changes when altered, so this file is the reference the tests assert
against — not documentation written after the fact.

---

## Invocation

```
tabularium [flags] <file>
```

Exactly one positional argument. Zero, or more than one, is a **usage error** (FR-001).
Batching is `find … | xargs -n1 tabularium`; parallelism is `xargs -P`. The exit code
therefore always describes exactly one document.

---

## Flags

| Flag | Type | Default | Requirement |
|---|---|---|---|
| `--config <path>` | string | `$XDG_CONFIG_HOME/tabularium/config.yaml` | FR-072 |
| `--output <text\|json>` | string | `text` | FR-064 |
| `--dry-run` | bool | `false` | FR-043 |
| `--disposition <move\|copy\|keep>` | string | `move` | FR-042 |
| `--no-analysis` | bool | `false` | FR-019 |
| `--no-file` | bool | `false` | FR-062 |
| `--no-archive` | bool | `false` | FR-062 |
| `--quiet` | bool | `false` | FR-071 |
| `--verbose` | bool | `false` | FR-071 |
| `--version` | bool | `false` | — |
| `--help`, `-h` | bool | `false` | FR-068, FR-072 |

Both `-flag` and `--flag` spellings are accepted, as is `--flag=value`; that is stdlib
`flag` behaviour and it is part of the contract.

**Interactions**
- `--dry-run` overrides `--disposition` unconditionally. No write occurs regardless of any
  other setting (FR-043, SC-009).
- `--no-analysis` implies no filing and no archiving: without metadata no rule can match and
  no name can be computed. Output is the extracted text and nothing else (FR-019, and the
  spec's assumption).
- `--quiet` and `--verbose` together is a usage error rather than a silent precedence rule.
- Filing and archiving are on by default whenever they are configured (FR-061); the two
  `--no-*` flags disable them independently (FR-062).

---

## Exit codes

| Code | Meaning | Cases |
|---|---|---|
| `0` | success | filed; duplicate detected (FR-049, FR-069); `--dry-run` completed; nothing left to replay (FR-052); `--help`; `--version` |
| `1` | runtime failure | no text extracted (FR-015); schema-invalid response after retries (FR-018); no matching rule and no default (FR-040); write failure; archiver failed after a successful filing (FR-063) |
| `2` | usage error | wrong argument count (FR-001); unreadable or non-regular file (FR-005); unsupported type (FR-004); invalid configuration; endpoint that refuses schema-constrained output (FR-024) |

`flag.ContinueOnError` is mandatory in the flag set: `ExitOnError` calls `os.Exit(2)`
itself and would bypass the classification below. `flag.ErrHelp` maps to `0`, not `2`.

```go
// cmd/tabularium/main.go
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var usage *cli.UsageError
		if errors.As(err, &usage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
```

`cli.Run` takes both writers as parameters. That is what lets a test assert on stdout and
stderr independently without capturing process-global state, and it is the mechanical
enforcement of the split below.

---

## Stream split (FR-067, SC-005)

**stdout — data only.** Never a log line, never a progress indicator, never a warning. In
`--output=json` the entire stream is one JSON object and nothing else, which is what makes
`tabularium --output=json f.pdf | jq` reliable on every supported document type.

**stderr — everything else.** `slog` diagnostics, errors, progress, the duplicate notice,
the truncation notice, the sidecar-anomaly notice.

A notice that a duplicate was detected goes to **stderr** while the exit code is **0**
(FR-049, FR-069). The user is told; a parser reading stdout is not disturbed.

---

## Output — text (FR-065)

An aligned, borderless table via `text/tabwriter`:

```console
$ tabularium ~/Scans/facture.pdf
SOURCE       DEST                                                  TYPE     TAGS
facture.pdf  factures/voiture/2025/2025-03-14-facture-voiture.pdf  facture  voiture, entretien
```

`--dry-run` additionally prints the applied rule and the full metadata, since seeing the
plan before anything moves is the entire point of the mode (FR-043).

## Output — JSON (FR-066)

One object, schema at [`output.schema.json`](./output.schema.json). `SetEscapeHTML(false)`
so `&` in a title stays `&`.

---

## Terminal behaviour (FR-070)

Colour, spinners, and progress bars are emitted only when **both** hold:

1. `os.Stdout.Stat()` reports `ModeCharDevice`, and
2. `NO_COLOR` is unset.

Not either-or. A piped stdout suppresses them even on a colour-capable terminal.

---

## Configuration precedence (FR-072)

**flags > environment > config file > defaults**, stated verbatim in `--help`.

Implemented by resolving in the reverse order and applying the flag layer with
`FlagSet.Visit`, which iterates only flags actually present on the command line. Reading
the flag variables directly would let an unset `--output` overwrite a configured value with
its default — the precedence would be silently inverted for every flag left off.

Environment variables: `TABULARIUM_<SECTION>_<KEY>`, upper-snake — e.g.
`TABULARIUM_OCR_MODEL`, `TABULARIUM_ARCHIVE_ROOT`.

---

## `--help` (FR-068, FR-072, FR-013)

Written by hand, and asserted in a test: FR-068 and FR-072 require the exit codes and the
precedence order to appear here, so their presence is a test case, not a convention.

```text
tabularium — read one document, name it, file it, hand it to your archiver.

Usage:
  tabularium [flags] <file>

Flags:
  --config PATH        configuration file (default $XDG_CONFIG_HOME/tabularium/config.yaml)
  --output FORMAT      text | json (default text)
  --dry-run            compute and print the plan; write nothing
  --disposition WHAT   move | copy | keep (default move)
  --no-analysis        extract text only; implies --no-file and --no-archive
  --no-file            skip local filing
  --no-archive         skip the external archiver
  --quiet              errors only
  --verbose            debug diagnostics
  --version            print version and exit
  -h, --help           print this help and exit

Exit codes:
  0  success, including a detected duplicate and a completed --dry-run
  1  runtime failure
  2  usage error, including invalid configuration

Configuration precedence, highest first:
  flags > environment > config file > defaults

Output:
  stdout carries data only. Logs, errors and progress go to stderr.

Host tools, required only for the formats that need them:
  pdftotext, pdftoppm  (poppler-utils)  — PDF
  libreoffice                           — legacy .doc/.xls/.ppt
```

---

## External archiver invocation (FR-054 … FR-060)

The archiver is described entirely by configuration and treated as a black box.

**Process construction**
- `exec.CommandContext` with a rendered `argv`. **No shell** — each argument is its own
  template, rendered separately, and passed as a distinct element of `Cmd.Args` (FR-055).
- Credentials are placed in `Cmd.Env`, never in `Args`: an argv is world-readable through
  `ps` and `/proc` (FR-058, FR-073, SC-008). `Cmd.Env` is built explicitly from
  `os.Environ()` plus the configured entries, not inherited wholesale.
- `SysProcAttr{Setpgid: true}` and a `Cmd.Cancel` that signals the negated PID, so the
  timeout kills the whole process group and not just the direct child (FR-056).
  `Cmd.WaitDelay` bounds a child that ignores the signal or holds the pipes open.
- `exec.LookPath` before the first use; a missing binary is reported **by name** (FR-060).

**Result handling**
- A non-zero exit code is a failure of the step, recorded in the sidecar (FR-059).
- stdout and stderr are captured and stored verbatim, truncated to 8 KiB (FR-057). No
  identifier is parsed and no output format is presumed.
- The same bytes are echoed to **stderr** as they arrive, one line at a time, each prefixed
  with the command's base name — `archivis| unknown flag: --created` (FR-057a). `--quiet`
  switches the echo off, and a refused hand-off then prints the captured output with its
  error instead, so the reason is never hidden.
- A failure here does **not** unwind the filing: the file stays where it was filed, the
  error goes to stderr, the exit code is `1` (FR-063). Re-running replays only the
  hand-off (FR-052).

**Template fields available to `argv`**: `.Path` (the archived file), `.Title`, `.Type`,
`.Correspondent`, `.Reference`, `.Description`, `.Amount`, `.Currency`, `.Tags`,
`.DocumentDate`, `.DueDate`.

Tags expand to one argument each. In configuration:

```yaml
archive:
  command: paperless-cli
  args: ["document", "upload", "--title", "{{.Title}}", "{{.Path}}"]
  args_each_tag: ["--tag", "{{.}}"]      # repeated once per tag (FR-055)
  timeout: 60s
  env: ["PAPERLESS_TOKEN"]               # forwarded from the environment, never from argv
```

---

## Host tool contract (FR-013)

| Tool | Needed for | On absence |
|---|---|---|
| `pdftotext` | PDF text layer | error naming the binary and `poppler-utils` |
| `pdftoppm` | PDF rasterisation | same |
| `libreoffice` | legacy `.doc`/`.xls`/`.ppt` | error naming the binary |

Each is looked up only when a document of that kind is actually processed — a PDF-free run
never touches poppler — and all three are listed in `--help`.
