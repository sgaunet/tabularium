package cli_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/cli"
)

// run invokes Run with both streams captured, the way main does.
func run(args ...string) (stdout, stderr string, err error) {
	var out, errOut strings.Builder
	err = cli.Run(context.Background(), args, &out, &errOut)
	return out.String(), errOut.String(), err
}

// isUsage reports whether err would send main down the exit-2 path.
func isUsage(err error) bool {
	var u *cli.UsageError
	return errors.As(err, &u)
}

func TestParsingContinuesOnErrorInsteadOfExiting(t *testing.T) {
	// flag.ExitOnError calls os.Exit(2) itself, which would bypass the
	// classification in main entirely — including the cases that must exit 1.
	// The observable proof that ContinueOnError is set is that Run *returns*.
	_, _, err := run("--no-such-flag", "doc.pdf")
	if err == nil {
		t.Fatal("Run() with an unknown flag = nil error; the process should not have survived to here silently")
	}
	if !isUsage(err) {
		t.Errorf("Run() with an unknown flag = %v; want a *cli.UsageError so main exits 2", err)
	}
}

func TestFlagSpellings(t *testing.T) {
	// Both -flag and --flag, and --flag=value, are stdlib flag behaviour and
	// part of the contract.
	tests := []struct {
		name string
		args []string
	}{
		{name: "single dash", args: []string{"-dry-run", "-output", "json"}},
		{name: "double dash", args: []string{"--dry-run", "--output", "json"}},
		{name: "equals form", args: []string{"--dry-run", "--output=json"}},
		{name: "mixed", args: []string{"-dry-run", "--output=json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append(append([]string{}, tt.args...), "doc.pdf")
			_, _, err := run(args...)
			// The document does not exist, so this fails — but it must fail on
			// the document, not on the spelling of the flags.
			if isUsage(err) && strings.Contains(err.Error(), "flag provided but not defined") {
				t.Fatalf("Run(%v) rejected the flag spelling: %v", args, err)
			}
		})
	}
}

func TestHelpIsExitZeroNotTwo(t *testing.T) {
	// flag.ErrHelp is not a usage error: the user asked for help and got it.
	for _, arg := range []string{"--help", "-h", "-help"} {
		t.Run(arg, func(t *testing.T) {
			stdout, _, err := run(arg)
			if err != nil {
				t.Fatalf("Run(%s) = %v, want nil so main exits 0", arg, err)
			}
			if stdout == "" {
				t.Errorf("Run(%s) printed nothing", arg)
			}
		})
	}
}

func TestVersionIsExitZeroAndNonEmpty(t *testing.T) {
	stdout, _, err := run("--version")
	if err != nil {
		t.Fatalf("Run(--version) = %v, want nil", err)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("Run(--version) printed nothing to stdout")
	}
	if !strings.Contains(stdout, "tabularium") {
		t.Errorf("Run(--version) = %q; want it to name the tool", stdout)
	}
}

func TestQuietAndVerboseTogetherIsAUsageError(t *testing.T) {
	// A silent precedence rule here would leave the user unsure which one won.
	_, _, err := run("--quiet", "--verbose", "doc.pdf")
	if err == nil {
		t.Fatal("Run(--quiet --verbose) = nil error; want a usage error")
	}
	if !isUsage(err) {
		t.Fatalf("Run(--quiet --verbose) = %v; want a *cli.UsageError", err)
	}
	if !strings.Contains(err.Error(), "quiet") || !strings.Contains(err.Error(), "verbose") {
		t.Errorf("error = %v; want it to name both flags", err)
	}
}

func TestArgumentCount(t *testing.T) {
	// FR-001: exactly one document. Batching is xargs -n1, which is what keeps
	// the exit code describing exactly one document.
	tests := []struct {
		name string
		args []string
	}{
		{name: "no argument at all", args: nil},
		{name: "two documents", args: []string{"a.pdf", "b.pdf"}},
		{name: "three documents", args: []string{"a.pdf", "b.pdf", "c.pdf"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := run(tt.args...)
			if err == nil {
				t.Fatalf("Run(%v) = nil error; want a usage error", tt.args)
			}
			if !isUsage(err) {
				t.Errorf("Run(%v) = %v; want a *cli.UsageError so main exits 2", tt.args, err)
			}
		})
	}
}

func TestUnknownFlagValuesAreUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "output format", args: []string{"--output", "xml", "doc.pdf"}, want: "xml"},
		{name: "disposition", args: []string{"--disposition", "teleport", "doc.pdf"}, want: "teleport"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := run(tt.args...)
			if err == nil {
				t.Fatalf("Run(%v) = nil error; want a usage error", tt.args)
			}
			if !isUsage(err) {
				t.Fatalf("Run(%v) = %v; want a *cli.UsageError", tt.args, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v; want it to quote %q", err, tt.want)
			}
		})
	}
}

func TestUsageErrorPrintsHelpOnStderrNotStdout(t *testing.T) {
	// A usage error must leave stdout untouched: a caller parsing JSON from a
	// pipeline of documents must not find a help screen mixed into its input.
	stdout, stderr, err := run()
	if err == nil {
		t.Fatal("Run() with no argument = nil error")
	}
	if stdout != "" {
		t.Errorf("a usage error wrote to stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("a usage error did not print usage on stderr; stderr was:\n%s", stderr)
	}
}
