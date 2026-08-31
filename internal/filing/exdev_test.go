package filing_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"testing"

	"github.com/sgaunet/tabularium/internal/filing"
)

func TestCrossDeviceIsMatchedThroughTheLinkError(t *testing.T) {
	// FR-044. EXDEV arrives wrapped in an *os.LinkError, so a comparison
	// against the bare errno silently never fires — which is the bug this
	// classifier exists to not have.
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "EXDEV inside a LinkError, which is how a rename reports it",
			err:  &os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EXDEV},
			want: true,
		},
		{
			name: "a bare EXDEV",
			err:  syscall.EXDEV,
			want: true,
		},
		{
			name: "EXDEV wrapped again with %w, as an error travelling up a stack is",
			err:  fmt.Errorf("filing the document: %w", &os.LinkError{Err: syscall.EXDEV}),
			want: true,
		},
		{
			name: "a permission error is not a cross-device rename",
			err:  &os.LinkError{Op: "rename", Err: syscall.EACCES},
			want: false,
		},
		{
			name: "a missing file is not a cross-device rename",
			err:  &os.PathError{Op: "rename", Err: fs.ErrNotExist},
			want: false,
		},
		{
			name: "no error at all",
			err:  nil,
			want: false,
		},
		{
			name: "an unrelated error",
			err:  errors.New("something else"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filing.CrossDevice(tt.err); got != tt.want {
				t.Errorf("CrossDevice(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestFilingWorksWhenTheSourceIsOnAnotherVolume(t *testing.T) {
	// FR-044, structurally. The document is copied into the archive and
	// verified before the source is unlinked, so a source on another volume is
	// not a special case at all — there is no rename across the boundary to
	// fail. That is why this works without EXDEV ever arising.
	root, scans := t.TempDir(), t.TempDir()
	f := openFiler(t, root)
	src := newSource(t, scans, "scan.pdf", "a document from another volume")

	got, err := f.Place(t.Context(), src.path, src.size, src.digest, "divers/2025/scan.pdf", true)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if got.Duplicate {
		t.Error("Duplicate = true for a first filing")
	}

	// The bytes arrived.
	body, err := os.ReadFile(root + "/divers/2025/scan.pdf")
	if err != nil {
		t.Fatalf("reading the filed document: %v", err)
	}
	if string(body) != "a document from another volume" {
		t.Errorf("the filed document reads %q", body)
	}
	// And only then did the source go.
	if _, err := os.Stat(src.path); err == nil {
		t.Error("the source survived a move")
	}
}
