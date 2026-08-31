package triage

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/sgaunet/tabularium/internal/archiver"
	"github.com/sgaunet/tabularium/internal/filing"
	"github.com/sgaunet/tabularium/internal/sidecar"
)

// Outcome is what became of the document once the plan was executed.
type Outcome struct {
	// Dest is where the document actually landed, relative to the archive
	// root. It differs from the plan's when a collision suffix was needed, and
	// is empty when nothing was filed.
	Dest string

	// Duplicate is true when identical content was already filed. Nothing was
	// written, the fact goes to stderr, and the exit code stays 0 (FR-049).
	Duplicate bool

	// Filed is true when bytes were placed in the archive.
	Filed bool

	// Archive is the external hand-off's result. Attempted is false when the
	// archiver is unconfigured or disabled (FR-062).
	Archive archiver.Result
}

// FileOptions are the per-run choices that govern the write side.
type FileOptions struct {
	// Filer is the archive. A nil Filer means filing is off.
	Filer *filing.Filer

	// Archive is the external hand-off. A nil Archiver means archiving is off.
	Archiver *archiver.Archiver

	// Now is the clock, injected so a sidecar's filed_at is assertable.
	Now func() time.Time
}

func (o FileOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Execute carries out a plan: file the document, record the trace, hand it off.
//
// Nothing here recomputes anything. Everything it needs was decided by Build,
// which is what makes the write side short enough to read in one sitting — and
// what makes --dry-run's guarantee structural rather than conditional.
func Execute(ctx context.Context, plan Plan, o FileOptions) (Outcome, error) {
	var out Outcome

	if plan.Disposition == Keep || o.Filer == nil {
		// FR-042's "conservation sans écriture": the document is described and
		// left exactly where it is. There is then no archived path to hand off,
		// so the external step has nothing to be given.
		return out, nil
	}

	placed, err := o.Filer.Place(ctx, plan.Source.Path, plan.Source.Size, plan.Source.Digest,
		plan.Dest, plan.Disposition == Move)
	if err != nil {
		return out, err
	}
	out.Dest = placed.Dest
	out.Duplicate = placed.Duplicate
	out.Filed = !placed.Duplicate

	// The hand-off runs for a duplicate too: the archive already holds the
	// bytes, but the external system may never have been told about them.
	out.Archive = o.hand(ctx, plan, placed.Dest)

	// The sidecar is written last and always, so it records the hand-off's
	// outcome as well as the filing's (FR-051).
	if err := o.trace(plan, placed.Dest, out.Archive); err != nil {
		return out, err
	}

	if out.Archive.Attempted && !out.Archive.Success {
		// FR-063: the file stays where it was filed. Unwinding a good filing
		// because a separate system was unavailable would turn one problem into
		// two, and the sidecar above is what makes the missing half replayable.
		return out, &ArchiveFailedError{
			Dest: placed.Dest, Command: o.Archiver.Command(), Result: out.Archive,
		}
	}
	return out, nil
}

// hand runs the external archiver, if one is configured.
func (o FileOptions) hand(ctx context.Context, plan Plan, dest string) archiver.Result {
	if o.Archiver == nil {
		return archiver.Result{}
	}
	// The external system is given an absolute path: it is a separate process
	// with its own working directory, and a path relative to the archive root
	// means nothing to it.
	archived := filepath.Join(o.Filer.Root(), filepath.FromSlash(dest))
	return o.Archiver.Run(ctx, archiver.Subject{
		Path:     archived,
		Metadata: plan.Metadata,
	})
}

// trace deposits the sidecar beside the archived document.
func (o FileOptions) trace(plan Plan, dest string, result archiver.Result) error {
	return sidecar.Write(o.Filer, dest, sidecar.Sidecar{
		Source:       plan.Source.Path,
		SourceDigest: plan.Source.HexDigest(),
		Metadata:     plan.Metadata,
		Rule:         plan.Rule.Name,
		FiledAt:      o.now(),
		Truncation:   plan.Text.Truncation,
		Archive:      result,
	})
}

// ArchiveFailedError says the document was filed and the hand-off was not.
//
// It is a runtime failure — exit 1 — and deliberately not a reason to undo the
// filing: the document is where the user asked for it to be, and re-running
// replays only the missing half (FR-063, FR-052).
type ArchiveFailedError struct {
	Dest string

	// Command is the archiver as configured, so a caller reporting what it said
	// can name where the words came from (FR-057a).
	Command string
	Result  archiver.Result
}

func (e *ArchiveFailedError) Error() string {
	reason := e.Result.Err
	if reason == "" && e.Result.ExitCode != nil {
		reason = fmt.Sprintf("it exited %d", *e.Result.ExitCode)
	}
	if reason == "" {
		reason = "it failed"
	}
	return fmt.Sprintf("the document is filed at %s, but the archiver did not accept it: %s. "+
		"Re-run on the filed document to replay only the hand-off", e.Dest, reason)
}
