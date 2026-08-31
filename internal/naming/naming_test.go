package naming_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sgaunet/tabularium/internal/naming"
)

func TestSanitise(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "accents are transliterated, not dropped",
			in:   "Réf. n°42 — Facture",
			want: "ref-n-42-facture",
		},
		{
			name: "every French accent survives as its base letter",
			in:   "àâäéèêëîïôöùûüÿçÀÂÄÉÈÊËÎÏÔÖÙÛÜŸÇ",
			want: "aaaeeeeiioouuuycaaaeeeeiioouuuyc",
		},
		{
			name: "runs of punctuation collapse to a single hyphen",
			in:   "facture___2025///garage",
			want: "facture-2025-garage",
		},
		{
			name: "leading and trailing separators are trimmed",
			in:   "  ---facture---  ",
			want: "facture",
		},
		{
			name: "case is folded",
			in:   "FACTURE Garage Central",
			want: "facture-garage-central",
		},
		{
			name: "digits survive",
			in:   "FA-2025-0312",
			want: "fa-2025-0312",
		},
		{
			name: "a script with no Latin equivalent degrades to nothing rather than to mojibake",
			in:   "日本語",
			want: "",
		},
		{
			name: "a mixed string keeps the part that transliterates",
			in:   "facture 日本語 2025",
			want: "facture-2025",
		},
		{
			name: "an empty string stays empty",
			in:   "",
			want: "",
		},
		{
			name: "punctuation alone sanitises to nothing",
			in:   "...///___",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := naming.Sanitise(tt.in); got != tt.want {
				t.Errorf("Sanitise(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitiseDefeatsPathTraversal(t *testing.T) {
	// FR-033 and FR-041. This is defence in depth, not the primary control —
	// os.Root is what actually confines a write — but the [^a-z0-9]+ collapse
	// removes /, \ and . as a side effect, so none of these can survive as a
	// name at all.
	hostile := []string{
		"../../etc/passwd",
		"..",
		".",
		"/etc/shadow",
		`..\..\windows\system32`,
		"....//....//etc",
		"~/.ssh/id_rsa",
		"a/../../b",
	}

	for _, in := range hostile {
		t.Run(in, func(t *testing.T) {
			got := naming.Sanitise(in)
			for _, forbidden := range []string{"/", `\`, ".."} {
				if strings.Contains(got, forbidden) {
					t.Errorf("Sanitise(%q) = %q, which still contains %q", in, got, forbidden)
				}
			}
			if got == "." || got == ".." {
				t.Errorf("Sanitise(%q) = %q, which names a directory", in, got)
			}
		})
	}
}

func TestSanitiseCapsAt120BytesOnARuneBoundary(t *testing.T) {
	// D8: 120 bytes leaves room under the common 255-byte NAME_MAX for the date
	// prefix, the extension, and a -2 collision suffix.
	long := strings.Repeat("facture-garage-central-", 30)

	got := naming.Sanitise(long)
	if len(got) > 120 {
		t.Errorf("Sanitise() returned %d bytes, want at most 120", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("Sanitise() cut a rune in half: %q", got)
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("Sanitise() = %q; a truncation must not leave a trailing separator", got)
	}
}

func TestSanitiseTruncatesMultibyteTextWithoutSplittingARune(t *testing.T) {
	// The cap is in bytes and the input is not. Cutting mid-rune would produce
	// a filename the shell cannot round-trip.
	long := strings.Repeat("é", 200)

	got := naming.Sanitise(long)
	if len(got) > 120 {
		t.Errorf("Sanitise() returned %d bytes, want at most 120", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("Sanitise() produced invalid UTF-8: %q", got)
	}
}

func TestDerive(t *testing.T) {
	tests := []struct {
		name         string
		proposal     string
		original     string
		ext          string
		datePrefix   string
		want         string
		fromProposal bool
	}{
		{
			name:     "the model's proposal, sanitised, with the document's date in front",
			proposal: "Facture Garage Central", original: "scan001", ext: ".pdf",
			datePrefix: "2025-03-14",
			want:       "2025-03-14-facture-garage-central.pdf", fromProposal: true,
		},
		{
			name:     "no date means no prefix — today's date is never substituted",
			proposal: "facture-voiture", original: "scan001", ext: ".pdf",
			datePrefix: "",
			want:       "facture-voiture.pdf", fromProposal: true,
		},
		{
			name:     "a proposal that sanitises to nothing keeps the original base name",
			proposal: "///", original: "IMG_4821", ext: ".jpg",
			datePrefix: "2025-03-14",
			want:       "2025-03-14-img-4821.jpg", fromProposal: false,
		},
		{
			name:     "an empty proposal keeps the original base name",
			proposal: "", original: "scan001", ext: ".pdf",
			datePrefix: "",
			want:       "scan001.pdf", fromProposal: false,
		},
		{
			name:     "the original extension is preserved verbatim, whatever the type",
			proposal: "releve", original: "x", ext: ".JPEG",
			datePrefix: "",
			want:       "releve.JPEG", fromProposal: true,
		},
		{
			name:     "a document with no extension gets none",
			proposal: "releve", original: "x", ext: "",
			datePrefix: "",
			want:       "releve", fromProposal: true,
		},
		{
			name:     "a proposal that already carries a date does not get a second one",
			proposal: "2025-03-14-facture", original: "x", ext: ".pdf",
			datePrefix: "2025-03-14",
			want:       "2025-03-14-facture.pdf", fromProposal: true,
		},
		{
			name:     "a hostile proposal cannot escape",
			proposal: "../../etc/passwd", original: "scan", ext: ".pdf",
			datePrefix: "",
			want:       "etc-passwd.pdf", fromProposal: true,
		},
		{
			name:     "an original base name that sanitises to nothing still yields a name",
			proposal: "", original: "日本語", ext: ".pdf",
			datePrefix: "2025-01-02",
			want:       "2025-01-02.pdf", fromProposal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := naming.Derive(tt.proposal, tt.original, tt.ext, tt.datePrefix)
			if got.Base != tt.want {
				t.Errorf("Derive() base = %q, want %q", got.Base, tt.want)
			}
			if got.FromProposal != tt.fromProposal {
				t.Errorf("Derive() FromProposal = %v, want %v", got.FromProposal, tt.fromProposal)
			}
		})
	}
}

func TestDeriveNeverReturnsAnEmptyName(t *testing.T) {
	// Every branch has to produce something openable. A name that sanitises to
	// nothing on both the proposal and the original is the case that would
	// otherwise write a file called ".pdf".
	got := naming.Derive("///", "日本語", ".pdf", "")
	if got.Base == "" || got.Base == ".pdf" {
		t.Errorf("Derive() = %q; want a name with a stem", got.Base)
	}
}

func TestDeriveIsPure(t *testing.T) {
	// naming is given the date prefix as a string and has no access to a clock,
	// which is what makes "today's date is never substituted" (FR-035) a
	// property of the design rather than of a check somebody could remove.
	first := naming.Derive("facture", "scan", ".pdf", "")
	for range 100 {
		if got := naming.Derive("facture", "scan", ".pdf", ""); got != first {
			t.Fatalf("Derive() drifted: %+v, want %+v", got, first)
		}
	}
}
