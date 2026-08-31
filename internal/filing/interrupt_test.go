package filing_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/filing"
)

func TestCancellingMidCopyLeavesNothingBehind(t *testing.T) {
	// FR-047, FR-074, SC-006. A CLI is run interactively and killed
	// interactively, and the state it is killed in has to be a state the user
	// can live with: no temp file, no file under the final name, source intact.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)

	// Large enough that the copy loop checks the context several times.
	src := newSource(t, scans, "scan.pdf", strings.Repeat("x", 8<<20))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := f.Place(ctx, src.path, src.size, src.digest, "factures/2025/facture.pdf", true)
	if err == nil {
		t.Fatal("Place() with a cancelled context = nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Place() = %v; want it to wrap context.Canceled", err)
	}

	assertNothingLeftBehind(t, root, src.path)
}

func TestCancellingPartWayThroughLeavesNothingBehind(t *testing.T) {
	// The same property, but with the cancellation arriving while bytes are
	// actually moving rather than before the first one.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)
	src := newSource(t, scans, "scan.pdf", strings.Repeat("y", 16<<20))

	ctx, cancel := context.WithCancel(context.Background())
	go cancel()

	// Whether the copy finishes before the cancel lands is a race by design;
	// either outcome must leave the tree in a state the user can live with.
	_, err := f.Place(ctx, src.path, src.size, src.digest, "factures/2025/facture.pdf", true)
	if err == nil {
		// It won the race. The document is filed and the source is gone, which
		// is a perfectly good outcome.
		if _, statErr := os.Stat(src.path); statErr == nil {
			t.Error("Place() reported success but the source survived a move")
		}
		assertNoTempFiles(t, root)
		return
	}
	assertNothingLeftBehind(t, root, src.path)
}

// assertNothingLeftBehind is the whole of SC-006: an interrupted run leaves the
// archive as it was and the document where it was.
func assertNothingLeftBehind(t *testing.T, root, srcPath string) {
	t.Helper()

	if left := entries(t, root); len(left) != 0 {
		t.Errorf("an interrupted run left files in the archive: %v", left)
	}
	if _, err := os.Stat(srcPath); err != nil {
		t.Errorf("an interrupted run removed the source: %v", err)
	}
}

func assertNoTempFiles(t *testing.T, root string) {
	t.Helper()
	for _, e := range entries(t, root) {
		if strings.Contains(e, filing.TempPrefix) {
			t.Errorf("a temporary file was left behind: %s", e)
		}
	}
}
