package main_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

// binary is the built tabularium, shared by every test in this file. Building
// once keeps the end-to-end suite honest about what it costs.
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tabularium-e2e-")
	if err != nil {
		panic("creating the build directory: " + err.Error())
	}
	defer os.RemoveAll(dir)

	binary = filepath.Join(dir, "tabularium")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", binary, "./cmd/tabularium")
	build.Dir = repoRoot()
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		panic("building the binary: " + err.Error() + "\n" + string(out))
	}

	os.Exit(m.Run())
}

func repoRoot() string {
	// The test runs in cmd/tabularium.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(err)
	}
	return root
}

// result is one invocation of the real binary, with the two streams kept apart
// — which is the only way to assert the contract Principle II makes public.
type result struct {
	stdout string
	stderr string
	code   int
}

func tabularium(t *testing.T, args ...string) result {
	t.Helper()
	return tabulariumEnv(t, nil, args...)
}

func tabulariumEnv(t *testing.T, extraEnv []string, args ...string) result {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), extraEnv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running %v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func TestExitCodes(t *testing.T) {
	// FR-001, FR-068 and contracts/cli.md. The exit code is the tool's contract
	// inside a shell script, so it is asserted against the real binary — the
	// flag package's own os.Exit(2) would bypass every in-process check.
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{name: "--help succeeds", args: []string{"--help"}, wantCode: 0},
		{name: "-h succeeds", args: []string{"-h"}, wantCode: 0},
		{name: "--version succeeds", args: []string{"--version"}, wantCode: 0},
		{name: "no argument is a usage error", args: nil, wantCode: 2},
		{name: "two arguments is a usage error", args: []string{"a.pdf", "b.pdf"}, wantCode: 2},
		{name: "three arguments is a usage error", args: []string{"a", "b", "c"}, wantCode: 2},
		{name: "an unknown flag is a usage error", args: []string{"--nope", "a.pdf"}, wantCode: 2},
		{name: "--quiet with --verbose is a usage error", args: []string{"--quiet", "--verbose", "a.pdf"}, wantCode: 2},
		{name: "an unknown --output is a usage error", args: []string{"--output", "xml", "a.pdf"}, wantCode: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tabularium(t, tt.args...)
			if got.code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					got.code, tt.wantCode, got.stdout, got.stderr)
			}
		})
	}
}

func TestVersionPrintsSomething(t *testing.T) {
	got := tabularium(t, "--version")

	if got.code != 0 {
		t.Fatalf("--version exit code = %d, want 0", got.code)
	}
	if strings.TrimSpace(got.stdout) == "" {
		t.Error("--version printed nothing on stdout")
	}
	if got.stderr != "" {
		t.Errorf("--version wrote to stderr:\n%s", got.stderr)
	}
}

func TestHelpGoesToStdoutAndUsageErrorsDoNot(t *testing.T) {
	// SC-005: stdout carries data only. A help screen appearing on the stdout
	// of a *failed* invocation is exactly what would corrupt `… | jq`.
	t.Run("--help writes stdout", func(t *testing.T) {
		got := tabularium(t, "--help")
		if got.stdout == "" {
			t.Error("--help wrote nothing to stdout")
		}
		if got.stderr != "" {
			t.Errorf("--help wrote to stderr:\n%s", got.stderr)
		}
	})

	t.Run("a usage error leaves stdout empty", func(t *testing.T) {
		got := tabularium(t)
		if got.stdout != "" {
			t.Errorf("a usage error wrote to stdout:\n%s", got.stdout)
		}
		if got.stderr == "" {
			t.Error("a usage error explained nothing on stderr")
		}
	})
}

// --- User Story 1: describe a document without touching anything ------------

// stubModel serves a fixed analysis reply, so the end-to-end tests need no
// network and no model.
func stubModel(t *testing.T, content string) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reply, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
		})
		if err != nil {
			panic("encoding a fixed test reply: " + err.Error())
		}
		_, _ = w.Write(reply)
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func metadataJSON(overrides map[string]any) string {
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
	return string(raw)
}

// workspace is a config file, an archive root, and a scans directory.
type workspace struct {
	config string
	root   string
	scans  string
}

