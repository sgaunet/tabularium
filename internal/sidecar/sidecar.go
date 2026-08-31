package sidecar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sgaunet/tabularium/internal/analysis"
	"github.com/sgaunet/tabularium/internal/archiver"
	"github.com/sgaunet/tabularium/internal/extract"
)

// Version is the sidecar schema version. A value this build does not know means
// the sidecar is treated as absent (FR-053).
const Version = 1

// Suffix is appended to the archived filename, so a sidecar sits beside the
// document it describes and travels with it if the tree is moved.
const Suffix = ".tabularium.json"

// Name is the sidecar's path for an archived document.
func Name(dest string) string { return dest + Suffix }

// Sidecar is the trace deposited beside an archived document.
//
// It is written on every successful filing, not only on failure: it is a record
// of what happened, and it is what makes a failed hand-off replayable without
// redoing any of the work that succeeded (FR-051, FR-052).
type Sidecar struct {
	Version      int
	Source       string
	SourceDigest string
	Metadata     analysis.Metadata
	Rule         string
	FiledAt      time.Time
	Truncation   *extract.Truncation
	Archive      archiver.Result
}

// Store is the archive, as much of it as a sidecar needs.
//
// It is an interface so that internal/sidecar does not import internal/filing:
// the sidecar is a document like any other and goes through the same confined
// root, but nothing here needs to know how that root is enforced.
type Store interface {
	// WriteFileAtomic writes body to name inside the archive, without a partial
	// file ever being visible under that name.
	WriteFileAtomic(name string, body []byte) error

	// ReadFile reads name from inside the archive.
	ReadFile(name string) ([]byte, error)
}

// wire is contracts/sidecar.schema.json, field for field.
//
// It is a separate type from Sidecar because the on-disk shape is a contract
// and the in-memory one is not: renaming a Go field must not change the file.
type wire struct {
	Version      int               `json:"version"`
	Source       string            `json:"source"`
	SourceDigest string            `json:"source_digest"`
	Metadata     analysis.Metadata `json:"metadata"`
	Rule         string            `json:"rule"`
	FiledAt      string            `json:"filed_at"`
	Truncation   *wireTruncation   `json:"truncation"`
	Archive      wireArchive       `json:"archive"`
}

type wireTruncation struct {
	PagesProcessed int `json:"pages_processed"`
	PagesTotal     int `json:"pages_total"`
}

type wireArchive struct {
	Attempted   bool    `json:"attempted"`
	Success     bool    `json:"success"`
	ExitCode    *int    `json:"exit_code"`
	Output      *string `json:"output"`
	Error       *string `json:"error"`
	AttemptedAt *string `json:"attempted_at"`
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Write deposits the sidecar beside the archived document.
//
// It goes through the same temp-plus-rename dance as the document itself: a
// half-written sidecar is indistinguishable from a corrupt one on the next run,
// which is survivable but noisy, and there is no reason to accept it.
func Write(store Store, dest string, s Sidecar) error {
	body, err := Marshal(s)
	if err != nil {
		return err
	}
	if err := store.WriteFileAtomic(Name(dest), body); err != nil {
		return fmt.Errorf("writing the sidecar for %s: %w", dest, err)
	}
	return nil
}

// Marshal renders the sidecar exactly as it is written to disk.
func Marshal(s Sidecar) ([]byte, error) {
	out := wire{
		Version:      Version,
		Source:       s.Source,
		SourceDigest: s.SourceDigest,
		Metadata:     s.Metadata,
		Rule:         s.Rule,
		FiledAt:      s.FiledAt.Format(time.RFC3339),
		Archive: wireArchive{
			Attempted: s.Archive.Attempted,
			Success:   s.Archive.Success,
			ExitCode:  s.Archive.ExitCode,
			Output:    nilIfEmpty(s.Archive.Output),
			Error:     nilIfEmpty(s.Archive.Err),
		},
	}
	if t := s.Truncation; t != nil {
		out.Truncation = &wireTruncation{
			PagesProcessed: t.PagesProcessed,
			PagesTotal:     t.PagesTotal,
		}
	}
	if at := s.Archive.AttemptedAt; at != nil {
		formatted := at.Format(time.RFC3339)
		out.Archive.AttemptedAt = &formatted
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// An ampersand in a title stays an ampersand here too: the sidecar is
	// machine-written and machine-read, and & helps nobody reading it.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("encoding the sidecar: %w", err)
	}
	return buf.Bytes(), nil
}

// Read returns the sidecar beside an archived document, if there is a usable
// one.
//
// A sidecar that is missing, unparseable, of an unknown version, or whose
// recorded digest does not match the file it sits beside is reported as absent
// with a reason, and the caller reprocesses the document from the start
// (FR-053). The bytes always win over the sidecar: the file is the document,
// and the sidecar is only a claim about it.
func Read(store Store, dest string, digest string) (Sidecar, bool, error) {
	body, err := store.ReadFile(Name(dest))
	if err != nil {
		// Absent is the ordinary case: most documents have never been filed.
		return Sidecar{}, false, nil //nolint:nilerr // a missing sidecar is not a failure
	}

	var in wire
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return Sidecar{}, false, fmt.Errorf("%s does not parse: %w", Name(dest), err)
	}

	if in.Version != Version {
		return Sidecar{}, false, fmt.Errorf("%s records schema version %d, and this build knows %d",
			Name(dest), in.Version, Version)
	}
	if in.SourceDigest != digest {
		// It belongs to something else — a different document filed under the
		// same name, or a file replaced in place since.
		return Sidecar{}, false, fmt.Errorf("%s records digest %s, and the file beside it hashes to %s",
			Name(dest), short(in.SourceDigest), short(digest))
	}

	filedAt, err := time.Parse(time.RFC3339, in.FiledAt)
	if err != nil {
		return Sidecar{}, false, fmt.Errorf("%s has an unreadable filed_at %q: %w",
			Name(dest), in.FiledAt, err)
	}

	out := Sidecar{
		Version:      in.Version,
		Source:       in.Source,
		SourceDigest: in.SourceDigest,
		Metadata:     in.Metadata,
		Rule:         in.Rule,
		FiledAt:      filedAt,
		Archive: archiver.Result{
			Attempted: in.Archive.Attempted,
			Success:   in.Archive.Success,
			ExitCode:  in.Archive.ExitCode,
		},
	}
	if in.Truncation != nil {
		out.Truncation = &extract.Truncation{
			PagesProcessed: in.Truncation.PagesProcessed,
			PagesTotal:     in.Truncation.PagesTotal,
		}
	}
	if in.Archive.Output != nil {
		out.Archive.Output = *in.Archive.Output
	}
	if in.Archive.Error != nil {
		out.Archive.Err = *in.Archive.Error
	}
	if in.Archive.AttemptedAt != nil {
		if at, err := time.Parse(time.RFC3339, *in.Archive.AttemptedAt); err == nil {
			out.Archive.AttemptedAt = &at
		}
	}
	return out, true, nil
}

// HandOffOutstanding reports whether there is a hand-off still to replay.
//
// This is the whole of FR-052's decision: attempted-and-failed means run only
// the archiver; success means there is nothing left to do at all.
func (s Sidecar) HandOffOutstanding() bool {
	return !s.Archive.Success
}

// short abbreviates a digest for a message, because sixty-four hex characters
// twice over is not something a person reads.
func short(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12] + "…"
}
