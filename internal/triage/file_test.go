package triage_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/filing"
	"github.com/sgaunet/tabularium/internal/triage"
)

// filedAt is a fixed clock, so a sidecar's timestamp is assertable.
var filedAt = time.Date(2025, 3, 16, 9, 41, 12, 0, time.UTC)

// execute builds a plan and carries it out, returning everything a test needs
// to say what happened.
func execute(t *testing.T, root, doc string, d triage.Disposition) (triage.Plan, triage.Outcome, error) {
	t.Helper()

	cfg := testConfig(t, root, testRules)
	plan, err := triage.Build(context.Background(), doc, deps(t, cfg, metadataReply(nil)),
		triage.Options{Disposition: d})
	if err != nil {
		return triage.Plan{}, triage.Outcome{}, err
	}

	f, err := filing.Open(root, nil)
	if err != nil {
		t.Fatalf("filing.Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	out, err := triage.Execute(context.Background(), plan, triage.FileOptions{
		Filer: f,
		Now:   func() time.Time { return filedAt },
	})
	return plan, out, err
}

func TestDispositions(t *testing.T) {
	// FR-042. "conservation sans écriture" is the spec's phrase for keep: the
	// document is described and nothing at all is written.
	tests := []struct {
		name        string
		disposition triage.Disposition
		wantFiled   bool
		wantSource  bool
	}{
		{
			name:        "move is the default and empties the scan box",
			disposition: triage.Move, wantFiled: true, wantSource: false,
		},
		{
			name:        "copy files the document and leaves the source",
			disposition: triage.Copy, wantFiled: true, wantSource: true,
		},
		{
			name:        "keep writes nothing at all",
			disposition: triage.Keep, wantFiled: false, wantSource: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, scans := t.TempDir(), t.TempDir()
			doc := corpusFile(t, scans, "notes.txt")

			plan, out, err := execute(t, root, doc, tt.disposition)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}

			if out.Filed != tt.wantFiled {
				t.Errorf("Filed = %v, want %v", out.Filed, tt.wantFiled)
			}

			_, statErr := os.Stat(doc)
			if tt.wantSource && statErr != nil {
				t.Errorf("the source is gone: %v", statErr)
			}
			if !tt.wantSource && statErr == nil {
				t.Error("the source survived a move")
			}

			filed := filepath.Join(root, filepath.FromSlash(plan.Dest))
			_, filedErr := os.Stat(filed)
			if tt.wantFiled && filedErr != nil {
				t.Errorf("the document was not filed: %v", filedErr)
			}
			if !tt.wantFiled && filedErr == nil {
				t.Errorf("keep wrote %s despite meaning no write at all", plan.Dest)
			}
		})
	}
}

func TestKeepWritesNoSidecarEither(t *testing.T) {
	// "sans écriture" means no write of any kind, and a sidecar is a write.
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")

	if _, _, err := execute(t, root, doc, triage.Keep); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if left := fingerprint(t, root); len(left) != 0 {
		t.Errorf("keep wrote into the archive: %v", left)
	}
}

func TestFilingWritesTheSidecarBesideTheDocument(t *testing.T) {
	// FR-051: on every successful filing, not only on failure.
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")

	plan, out, err := execute(t, root, doc, triage.Move)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	path := filepath.Join(root, filepath.FromSlash(out.Dest)+".tabularium.json")
	body, err := os.ReadFile(path) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("reading the sidecar: %v", err)
	}

	for _, want := range []string{
		`"version": 1`,
		`"rule": "` + plan.Rule.Name + `"`,
		`"filed_at": "2025-03-16T09:41:12Z"`,
		doc, // the source it came from
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the sidecar does not carry %q:\n%s", want, body)
		}
	}
}

func TestTheSidecarRecordsAnUnattemptedHandOff(t *testing.T) {
	// FR-062: no archiver configured means attempted=false, which is what
	// distinguishes "did not run" from "ran and failed" on the next run.
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")

	_, out, err := execute(t, root, doc, triage.Move)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Archive.Attempted {
		t.Error("Archive.Attempted = true with no archiver configured")
	}

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(out.Dest)+".tabularium.json")) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("reading the sidecar: %v", err)
	}
	if !strings.Contains(string(body), `"attempted": false`) {
		t.Errorf("the sidecar does not record an unattempted hand-off:\n%s", body)
	}
}

func TestADuplicateIsReportedAndNothingIsWritten(t *testing.T) {
	// FR-049 and SC-003: the user re-scanned something already filed.
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")

	// File it once.
	_, first, err := execute(t, root, doc, triage.Copy)
	if err != nil {
		t.Fatalf("the first Execute: %v", err)
	}
	if first.Duplicate {
		t.Fatal("the first filing reported a duplicate")
	}
	before := fingerprint(t, root)

	// And again, from the same bytes.
	_, second, err := execute(t, root, doc, triage.Copy)
	if err != nil {
		t.Fatalf("the second Execute: %v", err)
	}

	if !second.Duplicate {
		t.Error("Duplicate = false on re-filing identical content")
	}
	if second.Filed {
		t.Error("Filed = true for a duplicate")
	}
	if second.Dest != first.Dest {
		t.Errorf("Dest = %q, want the existing %q", second.Dest, first.Dest)
	}

	// Only the sidecar was rewritten; the document was not duplicated.
	after := fingerprint(t, root)
	if len(after) != len(before) {
		t.Errorf("the archive grew for a duplicate:\nbefore %v\nafter  %v", before, after)
	}
}

func TestExecuteHonoursCancellation(t *testing.T) {
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := triage.Execute(ctx, plan, triage.FileOptions{Filer: f}); err == nil {
		t.Fatal("Execute() with a cancelled context = nil error")
	}
	if left := fingerprint(t, root); len(left) != 0 {
		t.Errorf("an interrupted Execute left files behind: %v", left)
	}
	if _, err := os.Stat(doc); err != nil {
		t.Errorf("an interrupted Execute removed the source: %v", err)
	}
}
