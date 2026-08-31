package extract_test

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/document"
	"github.com/sgaunet/tabularium/internal/extract"
)

func TestOOXMLAndODFCostZeroModelCalls(t *testing.T) {
	// FR-010: the open formats are zip plus XML, and the standard library reads
	// both. Sending an .odt to a vision model would be absurd and expensive.
	tests := []struct {
		name string
		file string
		want []string
	}{
		{
			name: "a .docx", file: "letter.docx",
			want: []string{"Assurance Habitation", "CT-2024-8891", "512.00 EUR"},
		},
		{
			name: "an .odt", file: "letter.odt",
			want: []string{"Avis d'impot sur le revenu", "24 31 000 123 456", "1240.00 EUR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vision := stubVision(t, "this should never be reached")

			got, err := extract.Extract(context.Background(), corpus(t, tt.file), opts(vision))
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if got.Origin != extract.OriginOffice {
				t.Errorf("Origin = %q, want %q", got.Origin, extract.OriginOffice)
			}
			if n := vision.calls.Load(); n != 0 {
				t.Errorf("the vision endpoint was called %d times for an office document, want 0", n)
			}
			for _, want := range tt.want {
				if !strings.Contains(got.Content, want) {
					t.Errorf("the extracted text is missing %q:\n%s", want, got.Content)
				}
			}
		})
	}
}

func TestParagraphsBecomeLines(t *testing.T) {
	// Without a line break per paragraph the whole document arrives as one
	// run-on line, and the model reads a wall of text where a form was.
	got, err := extract.Extract(context.Background(), corpus(t, "letter.docx"), opts(nil))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(got.Content, "\n") {
		t.Errorf("the extracted text has no line breaks:\n%q", got.Content)
	}
}

// buildArchive writes a zip and opens it as a document, so the bounds are
// exercised through the real sniffing and extraction path.
func buildArchive(t *testing.T, name string, entries []zipEntry) document.Source {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path) //nolint:gosec // a path this test just made
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}

	w := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.stored {
			h.Method = zip.Store
		}
		part, err := w.CreateHeader(h)
		if err != nil {
			t.Fatalf("adding %s: %v", e.name, err)
		}
		if e.repeat > 0 {
			writeRepeated(t, part, e.body, e.repeat)
			continue
		}
		if _, err := part.Write([]byte(e.body)); err != nil {
			t.Fatalf("writing %s: %v", e.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the archive: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing %s: %v", path, err)
	}

	src, err := document.Open(path)
	if err != nil {
		t.Fatalf("document.Open(%s): %v", name, err)
	}
	return src
}

type zipEntry struct {
	name   string
	body   string
	stored bool
	repeat int
}

// writeRepeated streams body repeat times, so a hostile fixture costs the test
// no more memory than one copy of it.
func writeRepeated(t *testing.T, w io.Writer, body string, repeat int) {
	t.Helper()
	chunk := []byte(body)
	for range repeat {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("writing a repeated chunk: %v", err)
		}
	}
}

const minimalContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
</Types>`

func TestAnArchiveDeclaringTooManyEntriesIsRefused(t *testing.T) {
	// FR-014, first bound. This one is checked before a single entry is read,
	// which is what makes it cheap enough to always apply.
	entries := make([]zipEntry, 0, 1102)
	entries = append(entries,
		zipEntry{name: "[Content_Types].xml", body: minimalContentTypes},
		zipEntry{name: "word/document.xml", body: `<w:document xmlns:w="x"><w:body><w:p><w:r><w:t>hello</w:t></w:r></w:p></w:body></w:document>`},
	)
	for i := range 1100 {
		entries = append(entries, zipEntry{name: fmt.Sprintf("word/media/img%04d.bin", i), body: "x"})
	}

	src := buildArchive(t, "bomb.docx", entries)
	_, err := extract.Extract(context.Background(), src, opts(nil))
	if err == nil {
		t.Fatal("Extract() on an archive of 1102 entries = nil error")
	}

	var bomb *extract.BombError
	if !errors.As(err, &bomb) {
		t.Fatalf("Extract() = %v; want a *extract.BombError", err)
	}
	if !strings.Contains(err.Error(), "1024") {
		t.Errorf("error = %v; want it to state the limit", err)
	}
}

func TestAZipBombAbortsOnBytesActuallyRead(t *testing.T) {
	// FR-014, second bound, and the reason it is written this way: archive/zip
	// reports UncompressedSize64 from the header, which the archive controls.
	// The only number an archive cannot lie about is how much came out of the
	// reader, so that is what is counted.
	//
	// One highly compressible part, past 64 MiB decompressed, a few dozen
	// kilobytes on disk. Only just past: the extractor stops the moment the
	// budget is crossed, so a larger fixture would prove nothing more and cost
	// the race detector real time.
	const chunk = `<w:p><w:r><w:t>AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA</w:t></w:r></w:p>`
	repeat := (68 << 20) / len(chunk)

	src := buildArchive(t, "bomb.docx", []zipEntry{
		{name: "[Content_Types].xml", body: minimalContentTypes},
		{name: "word/document.xml", body: chunk, repeat: repeat},
	})

	if src.Size > 4<<20 {
		t.Fatalf("the fixture is %d bytes on disk; it was meant to be small", src.Size)
	}

	_, err := extract.Extract(context.Background(), src, opts(nil))
	if err == nil {
		t.Fatal("Extract() on an 80 MiB decompression = nil error")
	}

	var bomb *extract.BombError
	if !errors.As(err, &bomb) {
		t.Fatalf("Extract() = %v; want a *extract.BombError", err)
	}
	if !strings.Contains(err.Error(), "64 MiB") {
		t.Errorf("error = %v; want it to state the limit", err)
	}
}

func TestManySmallPartsShareOneBudget(t *testing.T) {
	// A limit applied to each entry alone is defeated by many entries, which is
	// why the budget is cumulative across the parts that are read.
	// Seven sheets of 10 MiB: each one comfortably under the 64 MiB limit, and
	// seven of them comfortably over it. That is the whole point — a limit
	// applied per entry would let all seven through.
	const chunk = `<c:v>BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB</c:v>`
	perSheet := (10 << 20) / len(chunk)

	entries := make([]zipEntry, 0, 8)
	entries = append(entries, zipEntry{name: "[Content_Types].xml", body: minimalContentTypes})
	for i := range 7 {
		entries = append(entries, zipEntry{
			name:   fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1),
			body:   chunk,
			repeat: perSheet,
		})
	}

	src := buildArchive(t, "bomb.xlsx", entries)
	_, err := extract.Extract(context.Background(), src, opts(nil))
	if err == nil {
		t.Fatal("Extract() on seven 10 MiB sheets = nil error; the budget is not shared")
	}

	var bomb *extract.BombError
	if !errors.As(err, &bomb) {
		t.Fatalf("Extract() = %v; want a *extract.BombError", err)
	}
}

func TestANestedArchiveIsNeverEntered(t *testing.T) {
	// FR-014, third bound: depth is fixed at one. It needs no arithmetic to
	// enforce, which is what makes it the bound hardest to get wrong.
	inner := buildArchive(t, "inner.docx", []zipEntry{
		{name: "[Content_Types].xml", body: minimalContentTypes},
		{name: "word/document.xml", body: `<w:document xmlns:w="x"><w:body><w:p><w:r><w:t>SECRET-INNER-TEXT</w:t></w:r></w:p></w:body></w:document>`},
	})
	innerBytes, err := os.ReadFile(inner.Path)
	if err != nil {
		t.Fatalf("reading the inner archive: %v", err)
	}

	src := buildArchive(t, "outer.docx", []zipEntry{
		{name: "[Content_Types].xml", body: minimalContentTypes},
		{name: "word/document.xml", body: `<w:document xmlns:w="x"><w:body><w:p><w:r><w:t>outer text with enough words to be real</w:t></w:r></w:p></w:body></w:document>`},
		{name: "word/embeddings/nested.docx", body: string(innerBytes), stored: true},
	})

	got, err := extract.Extract(context.Background(), src, opts(nil))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if strings.Contains(got.Content, "SECRET-INNER-TEXT") {
		t.Error("the extractor descended into a nested archive")
	}
	if !strings.Contains(got.Content, "outer text") {
		t.Errorf("the outer document's own text is missing:\n%s", got.Content)
	}
}

func TestALegacyOLE2FileRoutesToLibreOfficeAndNamesItWhenAbsent(t *testing.T) {
	// FR-010 and FR-013. The legacy branch mirrors the precedent poppler sets:
	// a host tool, named in --help, reported by name when missing.
	t.Setenv("PATH", t.TempDir())

	_, err := extract.Extract(context.Background(), corpus(t, "legacy.doc"), opts(nil))
	if err == nil {
		t.Fatal("Extract() on a .doc with no libreoffice on PATH = nil error")
	}

	var missing *extract.MissingToolError
	if !errors.As(err, &missing) {
		t.Fatalf("Extract() = %v; want a *extract.MissingToolError", err)
	}
	if !strings.Contains(err.Error(), "libreoffice") {
		t.Errorf("error = %v; want it to name libreoffice", err)
	}
}

func TestAnOfficeRunNeverLooksForLibreOfficeUnlessItNeedsIt(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	got, err := extract.Extract(context.Background(), corpus(t, "letter.docx"), opts(nil))
	if err != nil {
		t.Fatalf("Extract() on a .docx with an empty PATH: %v", err)
	}
	if got.Origin != extract.OriginOffice {
		t.Errorf("Origin = %q, want %q", got.Origin, extract.OriginOffice)
	}
}
