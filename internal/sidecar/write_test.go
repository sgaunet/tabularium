package sidecar_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/analysis"
	"github.com/sgaunet/tabularium/internal/archiver"
	"github.com/sgaunet/tabularium/internal/extract"
	"github.com/sgaunet/tabularium/internal/sidecar"
)

// store records what was written, so a test can assert on the bytes without a
// filesystem.
type store struct {
	files map[string][]byte
}

func newStore() *store { return &store{files: map[string][]byte{}} }

func (s *store) WriteFileAtomic(name string, body []byte) error {
	s.files[name] = append([]byte(nil), body...)
	return nil
}

func (s *store) ReadFile(name string) ([]byte, error) {
	body, ok := s.files[name]
	if !ok {
		return nil, &missingError{name: name}
	}
	return body, nil
}

type missingError struct{ name string }

func (e *missingError) Error() string { return e.name + ": no such file" }

// full is a sidecar with every field populated.
func full(t *testing.T) sidecar.Sidecar {
	t.Helper()

	md, err := analysis.Decode(`{
		"title":"Facture entretien véhicule & remorquage",
		"type":"facture",
		"correspondent":"Garage Central",
		"tags":["voiture","entretien"],
		"document_date":"2025-03-14",
		"due_date":"2025-04-14",
		"reference":"FA-2025-0312",
		"description":"Révision annuelle.",
		"amount":"384.50",
		"currency":"EUR",
		"filename":"facture-entretien-vehicule"
	}`, analysis.Vocabularies{})
	if err != nil {
		t.Fatalf("building the metadata fixture: %v", err)
	}

	code := 0
	at := time.Date(2025, 3, 16, 9, 41, 13, 0, time.UTC)
	return sidecar.Sidecar{
		Source:       "/home/sylvain/Scans/facture.pdf",
		SourceDigest: strings.Repeat("9f", 32),
		Metadata:     md,
		Rule:         "factures-voiture",
		FiledAt:      time.Date(2025, 3, 16, 9, 41, 12, 0, time.UTC),
		Archive: archiver.Result{
			Attempted: true, Success: true, ExitCode: &code,
			Output: "Document 4127 created\n", AttemptedAt: &at,
		},
	}
}

func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("the sidecar is not valid JSON: %v\n%s", err, body)
	}
	return out
}

