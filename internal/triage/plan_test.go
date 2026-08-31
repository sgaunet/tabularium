package triage_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/analysis"
	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/config"
	"github.com/sgaunet/tabularium/internal/extract"
	"github.com/sgaunet/tabularium/internal/triage"
)

// --- fixtures ---------------------------------------------------------------

// metadataReply is a conforming analysis response. Tests vary one field.
func metadataReply(overrides map[string]any) string {
	body := map[string]any{
		"title":         "Facture entretien véhicule",
		"type":          "facture",
		"correspondent": "Garage Central",
		"tags":          []string{"voiture", "entretien"},
		"document_date": "2025-03-14",
		"due_date":      nil,
		"reference":     "FA-2025-0312",
		"description":   nil,
		"amount":        "384.50",
		"currency":      "EUR",
		"filename":      "facture-entretien-vehicule",
	}
	for k, v := range overrides {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic("encoding a fixed test reply: " + err.Error())
	}
	reply, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": string(raw)}}},
	})
	if err != nil {
		panic("encoding a fixed test reply: " + err.Error())
	}
	return string(reply)
}

// stubModel answers every request with the same reply, and counts the calls.
func stubModel(t *testing.T, reply string) *chat.Client {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(s.Close)

	c, err := chat.New(chat.Options{BaseURL: s.URL, Model: "stub", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	return c
}

const testRules = `
types:
  fallback: autre
  values: [facture, releve-bancaire, contrat, autre]
rules:
  - name: factures-voiture
    type: facture
    tags: [voiture]
    path: "factures/voiture/{{.Year}}"
  - name: factures
    type: facture
    path: "factures/{{.Correspondent}}/{{.Year}}"
  - name: divers
    path: "divers/{{.Year}}"
`

// testConfig writes a configuration whose archive_root is root and returns it
// loaded and validated.
func testConfig(t *testing.T, root, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("archive_root: "+root+"\n"+body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := config.Load(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config.Validate: %v", err)
	}
	return cfg
}

// deps assembles the pipeline's collaborators against a stub endpoint.
func deps(t *testing.T, cfg *config.Config, reply string) triage.Deps {
	t.Helper()
	client := stubModel(t, reply)
	return triage.Deps{
		Config:   cfg,
		Extract:  extract.Options{Vision: client, MaxPages: 20, DPI: 72},
		Analyser: analysis.New(client, analysis.Vocabularies{Types: cfg.Types, Tags: cfg.Tags}, 3),
	}
}

// corpusFile copies a fixture into dir.
func corpusFile(t *testing.T, dir, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("reading corpus file %s: %v", name, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// fingerprint is every file under root, with its digest. Comparing two of these
// is how "byte-identical tree" is checked rather than asserted.
func fingerprint(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(path) //nolint:gosec // a path this test made
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s  %x", rel, h.Sum(nil)))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// --- tests ------------------------------------------------------------------

func TestBuildWritesNothing(t *testing.T) {
	// FR-043 and SC-009. This is the test that makes the whole design's central
	// claim checkable: computing a Plan cannot write, so --dry-run cannot.
	root := t.TempDir()
	scans := t.TempDir()

	// A tree with something already in it, so "unchanged" means something.
	if err := os.MkdirAll(filepath.Join(root, "factures", "voiture", "2025"), 0o750); err != nil {
		t.Fatalf("preparing the tree: %v", err)
	}
	existing := filepath.Join(root, "factures", "voiture", "2025", "already-here.pdf")
	if err := os.WriteFile(existing, []byte("previous contents"), 0o600); err != nil {
		t.Fatalf("preparing the tree: %v", err)
	}

	cfg := testConfig(t, root, testRules)
	doc := corpusFile(t, scans, "notes.txt")

	before := fingerprint(t, root)

	plan, err := triage.Build(context.Background(), doc, deps(t, cfg, metadataReply(nil)),
		triage.Options{Disposition: triage.Move})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := fingerprint(t, root); !equal(before, got) {
		t.Errorf("Build() changed the archive tree:\nbefore %v\nafter  %v", before, got)
	}
	if _, err := os.Stat(doc); err != nil {
		t.Errorf("Build() disturbed the source document: %v", err)
	}
	if plan.Dest == "" {
		t.Error("Build() computed no destination")
	}
}

func TestDestIsRelativeToTheArchiveRoot(t *testing.T) {
	// Storing it absolute would invite the string concatenation os.Root exists
	// to eliminate.
	root := t.TempDir()
	cfg := testConfig(t, root, testRules)
	doc := corpusFile(t, t.TempDir(), "notes.txt")

	plan, err := triage.Build(context.Background(), doc, deps(t, cfg, metadataReply(nil)),
		triage.Options{Disposition: triage.Move})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if filepath.IsAbs(plan.Dest) {
		t.Errorf("Dest = %q, want a path relative to the archive root", plan.Dest)
	}
	if strings.HasPrefix(plan.Dest, root) {
		t.Errorf("Dest = %q, want it not to repeat the archive root", plan.Dest)
	}
	want := "factures/voiture/2025/2025-03-14-facture-entretien-vehicule.txt"
	if plan.Dest != want {
		t.Errorf("Dest = %q, want %q", plan.Dest, want)
	}
}

func TestTheFirstMatchingRuleDecides(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t, root, testRules)

	tests := []struct {
		name     string
		reply    map[string]any
		wantRule string
		wantDir  string
	}{
		{
			name:     "the specific rule when its tag is present",
			reply:    map[string]any{"tags": []string{"voiture"}},
			wantRule: "factures-voiture", wantDir: "factures/voiture/2025",
		},
		{
			name:     "the general facture rule when it is not",
			reply:    map[string]any{"tags": []string{"assurance"}},
			wantRule: "factures", wantDir: "factures/garage-central/2025",
		},
		{
			name:     "the default rule for the fallback type",
			reply:    map[string]any{"type": "autre", "tags": []string{}},
			wantRule: "divers", wantDir: "divers/2025",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := corpusFile(t, t.TempDir(), "notes.txt")
			plan, err := triage.Build(context.Background(), doc,
				deps(t, cfg, metadataReply(tt.reply)), triage.Options{Disposition: triage.Move})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			if plan.Rule.Name != tt.wantRule {
				t.Errorf("Rule = %q, want %q", plan.Rule.Name, tt.wantRule)
			}
			if dir := filepath.Dir(plan.Dest); dir != tt.wantDir {
				t.Errorf("destination directory = %q, want %q", dir, tt.wantDir)
			}
		})
	}
}

func TestNoMatchingRuleAndNoDefaultIsARuntimeFailureThatMovesNothing(t *testing.T) {
	// FR-040. Inventing a destination would put the document somewhere the user
	// never named, which is the one outcome the rules design exists to prevent.
	root := t.TempDir()
	scans := t.TempDir()
	cfg := testConfig(t, root, `
types:
  fallback: autre
  values: [facture, autre]
rules:
  - name: factures
    type: facture
    path: "factures/{{.Year}}"
`)
	doc := corpusFile(t, scans, "notes.txt")

	before := fingerprint(t, root)

	_, err := triage.Build(context.Background(), doc,
		deps(t, cfg, metadataReply(map[string]any{"type": "autre"})),
		triage.Options{Disposition: triage.Move})
	if err == nil {
		t.Fatal("Build() with no matching rule and no default = nil error")
	}

	var noRule *triage.NoRuleError
	if !errors.As(err, &noRule) {
		t.Fatalf("Build() = %v; want a *triage.NoRuleError", err)
	}
	if !strings.Contains(err.Error(), doc) {
		t.Errorf("error = %v; want it to name the document", err)
	}
	if got := fingerprint(t, root); !equal(before, got) {
		t.Error("Build() changed the tree despite failing to place the document")
	}
	if _, statErr := os.Stat(doc); statErr != nil {
		t.Errorf("the source was disturbed: %v", statErr)
	}
}

func TestARuleReferencingAMissingValueFailsLoudly(t *testing.T) {
	// FR-039: `factures//2025` must never be produced. A rule that needs a
	// correspondent on a document that has none is an error the user can see
	// and fix, not a directory with an empty name.
	root := t.TempDir()
	cfg := testConfig(t, root, `
types:
  fallback: autre
  values: [facture, autre]
rules:
  - name: factures
    type: facture
    path: "factures/{{.Correspondent}}/{{.Year}}"
`)
	doc := corpusFile(t, t.TempDir(), "notes.txt")

	_, err := triage.Build(context.Background(), doc,
		deps(t, cfg, metadataReply(map[string]any{"correspondent": nil})),
		triage.Options{Disposition: triage.Move})
	if err == nil {
		t.Fatal("Build() with an empty path segment = nil error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %v; want it to say a segment came out empty", err)
	}
}

func TestNoAnalysisStopsAfterExtraction(t *testing.T) {
	// FR-019: without metadata no rule can match and no name can be computed.
	// The output is the extracted text and nothing else.
	root := t.TempDir()
	cfg := testConfig(t, root, testRules)
	doc := corpusFile(t, t.TempDir(), "notes.txt")

	plan, err := triage.Build(context.Background(), doc, deps(t, cfg, metadataReply(nil)),
		triage.Options{Disposition: triage.Move, NoAnalysis: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if plan.Text.Content == "" {
		t.Error("--no-analysis recovered no text")
	}
	if plan.Dest != "" {
		t.Errorf("Dest = %q with --no-analysis, want empty", plan.Dest)
	}
	if plan.Rule.Name != "" {
		t.Errorf("Rule = %q with --no-analysis, want none", plan.Rule.Name)
	}
	if plan.Metadata.Type != "" {
		t.Errorf("Metadata was inferred despite --no-analysis: %+v", plan.Metadata)
	}
}

func TestBuildCarriesTheTruncationThrough(t *testing.T) {
	// FR-008 and SC-010: the fact has to survive as far as the output and the
	// sidecar, so it is on the Plan rather than logged and forgotten.
	if _, err := lookPDFToText(); err != nil {
		t.Skip("pdftotext is not installed")
	}

	root := t.TempDir()
	cfg := testConfig(t, root, testRules)
	doc := corpusFile(t, t.TempDir(), "multipage.pdf")

	d := deps(t, cfg, metadataReply(nil))
	d.Extract.MaxPages = 4

	plan, err := triage.Build(context.Background(), doc, d,
		triage.Options{Disposition: triage.Move})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if plan.Text.Truncation == nil {
		t.Fatal("Truncation = nil for a 25-page document capped at 4")
	}
	if plan.Text.Truncation.PagesTotal != 25 {
		t.Errorf("PagesTotal = %d, want 25", plan.Text.Truncation.PagesTotal)
	}
}

func TestBuildRefusesAnUnreadableDocument(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t, root, testRules)

	_, err := triage.Build(context.Background(), filepath.Join(t.TempDir(), "nope.pdf"),
		deps(t, cfg, metadataReply(nil)), triage.Options{Disposition: triage.Move})
	if err == nil {
		t.Fatal("Build() on a missing document = nil error")
	}
}

func TestBuildHonoursCancellation(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t, root, testRules)
	doc := corpusFile(t, t.TempDir(), "notes.txt")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := triage.Build(ctx, doc, deps(t, cfg, metadataReply(nil)),
		triage.Options{Disposition: triage.Move}); err == nil {
		t.Fatal("Build() with a cancelled context = nil error")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lookPDFToText() (string, error) {
	return exec.LookPath("pdftotext")
}

func TestAModelSuppliedValueCannotInventADirectoryLevel(t *testing.T) {
	// SC-014: a model mistake can move a document between two declared folders;
	// it can never invent a third. A correspondent carrying a separator would
	// otherwise render a directory level that appears in no rule.
	root := t.TempDir()
	cfg := testConfig(t, root, `
types:
  fallback: autre
  values: [facture, autre]
rules:
  - name: factures
    type: facture
    path: "factures/{{.Correspondent}}/{{.Year}}"
`)

	tests := []struct {
		name          string
		correspondent string
		wantDir       string
		wantErr       bool
	}{
		{
			name:          "a separator collapses rather than nesting",
			correspondent: "Garage / Central", wantDir: "factures/garage-central/2025",
		},
		{
			name:          "a traversal cannot survive",
			correspondent: "../../etc", wantDir: "factures/etc/2025",
		},
		{
			name:          "a deep path collapses to one segment",
			correspondent: "a/b/c/d", wantDir: "factures/a-b-c-d/2025",
		},
		{
			name:          "a value that is nothing but separators fails loudly",
			correspondent: "///", wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := corpusFile(t, t.TempDir(), "notes.txt")
			plan, err := triage.Build(context.Background(), doc,
				deps(t, cfg, metadataReply(map[string]any{"correspondent": tt.correspondent})),
				triage.Options{Disposition: triage.Move})

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Build() with correspondent %q = nil error, dest %q",
						tt.correspondent, plan.Dest)
				}
				return
			}
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if dir := filepath.Dir(plan.Dest); dir != tt.wantDir {
				t.Errorf("destination directory = %q, want %q", dir, tt.wantDir)
			}
			if strings.Count(plan.Dest, "/") != 3 {
				t.Errorf("Dest = %q has %d levels; the rule declares exactly three",
					plan.Dest, strings.Count(plan.Dest, "/"))
			}
		})
	}
}
