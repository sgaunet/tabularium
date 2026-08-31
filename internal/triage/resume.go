package triage

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/sgaunet/tabularium/internal/document"
	"github.com/sgaunet/tabularium/internal/sidecar"
)

// Replay finishes work an earlier run left undone.
//
// It is the second entry point of FR-052: when the argument is a document that
// is already filed and whose sidecar records a hand-off that did not succeed,
// only the hand-off runs. No extraction, no analysis, no move — which is what
// SC-004 measures, and what makes recovering from an archiver outage a matter
// of re-running the same command.
//
// The second return is false when there is nothing to replay and the document
// should be processed from the start.
func Replay(ctx context.Context, docPath string, deps Deps, o FileOptions) (Outcome, bool, error) {
	log := deps.logger()

	if o.Filer == nil {
		return Outcome{}, false, nil
	}
	dest, inside := relativeTo(o.Filer.Root(), docPath)
	if !inside {
		// The document is not in the archive, so it has not been filed and
		// there is nothing an earlier run could have left behind.
		return Outcome{}, false, nil
	}

	// The digest is what the sidecar is checked against, and it comes from the
	// bytes rather than from the sidecar's own claim about them.
	src, err := document.Open(docPath)
	if err != nil {
		// Let the ordinary path report this properly.
		return Outcome{}, false, nil //nolint:nilerr // the main path produces the real error
	}

	sc, ok, err := sidecar.Read(o.Filer, dest, src.HexDigest())
	if err != nil {
		// FR-053: unparseable, an unknown version, or a digest that does not
		// match are all treated as absent — the anomaly is logged and the
		// document is reprocessed. The bytes always win over the sidecar.
		log.Warn("ignoring an unusable sidecar and reprocessing from the start",
			"path", docPath, "reason", err)
		return Outcome{}, false, nil
	}
	if !ok {
		return Outcome{}, false, nil
	}

	out := Outcome{Dest: dest, Archive: sc.Archive}

	if !sc.HandOffOutstanding() {
		// FR-052: the hand-off already succeeded. There is nothing left to do,
		// and doing it again would file the document twice in the external
		// system.
		log.Info("this document is already filed and already handed off; nothing to do",
			"dest", dest, "filed_at", sc.FiledAt.Format(time.RFC3339))
		return out, true, nil
	}

	if o.Archiver == nil {
		// Filed, the hand-off is outstanding, and archiving is off or
		// unconfigured. Saying so is more use than silently succeeding.
		log.Warn("this document is filed and its hand-off is still outstanding, "+
			"but no archiver is configured or it is disabled", "dest", dest)
		return out, true, nil
	}

	log.Info("replaying only the hand-off", "dest", dest)

	plan := Plan{
		Source:      src,
		Metadata:    sc.Metadata,
		Dest:        dest,
		Disposition: Keep,
	}
	plan.Rule.Name = sc.Rule

	out.Archive = o.hand(ctx, plan, dest)

	// The sidecar is rewritten either way, so the next run sees the new state
	// rather than the old one.
	sc.Archive = out.Archive
	if err := sidecar.Write(o.Filer, dest, sc); err != nil {
		return out, true, err
	}

	if out.Archive.Attempted && !out.Archive.Success {
		return out, true, &ArchiveFailedError{
			Dest: dest, Command: o.Archiver.Command(), Result: out.Archive,
		}
	}
	return out, true, nil
}

// relativeTo reports whether path is inside root, and where.
//
// The result is slash-separated, because that is what the archive's own paths
// are and what the sidecar records.
func relativeTo(root, path string) (string, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
