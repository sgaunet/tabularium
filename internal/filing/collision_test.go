package filing_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// preplace puts a document into the archive at dest, so a later Place collides
// with it.
func preplace(t *testing.T, root, dest, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(dest))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("preparing %s: %v", dest, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", dest, err)
	}
}

func TestIdenticalContentIsADuplicateAndNothingIsWritten(t *testing.T) {
	// FR-048, FR-049, SC-003. The user re-scanned a document they had already
	// filed. Writing a second copy would quietly double the archive; saying so
	// and stopping is what they want.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)

	const body = "the very same document"
	preplace(t, root, "factures/2025/facture.pdf", body)
	src := newSource(t, scans, "scan.pdf", body)

	before := entries(t, root)

	got, err := f.Place(context.Background(), src.path, src.size, src.digest,
		"factures/2025/facture.pdf", false)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	if !got.Duplicate {
		t.Error("Duplicate = false for identical content")
	}
	if got.Dest != "factures/2025/facture.pdf" {
		t.Errorf("Dest = %q, want the existing file's path", got.Dest)
	}

	after := entries(t, root)
	sort.Strings(before)
	sort.Strings(after)
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Errorf("the archive changed for a duplicate:\nbefore %v\nafter  %v", before, after)
	}
}

func TestADuplicateStillEmptiesTheScanBoxOnAMove(t *testing.T) {
	// The document is already archived, which is what the user wanted. Leaving
	// the re-scan in the scan box would make them look at it again tomorrow.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)

	const body = "the very same document"
	preplace(t, root, "factures/2025/facture.pdf", body)
	src := newSource(t, scans, "scan.pdf", body)

	got, err := f.Place(context.Background(), src.path, src.size, src.digest,
		"factures/2025/facture.pdf", true)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if !got.Duplicate {
		t.Fatal("Duplicate = false for identical content")
	}
	if _, err := os.Stat(src.path); err == nil {
		t.Error("the duplicated source survived a move")
	}
}

func TestDifferentContentGetsASuffix(t *testing.T) {
	// FR-050: two genuinely different documents that compute the same name.
	// Overwriting either would lose one of them.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)

	preplace(t, root, "factures/2025/facture.pdf", "the first document")

	second := newSource(t, scans, "second.pdf", "a different document")
	got, err := f.Place(context.Background(), second.path, second.size, second.digest,
		"factures/2025/facture.pdf", false)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if got.Duplicate {
		t.Error("Duplicate = true for different content")
	}
	if got.Dest != "factures/2025/facture-2.pdf" {
		t.Errorf("Dest = %q, want %q", got.Dest, "factures/2025/facture-2.pdf")
	}

	third := newSource(t, scans, "third.pdf", "a third, also different")
	got, err = f.Place(context.Background(), third.path, third.size, third.digest,
		"factures/2025/facture.pdf", false)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if got.Dest != "factures/2025/facture-3.pdf" {
		t.Errorf("Dest = %q, want %q", got.Dest, "factures/2025/facture-3.pdf")
	}

	// The original is untouched.
	body, err := os.ReadFile(filepath.Join(root, "factures", "2025", "facture.pdf"))
	if err != nil {
		t.Fatalf("reading the original: %v", err)
	}
	if string(body) != "the first document" {
		t.Errorf("the existing document was overwritten: %q", body)
	}
}

func TestTheSuffixGoesBeforeTheExtension(t *testing.T) {
	// `facture-2.pdf`, not `facture.pdf-2`: the extension is what tells every
	// other program what the file is.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)

	tests := []struct {
		name string
		dest string
		want string
	}{
		{name: "a normal extension", dest: "a/doc.pdf", want: "a/doc-2.pdf"},
		{name: "a compound name", dest: "a/2025-03-14-facture.pdf", want: "a/2025-03-14-facture-2.pdf"},
		{name: "no extension at all", dest: "a/doc", want: "a/doc-2"},
		{name: "an uppercase extension", dest: "a/doc.JPEG", want: "a/doc-2.JPEG"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preplace(t, root, tt.dest, "occupied by "+tt.name)
			src := newSource(t, scans, strings.ReplaceAll(tt.name, " ", "-"), "different "+tt.name)

			got, err := f.Place(context.Background(), src.path, src.size, src.digest, tt.dest, false)
			if err != nil {
				t.Fatalf("Place: %v", err)
			}
			if got.Dest != tt.want {
				t.Errorf("Dest = %q, want %q", got.Dest, tt.want)
			}
		})
	}
}

func TestSameSizeDifferentContentIsNotADuplicate(t *testing.T) {
	// The size guard is a shortcut past hashing, not a substitute for it.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)

	preplace(t, root, "a/doc.pdf", "AAAAAAAAAA")
	src := newSource(t, scans, "scan.pdf", "BBBBBBBBBB")

	got, err := f.Place(context.Background(), src.path, src.size, src.digest, "a/doc.pdf", false)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if got.Duplicate {
		t.Error("Duplicate = true for two files of equal size and different content")
	}
	if got.Dest != "a/doc-2.pdf" {
		t.Errorf("Dest = %q, want a suffixed name", got.Dest)
	}
}

func TestConcurrentClaimsResolveToOneWinnerAndNoLostFile(t *testing.T) {
	// research.md D16 and FR-069: O_CREAT|O_EXCL makes the probe and the claim
	// one atomic step, which is what makes `xargs -P` safe by construction. No
	// lock file, so a killed process cannot wedge the tree.
	const runners = 8

	root, scans := t.TempDir(), t.TempDir()

	sources := make([]source, runners)
	for i := range runners {
		// Each is a different document, so every one must survive.
		sources[i] = newSource(t, scans, filepathName(i), strings.Repeat(string(rune('a'+i)), 64+i))
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed []string
		failed  []error
	)
	for i := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A Filer each: this is what separate processes would have.
			f, err := openFilerConcurrent(root)
			if err != nil {
				mu.Lock()
				failed = append(failed, err)
				mu.Unlock()
				return
			}
			defer func() { _ = f.Close() }()

			got, err := f.Place(context.Background(), sources[i].path, sources[i].size,
				sources[i].digest, "factures/2025/facture.pdf", false)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, err)
				return
			}
			claimed = append(claimed, got.Dest)
		}()
	}
	wg.Wait()

	for _, err := range failed {
		t.Errorf("a concurrent Place failed: %v", err)
	}
	if len(claimed) != runners {
		t.Fatalf("%d of %d runners claimed a name", len(claimed), runners)
	}

	// Every runner got a distinct name, and every document is on disk.
	seen := map[string]bool{}
	for _, name := range claimed {
		if seen[name] {
			t.Errorf("two runners claimed %q: a document was lost", name)
		}
		seen[name] = true
	}

	files := entries(t, root)
	if len(files) != runners {
		t.Errorf("the archive holds %d files, want %d: %v", len(files), runners, files)
	}
	for _, e := range files {
		if strings.Contains(e, ".tabularium-") {
			t.Errorf("a temporary file was left behind: %s", e)
		}
	}
}

func filepathName(i int) string {
	return "scan-" + string(rune('a'+i)) + ".pdf"
}
