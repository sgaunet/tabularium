package extract_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/extract"
)

func TestABornDigitalPDFCostsZeroModelCalls(t *testing.T) {
	// SC-001, and the one performance goal worth stating. A user filing a pile
	// of born-digital PDFs should not be paying for OCR they do not need.
	requireTool(t, "pdftotext")

	vision := stubVision(t, "this should never be reached")
	got, err := extract.Extract(context.Background(), corpus(t, "born-digital.pdf"), opts(vision))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if got.Origin != extract.OriginTextLayer {
		t.Errorf("Origin = %q, want %q", got.Origin, extract.OriginTextLayer)
	}
	if n := vision.calls.Load(); n != 0 {
		t.Errorf("the vision endpoint was called %d times for a PDF with a text layer, want 0", n)
	}
	if !strings.Contains(got.Content, "FACTURE FA-2025-0312") {
		t.Errorf("the text layer did not come through:\n%s", got.Content)
	}
	if got.Truncation != nil {
		t.Errorf("Truncation = %+v for a one-page document, want nil", got.Truncation)
	}
}

func TestAScannedPDFIsRasterisedAndTranscribed(t *testing.T) {
	// FR-007: fewer than the threshold's worth of runes in the text layer means
	// there is not one, and the page has to be looked at.
	requireTool(t, "pdftoppm")

	vision := stubVision(t, "Facture Garage Central, total 384.50 EUR, le 14 mars 2025.")
	got, err := extract.Extract(context.Background(), corpus(t, "scanned.pdf"), opts(vision))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if got.Origin != extract.OriginVision {
		t.Errorf("Origin = %q, want %q", got.Origin, extract.OriginVision)
	}
	if n := vision.calls.Load(); n != 1 {
		t.Errorf("the vision endpoint was called %d times for a one-page scan, want 1", n)
	}
	if !strings.Contains(got.Content, "Garage Central") {
		t.Errorf("the transcription did not come through:\n%s", got.Content)
	}
}

func TestThePageCapDropsTheExcessAndSaysSo(t *testing.T) {
	// FR-008 and SC-010: pages beyond the cap are dropped, and the truncation
	// is recorded. Never silently — a user who does not know half the document
	// was skipped cannot tell that the metadata is partial.
	requireTool(t, "pdftotext")

	o := opts(nil)
	o.MaxPages = 5

	got, err := extract.Extract(context.Background(), corpus(t, "multipage.pdf"), o)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if got.Truncation == nil {
		t.Fatal("Truncation = nil for a 25-page document capped at 5")
	}
	if got.Truncation.PagesProcessed != 5 {
		t.Errorf("PagesProcessed = %d, want 5", got.Truncation.PagesProcessed)
	}
	if got.Truncation.PagesTotal != 25 {
		t.Errorf("PagesTotal = %d, want 25", got.Truncation.PagesTotal)
	}

	if !strings.Contains(got.Content, "page 5 sur 25") {
		t.Errorf("page 5 is missing from the extracted text:\n%s", got.Content)
	}
	if strings.Contains(got.Content, "page 6 sur 25") {
		t.Error("page 6 was processed despite a cap of 5")
	}
}

func TestNoTruncationWhenTheDocumentFitsUnderTheCap(t *testing.T) {
	// The pointer is what keeps "nothing was dropped" distinct from "truncated
	// to zero pages", and this is the case that must produce nil.
	requireTool(t, "pdftotext")

	o := opts(nil)
	o.MaxPages = 100

	got, err := extract.Extract(context.Background(), corpus(t, "multipage.pdf"), o)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got.Truncation != nil {
		t.Errorf("Truncation = %+v for a 25-page document capped at 100, want nil", got.Truncation)
	}
}

func TestACapOfZeroMeansNoCap(t *testing.T) {
	requireTool(t, "pdftotext")

	o := opts(nil)
	o.MaxPages = 0

	got, err := extract.Extract(context.Background(), corpus(t, "multipage.pdf"), o)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got.Truncation != nil {
		t.Errorf("Truncation = %+v with no cap configured, want nil", got.Truncation)
	}
	if !strings.Contains(got.Content, "page 25 sur 25") {
		t.Error("the last page is missing with no cap configured")
	}
}

func TestTheCapAlsoBoundsTheNumberOfVisionCalls(t *testing.T) {
	// The cap exists to bound cost, and a scanned document is where the cost
	// is: one model call per page.
	requireTool(t, "pdftoppm")

	vision := stubVision(t, "Page {{n}} of a scanned document with words on it.")
	o := opts(vision)
	o.MaxPages = 3

	got, err := extract.Extract(context.Background(), corpus(t, "scanned-multipage.pdf"), o)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if n := vision.calls.Load(); n != 3 {
		t.Errorf("the vision endpoint was called %d times with a cap of 3, want 3", n)
	}
	if got.Truncation == nil || got.Truncation.PagesProcessed != 3 {
		t.Errorf("Truncation = %+v, want 3 pages processed", got.Truncation)
	}
}

func TestAMissingHostToolIsReportedByName(t *testing.T) {
	// FR-013: "command not found" leaves the user to work out which of
	// poppler's dozen binaries they are missing and which package ships it.
	t.Setenv("PATH", t.TempDir())

	_, err := extract.Extract(context.Background(), corpus(t, "born-digital.pdf"), opts(nil))
	if err == nil {
		t.Fatal("Extract() with no poppler on PATH = nil error")
	}

	var missing *extract.MissingToolError
	if !errors.As(err, &missing) {
		t.Fatalf("Extract() = %v; want a *extract.MissingToolError", err)
	}
	if !strings.Contains(err.Error(), "pdftotext") {
		t.Errorf("error = %v; want it to name the binary", err)
	}
	if !strings.Contains(err.Error(), "poppler-utils") {
		t.Errorf("error = %v; want it to name the package that provides it", err)
	}
}

func TestANonPDFRunNeverLooksForPoppler(t *testing.T) {
	// FR-013: each tool is looked up only when a document of that kind is
	// actually processed, so a machine with no poppler files text and images
	// perfectly well.
	t.Setenv("PATH", t.TempDir())

	got, err := extract.Extract(context.Background(), corpus(t, "notes.txt"), opts(nil))
	if err != nil {
		t.Fatalf("Extract() on plain text with an empty PATH: %v", err)
	}
	if got.Origin != extract.OriginPlain {
		t.Errorf("Origin = %q, want %q", got.Origin, extract.OriginPlain)
	}
}
