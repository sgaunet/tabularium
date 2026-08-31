package extract_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/document"
	"github.com/sgaunet/tabularium/internal/extract"
)

func TestPlainTextAndMarkdownAreReadDirectly(t *testing.T) {
	// FR-011: the text is already the text. No host tool, no model call, and
	// the origin says so.
	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "Markdown", file: "notes.md", want: "Banque Populaire du Sud"},
		{name: "plain text", file: "notes.txt", want: "Energie du Sud"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vision := stubVision(t, "this should never be reached")

			got, err := extract.Extract(context.Background(), corpus(t, tt.file), opts(vision))
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if got.Origin != extract.OriginPlain {
				t.Errorf("Origin = %q, want %q", got.Origin, extract.OriginPlain)
			}
			if n := vision.calls.Load(); n != 0 {
				t.Errorf("the vision endpoint was called %d times for plain text, want 0", n)
			}
			if !strings.Contains(got.Content, tt.want) {
				t.Errorf("the extracted text is missing %q:\n%s", tt.want, got.Content)
			}
			if got.Truncation != nil {
				t.Errorf("Truncation = %+v for a text file, want nil", got.Truncation)
			}
		})
	}
}

func TestMarkdownIsNotRenderedOrStripped(t *testing.T) {
	// The model reads the markup perfectly well, and stripping it would throw
	// away the table structure that says which number is which.
	got, err := extract.Extract(context.Background(), corpus(t, "notes.md"), opts(nil))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, want := range []string{"# Releve trimestriel", "**Banque Populaire du Sud**", "| Poste"} {
		if !strings.Contains(got.Content, want) {
			t.Errorf("the markup %q did not survive:\n%s", want, got.Content)
		}
	}
}

func TestAnAbsurdlyLargeTextFileIsRefused(t *testing.T) {
	// A "text file" that is 40 MiB is not a document, and reading it into
	// memory to find that out is the wrong order of operations.
	path := filepath.Join(t.TempDir(), "huge.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	chunk := []byte(strings.Repeat("word ", 1024))
	for range (40 << 20) / len(chunk) {
		if _, err := f.Write(chunk); err != nil {
			t.Fatalf("writing: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	src, err := document.Open(path)
	if err != nil {
		t.Fatalf("document.Open: %v", err)
	}
	if _, err := extract.Extract(context.Background(), src, opts(nil)); err == nil {
		t.Fatal("Extract() on a 40 MiB text file = nil error")
	}
}
