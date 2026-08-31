# Proposed amendment to the constitution — for the author's approval

**Status**: proposed, not applied. The constitution's amendment procedure requires the
author's approval, so this is a diff and a rationale rather than a change.

**Version bump**: `1.0.0` → `1.0.1` (PATCH).

PATCH is the right level: the versioning policy reserves it for "clarifications, wording,
and non-semantic refinements". Nothing normative changes. Both edits are to *rationale*
and to an illustrative parenthesis, and no existing code becomes compliant or
non-compliant either way.

---

## Why

Phase 0 decision D1 chose the standard library's `flag` over Cobra, under the carve-out
Principle III already grants ("cobra, or stdlib `flag` when there are no subcommands").
Tabularium has no subcommands, so the carve-out applies and the choice is compliant as the
constitution stands.

What is now wrong is not the requirement but the *explanation* attached to it. Principle
II's rationale explains the exit-code rule by describing what Cobra does. A reader of this
repository will not find Cobra anywhere, and an explanation that refers to a dependency the
project does not have is worse than no explanation — it sends them looking for something
that is not there.

The same drift has already been corrected in `CLAUDE.md`, `docs/architecture.md` and
`docs/patterns.md`, which were changed as part of task T084. This file exists because the
constitution may not be edited the same way.

---

## Edit 1 — Principle II, the fourth bullet

The bullet explains the exit-code requirement in terms of Cobra's behaviour. The
requirement itself — that code `2` be wired explicitly in `main` — is unchanged and remains
exactly as binding.

```diff
 ### II. Pipe-Safe Output Contract (NON-NEGOTIABLE)

 - Stdout MUST carry data only, machine-parseable, selectable with `--output=text|json`.
 - Stderr MUST carry logs, errors, and progress. Nothing diagnostic may reach stdout.
 - Exit codes MUST be `0` success, `1` runtime failure, `2` usage error. They MUST be
   documented in `--help` and covered by a test.
-- Cobra sets no exit code of its own — `Execute()` returns an error and the scaffolded
-  `main` exits 1 for everything — so code `2` MUST be wired explicitly in `main`.
+- A command layer sets no exit code of its own: it returns an error, and a scaffolded
+  `main` exits 1 for everything — so code `2` MUST be wired explicitly in `main`. With
+  the standard library's `flag`, `ContinueOnError` is also mandatory, because
+  `ExitOnError` calls `os.Exit(2)` itself and would bypass that wiring entirely,
+  including for the cases that must exit 1.
```

The added sentence is not a new requirement. It is the same requirement stated for the
parser this project actually uses, and it names the specific way that requirement is
silently defeated — which is the sort of thing a rationale is for.

## Edit 2 — Principle III, the third paragraph of the rationale

```diff
 Rationale: Logic reachable only through a cobra command is logic that can only be tested
-through a cobra command. Domain-named packages keep dependencies pointing one way.
+through a cobra command. Domain-named packages keep dependencies pointing one way.
```

```diff
-Rationale: Logic reachable only through a cobra command is logic that can only be tested
-through a cobra command. Domain-named packages keep dependencies pointing one way.
+Rationale: Logic reachable only through a command object is logic that can only be tested
+by constructing one. Domain-named packages keep dependencies pointing one way.
```

## Not changed

Principle III's normative bullet already names both parsers and needs no edit:

> Commands MUST be thin wrappers (cobra, or stdlib `flag` when there are no subcommands):
> parse, validate, call, format.

This is the clause that made D1 compliant in the first place. It stays as it is.

---

## Sync impact, if approved

The constitution's own header carries a Sync Impact Report. Approving this would add:

```
Version change: 1.0.0 → 1.0.1
Bump rationale: PATCH — wording only. Principle II's rationale and Principle III's
rationale described the exit-code and testability requirements in terms of Cobra, which
feature 001-document-triage does not use (research.md D1 chose stdlib `flag` under the
carve-out Principle III already grants). No normative requirement changed.

Templates requiring updates:
- ✅ .specify/templates/plan-template.md — no Cobra reference; no change needed.
- ✅ .specify/templates/tasks-template.md — no Cobra reference; no change needed.
- ✅ CLAUDE.md, docs/architecture.md, docs/patterns.md — already corrected (T084).
```

The amendment procedure also requires that "an amendment that changes a gate MUST update
`.specify/templates/` in the same change". This one changes no gate, and the templates
were checked: neither mentions Cobra.
