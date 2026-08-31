package sidecar_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/archiver"
	"github.com/sgaunet/tabularium/internal/sidecar"
)

// written puts a sidecar in the store and returns the digest it records.
func written(t *testing.T, s *store, dest string, sc sidecar.Sidecar) string {
	t.Helper()
	if err := sidecar.Write(s, dest, sc); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return sc.SourceDigest
}

func TestReadHonoursASidecarWhoseDigestMatches(t *testing.T) {
	s := newStore()
	digest := written(t, s, "a/doc.pdf", full(t))

	got, ok, err := sidecar.Read(s, "a/doc.pdf", digest)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !ok {
		t.Fatal("Read() reported the sidecar as absent")
	}

	if got.Rule != "factures-voiture" {
		t.Errorf("Rule = %q", got.Rule)
	}
	if got.Metadata.Type != "facture" {
		t.Errorf("Metadata.Type = %q", got.Metadata.Type)
	}
	if got.Metadata.DocumentDate == nil || got.Metadata.DocumentDate.String() != "2025-03-14" {
		t.Errorf("Metadata.DocumentDate = %v", got.Metadata.DocumentDate)
	}
	if !got.Archive.Success {
		t.Error("Archive.Success = false; the fixture records a success")
	}
	if got.FiledAt.IsZero() {
		t.Error("FiledAt is zero")
	}
}

func TestReadTreatsAnUnusableSidecarAsAbsent(t *testing.T) {
	// FR-053: unparseable, an unknown version, and a digest mismatch are all
	// "absent" — the anomaly is reported, and the caller reprocesses from the
	// start. The bytes always win over the sidecar.
	valid := full(t)

	tests := []struct {
		name     string
		body     string
		digest   string
		wantSays string
	}{
		{
			name:     "not JSON at all",
			body:     "this is not a sidecar",
			digest:   valid.SourceDigest,
			wantSays: "parse",
		},
		{
			name:     "truncated JSON, as a killed run would leave",
			body:     `{"version": 1, "source": "/a/b.pdf",`,
			digest:   valid.SourceDigest,
			wantSays: "parse",
		},
		{
			name:     "a version this build does not know",
			body:     `{"version": 99, "source": "", "source_digest": "` + valid.SourceDigest + `", "metadata": {"title":"t","type":"x","tags":[]}, "rule": "r", "filed_at": "2025-03-16T09:41:12Z", "truncation": null, "archive": {"attempted": false, "success": false}}`,
			digest:   valid.SourceDigest,
			wantSays: "version",
		},
		{
			name:     "a field this build does not know",
			body:     `{"version": 1, "invented": true}`,
			digest:   valid.SourceDigest,
			wantSays: "parse",
		},
		{
			name:     "an unreadable filed_at",
			body:     `{"version": 1, "source": "", "source_digest": "` + valid.SourceDigest + `", "metadata": {"title":"t","type":"x","tags":[]}, "rule": "r", "filed_at": "last Tuesday", "truncation": null, "archive": {"attempted": false, "success": false}}`,
			digest:   valid.SourceDigest,
			wantSays: "filed_at",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore()
			s.files["a/doc.pdf"+sidecar.Suffix] = []byte(tt.body)

			got, ok, err := sidecar.Read(s, "a/doc.pdf", tt.digest)
			if ok {
				t.Fatalf("Read() honoured an unusable sidecar: %+v", got)
			}
			if err == nil {
				t.Fatal("Read() reported no reason; the anomaly must be loggable")
			}
			if !strings.Contains(err.Error(), tt.wantSays) {
				t.Errorf("Read() error = %v; want it to mention %q", err, tt.wantSays)
			}
		})
	}
}

func TestADigestMismatchMeansTheSidecarBelongsToSomethingElse(t *testing.T) {
	// The failure being prevented: a different document filed under the same
	// name, whose sidecar would otherwise be taken as describing this one.
	s := newStore()
	written(t, s, "a/doc.pdf", full(t))

	other := strings.Repeat("ab", 32)
	got, ok, err := sidecar.Read(s, "a/doc.pdf", other)
	if ok {
		t.Fatalf("Read() honoured a sidecar for different bytes: %+v", got)
	}
	if err == nil {
		t.Fatal("Read() reported no reason")
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Errorf("Read() error = %v; want it to say the digest did not match", err)
	}
}

func TestAMissingSidecarIsAbsentWithoutBeingAnAnomaly(t *testing.T) {
	// Most documents have never been filed. That is the ordinary case, and it
	// must not put a warning on stderr for every one of them.
	s := newStore()

	got, ok, err := sidecar.Read(s, "a/doc.pdf", strings.Repeat("00", 32))
	if ok {
		t.Fatalf("Read() found a sidecar that was never written: %+v", got)
	}
	if err != nil {
		t.Errorf("Read() = %v for a document that was simply never filed", err)
	}
}

func TestHandOffOutstanding(t *testing.T) {
	// FR-052's whole decision, in one table.
	code := 0
	tests := []struct {
		name string
		in   archiver.Result
		want bool
	}{
		{
			name: "never attempted: there is a hand-off to make",
			in:   archiver.Result{},
			want: true,
		},
		{
			name: "attempted and failed: replay it",
			in:   archiver.Result{Attempted: true, Success: false},
			want: true,
		},
		{
			name: "attempted and succeeded: nothing left to do",
			in:   archiver.Result{Attempted: true, Success: true, ExitCode: &code},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := sidecar.Sidecar{Archive: tt.in}
			if got := sc.HandOffOutstanding(); got != tt.want {
				t.Errorf("HandOffOutstanding() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestASidecarRoundTrips(t *testing.T) {
	// Written and read back by the same type. A shape that does not round-trip
	// would make every re-run reprocess from the start, which is exactly the
	// work FR-052 exists to avoid.
	original := full(t)
	original.Archive = archiver.Result{
		Attempted: true, Success: false,
		Err:         "connection refused",
		AttemptedAt: ptr(time.Date(2025, 3, 16, 9, 41, 13, 0, time.UTC)),
	}

	s := newStore()
	if err := sidecar.Write(s, "a/doc.pdf", original); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, ok, err := sidecar.Read(s, "a/doc.pdf", original.SourceDigest)
	if err != nil || !ok {
		t.Fatalf("Read: %v (ok=%v)", err, ok)
	}

	if got.Source != original.Source {
		t.Errorf("Source = %q, want %q", got.Source, original.Source)
	}
	if got.Rule != original.Rule {
		t.Errorf("Rule = %q, want %q", got.Rule, original.Rule)
	}
	if !got.FiledAt.Equal(original.FiledAt) {
		t.Errorf("FiledAt = %v, want %v", got.FiledAt, original.FiledAt)
	}
	if got.Metadata.Title != original.Metadata.Title {
		t.Errorf("Metadata.Title = %q, want %q", got.Metadata.Title, original.Metadata.Title)
	}
	if got.Archive.Err != "connection refused" {
		t.Errorf("Archive.Err = %q", got.Archive.Err)
	}
	if got.Archive.AttemptedAt == nil || !got.Archive.AttemptedAt.Equal(*original.Archive.AttemptedAt) {
		t.Errorf("Archive.AttemptedAt = %v, want %v", got.Archive.AttemptedAt, original.Archive.AttemptedAt)
	}
	if !got.HandOffOutstanding() {
		t.Error("HandOffOutstanding() = false for a recorded failure")
	}
}

func ptr[T any](v T) *T { return &v }