func newWorkspace(t *testing.T, url string, extra string) workspace {
	t.Helper()
	base := t.TempDir()
	ws := workspace{
		config: filepath.Join(base, "config.yaml"),
		root:   filepath.Join(base, "archive"),
		scans:  filepath.Join(base, "scans"),
	}
	for _, dir := range []string{ws.root, ws.scans} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	body := "archive_root: " + ws.root + `
ocr:
  base_url: ` + url + `
  model: stub-vision
  timeout: 20s
analysis:
  base_url: ` + url + `
  model: stub-analysis
  timeout: 20s
  retries: 2
types:
  fallback: autre
  values: [facture, releve-bancaire, contrat, autre]
rules:
  - name: factures-voiture
    type: facture
    tags: [voiture]
    path: "factures/voiture/{{.Year}}"
  - name: divers
    path: "divers/{{.Year}}"
` + extra

	if err := os.WriteFile(ws.config, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return ws
}

// place copies a corpus file into the scans directory.
func (w workspace) place(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(), "testdata", name))
	if err != nil {
		t.Fatalf("reading corpus file %s: %v", name, err)
	}
	path := filepath.Join(w.scans, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// fingerprint is every file under root with its digest, for proving a tree did
// not change.
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
		body, err := os.ReadFile(path) //nolint:gosec // a path this test made
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s  %x", rel, sha256.Sum256(body)))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

func TestDryRunDescribesTheDocumentAndWritesNothing(t *testing.T) {
	// SC-005 and SC-009 together, against the real binary: stdout parses under
	// a strict decoder with nothing else on it, diagnostics are on stderr, and
	// the archive tree is byte-identical afterwards.
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), "")
	doc := ws.place(t, "born-digital.pdf")

	// Something already in the tree, so "unchanged" means something.
	dir := filepath.Join(ws.root, "factures", "voiture", "2025")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("preparing the tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "existing.pdf"), []byte("older"), 0o600); err != nil {
		t.Fatalf("preparing the tree: %v", err)
	}

	before := fingerprint(t, ws.root)

	got := tabularium(t, "--config", ws.config, "--dry-run", "--output", "json", "--verbose", doc)

	if got.code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}

	dec := json.NewDecoder(strings.NewReader(got.stdout))
	dec.DisallowUnknownFields()
	var out struct {
		Version     int     `json:"version"`
		Action      string  `json:"action"`
		Destination *string `json:"destination"`
		Rule        *string `json:"rule"`
		Source      struct {
			Path   string `json:"path"`
			Type   string `json:"type"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"source"`
		Text struct {
			Origin  string  `json:"origin"`
			Content *string `json:"content"`
		} `json:"text"`
		Truncation  any            `json:"truncation"`
		Metadata    map[string]any `json:"metadata"`
		Archive     any            `json:"archive"`
		Disposition *string        `json:"disposition"`
	}
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("stdout does not parse under a strict decoder: %v\n%s", err, got.stdout)
	}
	if dec.More() {
		t.Errorf("stdout carries more than one value:\n%s", got.stdout)
	}

	if out.Version != 1 {
		t.Errorf("version = %d, want 1", out.Version)
	}
	if out.Action != "planned" {
		t.Errorf("action = %q, want %q", out.Action, "planned")
	}
	if out.Rule == nil || *out.Rule != "factures-voiture" {
		t.Errorf("rule = %v, want factures-voiture", out.Rule)
	}
	if out.Source.Type != "application/pdf" {
		t.Errorf("source.type = %q, want application/pdf", out.Source.Type)
	}
	if out.Text.Origin != "text-layer" {
		t.Errorf("text.origin = %q, want text-layer — a born-digital PDF costs no model calls",
			out.Text.Origin)
	}

	if got.stderr == "" {
		t.Error("--verbose produced no diagnostics on stderr")
	}
	if strings.Contains(got.stdout, "level=") {
		t.Errorf("a diagnostic reached stdout:\n%s", got.stdout)
	}

	if after := fingerprint(t, ws.root); !slices.Equal(before, after) {
		t.Errorf("--dry-run changed the archive tree:\nbefore %v\nafter  %v", before, after)
	}
	if _, err := os.Stat(doc); err != nil {
		t.Errorf("--dry-run disturbed the source document: %v", err)
	}
}

func TestABornDigitalPDFMakesNoModelCallForOCR(t *testing.T) {
	// SC-001 measured against the real binary: the OCR endpoint is never
	// reached for a PDF that already carries its text.
	var ocrCalls atomic.Int32
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ocrCalls.Add(1)
		reply, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "transcribed"}}},
		})
		if err != nil {
			panic("encoding a fixed test reply: " + err.Error())
		}
		_, _ = w.Write(reply)
	}))
	t.Cleanup(ocr.Close)

	analysis := stubModel(t, metadataJSON(nil))

	base := t.TempDir()
	root := filepath.Join(base, "archive")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("creating the root: %v", err)
	}
	cfg := filepath.Join(base, "config.yaml")
	body := "archive_root: " + root + `
ocr:
  base_url: ` + ocr.URL + `
  model: stub-vision
  timeout: 20s
analysis:
  base_url: ` + analysis + `
  model: stub-analysis
  timeout: 20s
types:
  fallback: autre
  values: [facture, autre]
rules:
  - name: divers
    path: "divers/{{.Year}}"
`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	src, err := os.ReadFile(filepath.Join(repoRoot(), "testdata", "born-digital.pdf"))
	if err != nil {
		t.Fatalf("reading the corpus: %v", err)
	}
	doc := filepath.Join(base, "facture.pdf")
	if err := os.WriteFile(doc, src, 0o600); err != nil {
		t.Fatalf("writing the document: %v", err)
	}

	got := tabularium(t, "--config", cfg, "--dry-run", doc)
	if got.code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if n := ocrCalls.Load(); n != 0 {
		t.Errorf("the OCR endpoint was called %d times for a born-digital PDF, want 0", n)
	}
}

