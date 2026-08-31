package extract_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/document"
	"github.com/sgaunet/tabularium/internal/extract"
)

// corpus copies a fixture into a temp directory and opens it, so every test
// works on its own copy and goes through the real sniffing path.
func corpus(t *testing.T, name string) document.Source {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("reading corpus file %s: %v", name, err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	src, err := document.Open(path)
	if err != nil {
		t.Fatalf("document.Open(%s): %v", name, err)
	}
	return src
}

// visionEndpoint is a stub multimodal model. It counts its calls, because
// "costs zero model calls" is a claim only a counter can check.
type visionEndpoint struct {
	url    string
	calls  *atomic.Int32
	client *chat.Client
}

func stubVision(t *testing.T, transcript string) *visionEndpoint {
	t.Helper()

	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		raw, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"content": strings.ReplaceAll(
					transcript, "{{n}}", strconv.Itoa(int(n%10)))},
			}},
		})
		_, _ = w.Write(raw)
	}))
	t.Cleanup(s.Close)

	c, err := chat.New(chat.Options{BaseURL: s.URL, Model: "stub-vision", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	return &visionEndpoint{url: s.URL, calls: &calls, client: c}
}

func opts(v *visionEndpoint) extract.Options {
	o := extract.Options{MaxPages: 20, DPI: 72}
	if v != nil {
		o.Vision = v.client
	}
	return o
}

func TestExtractRoutesEachKindToItsStrategy(t *testing.T) {
	tests := []struct {
		name       string
		file       string
		wantOrigin extract.Origin
		wantCalls  int32
		needs      string
	}{
		{name: "a PDF with a text layer", file: "born-digital.pdf",
			wantOrigin: extract.OriginTextLayer, wantCalls: 0, needs: "pdftotext"},
		{name: "a scanned PDF", file: "scanned.pdf",
			wantOrigin: extract.OriginVision, wantCalls: 1, needs: "pdftoppm"},
		{name: "a JPEG", file: "receipt.jpg",
			wantOrigin: extract.OriginVision, wantCalls: 1},
		{name: "a PNG", file: "receipt.png",
			wantOrigin: extract.OriginVision, wantCalls: 1},
		{name: "a TIFF", file: "little-endian.tiff",
			wantOrigin: extract.OriginVision, wantCalls: 1},
		{name: "a .docx", file: "letter.docx",
			wantOrigin: extract.OriginOffice, wantCalls: 0},
		{name: "an .odt", file: "letter.odt",
			wantOrigin: extract.OriginOffice, wantCalls: 0},
		{name: "Markdown", file: "notes.md",
			wantOrigin: extract.OriginPlain, wantCalls: 0},
		{name: "plain text", file: "notes.txt",
			wantOrigin: extract.OriginPlain, wantCalls: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireTool(t, tt.needs)

			vision := stubVision(t, "Transcribed page {{n}} with plenty of words on it.")
			got, err := extract.Extract(context.Background(), corpus(t, tt.file), opts(vision))
			if err != nil {
				t.Fatalf("Extract(%s): %v", tt.file, err)
			}

			if got.Origin != tt.wantOrigin {
				t.Errorf("Origin = %q, want %q", got.Origin, tt.wantOrigin)
			}
			if strings.TrimSpace(got.Content) == "" {
				t.Error("Extract() recovered no text")
			}
			if n := vision.calls.Load(); n != tt.wantCalls {
				t.Errorf("the vision endpoint was called %d times, want %d", n, tt.wantCalls)
			}
		})
	}
}

func TestADocumentWithNoRecoverableTextIsARuntimeFailure(t *testing.T) {
	// FR-015: a blank page must not become metadata invented from nothing, so
	// an empty extraction is an explicit failure and the document is not filed.
	tests := []struct {
		name  string
		reply string
	}{
		{name: "the model transcribed nothing", reply: ""},
		{name: "the model answered with whitespace", reply: "   \n\t  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vision := stubVision(t, tt.reply)
			_, err := extract.Extract(context.Background(), corpus(t, "receipt.png"), opts(vision))
			if err == nil {
				t.Fatal("Extract() on an empty transcription = nil error")
			}

			var empty *extract.EmptyError
			if !errors.As(err, &empty) {
				t.Fatalf("Extract() = %v; want a *extract.EmptyError", err)
			}
			if !strings.Contains(err.Error(), "not been filed") {
				t.Errorf("error = %v; want it to say the document was not filed", err)
			}
		})
	}
}

