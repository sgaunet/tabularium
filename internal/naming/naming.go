package naming

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// maxStem caps the sanitised stem at 120 bytes.
//
// The common NAME_MAX is 255 bytes. 120 leaves room for the "YYYY-MM-DD-"
// prefix, the original extension, and a "-2" collision suffix, with margin.
const maxStem = 120

// Name is the archived filename.
//
// FromProposal is false when the model's suggestion sanitised to nothing and
// the original base name was kept instead. It exists so that fallback is
// visible in the output and in a test, rather than being an invisible branch.
type Name struct {
	Base         string
	FromProposal bool
}

// separators collapses every run of characters that is not a lowercase letter
// or a digit. Removing '/', '\\' and '.' is a side effect of that, which is why
// no traversal can survive sanitisation.
var separators = regexp.MustCompile(`[^a-z0-9]+`)

// stripMarks removes the combining marks that NFD decomposition exposes.
//
// NFD → strip Mn → NFC is the correct general answer for Latin scripts: "é"
// decomposes to "e" plus a combining acute, the acute is dropped, and "e"
// remains. A script with no Latin equivalent degrades to nothing, which is
// predictable — and better than mojibake in a filename.
var stripMarks = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// Sanitise turns arbitrary text into something safe to use as a filename.
func Sanitise(s string) string {
	folded, _, err := transform.String(stripMarks, s)
	if err != nil {
		// transform.String on a Chain of these three cannot fail on valid
		// input; on invalid UTF-8 the untransformed string is still safe to
		// feed to the collapse below, which drops everything it does not know.
		folded = s
	}

	out := separators.ReplaceAllString(strings.ToLower(folded), "-")
	out = strings.Trim(out, "-")

	if len(out) > maxStem {
		out = truncateRunes(out, maxStem)
		out = strings.Trim(out, "-")
	}
	return out
}

// truncateRunes cuts to at most n bytes without splitting a rune.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// Derive builds the archived filename.
//
// datePrefix is "YYYY-MM-DD" when the document's own date survived validation,
// and empty otherwise. It arrives as a string, and this package has no access
// to a clock — which is what makes FR-035's "today's date is never
// substituted" a property of the design rather than of a check that could be
// removed. A wrong date in a filename sorts wrong forever and looks
// authoritative.
func Derive(proposal, original, ext, datePrefix string) Name {
	stem := Sanitise(proposal)
	fromProposal := stem != ""

	if stem == "" {
		// FR-033: keep the original base name; never substitute a generated
		// one. The user can still recognise the file they scanned.
		stem = Sanitise(original)
	}

	base := stem
	if datePrefix != "" {
		switch {
		case stem == "":
			// Nothing survived from either name. The date alone still yields
			// something openable and sortable.
			base = datePrefix
		case strings.HasPrefix(stem, datePrefix):
			// The proposal already carries the date; a second copy helps nobody.
			base = stem
		default:
			base = datePrefix + "-" + stem
		}
	}

	if base == "" {
		base = "document"
	}
	return Name{Base: base + ext, FromProposal: fromProposal}
}