func TestJSONOutputParsesForEverySupportedType(t *testing.T) {
	// SC-005: the one property to check against every document type the tool
	// claims to support.
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), "")

	files := []string{
		"born-digital.pdf", "scanned.pdf", "receipt.jpg", "receipt.png",
		"little-endian.tiff", "big-endian.tiff", "letter.docx", "letter.odt",
		"notes.md", "notes.txt",
	}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			if strings.HasSuffix(name, ".pdf") {
				if _, err := exec.LookPath("pdftotext"); err != nil {
					t.Skip("pdftotext is not installed")
				}
			}
			doc := ws.place(t, name)

			got := tabularium(t, "--config", ws.config, "--dry-run", "--output", "json", doc)
			if got.code != 0 {
				t.Fatalf("exit code = %d, want 0\nstderr:\n%s", got.code, got.stderr)
			}

			var out map[string]any
			if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
				t.Fatalf("stdout is not valid JSON: %v\n%s", err, got.stdout)
			}
		})
	}
}

// --- User Story 2: file the document into the tree --------------------------

func TestAFiledDocumentLandsWhereTheRuleSaysAndLeavesATrace(t *testing.T) {
	// FR-034, FR-037, FR-066 and SC-002 together, against the real binary.
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), "")
	doc := ws.place(t, "notes.txt")

	got := tabularium(t, "--config", ws.config, "--output", "json", doc)
	if got.code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}

	var out struct {
		Action      string  `json:"action"`
		Destination *string `json:"destination"`
		Rule        *string `json:"rule"`
		Disposition *string `json:"disposition"`
		Archive     any     `json:"archive"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
		t.Fatalf("decoding: %v\n%s", err, got.stdout)
	}

	if out.Action != "filed" {
		t.Errorf("action = %q, want %q", out.Action, "filed")
	}
	if out.Rule == nil || *out.Rule != "factures-voiture" {
		t.Errorf("rule = %v, want factures-voiture", out.Rule)
	}
	// The date prefix comes from the DOCUMENT's date, not today's (FR-034).
	const want = "factures/voiture/2025/2025-03-14-facture-entretien-vehicule.txt"
	if out.Destination == nil || *out.Destination != want {
		t.Errorf("destination = %v, want %q", out.Destination, want)
	}

	// The document is there.
	filed := filepath.Join(ws.root, filepath.FromSlash(want))
	if _, err := os.Stat(filed); err != nil {
		t.Errorf("the document is not at the rule's path: %v", err)
	}
	// The scan box is empty (SC-002).
	if _, err := os.Stat(doc); err == nil {
		t.Error("the source survived the default move")
	}
	// And the sidecar sits beside it.
	if _, err := os.Stat(filed + ".tabularium.json"); err != nil {
		t.Errorf("no sidecar beside the filed document: %v", err)
	}
}

func TestThreeDocumentsLandUnderTheFirstRuleThatMatches(t *testing.T) {
	// SC-002's independent test: three documents of different types, each under
	// the path the first matching rule dictates, and an empty scan box.
	tests := []struct {
		name    string
		reply   map[string]any
		wantDir string
	}{
		{
			name:    "a facture tagged voiture takes the specific rule",
			reply:   map[string]any{"tags": []string{"voiture"}},
			wantDir: "factures/voiture/2025",
		},
		{
			name:    "a releve falls to the default",
			reply:   map[string]any{"type": "releve-bancaire", "tags": []string{}},
			wantDir: "divers/2025",
		},
		{
			name:    "the fallback type falls to the default too",
			reply:   map[string]any{"type": "autre", "tags": []string{}},
			wantDir: "divers/2025",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := newWorkspace(t, stubModel(t, metadataJSON(tt.reply)), "")
			doc := ws.place(t, "notes.txt")

			got := tabularium(t, "--config", ws.config, "--output", "json", doc)
			if got.code != 0 {
				t.Fatalf("exit code = %d, want 0\nstderr:\n%s", got.code, got.stderr)
			}

			var out struct {
				Destination *string `json:"destination"`
			}
			if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if out.Destination == nil {
				t.Fatal("no destination reported")
			}
			if dir := path.Dir(*out.Destination); dir != tt.wantDir {
				t.Errorf("filed under %q, want %q", dir, tt.wantDir)
			}

			// The scan box is empty.
			if entries, err := os.ReadDir(ws.scans); err == nil && len(entries) != 0 {
				t.Errorf("the scan box still holds %d files", len(entries))
			}
		})
	}
}

func TestADuplicateIsAnnouncedOnStderrAndExitsZero(t *testing.T) {
	// FR-049, FR-069, SC-003. The user is told; a parser reading stdout is not
	// disturbed; the shell script around it does not stop.
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), "")

	first := ws.place(t, "notes.txt")
	if got := tabularium(t, "--config", ws.config, "--disposition", "copy", first); got.code != 0 {
		t.Fatalf("the first run exited %d\nstderr:\n%s", got.code, got.stderr)
	}

	// The same bytes again.
	got := tabularium(t, "--config", ws.config, "--output", "json", "--disposition", "copy", first)

	if got.code != 0 {
		t.Errorf("exit code = %d for a duplicate, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(strings.ToLower(got.stderr), "identical") {
		t.Errorf("stderr does not announce the duplicate:\n%s", got.stderr)
	}

	var out struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON for a duplicate: %v\n%s", err, got.stdout)
	}
	if out.Action != "duplicate" {
		t.Errorf("action = %q, want %q", out.Action, "duplicate")
	}
}

func TestNoFileComputesThePlanAndWritesNothing(t *testing.T) {
	// FR-062: filing disabled by flag, reported as not-filed.
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), "")
	doc := ws.place(t, "notes.txt")

	before := fingerprint(t, ws.root)
	got := tabularium(t, "--config", ws.config, "--output", "json", "--no-file", doc)

	if got.code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	var out struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.Action != "not-filed" {
		t.Errorf("action = %q, want %q", out.Action, "not-filed")
	}
	if after := fingerprint(t, ws.root); !slices.Equal(before, after) {
		t.Errorf("--no-file wrote into the archive: %v", after)
	}
	if _, err := os.Stat(doc); err != nil {
		t.Errorf("--no-file disturbed the source: %v", err)
	}
}

func TestTwoDifferentDocumentsWithOneNameBothSurvive(t *testing.T) {
	// FR-050: overwriting either would lose one of them.
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), "")

	first := ws.place(t, "notes.txt")
	if got := tabularium(t, "--config", ws.config, first); got.code != 0 {
		t.Fatalf("the first run exited %d\nstderr:\n%s", got.code, got.stderr)
	}

	// A different document that computes the same name.
	second := ws.place(t, "notes.md")
	renamed := filepath.Join(ws.scans, "notes.txt")
	if err := os.Rename(second, renamed); err != nil {
		t.Fatalf("renaming: %v", err)
	}

	got := tabularium(t, "--config", ws.config, "--output", "json", renamed)
	if got.code != 0 {
		t.Fatalf("the second run exited %d\nstderr:\n%s", got.code, got.stderr)
	}

	var out struct {
		Action      string  `json:"action"`
		Destination *string `json:"destination"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.Action != "filed" {
		t.Errorf("action = %q, want %q — different content is not a duplicate", out.Action, "filed")
	}
	if out.Destination == nil || !strings.Contains(*out.Destination, "-2") {
		t.Errorf("destination = %v, want a -2 suffix", out.Destination)
	}
}