func TestAnEmptyPlainTextFileIsARuntimeFailure(t *testing.T) {
	// document.Open refuses a zero-byte file outright, so this is the case of a
	// file that has bytes but no text.
	path := filepath.Join(t.TempDir(), "blank.txt")
	if err := os.WriteFile(path, []byte("   \n\n\t  \n"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}
	src, err := document.Open(path)
	if err != nil {
		t.Fatalf("document.Open: %v", err)
	}

	if _, err := extract.Extract(context.Background(), src, opts(nil)); err == nil {
		t.Fatal("Extract() on a whitespace-only file = nil error")
	}
}

func TestADocumentNeedingVisionSaysSoWhenNoneIsConfigured(t *testing.T) {
	_, err := extract.Extract(context.Background(), corpus(t, "receipt.png"), opts(nil))
	if err == nil {
		t.Fatal("Extract() with no vision client = nil error")
	}
	for _, want := range []string{"ocr.base_url", "ocr.model"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v; want it to name the setting to fix (%s)", err, want)
		}
	}
}

func TestExtractHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := extract.Extract(ctx, corpus(t, "notes.txt"), opts(nil)); err == nil {
		t.Fatal("Extract() with a cancelled context = nil error")
	}
}

func TestUsableTextLayerThreshold(t *testing.T) {
	// The threshold reached through export_test.go, so the test and the
	// implementation cannot drift apart on the one number that decides whether
	// a run costs zero model calls or many.
	just := strings.Repeat("a", extract.MinTextLayerRunes)
	shy := strings.Repeat("a", extract.MinTextLayerRunes-1)

	tests := []struct {
		name  string
		pages []string
		want  bool
	}{
		{name: "exactly the threshold is usable", pages: []string{just}, want: true},
		{name: "one rune short is not", pages: []string{shy}, want: false},
		{name: "nothing at all is not", pages: []string{""}, want: false},
		{name: "a page of whitespace is not", pages: []string{"   \n\t\n   "}, want: false},
		{name: "a scanned page's stray ligature is not", pages: []string{"fi\n\n  \n"}, want: false},
		{name: "the count is across pages, not per page",
			pages: []string{shy[:32], shy[:32]}, want: true},
		{name: "whitespace does not count towards the threshold",
			pages: []string{strings.Repeat("a \n", extract.MinTextLayerRunes-1)}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extract.UsableTextLayer(tt.pages); got != tt.want {
				t.Errorf("UsableTextLayer() = %v, want %v", got, tt.want)
			}
		})
	}
}

// requireTool skips a test when the host tool it needs is not installed, so the
// suite is honest on a machine without poppler rather than red.
func requireTool(t *testing.T, name string) {
	t.Helper()
	if name == "" {
		return
	}
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed", name)
	}
}

func TestAMissingModelConfigurationIsATypedError(t *testing.T) {
	// The message names the two settings to add, and it will say the same thing
	// on every retry — which is the definition of a usage error in this tool's
	// contract, not a runtime one. The caller needs a type to classify it by.
	_, err := extract.Extract(context.Background(), corpus(t, "receipt.png"), opts(nil))
	if err == nil {
		t.Fatal("Extract() with no vision client = nil error")
	}

	var notConfigured *extract.NotConfiguredError
	if !errors.As(err, &notConfigured) {
		t.Fatalf("Extract() = %v; want a *extract.NotConfiguredError so main exits 2", err)
	}
	for _, want := range []string{"ocr.base_url", "ocr.model"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v; want it to name the setting to fix (%s)", err, want)
		}
	}
}
