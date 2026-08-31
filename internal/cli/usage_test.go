package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/cli"
)

// help runs --help and returns what landed on each stream.
func help(t *testing.T) (stdout, stderr string) {
	t.Helper()
	var out, errOut strings.Builder
	if err := cli.Run(context.Background(), []string{"--help"}, &out, &errOut); err != nil {
		t.Fatalf("Run(--help) = %v, want nil so main exits 0", err)
	}
	return out.String(), errOut.String()
}

func TestHelpDocumentsTheExitCodes(t *testing.T) {
	// FR-068: the exit codes are the tool's contract inside a shell script, so
	// their presence in --help is a test case, not a convention.
	stdout, _ := help(t)

	for _, want := range []string{
		"0", "success",
		"1", "runtime failure",
		"2", "usage error",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("--help does not mention %q", want)
		}
	}
}

func TestHelpStatesThePrecedenceVerbatim(t *testing.T) {
	// FR-072 requires this exact ordering to be stated. Quoting it verbatim is
	// what keeps the documentation and the implementation from drifting apart.
	const precedence = "flags > environment > config file > defaults"

	stdout, _ := help(t)
	if !strings.Contains(stdout, precedence) {
		t.Errorf("--help does not state the precedence %q", precedence)
	}
}

func TestHelpNamesTheHostTools(t *testing.T) {
	// FR-013: a user whose PDF fails because poppler is missing should be able
	// to find that out from --help rather than from the failure.
	stdout, _ := help(t)

	for _, tool := range []string{"pdftotext", "pdftoppm", "libreoffice", "poppler-utils"} {
		if !strings.Contains(stdout, tool) {
			t.Errorf("--help does not name the host tool %q", tool)
		}
	}
}

func TestHelpListsEveryFlag(t *testing.T) {
	stdout, _ := help(t)

	flags := []string{
		"--config", "--output", "--dry-run", "--disposition", "--no-analysis",
		"--no-file", "--no-archive", "--quiet", "--verbose", "--version", "--help",
	}
	for _, f := range flags {
		if !strings.Contains(stdout, f) {
			t.Errorf("--help does not list %s", f)
		}
	}
}

func TestHelpStatesTheStreamSplit(t *testing.T) {
	stdout, _ := help(t)
	if !strings.Contains(stdout, "stdout") || !strings.Contains(stdout, "stderr") {
		t.Error("--help does not explain which stream carries what")
	}
}

func TestHelpGoesToStdoutWhenItWasAskedFor(t *testing.T) {
	// --help is the requested output of the command, so it belongs on stdout —
	// `tabularium --help | less` is what a person actually types. Help printed
	// alongside a usage error is a different case, and goes to stderr.
	stdout, stderr := help(t)

	if stdout == "" {
		t.Fatal("--help wrote nothing to stdout")
	}
	if stderr != "" {
		t.Errorf("--help wrote to stderr as well:\n%s", stderr)
	}
}

func TestUsageErrorCarriesItsCause(t *testing.T) {
	inner := cli.Usagef("the %s is wrong", "thing")
	if got := inner.Error(); !strings.Contains(got, "the thing is wrong") {
		t.Errorf("UsageError.Error() = %q, want it to carry the message", got)
	}
}
