package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/cli"
)

// --- a stub endpoint, so the output tests need no network -------------------

func metadataReply() string {
	body := map[string]any{
		"title":         "Facture entretien véhicule & remorquage",
		"type":          "facture",
		"correspondent": "Garage Central",
		"tags":          []string{"voiture", "entretien"},
		"document_date": "2025-03-14",
		"due_date":      "2025-04-14",
		"reference":     "FA-2025-0312",
		"description":   "Révision annuelle.",
		"amount":        "384.50",
		"currency":      "EUR",
		"filename":      "facture-entretien-vehicule",
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

func stubEndpoint(t *testing.T, reply string) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(s.Close)
	return s.URL
}

// pipelineConfig is a configuration pointing at the stub endpoint.
func pipelineConfig(t *testing.T, url string) string {
	t.Helper()
	root := t.TempDir()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	body := "archive_root: " + root + `
ocr:
  base_url: ` + url + `
  model: stub-vision
  timeout: 10s
analysis:
  base_url: ` + url + `
  model: stub-analysis
  timeout: 10s
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
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return cfgPath
}

// dryRun runs the real pipeline against the stub and returns both streams.
func dryRun(t *testing.T, extraArgs ...string) (stdout, stderr string, err error) {
	t.Helper()
	cfg := pipelineConfig(t, stubEndpoint(t, metadataReply()))
	doc := document(t, "notes.txt")

	args := append([]string{"--config", cfg, "--dry-run"}, extraArgs...)
	args = append(args, doc)

	var out, errOut strings.Builder
	err = cli.Run(context.Background(), args, &out, &errOut)
	return out.String(), errOut.String(), err
}

// --- text output ------------------------------------------------------------

func TestTextOutputIsABorderlessTable(t *testing.T) {
	// FR-065: readable by a person and cuttable by awk. Box-drawing characters
	// serve neither.
	stdout, _, err := dryRun(t)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("the output has %d lines, want a header and a row:\n%s", len(lines), stdout)
	}

	header := lines[0]
	for _, col := range []string{"SOURCE", "DEST", "TYPE", "TAGS"} {
		if !strings.Contains(header, col) {
			t.Errorf("the header is missing the %s column: %q", col, header)
		}
	}
	for _, border := range []string{"|", "+", "─", "│", "┌", "└"} {
		if strings.Contains(stdout, border) {
			t.Errorf("the table is drawn with %q; it must be borderless:\n%s", border, stdout)
		}
	}

	row := lines[1]
	for _, want := range []string{"notes.txt", "factures/voiture/2025", "facture", "voiture, entretien"} {
		if !strings.Contains(row, want) {
			t.Errorf("the row is missing %q:\n%s", want, row)
		}
	}
}

func TestTheColumnsLineUp(t *testing.T) {
	// text/tabwriter's whole job. A column that does not align is a column that
	// cannot be read down.
	stdout, _, err := dryRun(t)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	header, row := lines[0], lines[1]

	// DEST starts at the same column in both lines.
	wantAt := strings.Index(header, "DEST")
	gotAt := strings.Index(row, "factures/voiture/2025")
	if wantAt != gotAt {
		t.Errorf("DEST starts at column %d in the header and %d in the row:\n%s\n%s",
			wantAt, gotAt, header, row)
	}
}

func TestDryRunPrintsTheRuleAndTheFullMetadata(t *testing.T) {
	// FR-043: seeing the plan before anything moves is the entire point, and
	// the rule that produced the destination is what a user needs in order to
	// correct it.
	stdout, _, err := dryRun(t)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, want := range []string{
		"factures-voiture",           // the rule name
		"factures/voiture/{{.Year}}", // the rule's own template
		"Facture entretien véhicule", // title
		"Garage Central",             // correspondent
		"2025-03-14",                 // document date
		"FA-2025-0312",               // reference
		"384.50 EUR",                 // amount and currency together
		"Révision annuelle.",         // description
		"plain",                      // how the text was obtained
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("--dry-run does not report %q:\n%s", want, stdout)
		}
	}
}

func TestARunWithoutDryRunPrintsNoDetailBlock(t *testing.T) {
	// The detail block belongs to --dry-run. A filing run's output is one row,
	// so a pile of them reads as a table.
	cfg := pipelineConfig(t, stubEndpoint(t, metadataReply()))
	doc := document(t, "notes.txt")

	var stdout, stderr strings.Builder
	if err := cli.Run(context.Background(),
		[]string{"--config", cfg, "--no-file", "--no-archive", doc},
		&stdout, &stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("the output has %d lines, want exactly a header and a row:\n%s",
			len(lines), stdout.String())
	}
}

func TestNoAnalysisPrintsTheTextAndNothingElse(t *testing.T) {
	// FR-019: the output is the extracted text and nothing else, so it can be
	// piped straight into something that wants text.
	cfg := pipelineConfig(t, stubEndpoint(t, metadataReply()))
	doc := document(t, "notes.txt")

	var stdout, stderr strings.Builder
	if err := cli.Run(context.Background(),
		[]string{"--config", cfg, "--no-analysis", doc}, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(stdout.String(), "Energie du Sud") {
		t.Errorf("--no-analysis did not print the extracted text:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "SOURCE") {
		t.Errorf("--no-analysis printed the table as well:\n%s", stdout.String())
	}
}

// --- JSON output ------------------------------------------------------------

func TestJSONOutputIsOneObjectAndNothingElse(t *testing.T) {
	// SC-005: `tabularium --output=json f.pdf | jq` has to work on every
	// supported document type, which means stdout is one object with nothing
	// before or after it.
	stdout, _, err := dryRun(t, "--output", "json")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dec := json.NewDecoder(strings.NewReader(stdout))
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Errorf("stdout carries more than one JSON value:\n%s", stdout)
	}
}

func TestJSONOutputMatchesTheContract(t *testing.T) {
	stdout, _, err := dryRun(t, "--output", "json")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decoding: %v\n%s", err, stdout)
	}

	if v, _ := out["version"].(float64); v != 1 {
		t.Errorf("version = %v, want 1", out["version"])
	}
	if out["action"] != "planned" {
		t.Errorf("action = %v, want %q", out["action"], "planned")
	}
	if out["rule"] != "factures-voiture" {
		t.Errorf("rule = %v, want %q", out["rule"], "factures-voiture")
	}
	if out["destination"] != "factures/voiture/2025/2025-03-14-facture-entretien-vehicule.txt" {
		t.Errorf("destination = %v", out["destination"])
	}

	src, ok := out["source"].(map[string]any)
	if !ok {
		t.Fatalf("source is missing or not an object: %v", out["source"])
	}
	for _, k := range []string{"path", "type", "size", "digest"} {
		if _, present := src[k]; !present {
			t.Errorf("source has no %q", k)
		}
	}
	if digest, _ := src["digest"].(string); !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
		t.Errorf("source.digest = %q, want 64 hex characters", digest)
	}
}

func TestTruncationIsPresentAndNullWhenNothingWasDropped(t *testing.T) {
	// The contract is explicit that the field is never omitted, so a consumer
	// can tell "no truncation" from "the tool forgot to say" (FR-008, SC-010).
	stdout, _, err := dryRun(t, "--output", "json")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	value, present := out["truncation"]
	if !present {
		t.Fatalf("truncation is absent from the output; it must be present and null:\n%s", stdout)
	}
	if value != nil {
		t.Errorf("truncation = %v for an untruncated document, want null", value)
	}
}

func TestEveryNullableFieldIsPresent(t *testing.T) {
	stdout, _, err := dryRun(t, "--output", "json")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	for _, field := range []string{"version", "source", "action", "destination",
		"rule", "disposition", "text", "truncation", "metadata", "archive"} {
		if _, present := out[field]; !present {
			t.Errorf("the output omits %q; the contract lists every field", field)
		}
	}
}

func TestArchiveIsNullWhenTheArchiverIsNotConfigured(t *testing.T) {
	stdout, _, err := dryRun(t, "--output", "json")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out["archive"] != nil {
		t.Errorf("archive = %v with no archiver configured, want null", out["archive"])
	}
}

func TestHTMLIsNotEscapedInJSON(t *testing.T) {
	// FR-066: an ampersand in a title is an ampersand. This is a document
	// title, not a web page, and &amp; would be wrong in a filename too.
	stdout, _, err := dryRun(t, "--output", "json")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(stdout, `\u0026`) {
		t.Errorf("the ampersand was escaped:\n%s", stdout)
	}
	if !strings.Contains(stdout, "véhicule & remorquage") {
		t.Errorf("the title did not survive verbatim:\n%s", stdout)
	}
}

func TestTheTextContentIsOmittedUnlessItIsTheAnswer(t *testing.T) {
	// The extracted text of a 25-page document is not something to put in every
	// invocation's output. It appears only for --no-analysis, where it is the
	// whole of the answer.
	t.Run("a planned run reports the origin only", func(t *testing.T) {
		stdout, _, err := dryRun(t, "--output", "json")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("decoding: %v", err)
		}

		text, ok := out["text"].(map[string]any)
		if !ok {
			t.Fatalf("text is missing or not an object: %v", out["text"])
		}
		if text["origin"] != "plain" {
			t.Errorf("text.origin = %v, want %q", text["origin"], "plain")
		}
		if text["content"] != nil {
			t.Errorf("text.content = %v for a planned run, want null", text["content"])
		}
	})

	t.Run("--no-analysis carries the text", func(t *testing.T) {
		cfg := pipelineConfig(t, stubEndpoint(t, metadataReply()))
		doc := document(t, "notes.txt")

		var stdout, stderr strings.Builder
		if err := cli.Run(context.Background(),
			[]string{"--config", cfg, "--output", "json", "--no-analysis", doc},
			&stdout, &stderr); err != nil {
			t.Fatalf("Run: %v", err)
		}

		var out map[string]any
		if err := json.Unmarshal([]byte(stdout.String()), &out); err != nil {
			t.Fatalf("decoding: %v\n%s", err, stdout.String())
		}
		if out["action"] != "extracted" {
			t.Errorf("action = %v, want %q", out["action"], "extracted")
		}
		text, _ := out["text"].(map[string]any)
		content, _ := text["content"].(string)
		if !strings.Contains(content, "Energie du Sud") {
			t.Errorf("text.content does not carry the extracted text: %q", content)
		}
		if out["metadata"] != nil {
			t.Errorf("metadata = %v with --no-analysis, want null", out["metadata"])
		}
	})
}

func TestMetadataInJSONNamesNoFolder(t *testing.T) {
	stdout, _, err := dryRun(t, "--output", "json")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	md, ok := out["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata is missing or not an object: %v", out["metadata"])
	}

	for name := range md {
		for _, forbidden := range []string{"folder", "path", "directory", "destination"} {
			if strings.Contains(strings.ToLower(name), forbidden) {
				t.Errorf("metadata carries a field %q, which names a place to file", name)
			}
		}
	}
	if _, leaked := md["filename"]; leaked {
		t.Error("the model's proposed filename reached the output")
	}
}

func TestStdoutStaysCleanInJSONModeEvenWithVerbose(t *testing.T) {
	// The case SC-005 actually protects: a user debugging with --verbose still
	// gets parseable stdout.
	stdout, stderr, err := dryRun(t, "--output", "json", "--verbose")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON with --verbose: %v\n%s", err, stdout)
	}
	if !strings.Contains(stderr, "DEBUG") {
		t.Error("--verbose produced no debug records on stderr")
	}
}
