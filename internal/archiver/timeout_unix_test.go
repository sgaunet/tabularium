//go:build unix

package archiver_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/archiver"
)

func TestATimeoutKillsTheWholeProcessGroup(t *testing.T) {
	// FR-056. Killing only the direct child leaves a grandchild it backgrounded
	// running — which is how a "timed out" archiver goes on writing to the
	// terminal minutes later. Signalling the negated PID reaches the group.
	dir := t.TempDir()
	marker := filepath.Join(dir, "grandchild-survived")

	// A child that backgrounds a grandchild and then waits. If only the child
	// is killed, the grandchild lives on and writes the marker.
	body := "#!/bin/sh\n" +
		"( sleep 2; : > '" + marker + "' ) &\n" +
		"sleep 30\n"
	command := filepath.Join(dir, "archiver")
	if err := os.WriteFile(command, []byte(body), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatalf("writing the fixture: %v", err)
	}

	a := build(t, archiver.Options{Command: command, Timeout: 300 * time.Millisecond})

	got := a.Run(context.Background(), subject(t))
	if got.Success {
		t.Fatal("Run() succeeded against a command that never finishes")
	}
	if !strings.Contains(got.Err, "did not finish") {
		t.Errorf("Err = %q; want it to say the command timed out", got.Err)
	}

	// Well past the grandchild's own sleep. If it survived, the marker is there.
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a backgrounded grandchild survived the timeout: " +
			"only the direct child was killed")
	}
}

func TestWaitDelayBoundsAChildHoldingThePipesOpen(t *testing.T) {
	// Cmd.Wait blocks until the output pipes close, and a grandchild that
	// inherited them keeps them open after the child is dead. Without
	// Cmd.WaitDelay the tool hangs on exactly the misbehaving archiver the
	// timeout exists to escape.
	dir := t.TempDir()

	// The child exits immediately; the grandchild holds stdout open for a long
	// time.
	body := "#!/bin/sh\n" +
		"( sleep 30 ) &\n" +
		"exit 0\n"
	command := filepath.Join(dir, "archiver")
	if err := os.WriteFile(command, []byte(body), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatalf("writing the fixture: %v", err)
	}

	a := build(t, archiver.Options{Command: command, Timeout: 30 * time.Second})

	start := time.Now()
	a.Run(context.Background(), subject(t))
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("Run() took %v; WaitDelay did not bound the wait", elapsed)
	}
}