func TestTheSidecarSitsBesideTheDocument(t *testing.T) {
	// FR-051: <archived-filename>.tabularium.json, so it travels with the
	// document if the tree is ever moved.
	s := newStore()
	if err := sidecar.Write(s, "factures/voiture/2025/facture.pdf", full(t)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	const want = "factures/voiture/2025/facture.pdf.tabularium.json"
	if _, ok := s.files[want]; !ok {
		t.Errorf("the sidecar was written to %v, want %q", keysOf(s.files), want)
	}
}

func TestTheSidecarMatchesTheContract(t *testing.T) {
	s := newStore()
	if err := sidecar.Write(s, "a/doc.pdf", full(t)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := decode(t, s.files["a/doc.pdf"+sidecar.Suffix])

	// Every required member of contracts/sidecar.schema.json.
	for _, field := range []string{"version", "source", "source_digest",
		"metadata", "rule", "filed_at", "archive"} {
		if _, ok := out[field]; !ok {
			t.Errorf("the sidecar has no %q", field)
		}
	}
	if v, _ := out["version"].(float64); v != 1 {
		t.Errorf("version = %v, want 1", out["version"])
	}
	if out["rule"] != "factures-voiture" {
		t.Errorf("rule = %v", out["rule"])
	}

	// truncation is present and null when nothing was dropped, never omitted.
	value, present := out["truncation"]
	if !present {
		t.Error("truncation is absent; it must be present and null")
	}
	if value != nil {
		t.Errorf("truncation = %v for an untruncated document, want null", value)
	}

	archive, ok := out["archive"].(map[string]any)
	if !ok {
		t.Fatalf("archive is missing or not an object: %v", out["archive"])
	}
	for _, field := range []string{"attempted", "success", "exit_code", "output",
		"error", "attempted_at"} {
		if _, ok := archive[field]; !ok {
			t.Errorf("archive has no %q", field)
		}
	}
}

func TestFiledAtIsRFC3339(t *testing.T) {
	s := newStore()
	if err := sidecar.Write(s, "a/doc.pdf", full(t)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := decode(t, s.files["a/doc.pdf"+sidecar.Suffix])

	filedAt, _ := out["filed_at"].(string)
	if _, err := time.Parse(time.RFC3339, filedAt); err != nil {
		t.Errorf("filed_at = %q, which does not parse as RFC 3339: %v", filedAt, err)
	}
}

func TestTheDigestIsHex(t *testing.T) {
	s := newStore()
	if err := sidecar.Write(s, "a/doc.pdf", full(t)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := decode(t, s.files["a/doc.pdf"+sidecar.Suffix])

	digest, _ := out["source_digest"].(string)
	if len(digest) != 64 {
		t.Errorf("source_digest = %q, want 64 hex characters", digest)
	}
	for _, r := range digest {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Errorf("source_digest = %q, which is not lowercase hex", digest)
			break
		}
	}
}

func TestTruncationIsRecordedWhenThereWasSome(t *testing.T) {
	// FR-008 and FR-051: the fact has to survive the run that discovered it, or
	// a reader of the archive cannot tell that the metadata came from part of
	// the document.
	sc := full(t)
	sc.Truncation = &extract.Truncation{PagesProcessed: 20, PagesTotal: 57}

	s := newStore()
	if err := sidecar.Write(s, "a/doc.pdf", sc); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := decode(t, s.files["a/doc.pdf"+sidecar.Suffix])

	tr, ok := out["truncation"].(map[string]any)
	if !ok {
		t.Fatalf("truncation = %v, want an object", out["truncation"])
	}
	if v, _ := tr["pages_processed"].(float64); v != 20 {
		t.Errorf("pages_processed = %v, want 20", tr["pages_processed"])
	}
	if v, _ := tr["pages_total"].(float64); v != 57 {
		t.Errorf("pages_total = %v, want 57", tr["pages_total"])
	}
}

func TestAFailedHandOffIsRecordedRatherThanHidden(t *testing.T) {
	// FR-052 turns on exactly this: attempted=true with success=false is the
	// signal to replay only the hand-off on the next run.
	sc := full(t)
	at := time.Date(2025, 3, 16, 9, 41, 13, 0, time.UTC)
	sc.Archive = archiver.Result{
		Attempted:   true,
		Success:     false,
		Err:         "dial tcp 127.0.0.1:8000: connect: connection refused",
		AttemptedAt: &at,
	}

	s := newStore()
	if err := sidecar.Write(s, "a/doc.pdf", sc); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := decode(t, s.files["a/doc.pdf"+sidecar.Suffix])

	archive, _ := out["archive"].(map[string]any)
	if archive["attempted"] != true {
		t.Errorf("archive.attempted = %v, want true", archive["attempted"])
	}
	if archive["success"] != false {
		t.Errorf("archive.success = %v, want false", archive["success"])
	}
	if archive["exit_code"] != nil {
		t.Errorf("archive.exit_code = %v, want null when the command produced none",
			archive["exit_code"])
	}
	msg, _ := archive["error"].(string)
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("archive.error = %q, want the reason it failed", msg)
	}
}

func TestTheSidecarIsWrittenOnEverySuccessfulFilingNotOnlyOnFailure(t *testing.T) {
	// It is a trace of the processing, not an error log. A user should be able
	// to ask any archived document where it came from.
	sc := full(t)
	code := 0
	sc.Archive = archiver.Result{Attempted: true, Success: true, ExitCode: &code}

	s := newStore()
	if err := sidecar.Write(s, "a/doc.pdf", sc); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(s.files) != 1 {
		t.Fatalf("%d files were written, want 1", len(s.files))
	}

	out := decode(t, s.files["a/doc.pdf"+sidecar.Suffix])
	archive, _ := out["archive"].(map[string]any)
	if archive["success"] != true {
		t.Errorf("a successful hand-off was not recorded: %v", archive)
	}
}

func TestAnUnattemptedHandOffIsRecordedAsSuch(t *testing.T) {
	// FR-062: the archiver was unconfigured or disabled. attempted=false is
	// what distinguishes "did not run" from "ran and failed".
	sc := full(t)
	sc.Archive = archiver.Result{}

	s := newStore()
	if err := sidecar.Write(s, "a/doc.pdf", sc); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := decode(t, s.files["a/doc.pdf"+sidecar.Suffix])

	archive, _ := out["archive"].(map[string]any)
	if archive["attempted"] != false {
		t.Errorf("archive.attempted = %v, want false", archive["attempted"])
	}
	if archive["attempted_at"] != nil {
		t.Errorf("archive.attempted_at = %v, want null", archive["attempted_at"])
	}
}

func TestHTMLIsNotEscaped(t *testing.T) {
	s := newStore()
	if err := sidecar.Write(s, "a/doc.pdf", full(t)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body := string(s.files["a/doc.pdf"+sidecar.Suffix])

	if strings.Contains(body, `\u0026`) {
		t.Errorf("the ampersand was escaped:\n%s", body)
	}
	if !strings.Contains(body, "véhicule & remorquage") {
		t.Errorf("the title did not survive verbatim:\n%s", body)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
