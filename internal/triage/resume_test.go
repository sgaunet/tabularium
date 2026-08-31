package triage_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/analysis"
	"github.com/sgaunet/tabularium/internal/archiver"
	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/config"
	"github.com/sgaunet/tabularium/internal/extract"
	"github.com/sgaunet/tabularium/internal/filing"
	"github.com/sgaunet/tabularium/internal/triage"
)

// countingModel answers correctly and counts every request, so "zero model
// calls" is a claim a test can check rather than assert.
func countingModel(t *testing.T, reply string) (*chat.Client, *atomic.Int32) {
	t.Helper()

	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(s.Close)

	c, err := chat.New(chat.Options{BaseURL: s.URL, Model: "stub", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	return c, &calls
}

// countingDeps is deps() with a call counter attached.
func countingDeps(t *testing.T, cfg *config.Config, reply string) (triage.Deps, *atomic.Int32) {
	t.Helper()
	client, calls := countingModel(t, reply)
	return triage.Deps{
		Config:   cfg,
		Extract:  extract.Options{Vision: client, MaxPages: 20, DPI: 72},
		Analyser: analysis.New(client, analysis.Vocabularies{Types: cfg.Types, Tags: cfg.Tags}, 3),
	}, calls
}

// filedDocument runs the pipeline once with a failing archiver, leaving a filed
// document whose hand-off is outstanding — the state US4 exists to recover.
func filedDocument(t *testing.T, root, scans string) (dest string) {
	t.Helper()

	doc := corpusFile(t, scans, "notes.txt")
	a := buildArchiver(t, archiver.Options{
		Command: fakeArchiver(t, "echo 'connection refused' >&2\nexit 7\n"),
		Args:    []string{"{{.Path}}"},
	})

	_, out, err := executeWith(t, root, doc, a)
	if err == nil {
		t.Fatal("the setup run succeeded; it was meant to fail at the hand-off")
	}
	if !out.Filed {
		t.Fatal("the setup run did not file the document")
	}
	return out.Dest
}

func TestReplayRunsOnlyTheHandOff(t *testing.T) {
	// FR-052 and SC-004: a single re-run finishes the job without redoing
	// extraction, analysis, or filing.
	root, scans := t.TempDir(), t.TempDir()
	dest := filedDocument(t, root, scans)

	filed := filepath.Join(root, filepath.FromSlash(dest))
	before, err := os.Stat(filed)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	cfg := testConfig(t, root, testRules)
	deps, calls := countingDeps(t, cfg, metadataReply(nil))

	f, err := filing.Open(root, nil)
	if err != nil {
		t.Fatalf("filing.Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	// This time the archiver works.
	ran := filepath.Join(t.TempDir(), "ran.txt")
	good := buildArchiver(t, archiver.Options{
		Command: fakeArchiver(t, "echo x > '"+ran+"'\nexit 0\n"),
		Args:    []string{"{{.Path}}"},
	})

	out, handled, err := triage.Replay(context.Background(), filed, deps,
		triage.FileOptions{Filer: f, Archiver: good})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !handled {
		t.Fatal("Replay() did not recognise an already-filed document")
	}

	// SC-004: zero model requests.
	if n := calls.Load(); n != 0 {
		t.Errorf("the model endpoint was called %d times on a replay, want 0", n)
	}
	// Zero file moves: the document is untouched.
	after, err := os.Stat(filed)
	if err != nil {
		t.Fatalf("the filed document moved: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("the filed document was rewritten by a replay")
	}
	// And the hand-off ran.
	if _, err := os.Stat(ran); err != nil {
		t.Errorf("the archiver did not run: %v", err)
	}
	if !out.Archive.Success {
		t.Errorf("Archive = %+v, want a success", out.Archive)
	}
}

func TestASecondReplayDoesNothingAtAll(t *testing.T) {
	// FR-052: success recorded means there is nothing left to do, and doing it
	// again would file the document twice in the external system.
	root, scans := t.TempDir(), t.TempDir()
	dest := filedDocument(t, root, scans)
	filed := filepath.Join(root, filepath.FromSlash(dest))

	cfg := testConfig(t, root, testRules)
	deps, calls := countingDeps(t, cfg, metadataReply(nil))

	f, err := filing.Open(root, nil)
	if err != nil {
		t.Fatalf("filing.Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	ran := filepath.Join(t.TempDir(), "ran.txt")
	good := func() *archiver.Archiver {
		return buildArchiver(t, archiver.Options{
			Command: fakeArchiver(t, "echo x >> '"+ran+"'\nexit 0\n"),
			Args:    []string{"{{.Path}}"},
		})
	}

	// The recovering run.
	if _, handled, err := triage.Replay(context.Background(), filed, deps,
		triage.FileOptions{Filer: f, Archiver: good()}); err != nil || !handled {
		t.Fatalf("the recovering Replay: %v (handled=%v)", err, handled)
	}

	// And a third run, which must do nothing.
	out, handled, err := triage.Replay(context.Background(), filed, deps,
		triage.FileOptions{Filer: f, Archiver: good()})
	if err != nil {
		t.Fatalf("the third Replay: %v", err)
	}
	if !handled {
		t.Fatal("the third run did not recognise the document as done")
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the model endpoint was called %d times, want 0", n)
	}

	body, err := os.ReadFile(ran) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("reading the call log: %v", err)
	}
	if n := strings.Count(string(body), "x"); n != 1 {
		t.Errorf("the archiver ran %d times across two replays, want 1", n)
	}
	if !out.Archive.Success {
		t.Errorf("Archive = %+v, want the recorded success", out.Archive)
	}
}

func TestAFailingReplayStillFails(t *testing.T) {
	root, scans := t.TempDir(), t.TempDir()
	dest := filedDocument(t, root, scans)
	filed := filepath.Join(root, filepath.FromSlash(dest))

	cfg := testConfig(t, root, testRules)
	deps, _ := countingDeps(t, cfg, metadataReply(nil))

	f, err := filing.Open(root, nil)
	if err != nil {
		t.Fatalf("filing.Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	bad := buildArchiver(t, archiver.Options{
		Command: fakeArchiver(t, "exit 9\n"),
		Args:    []string{"{{.Path}}"},
	})

	_, handled, err := triage.Replay(context.Background(), filed, deps,
		triage.FileOptions{Filer: f, Archiver: bad})
	if !handled {
		t.Fatal("Replay() did not recognise an already-filed document")
	}
	if err == nil {
		t.Fatal("Replay() = nil error when the archiver failed again")
	}

	// The new exit code is recorded, so the next run sees the latest state.
	body, readErr := os.ReadFile(filed + ".tabularium.json") //nolint:gosec // a path this test made
	if readErr != nil {
		t.Fatalf("reading the sidecar: %v", readErr)
	}
	if !strings.Contains(string(body), `"exit_code": 9`) {
		t.Errorf("the sidecar was not updated with the new outcome:\n%s", body)
	}
}

func TestADocumentOutsideTheArchiveIsNotAReplay(t *testing.T) {
	root, scans := t.TempDir(), t.TempDir()
	doc := corpusFile(t, scans, "notes.txt")

	cfg := testConfig(t, root, testRules)
	deps, _ := countingDeps(t, cfg, metadataReply(nil))

	f, err := filing.Open(root, nil)
	if err != nil {
		t.Fatalf("filing.Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if _, handled, err := triage.Replay(context.Background(), doc, deps,
		triage.FileOptions{Filer: f}); handled || err != nil {
		t.Errorf("Replay() handled a document from the scan box: handled=%v err=%v", handled, err)
	}
}

func TestAFiledDocumentWithNoSidecarIsNotAReplay(t *testing.T) {
	// Someone dropped a file into the archive by hand. It has never been
	// processed, so it is processed from the start.
	root := t.TempDir()
	dir := filepath.Join(root, "divers", "2025")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("preparing the tree: %v", err)
	}
	doc := filepath.Join(dir, "dropped.txt")
	if err := os.WriteFile(doc, []byte("a document somebody copied in by hand"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}

	cfg := testConfig(t, root, testRules)
	deps, _ := countingDeps(t, cfg, metadataReply(nil))

	f, err := filing.Open(root, nil)
	if err != nil {
		t.Fatalf("filing.Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if _, handled, err := triage.Replay(context.Background(), doc, deps,
		triage.FileOptions{Filer: f}); handled || err != nil {
		t.Errorf("Replay() handled a document with no sidecar: handled=%v err=%v", handled, err)
	}
}

func TestAnUnusableSidecarMeansReprocessFromTheStart(t *testing.T) {
	// FR-053: the bytes always win over the sidecar.
	root, scans := t.TempDir(), t.TempDir()
	dest := filedDocument(t, root, scans)
	filed := filepath.Join(root, filepath.FromSlash(dest))

	tests := []struct {
		name string
		body string
	}{
		{name: "unparseable", body: "not a sidecar at all"},
		{name: "an unknown version", body: `{"version": 99}`},
		{
			name: "a digest describing something else",
			body: mustReplaceDigest(t, filed+".tabularium.json", strings.Repeat("ab", 32)),
		},
	}

	cfg := testConfig(t, root, testRules)
	f, err := filing.Open(root, nil)
	if err != nil {
		t.Fatalf("filing.Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(filed+".tabularium.json", []byte(tt.body), 0o600); err != nil {
				t.Fatalf("writing the sidecar: %v", err)
			}

			deps, _ := countingDeps(t, cfg, metadataReply(nil))
			_, handled, err := triage.Replay(context.Background(), filed, deps,
				triage.FileOptions{Filer: f})
			if handled {
				t.Error("Replay() honoured an unusable sidecar")
			}
			if err != nil {
				t.Errorf("Replay() = %v; an unusable sidecar is not a failure, it is an absence", err)
			}
		})
	}
}

// mustReplaceDigest reads a sidecar and swaps its recorded digest, so the test
// exercises a mismatch against a file that is otherwise entirely valid.
func mustReplaceDigest(t *testing.T, path, digest string) string {
	t.Helper()
	body, err := os.ReadFile(path) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("reading the sidecar: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding the sidecar: %v", err)
	}
	out["source_digest"] = digest

	replaced, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("re-encoding the sidecar: %v", err)
	}
	return string(replaced)
}