// --- User Story 3: hand the document to an external archiver ----------------

// archiverScript writes a fake archiver and returns its path plus the file it
// records its argv into, one argument per line.
func archiverScript(t *testing.T, body string) (command, argvLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	dir := t.TempDir()
	command = filepath.Join(dir, "fake-archiver")
	argvLog = filepath.Join(dir, "argv.txt")

	script := "#!/bin/sh\n" +
		": > '" + argvLog + "'\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + argvLog + "'; done\n" +
		"printf 'TOKEN=%s\\n' \"$PAPERLESS_TOKEN\" >> '" + argvLog + "'\n" +
		body
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatalf("writing the fake archiver: %v", err)
	}
	return command, argvLog
}

func archiveConfig(command string) string {
	return `
archive:
  command: ` + command + `
  args: ["upload", "--title", "{{.Title}}", "{{.Path}}"]
  args_each_tag: ["--tag", "{{.}}"]
  timeout: 20s
  env: ["PAPERLESS_TOKEN"]
`
}

func TestTheArchiverReceivesEveryTagAndTheTokenFromTheEnvironment(t *testing.T) {
	// FR-055, FR-058, SC-008 against the real binary: each tag its own
	// argument, and the credential through the environment rather than argv.
	command, argvLog := archiverScript(t, "exit 0\n")
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), archiveConfig(command))
	doc := ws.place(t, "notes.txt")

	const token = "sk-canary-3f9a17c4e8b2"
	got := tabulariumEnv(t, []string{"PAPERLESS_TOKEN=" + token},
		"--config", ws.config, "--output", "json", doc)
	if got.code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}

	body, err := os.ReadFile(argvLog) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("reading the recorded argv: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")

	// Every tag arrives on its own line, which is to say as its own argument.
	tagArgs := 0
	for i, line := range lines {
		if line == "--tag" {
			tagArgs++
			if i+1 >= len(lines) {
				t.Fatal("--tag arrived with no value after it")
			}
		}
	}
	if tagArgs != 2 {
		t.Errorf("the archiver received %d --tag arguments, want 2:\n%s", tagArgs, body)
	}

	// The token came through the environment.
	if !strings.Contains(string(body), "TOKEN="+token) {
		t.Errorf("the token did not reach the archiver's environment:\n%s", body)
	}
	// And never through argv.
	for i, line := range lines {
		if strings.HasPrefix(line, "TOKEN=") {
			continue // the script's own report, not an argument
		}
		if strings.Contains(line, token) {
			t.Errorf("the token reached argv[%d] = %q", i, line)
		}
	}

	// Neither stream carries it either (SC-008).
	for name, stream := range map[string]string{"stdout": got.stdout, "stderr": got.stderr} {
		if strings.Contains(stream, token) {
			t.Errorf("the token leaked to %s:\n%s", name, stream)
		}
	}

	var out struct {
		Archive *struct {
			Attempted bool `json:"attempted"`
			Success   bool `json:"success"`
			ExitCode  *int `json:"exit_code"`
		} `json:"archive"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
		t.Fatalf("decoding: %v\n%s", err, got.stdout)
	}
	if out.Archive == nil || !out.Archive.Attempted || !out.Archive.Success {
		t.Errorf("archive = %+v, want an attempted success", out.Archive)
	}
}

func TestAFailingArchiverExitsOneWithTheDocumentStillFiled(t *testing.T) {
	// FR-063: the file stays where it was filed, the error goes to stderr, and
	// the exit code is 1.
	command, _ := archiverScript(t, "echo 'connection refused' >&2\nexit 7\n")
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), archiveConfig(command))
	doc := ws.place(t, "notes.txt")

	got := tabularium(t, "--config", ws.config, "--output", "json", doc)

	if got.code != 1 {
		t.Fatalf("exit code = %d, want 1\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "archiver") {
		t.Errorf("stderr does not explain that the archiver failed:\n%s", got.stderr)
	}

	// What the archiver actually said is the only thing that tells the user what
	// to fix, so it is echoed on the diagnostic channel, named by the command it
	// came from (FR-057a).
	if !strings.Contains(got.stderr, "fake-archiver| connection refused") {
		t.Errorf("stderr does not carry what the archiver said:\n%s", got.stderr)
	}
	// And nothing of it reaches stdout, which carries data alone (FR-067).
	if strings.Contains(got.stdout, "fake-archiver|") {
		t.Errorf("the archiver's echo leaked to stdout:\n%s", got.stdout)
	}

	// stdout is still one parseable object, and it says the document is filed.
	var out struct {
		Action      string  `json:"action"`
		Destination *string `json:"destination"`
		Archive     *struct {
			Attempted bool `json:"attempted"`
			Success   bool `json:"success"`
			ExitCode  *int `json:"exit_code"`
		} `json:"archive"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON after an archiver failure: %v\n%s", err, got.stdout)
	}
	if out.Action != "filed" {
		t.Errorf("action = %q, want %q — the filing succeeded", out.Action, "filed")
	}
	if out.Archive == nil || out.Archive.Success {
		t.Errorf("archive = %+v, want a recorded failure", out.Archive)
	}
	if out.Archive.ExitCode == nil || *out.Archive.ExitCode != 7 {
		t.Errorf("archive.exit_code = %v, want 7", out.Archive.ExitCode)
	}

	// The document really is filed.
	if out.Destination == nil {
		t.Fatal("no destination reported")
	}
	filed := filepath.Join(ws.root, filepath.FromSlash(*out.Destination))
	if _, err := os.Stat(filed); err != nil {
		t.Errorf("the filing was unwound: %v", err)
	}
	if _, err := os.Stat(doc); err == nil {
		t.Error("the source was restored; the document really was filed")
	}
}

