package filing_test

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/filing"
)

// source writes a document outside the archive and returns everything Place
// needs to know about it.
type source struct {
	path   string
	size   int64
	digest [32]byte
}

func newSource(t *testing.T, dir, name, body string) source {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return source{path: path, size: int64(len(body)), digest: sha256.Sum256([]byte(body))}
}

func openFiler(t *testing.T, root string) *filing.Filer {
	t.Helper()
	f, err := filing.Open(root, nil)
	if err != nil {
		t.Fatalf("filing.Open(%s): %v", root, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// entries lists every path under root, so leftovers are visible.
func entries(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
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
	return out
}

func TestPlaceCreatesIntermediateDirectories(t *testing.T) {
	// FR-045: the tree grows as the rules require it to, and the user does not
	// have to have guessed the shape in advance.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)
	src := newSource(t, scans, "scan.pdf", "the document")

	got, err := f.Place(context.Background(), src.path, src.size, src.digest,
		"factures/voiture/2025/facture.pdf", true)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if got.Dest != "factures/voiture/2025/facture.pdf" {
		t.Errorf("Dest = %q", got.Dest)
	}

	body, err := os.ReadFile(filepath.Join(root, "factures", "voiture", "2025", "facture.pdf"))
	if err != nil {
		t.Fatalf("reading the filed document: %v", err)
	}
	if string(body) != "the document" {
		t.Errorf("the filed document reads %q, want %q", body, "the document")
	}
}

func TestConfinementIsEnforcedByTheKernelNotByAStringCheck(t *testing.T) {
	// FR-041 and SC-012. Every one of these fails at the os.Root operation. A
	// filepath.Clean plus a prefix comparison validates a string; os.Root
	// validates the actual resolution, which is what a symlink defeats.
	root, scans, outside := t.TempDir(), t.TempDir(), t.TempDir()
	f := openFiler(t, root)
	src := newSource(t, scans, "scan.pdf", "the document")

	// A symlink inside the archive pointing out of it.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	tests := []struct {
		name string
		dest string
	}{
		{name: "a parent traversal", dest: "../escaped.pdf"},
		{name: "a deep traversal", dest: "a/../../../escaped.pdf"},
		{name: "an absolute path", dest: "/tmp/escaped.pdf"},
		{name: "through a symlink pointing outside", dest: "escape/escaped.pdf"},
		{name: "a traversal after a real directory", dest: "factures/../../escaped.pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.Place(context.Background(), src.path, src.size, src.digest, tt.dest, false)
			if err == nil {
				t.Fatalf("Place(%q) = nil error; the write escaped the archive root", tt.dest)
			}

			// Nothing landed outside.
			if left := entries(t, outside); len(left) != 0 {
				t.Errorf("files appeared outside the archive root: %v", left)
			}
			if _, statErr := os.Stat(src.path); statErr != nil {
				t.Errorf("the source was disturbed by a refused write: %v", statErr)
			}
		})
	}
}

func TestTheWriteIsAtomic(t *testing.T) {
	// FR-046: no partial file is ever visible under its final name. The bytes
	// go to a temp file in the destination's own directory — same filesystem,
	// so the rename is atomic — and only then take the final name.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)
	src := newSource(t, scans, "scan.pdf", strings.Repeat("x", 1<<20))

	if _, err := f.Place(context.Background(), src.path, src.size, src.digest,
		"factures/2025/facture.pdf", true); err != nil {
		t.Fatalf("Place: %v", err)
	}

	// No temp file survived a successful run.
	for _, e := range entries(t, root) {
		if strings.Contains(e, ".tabularium-") {
			t.Errorf("a temporary file was left behind: %s", e)
		}
	}

	info, err := os.Stat(filepath.Join(root, "factures", "2025", "facture.pdf"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != src.size {
		t.Errorf("the filed document is %d bytes, want %d — a partial file is visible",
			info.Size(), src.size)
	}
}

func TestMoveUnlinksTheSourceAndCopyDoesNot(t *testing.T) {
	tests := []struct {
		name       string
		move       bool
		wantSource bool
	}{
		{name: "move empties the scan box", move: true, wantSource: false},
		{name: "copy leaves the source", move: false, wantSource: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, scans := t.TempDir(), t.TempDir()
			f := openFiler(t, root)
			src := newSource(t, scans, "scan.pdf", "the document")

			if _, err := f.Place(context.Background(), src.path, src.size, src.digest,
				"divers/2025/scan.pdf", tt.move); err != nil {
				t.Fatalf("Place: %v", err)
			}

			_, err := os.Stat(src.path)
			if tt.wantSource && err != nil {
				t.Errorf("the source is gone: %v", err)
			}
			if !tt.wantSource && err == nil {
				t.Error("the source survived a move")
			}
		})
	}
}

func TestACorruptedCopyIsRefusedAndTheSourceSurvives(t *testing.T) {
	// FR-044's real point: the source is never removed on the strength of a
	// copy that was not verified. A digest that does not match means the bytes
	// on disk are not the document, and unlinking the original would lose it.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)
	src := newSource(t, scans, "scan.pdf", "the document")

	// A digest that does not describe the file, standing in for a truncated
	// read or a full disk.
	wrong := sha256.Sum256([]byte("something else entirely"))

	_, err := f.Place(context.Background(), src.path, src.size, wrong, "divers/scan.pdf", true)
	if err == nil {
		t.Fatal("Place() with a mismatched digest = nil error")
	}
	if _, statErr := os.Stat(src.path); statErr != nil {
		t.Errorf("the source was removed despite an unverified copy: %v", statErr)
	}
	for _, e := range entries(t, root) {
		if !strings.Contains(e, ".tabularium-") {
			t.Errorf("a file was left in the archive after a failed copy: %s", e)
		} else {
			t.Errorf("a temporary file was left behind: %s", e)
		}
	}
}

func TestPlaceRefusesAnUnreadableSource(t *testing.T) {
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)
	src := newSource(t, scans, "scan.pdf", "the document")
	if err := os.Remove(src.path); err != nil {
		t.Fatalf("removing: %v", err)
	}

	if _, err := f.Place(context.Background(), src.path, src.size, src.digest,
		"divers/scan.pdf", true); err == nil {
		t.Fatal("Place() on a missing source = nil error")
	}
	if left := entries(t, root); len(left) != 0 {
		t.Errorf("files were left in the archive: %v", left)
	}
}

func TestOpenRefusesAnArchiveRootThatIsNotThere(t *testing.T) {
	// Creating a tree the user did not ask for, at a path they may have
	// mistyped, would put documents somewhere nobody looks.
	missing := filepath.Join(t.TempDir(), "no", "such", "tree")
	if _, err := filing.Open(missing, nil); err == nil {
		t.Fatal("filing.Open() on a missing root = nil error")
	}
}

// openFilerConcurrent is openFiler without the *testing.T, for use inside a
// goroutine where t.Fatalf is not allowed.
func openFilerConcurrent(root string) (*filing.Filer, error) {
	return filing.Open(root, nil)
}
