package triage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/archiver"
	"github.com/sgaunet/tabularium/internal/filing"
	"github.com/sgaunet/tabularium/internal/triage"
)

// fakeArchiver writes a shell script that behaves as told.
func fakeArchiver(t *testing.T, body string) string {
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

// executeWith runs the pipeline with an archiver attached.
func executeWith(t *testing.T, root, doc string, a *archiver.Archiver) (triage.Plan, triage.Outcome, error) {
	t.Helper()

	cfg := testConfig(t, root, testRules)
	plan, err := triage.Build(context.Background(), doc, deps(t, cfg, metadataReply(nil)),
		triage.Options{Disposition: triage.Move})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	f, err := filing.Open(root, nil)
	if err != nil {
		t.Fatalf("filing.Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	out, err := triage.Execute(context.Background(), plan, triage.FileOptions{
		Filer: f, Archiver: a, Now: func() time.Time { return filedAt },
	})
	return plan, out, err
}

func buildArchiver(t *testing.T, o archiver.Options) *archiver.Archiver {
	t.Helper()
	a, err := archiver.New(o)
	if err != nil {
		t.Fatalf("archiver.New: %v", err)
	}
	return a
}

func TestAFailedHandOffLeavesTheDocumentFiledAndFailsTheRun(t *testing.T) {
	// FR-063. The document is where the user asked for it to be. Unwinding a
	// good filing because a separate system was unavailable would turn one
	// problem into two — and the sidecar makes the missing half replayable.
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")

	a := buildArchiver(t, archiver.Options{
		Command: fakeArchiver(t, "echo 'connection refused' >&2\nexit 7\n"),
		Args:    []string{"{{.Path}}"},
	})

	_, out, err := executeWith(t, root, doc, a)

	if err == nil {
		t.Fatal("Execute() = nil error when the archiver failed")
	}
	var failed *triage.ArchiveFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("Execute() = %v; want a *triage.ArchiveFailedError", err)
	}

	// Filed, and still there.
	if !out.Filed {
		t.Error("Filed = false; the document was filed before the hand-off ran")
	}
	filed := filepath.Join(root, filepath.FromSlash(out.Dest))
	if _, statErr := os.Stat(filed); statErr != nil {
		t.Errorf("the filing was unwound because the archiver failed: %v", statErr)
	}
	if _, statErr := os.Stat(doc); statErr == nil {
		t.Error("the source was restored; the document really was filed")
	}

	// And the failure is recorded, which is what makes it replayable.
	if !out.Archive.Attempted {
		t.Error("Archive.Attempted = false")
	}
	if out.Archive.Success {
		t.Error("Archive.Success = true for a command that exited 7")
	}
	if out.Archive.ExitCode == nil || *out.Archive.ExitCode != 7 {
		t.Errorf("Archive.ExitCode = %v, want 7", out.Archive.ExitCode)
	}

	body, readErr := os.ReadFile(filed + ".tabularium.json") //nolint:gosec // a path this test made
	if readErr != nil {
		t.Fatalf("reading the sidecar: %v", readErr)
	}
	for _, want := range []string{`"attempted": true`, `"success": false`, `"exit_code": 7`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the sidecar does not record %s:\n%s", want, body)
		}
	}
}

func TestASuccessfulHandOffIsRecordedToo(t *testing.T) {
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")

	a := buildArchiver(t, archiver.Options{
		Command: fakeArchiver(t, "echo 'Document 4127 created'\nexit 0\n"),
		Args:    []string{"{{.Path}}"},
	})

	_, out, err := executeWith(t, root, doc, a)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !out.Archive.Attempted || !out.Archive.Success {
		t.Errorf("Archive = %+v, want an attempted success", out.Archive)
	}
	if !strings.Contains(out.Archive.Output, "Document 4127 created") {
		t.Errorf("Archive.Output = %q, want the command's own words", out.Archive.Output)
	}

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(out.Dest)) + ".tabularium.json") //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("reading the sidecar: %v", err)
	}
	if !strings.Contains(string(body), `"success": true`) {
		t.Errorf("the sidecar does not record the success:\n%s", body)
	}
}

func TestTheArchiverIsGivenTheArchivedPathNotTheSource(t *testing.T) {
	// FR-054: the hand-off carries the archived path. The source may not even
	// exist any more after a move.
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")

	recorded := filepath.Join(t.TempDir(), "path.txt")
	a := buildArchiver(t, archiver.Options{
		Command: fakeArchiver(t, "printf '%s' \"$1\" > '"+recorded+"'\nexit 0\n"),
		Args:    []string{"{{.Path}}"},
	})

	_, out, err := executeWith(t, root, doc, a)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	body, err := os.ReadFile(recorded) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("reading the recorded path: %v", err)
	}
	got := string(body)

	want := filepath.Join(root, filepath.FromSlash(out.Dest))
	if got != want {
		t.Errorf("the archiver was given %q, want the archived path %q", got, want)
	}
	if got == doc {
		t.Error("the archiver was given the source path, which no longer exists")
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("the path handed to the archiver does not exist: %v", err)
	}
}

func TestNoArchiverConfiguredMeansTheStepIsSkippedEntirely(t *testing.T) {
	// FR-062: archive is reported as unattempted, and the run succeeds.
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")

	_, out, err := executeWith(t, root, doc, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Archive.Attempted {
		t.Error("Archive.Attempted = true with no archiver configured")
	}
	if !out.Filed {
		t.Error("Filed = false; filing does not depend on the archiver")
	}
}

func TestADuplicateIsStillHandedOff(t *testing.T) {
	// The archive already holds the bytes, but the external system may never
	// have been told about them — which is precisely the state a failed
	// hand-off leaves behind.
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")

	calls := filepath.Join(t.TempDir(), "calls.txt")
	newArchiver := func() *archiver.Archiver {
		return buildArchiver(t, archiver.Options{
			Command: fakeArchiver(t, "echo x >> '"+calls+"'\nexit 0\n"),
			Args:    []string{"{{.Path}}"},
		})
	}

	if _, _, err := executeWith(t, root, doc, newArchiver()); err != nil {
		t.Fatalf("the first Execute: %v", err)
	}

	// The same bytes again.
	again := corpusFile(t, scans, "notes.txt")
	_, out, err := executeWith(t, root, again, newArchiver())
	if err != nil {
		t.Fatalf("the second Execute: %v", err)
	}
	if !out.Duplicate {
		t.Fatal("Duplicate = false on re-filing identical content")
	}
	if !out.Archive.Attempted {
		t.Error("a duplicate was not handed off; the external system may never have been told")
	}

	body, err := os.ReadFile(calls) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("reading the call log: %v", err)
	}
	if n := strings.Count(string(body), "x"); n != 2 {
		t.Errorf("the archiver ran %d times, want 2", n)
	}
}