func TestQuietStillShowsWhatARefusingArchiverSaid(t *testing.T) {
	// FR-057a with FR-071: the silent mode is "errors only", and a hand-off the
	// archiver refused is an error. Its own words are the only thing that says
	// what to fix, so they survive --quiet.
	command, _ := archiverScript(t, "echo 'connection refused' >&2\nexit 7\n")
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), archiveConfig(command))
	doc := ws.place(t, "notes.txt")

	got := tabularium(t, "--config", ws.config, "--quiet", doc)

	if got.code != 1 {
		t.Fatalf("exit code = %d, want 1\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "fake-archiver| connection refused") {
		t.Errorf("--quiet hid the reason the archiver refused:\n%s", got.stderr)
	}
	// Once, not twice: the echo was off, so only the fallback printed it.
	if n := strings.Count(got.stderr, "connection refused"); n != 1 {
		t.Errorf("the reason appears %d times, want 1:\n%s", n, got.stderr)
	}
}

func TestQuietKeepsASucceedingArchiverQuiet(t *testing.T) {
	// The other half of FR-071: chatter from a command that worked is not an
	// error, and the silent mode is entitled to swallow it.
	command, _ := archiverScript(t, "echo 'created document 4711'\nexit 0\n")
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), archiveConfig(command))
	doc := ws.place(t, "notes.txt")

	got := tabularium(t, "--config", ws.config, "--quiet", doc)

	if got.code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if strings.Contains(got.stderr, "created document 4711") {
		t.Errorf("--quiet let a successful archiver speak:\n%s", got.stderr)
	}
}

