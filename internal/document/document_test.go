package document_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/document"
)

// corpus returns the path of a file from testdata, copied into a temp
// directory so no test can disturb the shared corpus.
func corpus(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("reading corpus file %s: %v", name, err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestOpenDetectsTypeFromContentNotExtension(t *testing.T) {
	// FR-002: the extension is a hint the user controls and frequently gets
	// wrong. Every one of these files is renamed to a lie before it is sniffed.
	tests := []struct {
		name     string
		file     string
		lyingAs  string
		wantKind document.Kind
	}{
		{name: "PDF", file: "born-digital.pdf", lyingAs: "invoice.png", wantKind: document.PDF},
		{name: "JPEG", file: "receipt.jpg", lyingAs: "scan.pdf", wantKind: document.JPEG},
		{name: "PNG", file: "receipt.png", lyingAs: "photo.jpg", wantKind: document.PNG},
		{name: "little-endian TIFF", file: "little-endian.tiff", lyingAs: "x.docx", wantKind: document.TIFF},
		{name: "big-endian TIFF", file: "big-endian.tiff", lyingAs: "x.txt", wantKind: document.TIFF},
		{name: "OOXML", file: "letter.docx", lyingAs: "letter.odt", wantKind: document.OOXMLWord},
		{name: "ODF", file: "letter.odt", lyingAs: "letter.docx", wantKind: document.ODFText},
		{name: "legacy OLE2", file: "legacy.doc", lyingAs: "legacy.txt", wantKind: document.OLE2},
		{name: "Markdown", file: "notes.md", lyingAs: "notes.pdf", wantKind: document.Text},
		{name: "plain text", file: "notes.txt", lyingAs: "notes.jpg", wantKind: document.Text},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := corpus(t, tt.file)
			lie := filepath.Join(filepath.Dir(src), tt.lyingAs)
			if err := os.Rename(src, lie); err != nil {
				t.Fatalf("renaming: %v", err)
			}

			got, err := document.Open(lie)
			if err != nil {
				t.Fatalf("Open(%s): %v", tt.lyingAs, err)
			}
			if got.Type != tt.wantKind {
				t.Errorf("Open(%s as %s).Type = %q, want %q — the extension must not decide",
					tt.file, tt.lyingAs, got.Type, tt.wantKind)
			}
		})
	}
}

func TestOpenPreservesTheOriginalExtension(t *testing.T) {
	// FR-036: whatever the type turns out to be, the archived file keeps the
	// extension it arrived with.
	src := corpus(t, "born-digital.pdf")

	got, err := document.Open(src)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got.Ext != ".pdf" {
		t.Errorf("Ext = %q, want %q", got.Ext, ".pdf")
	}
}

func TestOpenRecordsSizeAndDigest(t *testing.T) {
	src := corpus(t, "notes.txt")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}

	got, err := document.Open(src)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if got.Size != int64(len(body)) {
		t.Errorf("Size = %d, want %d", got.Size, len(body))
	}
	want := sha256.Sum256(body)
	if got.Digest != want {
		t.Errorf("Digest = %x, want %x", got.Digest, want)
	}
	if got.HexDigest() != hex.EncodeToString(want[:]) {
		t.Errorf("HexDigest() = %q, want %q", got.HexDigest(), hex.EncodeToString(want[:]))
	}
}

func TestOpenResolvesToAnAbsolutePath(t *testing.T) {
	src := corpus(t, "notes.txt")
	rel, err := filepath.Rel(mustGetwd(t), src)
	if err != nil {
		t.Skipf("no relative path to the corpus: %v", err)
	}

	got, err := document.Open(rel)
	if err != nil {
		t.Fatalf("Open(%s): %v", rel, err)
	}
	if !filepath.IsAbs(got.Path) {
		t.Errorf("Path = %q, want an absolute path", got.Path)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return wd
}

func TestUnsupportedTypeNamesWhatWasDetected(t *testing.T) {
	// FR-004: "unsupported file" tells the user nothing. Naming the type is
	// what lets them see that their .pdf is actually a zip.
	tests := []struct {
		name string
		file string
		want string
	}{
		{
			name: "an opaque binary",
			file: "unsupported.bin",
			want: "application/octet-stream",
		},
		{
			name: "an ordinary zip is not an office document",
			file: "plain.zip",
			want: "zip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := document.Open(corpus(t, tt.file))
			if err == nil {
				t.Fatal("Open() on an unsupported file = nil error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Open() error = %v; want it to name the detected type %q", err, tt.want)
			}
			var unsupported *document.UnsupportedError
			if !asUnsupported(err, &unsupported) {
				t.Errorf("Open() error = %v; want a *document.UnsupportedError", err)
			}
		})
	}
}

func TestNonRegularFilesAreRefused(t *testing.T) {
	// FR-005: Mode().IsRegular() catches directories, devices, sockets and
	// FIFOs in one check, which is why it is the check.
	t.Run("a directory", func(t *testing.T) {
		if _, err := document.Open(t.TempDir()); err == nil {
			t.Fatal("Open(directory) = nil error")
		}
	})

	t.Run("a character device", func(t *testing.T) {
		if _, err := document.Open(os.DevNull); err == nil {
			t.Fatalf("Open(%s) = nil error", os.DevNull)
		}
	})

	t.Run("a file that is not there", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope.pdf")
		if _, err := document.Open(missing); err == nil {
			t.Fatal("Open(missing) = nil error")
		}
	})

	t.Run("an empty file", func(t *testing.T) {
		// Nothing can be detected from no bytes, and nothing can be extracted
		// from it either — saying so here is clearer than a confusing failure
		// three stages later.
		empty := filepath.Join(t.TempDir(), "empty.pdf")
		if err := os.WriteFile(empty, nil, 0o600); err != nil {
			t.Fatalf("writing: %v", err)
		}
		if _, err := document.Open(empty); err == nil {
			t.Fatal("Open(empty file) = nil error")
		}
	})
}

func TestAnUnreadableFileIsRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permissions do not apply")
	}
	src := corpus(t, "notes.txt")
	if err := os.Chmod(src, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, 0o600) })

	if _, err := document.Open(src); err == nil {
		t.Fatal("Open() on an unreadable file = nil error")
	}
}

func TestKindsRouteToAStrategy(t *testing.T) {
	// The supported set is a closed list; a Kind that is in it must say which
	// extraction strategy it wants, and one that is not must say so.
	supported := []document.Kind{
		document.PDF, document.JPEG, document.PNG, document.TIFF, document.WebP,
		document.OOXMLWord, document.OOXMLSheet, document.OOXMLSlides,
		document.ODFText, document.ODFSheet, document.ODFSlides,
		document.OLE2, document.Text,
	}
	for _, k := range supported {
		if !k.Supported() {
			t.Errorf("Kind(%q).Supported() = false, want true", k)
		}
	}

	for _, k := range []document.Kind{"application/zip", "application/octet-stream", ""} {
		if k.Supported() {
			t.Errorf("Kind(%q).Supported() = true, want false", k)
		}
	}
}

// asUnsupported is errors.As, spelled out so the test file does not need to
// import errors purely for one call.
func asUnsupported(err error, target **document.UnsupportedError) bool {
	for err != nil {
		if u, ok := err.(*document.UnsupportedError); ok { //nolint:errorlint // walking deliberately
			*target = u
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
