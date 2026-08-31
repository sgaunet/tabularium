package archiver_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/archiver"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	path := filepath.Join(t.TempDir(), "archiver")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatalf("writing the fixture: %v", err)
	}
	return path
}

func TestANonZeroExitIsAFailureOfTheStep(t *testing.T) {
	// FR-059: the exit code is the failure signal, and it is recorded as the
	// command's own rather than translated into something of ours.
	for _, code := range []int{1, 2, 42, 127} {
		t.Run(strings.TrimSpace(string(rune('0'+code%10))), func(t *testing.T) {
			a := build(t, archiver.Options{
				Command: script(t, "exit "+itoa(code)+"\n"),
			})

			got := a.Run(context.Background(), subject(t))
			if got.Success {
				t.Errorf("Success = true for a command that exited %d", code)
			}
			if !got.Attempted {
				t.Error("Attempted = false; the command did run")
			}
			if got.ExitCode == nil {
				t.Fatal("ExitCode = nil; the command produced one")
			}
			if *got.ExitCode != code {
				t.Errorf("ExitCode = %d, want %d", *got.ExitCode, code)
			}
		})
	}
}

func TestASuccessRecordsExitZero(t *testing.T) {
	a := build(t, archiver.Options{Command: script(t, "exit 0\n")})

	got := a.Run(context.Background(), subject(t))
	if !got.Success {
		t.Fatalf("Success = false: %+v", got)
	}
	if got.ExitCode == nil || *got.ExitCode != 0 {
		t.Errorf("ExitCode = %v, want 0", got.ExitCode)
	}
	if got.Err != "" {
		t.Errorf("Err = %q for a successful run", got.Err)
	}
}

func TestBothStreamsAreCapturedVerbatim(t *testing.T) {
	// FR-057: stdout and stderr are kept for a human to read. Nothing is parsed
	// for an identifier and no output format is presumed — the archiver is a
	// black box, and this is the trace of what it said.
	a := build(t, archiver.Options{
		Command: script(t, "echo 'on stdout'\necho 'on stderr' >&2\nexit 0\n"),
	})

	got := a.Run(context.Background(), subject(t))
	if !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}
	for _, want := range []string{"on stdout", "on stderr"} {
		if !strings.Contains(got.Output, want) {
			t.Errorf("Output does not carry %q:\n%s", want, got.Output)
		}
	}
}

func TestOutputIsTruncatedAtEightKiB(t *testing.T) {
	// A runaway archiver must not fill the sidecar. The cut is marked, so a
	// reader knows there was more rather than wondering why it stops.
	a := build(t, archiver.Options{
		Command: script(t, "i=0\nwhile [ $i -lt 2000 ]; do echo 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; i=$((i+1)); done\nexit 0\n"),
	})

	got := a.Run(context.Background(), subject(t))
	if !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}
	if len(got.Output) > (8<<10)+64 {
		t.Errorf("Output is %d bytes, want it truncated at 8 KiB", len(got.Output))
	}
	if !strings.Contains(got.Output, "truncated") {
		t.Errorf("the truncation is not marked:\n%s", got.Output[max(0, len(got.Output)-200):])
	}
}

func TestNothingIsParsedOutOfTheOutput(t *testing.T) {
	// The archiver is a black box. An identifier in its output is for a person
	// to read, and presuming a format would tie the tool to one product.
	a := build(t, archiver.Options{
		Command: script(t, `echo '{"id": 4127, "status": "created"}'`+"\nexit 0\n"),
	})

	got := a.Run(context.Background(), subject(t))
	if !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}
	if !strings.Contains(got.Output, `{"id": 4127, "status": "created"}`) {
		t.Errorf("the output was not kept verbatim:\n%s", got.Output)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