func TestTheArchiversOutputIsEchoedOnASuccessfulRun(t *testing.T) {
	// FR-057a: an archiver that reports where it filed the document is worth
	// reading, and the default verbosity shows it.
	command, _ := archiverScript(t, "echo 'created document 4711'\nexit 0\n")
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), archiveConfig(command))
	doc := ws.place(t, "notes.txt")

	got := tabularium(t, "--config", ws.config, doc)

	if got.code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "fake-archiver| created document 4711") {
		t.Errorf("the archiver's report did not reach stderr:\n%s", got.stderr)
	}
	// stdout carries the table alone (FR-067).
	if strings.Contains(got.stdout, "created document 4711") {
		t.Errorf("the archiver's report leaked to stdout:\n%s", got.stdout)
	}
}

func TestTheConfigurationIsFoundAtTheDefaultLocationWithoutTheFlag(t *testing.T) {
	// FR-072: --config names a file; leaving it off means
	// ~/.config/tabularium/config.yaml, which is where a hand-edited YAML file
	// belongs on every platform.
	if runtime.GOOS == "windows" {
		t.Skip("HOME is not the home-directory variable on Windows")
	}
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), "")
	doc := ws.place(t, "notes.txt")

	home := t.TempDir()
	dir := filepath.Join(home, ".config", "tabularium")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("creating the default configuration directory: %v", err)
	}
	body, err := os.ReadFile(ws.config)
	if err != nil {
		t.Fatalf("reading the workspace configuration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), body, 0o600); err != nil {
		t.Fatalf("writing the default configuration: %v", err)
	}

	// XDG_CONFIG_HOME is blanked rather than left alone: a developer who sets it
	// would otherwise send the run somewhere this test did not prepare.
	got := tabulariumEnv(t, []string{"HOME=" + home, "XDG_CONFIG_HOME="},
		"--output", "json", doc)

	if got.code != 0 {
		t.Fatalf("exit code = %d, want 0 — the default location was not read\nstderr:\n%s",
			got.code, got.stderr)
	}
	var out struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
		t.Fatalf("decoding: %v\n%s", err, got.stdout)
	}
	if out.Action != "filed" {
		t.Errorf("action = %q, want %q", out.Action, "filed")
	}
}

func TestNoConfigurationAnywhereNamesThePathItWanted(t *testing.T) {
	// The old failure was "archive_root is not set", which never mentioned a
	// configuration file, let alone where to put one.
	if runtime.GOOS == "windows" {
		t.Skip("HOME is not the home-directory variable on Windows")
	}
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), "")
	doc := ws.place(t, "notes.txt")
	home := t.TempDir() // no .config/tabularium in it

	got := tabulariumEnv(t, []string{"HOME=" + home, "XDG_CONFIG_HOME="}, doc)

	if got.code != 2 {
		t.Fatalf("exit code = %d, want 2\nstderr:\n%s", got.code, got.stderr)
	}
	want := filepath.Join(home, ".config", "tabularium", "config.yaml")
	if !strings.Contains(got.stderr, want) {
		t.Errorf("stderr does not name %q:\n%s", want, got.stderr)
	}
}

