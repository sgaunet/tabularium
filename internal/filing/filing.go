package filing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"syscall"
)

// tempPrefix marks a partially written file. It is prefixed and greppable
// because the one window where a crash can leak a file is this name, and a user
// who finds one should be able to tell what left it.
const tempPrefix = ".tabularium-"

// Filer writes into the archive tree.
//
// Every operation goes through os.Root, which resolves paths with openat2-style
// semantics inside the kernel: a traversal or a symlink pointing outside the
// root fails at the syscall rather than at a string comparison that a symlink
// or a race can defeat. filepath.Clean plus a prefix check is the usual
// approach and it is not equivalent — it validates a string, while this
// validates the actual resolution (FR-041).
type Filer struct {
	root *os.Root
	log  *slog.Logger
}

// Open opens the archive root. The directory must already exist: creating a
// tree the user did not ask for, at a path they may have mistyped, is not this
// tool's business.
func Open(archiveRoot string, log *slog.Logger) (*Filer, error) {
	root, err := os.OpenRoot(archiveRoot)
	if err != nil {
		return nil, fmt.Errorf("opening the archive root %s: %w", archiveRoot, err)
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Filer{root: root, log: log}, nil
}

// Close releases the root's file descriptor.
func (f *Filer) Close() error {
	if err := f.root.Close(); err != nil {
		return fmt.Errorf("closing the archive root: %w", err)
	}
	return nil
}

// Root is the archive root's path, for a message that needs to name it.
func (f *Filer) Root() string { return f.root.Name() }

// Outcome is what became of one document.
type Outcome struct {
	// Dest is the path actually written, relative to the archive root. It
	// differs from the requested destination when a collision suffix was
	// needed.
	Dest string

	// Duplicate is true when a file of identical content was already there.
	// Nothing was written, and the caller reports it on stderr while exiting 0
	// (FR-049, FR-069).
	Duplicate bool
}

// Place puts the bytes of src at dest inside the archive root.
//
// The write is temp-file-then-rename: a partial file is never visible under its
// final name (FR-046). When move is true the source is unlinked, and only after
// the copy's digest has been verified (FR-044).
func (f *Filer) Place(ctx context.Context, src string, size int64, digest [32]byte, dest string, move bool) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}

	dir := path.Dir(dest)
	if dir != "." {
		if err := f.root.MkdirAll(dir, 0o750); err != nil {
			return Outcome{}, fmt.Errorf("creating %s in the archive: %w", dir, err)
		}
	}

	// The temp file is written first and in the destination's own directory —
	// same filesystem, so the final rename is atomic — and it is fully written
	// and synced before any name is claimed.
	tmp, err := f.writeTemp(ctx, src, dir, digest)
	if err != nil {
		return Outcome{}, err
	}
	// Removed on every error path below, including cancellation (FR-047).
	defer func() { _ = f.root.Remove(tmp) }()

	claimed, duplicate, err := f.claim(dest, size, digest)
	if err != nil {
		return Outcome{}, err
	}
	if duplicate {
		f.log.Debug("identical content is already filed", "dest", claimed)
		// Nothing is written, but a move still means the user asked for the
		// scan box to be emptied, and the archive already holds these bytes.
		if move {
			if err := os.Remove(src); err != nil {
				return Outcome{}, fmt.Errorf("removing the duplicated source %s: %w", src, err)
			}
		}
		return Outcome{Dest: claimed, Duplicate: true}, nil
	}

	if err := f.root.Rename(tmp, claimed); err != nil {
		return Outcome{}, fmt.Errorf("placing %s in the archive: %w", claimed, err)
	}

	if move {
		// Only now: the bytes are in the archive and their digest has been
		// verified against the source's. FR-044 is the requirement that the
		// source is never removed before that holds.
		if err := os.Remove(src); err != nil {
			return Outcome{}, fmt.Errorf("removing the source %s after filing it: %w", src, err)
		}
	}

	f.log.Debug("filed", "dest", claimed, "move", move)
	return Outcome{Dest: claimed}, nil
}

// WriteFileAtomic writes body to name inside the archive.
//
// It is the sidecar's way in, and it takes the same temp-plus-rename route the
// documents do — with one difference: a sidecar replaces the one already there,
// because it describes the same document and a stale trace is worse than none.
func (f *Filer) WriteFileAtomic(name string, body []byte) error {
	dir := path.Dir(name)
	if dir != "." {
		if err := f.root.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating %s in the archive: %w", dir, err)
		}
	}

	tmp := tempName()
	if dir != "." {
		tmp = dir + "/" + tmp
	}

	out, err := f.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating a temporary file in the archive: %w", err)
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = f.root.Remove(tmp)
		}
	}()

	if _, err := out.Write(body); err != nil {
		_ = out.Close()
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("flushing %s: %w", name, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", name, err)
	}

	if err := f.root.Rename(tmp, name); err != nil {
		return fmt.Errorf("placing %s in the archive: %w", name, err)
	}
	removeTemp = false
	return nil
}

// ReadFile reads a file from inside the archive.
func (f *Filer) ReadFile(name string) ([]byte, error) {
	body, err := f.root.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("reading %s from the archive: %w", name, err)
	}
	return body, nil
}

// writeTemp copies src into a temp file in dir, verifying as it goes.
//
// The digest is computed from the bytes that were actually written, and
// compared against the source's — so a truncated read or a full disk is caught
// here rather than discovered when the document is next opened.
func (f *Filer) writeTemp(ctx context.Context, src, dir string, want [32]byte) (string, error) {
	in, err := os.Open(src) //nolint:gosec // the document the user named
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	name := tempName()
	if dir != "." {
		name = dir + "/" + name
	}

	out, err := f.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("creating a temporary file in the archive: %w", err)
	}

	h := sha256.New()
	if err := copyVerified(ctx, io.MultiWriter(out, h), in); err != nil {
		_ = out.Close()
		_ = f.root.Remove(name)
		return "", err
	}

	// Sync before the rename: without it the rename can be durable while the
	// contents are not, which is a file that exists and is empty after a crash.
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = f.root.Remove(name)
		return "", fmt.Errorf("flushing the archived copy: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = f.root.Remove(name)
		return "", fmt.Errorf("closing the archived copy: %w", err)
	}

	var got [32]byte
	copy(got[:], h.Sum(nil))
	if got != want {
		_ = f.root.Remove(name)
		return "", fmt.Errorf("the archived copy of %s does not match the source: "+
			"read %s, expected %s", src, hex.EncodeToString(got[:]), hex.EncodeToString(want[:]))
	}
	return name, nil
}

// copyChunk is how often the copy checks for cancellation. Small enough that a
// SIGINT is felt immediately, large enough not to matter.
const copyChunk = 512 << 10

// copyVerified copies in chunks so cancellation is honoured mid-file.
//
// io.Copy on a large file cannot be interrupted, and a scan can be hundreds of
// megabytes. Interrupting is what leaves no temp file behind (FR-047).
func copyVerified(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, copyChunk)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return fmt.Errorf("writing the archived copy: %w", werr)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the source: %w", err)
		}
	}
}

// tempName is unpredictable so two concurrent runs cannot collide on it.
func tempName() string {
	var b [8]byte
	// crypto/rand.Read cannot fail in Go 1.24 and later.
	_, _ = rand.Read(b[:])
	return tempPrefix + hex.EncodeToString(b[:]) + ".tmp"
}

// crossDevice reports whether a failed rename means the two paths are on
// different filesystems.
//
// It is the one rename failure worth recovering from, and it arrives wrapped in
// an *os.LinkError — which is why this is errors.Is and not a comparison
// (FR-044).
func crossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
