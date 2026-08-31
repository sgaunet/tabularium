package archiver_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/archiver"
)

func TestTheOutputIsEchoedLineByLineAsItArrives(t *testing.T) {
	// FR-057a: what the archiver said is the only thing that tells the user what
	// to fix. It reaches the diagnostic channel named by the command it came
	// from, so it is never mistaken for one of our own lines in a scrollback.
	var echo bytes.Buffer
	a := build(t, archiver.Options{
		Command: script(t, "echo 'on stdout'\necho 'on stderr' >&2\nexit 0\n"),
		Echo:    &echo,
	})

	got := a.Run(context.Background(), subject(t))
	if !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}

	for _, want := range []string{"archiver| on stdout", "archiver| on stderr"} {
		if !strings.Contains(echo.String(), want) {
			t.Errorf("the echo does not carry %q:\n%s", want, echo.String())
		}
	}

	// The capture is unchanged: the sidecar keeps the command's own words, not
	// ours (FR-057).
	for _, want := range []string{"on stdout", "on stderr"} {
		if !strings.Contains(got.Output, want) {
			t.Errorf("Output does not carry %q:\n%s", want, got.Output)
		}
	}
	if strings.Contains(got.Output, "archiver|") {
		t.Errorf("the prefix leaked into the recorded output:\n%s", got.Output)
	}
}

func TestALastLineWithoutANewlineIsStillEchoed(t *testing.T) {
	// A usage message that ends without a newline is exactly the kind a failing
	// command emits. Holding it back until a newline that never comes would drop
	// the one line the user needed.
	var echo bytes.Buffer
	a := build(t, archiver.Options{
		Command: script(t, "printf 'unknown flag: --created'\nexit 2\n"),
		Echo:    &echo,
	})

	got := a.Run(context.Background(), subject(t))
	if got.Success {
		t.Fatalf("Run() succeeded for a command that exited 2: %+v", got)
	}
	if !strings.Contains(echo.String(), "archiver| unknown flag: --created") {
		t.Errorf("the unterminated last line was dropped:\n%q", echo.String())
	}
}

func TestWithoutAnEchoTheOutputIsStillCaptured(t *testing.T) {
	// A nil Echo is the quiet case, and it must change nothing else: the sidecar
	// still gets the trace.
	a := build(t, archiver.Options{
		Command: script(t, "echo 'connection refused' >&2\nexit 7\n"),
	})

	got := a.Run(context.Background(), subject(t))
	if !strings.Contains(got.Output, "connection refused") {
		t.Errorf("Output does not carry what the command said:\n%s", got.Output)
	}
}
