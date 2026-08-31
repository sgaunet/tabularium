package filing

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
)

// maxSuffix bounds the -2, -3, … search.
//
// A thousand documents that differ from each other and share one computed name
// is not a collision any more; it is a rule that needs fixing, and saying so is
// more use than a thousandth suffix.
const maxSuffix = 1000

// claim reserves a name for the document.
//
// The probe and the claim are one atomic step: O_CREAT|O_EXCL either creates
// the file or tells us somebody else has it. That is what makes two concurrent
// runs racing for the same name resolve to one winner and one -2, with no lost
// file and no lock to leak if a process is killed (research.md D16) — and it is
// why `xargs -P` is safe by construction.
func (f *Filer) claim(dest string, size int64, digest [32]byte) (name string, duplicate bool, err error) {
	base, ext := splitExt(dest)

	for n := 1; n <= maxSuffix; n++ {
		candidate := dest
		if n > 1 {
			candidate = base + "-" + strconv.Itoa(n) + ext
		}

		handle, err := f.root.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			// The name is ours. Closing leaves a zero-byte placeholder that the
			// caller's rename replaces in one atomic step.
			if closeErr := handle.Close(); closeErr != nil {
				return "", false, fmt.Errorf("claiming %s: %w", candidate, closeErr)
			}
			return candidate, false, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", false, fmt.Errorf("claiming %s in the archive: %w", candidate, err)
		}

		same, err := f.sameContent(candidate, size, digest)
		if err != nil {
			return "", false, err
		}
		if same {
			// FR-049: identical content is a duplicate. Nothing is written, the
			// fact goes to stderr, and the exit code stays 0 — the document is
			// already archived, which is what the user wanted.
			return candidate, true, nil
		}
		// Different content under the same name: try the next suffix (FR-050).
	}

	return "", false, fmt.Errorf("%s: more than %d documents already claim this name; "+
		"the rule that produced it is probably too coarse", dest, maxSuffix)
}

// sameContent compares an occupant of the destination against the source.
//
// Size first: two files of different sizes cannot have the same content, and
// that skips hashing a large file in the common case where the collision is a
// genuinely different document (research.md D11).
func (f *Filer) sameContent(name string, size int64, digest [32]byte) (bool, error) {
	info, err := f.root.Stat(name)
	if err != nil {
		return false, fmt.Errorf("examining the existing %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		// A directory or a device sitting where a document should go is not a
		// duplicate, and overwriting it is not this tool's business.
		return false, nil
	}

	// A zero-byte occupant is another run's claim that has not landed yet, or
	// the residue of one that was killed between claiming and renaming. It is
	// not identical content, so it gets suffixed around rather than trusted.
	if info.Size() == 0 {
		return false, nil
	}
	// The cheap guard: different sizes cannot be identical content, and this
	// skips hashing a large file in the common case.
	if info.Size() != size {
		return false, nil
	}

	existing, err := f.root.Open(name)
	if err != nil {
		return false, fmt.Errorf("reading the existing %s: %w", name, err)
	}
	defer func() { _ = existing.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, existing); err != nil {
		return false, fmt.Errorf("hashing the existing %s: %w", name, err)
	}

	var got [32]byte
	copy(got[:], h.Sum(nil))
	return got == digest, nil
}

// splitExt splits a destination into the part a suffix goes after and the
// extension it goes before, so a collision becomes `facture-2.pdf` rather than
// `facture.pdf-2`.
func splitExt(dest string) (base, ext string) {
	ext = path.Ext(dest)
	return strings.TrimSuffix(dest, ext), ext
}