func TestNoArchiveSkipsTheStepEntirely(t *testing.T) {
	// FR-062: reported as archive: null, and the run succeeds even though the
	// configured archiver would have failed.
	command, argvLog := archiverScript(t, "exit 7\n")
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), archiveConfig(command))
	doc := ws.place(t, "notes.txt")

	got := tabularium(t, "--config", ws.config, "--output", "json", "--no-archive", doc)
	if got.code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}

	var out struct {
		Action  string `json:"action"`
		Archive any    `json:"archive"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.Action != "filed" {
		t.Errorf("action = %q, want %q", out.Action, "filed")
	}
	if out.Archive != nil {
		t.Errorf("archive = %v with --no-archive, want null", out.Archive)
	}
	if _, err := os.Stat(argvLog); err == nil {
		t.Error("the archiver ran despite --no-archive")
	}
}

func TestAMissingArchiverBinaryIsReportedByName(t *testing.T) {
	// FR-060.
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)),
		"\narchive:\n  command: tabularium-no-such-archiver\n  args: [\"{{.Path}}\"]\n  timeout: 5s\n")
	doc := ws.place(t, "notes.txt")

	got := tabularium(t, "--config", ws.config, doc)
	if got.code != 1 {
		t.Errorf("exit code = %d, want 1\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "tabularium-no-such-archiver") {
		t.Errorf("stderr does not name the missing command:\n%s", got.stderr)
	}
}

// --- User Story 4: recover a hand-off that failed ---------------------------

func TestAFailedHandOffIsRecoveredByOneRerun(t *testing.T) {
	// SC-004, end to end: fail the archiver, re-run on the filed document, and
	// confirm zero model requests, zero file moves, and exit 0. A third run
	// does nothing at all.
	var modelCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		modelCalls.Add(1)
		reply, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"content": metadataJSON(nil)},
			}},
		})
		if err != nil {
			panic("encoding a fixed test reply: " + err.Error())
		}
		_, _ = w.Write(reply)
	}))
	t.Cleanup(model.Close)

	// An archiver that fails while a marker file exists, and succeeds once it
	// is removed — so the same configuration covers both runs.
	dir := t.TempDir()
	failWhile := filepath.Join(dir, "fail")
	runLog := filepath.Join(dir, "runs.txt")
	if err := os.WriteFile(failWhile, []byte("x"), 0o600); err != nil {
		t.Fatalf("writing the marker: %v", err)
	}

	command := filepath.Join(dir, "archiver")
	script := "#!/bin/sh\n" +
		"echo run >> '" + runLog + "'\n" +
		"if [ -f '" + failWhile + "' ]; then echo 'refused' >&2; exit 7; fi\n" +
		"exit 0\n"
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatalf("writing the archiver: %v", err)
	}

	ws := newWorkspace(t, model.URL, "\narchive:\n  command: "+command+
		"\n  args: [\"{{.Path}}\"]\n  timeout: 20s\n")
	doc := ws.place(t, "notes.txt")

	// --- the first run: filed, hand-off failed, exit 1 ---
	first := tabularium(t, "--config", ws.config, "--output", "json", doc)
	if first.code != 1 {
		t.Fatalf("the first run exited %d, want 1\nstderr:\n%s", first.code, first.stderr)
	}

	var out struct {
		Action      string  `json:"action"`
		Destination *string `json:"destination"`
	}
	if err := json.Unmarshal([]byte(first.stdout), &out); err != nil {
		t.Fatalf("decoding the first run: %v\n%s", err, first.stdout)
	}
	if out.Action != "filed" || out.Destination == nil {
		t.Fatalf("the first run reported %+v; want the document filed", out)
	}

	filed := filepath.Join(ws.root, filepath.FromSlash(*out.Destination))
	before, err := os.Stat(filed)
	if err != nil {
		t.Fatalf("the document was not filed: %v", err)
	}
	callsAfterFirst := modelCalls.Load()
	if callsAfterFirst == 0 {
		t.Fatal("the first run made no model calls; the fixture is not exercising the pipeline")
	}

	// --- the second run: only the hand-off ---
	if err := os.Remove(failWhile); err != nil {
		t.Fatalf("removing the marker: %v", err)
	}

	second := tabularium(t, "--config", ws.config, "--output", "json", filed)
	if second.code != 0 {
		t.Fatalf("the recovering run exited %d, want 0\nstderr:\n%s", second.code, second.stderr)
	}

	// SC-004: zero model requests.
	if n := modelCalls.Load(); n != callsAfterFirst {
		t.Errorf("the recovering run made %d model calls, want 0", n-callsAfterFirst)
	}
	// Zero file moves.
	after, err := os.Stat(filed)
	if err != nil {
		t.Fatalf("the recovering run moved the document: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the recovering run rewrote the filed document")
	}

	// --- the third run: nothing at all ---
	third := tabularium(t, "--config", ws.config, "--output", "json", filed)
	if third.code != 0 {
		t.Fatalf("the third run exited %d, want 0\nstderr:\n%s", third.code, third.stderr)
	}
	if n := modelCalls.Load(); n != callsAfterFirst {
		t.Errorf("the third run made model calls")
	}

	runs, err := os.ReadFile(runLog) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("reading the archiver's log: %v", err)
	}
	if n := strings.Count(string(runs), "run"); n != 2 {
		t.Errorf("the archiver ran %d times across three invocations, want 2 "+
			"(one failure, one recovery, and nothing on the third)", n)
	}
}

func TestAnUnusableSidecarSendsTheDocumentThroughFromTheStart(t *testing.T) {
	// FR-053: the anomaly is logged on stderr and the document is reprocessed.
	command, _ := archiverScript(t, "exit 0\n")
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), archiveConfig(command))
	doc := ws.place(t, "notes.txt")

	first := tabularium(t, "--config", ws.config, "--output", "json", doc)
	if first.code != 0 {
		t.Fatalf("the first run exited %d\nstderr:\n%s", first.code, first.stderr)
	}
	var out struct {
		Destination *string `json:"destination"`
	}
	if err := json.Unmarshal([]byte(first.stdout), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	filed := filepath.Join(ws.root, filepath.FromSlash(*out.Destination))

	// Corrupt the sidecar.
	if err := os.WriteFile(filed+".tabularium.json", []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("corrupting the sidecar: %v", err)
	}

	second := tabularium(t, "--config", ws.config, "--verbose", "--output", "json", filed)
	if second.code != 0 {
		t.Fatalf("the second run exited %d\nstderr:\n%s", second.code, second.stderr)
	}
	if !strings.Contains(second.stderr, "sidecar") {
		t.Errorf("stderr does not report the sidecar anomaly:\n%s", second.stderr)
	}
}

// --- The safety claims, measured end to end -------------------------------

func TestNoDocumentIsFiledOnUnvalidatedMetadata(t *testing.T) {
	// SC-011: "le nombre de documents rangés à partir d'une réponse non
	// conforme au schéma est nul". The analysis tests prove such a response is
	// rejected; this proves the consequence — that nothing reaches the tree.
	tests := []struct {
		name     string
		content  string
		wantCode int
	}{
		{
			name: "prose on every attempt is a configuration error (FR-024, D7)",
			// The endpoint accepted response_format and ignored it, which is a
			// stable fault the user can fix — not an intermittent one.
			content:  "This looks like an invoice from a garage, dated March 2025.",
			wantCode: 2,
		},
		{
			name:     "a fenced code block is not conforming output either",
			content:  "```json\n" + metadataJSON(nil) + "\n```",
			wantCode: 2,
		},
		{
			name:     "an object carrying a field the schema does not name",
			content:  metadataJSON(map[string]any{"folder": "factures/voiture"}),
			wantCode: 2,
		},
		{
			name: "a well-shaped answer with a type outside the vocabulary (SC-012)",
			// The shape was honoured, so the endpoint is fine and the model
			// simply chose badly: a runtime failure, and still nothing filed.
			content:  metadataJSON(map[string]any{"type": "cryptomonnaie"}),
			wantCode: 1,
		},
		{
			name:     "a tag outside a bounded vocabulary",
			content:  metadataJSON(map[string]any{"tags": []string{"cryptomonnaie"}}),
			wantCode: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := newWorkspace(t, stubModel(t, tt.content), "\ntags:\n  values: [voiture, entretien]\n")
			doc := ws.place(t, "notes.txt")

			// Something already in the tree, so "unchanged" means something.
			dir := filepath.Join(ws.root, "divers", "2024")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatalf("preparing the tree: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("older"), 0o600); err != nil {
				t.Fatalf("preparing the tree: %v", err)
			}

			before := fingerprint(t, ws.root)
			dirsBefore := directories(t, ws.root)

			got := tabularium(t, "--config", ws.config, doc)

			if got.code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", got.code, tt.wantCode, got.stderr)
			}

			// The heart of SC-011: zero documents filed.
			if after := fingerprint(t, ws.root); !slices.Equal(before, after) {
				t.Errorf("a document was filed on unvalidated metadata:\nbefore %v\nafter  %v",
					before, after)
			}
			// And SC-012: no new branch of the tree, whatever the model said.
			if after := directories(t, ws.root); !slices.Equal(dirsBefore, after) {
				t.Errorf("a directory was created outside the declared vocabulary:\n%v", after)
			}
			// The document is still where the user left it.
			if _, err := os.Stat(doc); err != nil {
				t.Errorf("the source was disturbed by a rejected response: %v", err)
			}
		})
	}
}

func TestAnUnsupportedTypeAlwaysNamesWhatWasDetected(t *testing.T) {
	// SC-007: a usage error naming the detected type — never a runtime failure,
	// and never a success.
	ws := newWorkspace(t, stubModel(t, metadataJSON(nil)), "")

	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "an opaque binary", file: "unsupported.bin", want: "application/octet-stream"},
		{name: "an ordinary zip", file: "plain.zip", want: "zip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := ws.place(t, tt.file)

			got := tabularium(t, "--config", ws.config, doc)
			if got.code != 2 {
				t.Errorf("exit code = %d, want 2 — never a runtime failure, never a success\nstderr:\n%s",
					got.code, got.stderr)
			}
			if !strings.Contains(got.stderr, tt.want) {
				t.Errorf("stderr does not name the detected type %q:\n%s", tt.want, got.stderr)
			}
		})
	}
}

// directories lists every directory under root, so a new branch is visible.
func directories(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}
