package cli

import (
	"fmt"
	"io"
	"runtime/debug"
)

// Build metadata, set by the linker at release time. The -X flags in
// .goreleaser.yaml must name this package by its full module path; anything
// shorter is silently ignored by the linker and every release reports nothing.
var (
	Version string
	Commit  string
	Date    string
)

// UsageError is a mistake in how the tool was invoked or configured: the wrong
// number of arguments, an unreadable file, an unsupported type, a broken
// configuration, an endpoint that will not honour the schema.
//
// main tests for it with errors.As and exits 2. Everything else exits 1. The
// distinction matters inside a shell script: a usage error will fail the same
// way on every retry, and a runtime failure may not.
type UsageError struct {
	Err error
}

func (e *UsageError) Error() string { return e.Err.Error() }

func (e *UsageError) Unwrap() error { return e.Err }

// Usagef builds a UsageError. It takes a format so call sites can name the
// offending value, which is most of what makes exit 2 actionable.
func Usagef(format string, a ...any) *UsageError {
	return &UsageError{Err: fmt.Errorf(format, a...)}
}

// usageText is written by hand rather than generated from the flag set.
//
// FR-068 and FR-072 require the exit codes and the precedence order to appear
// here, and FR-013 requires the host tools; those are test cases, not
// conventions, so the text is a fixed string a test can assert against.
const usageText = `tabularium — read one document, name it, file it, hand it to your archiver.

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
`

// writeUsage prints the help block. It goes to stdout when the user asked for
// it and to stderr when it accompanies a usage error, so that a caller parsing
// stdout never finds a help screen mixed into its input.
func writeUsage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
}

// versionText is what --version prints. It is never empty: a release build gets
// the linker's values, and a plain `go build` falls back to the module version
// the toolchain stamped into the binary.
func versionText() string {
	v, commit, date := Version, Commit, Date
	if v == "" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
			v = bi.Main.Version
		}
	}
	if v == "" {
		v = "(unknown)"
	}

	out := "tabularium " + v
	if commit != "" {
		out += " (" + commit
		if date != "" {
			out += ", " + date
		}
		out += ")"
	}
	return out + "\n"
}
