package rules

import (
	"fmt"
	"slices"
	"strings"
	"text/template"
)

// Rule is one ordered filing rule: a condition, and the destination directory
// to render when it holds.
//
// A rule may constrain Type (equality), Tags (every listed tag must be present
// — set containment, not equality) and Correspondent (equality). An empty
// constraint is unconstrained; a rule with all three empty is the default rule.
type Rule struct {
	Name          string
	Type          string
	Tags          []string
	Correspondent string

	// PathText is the template as it was written in the configuration, kept so
	// an error can quote what the author actually typed.
	PathText string

	// Path is compiled at configuration load, never at match time, so a
	// malformed template fails before any document has been read.
	Path *template.Template
}

// Subject is what a rule is matched against: the parts of the inferred metadata
// a rule is allowed to condition on, and nothing else.
type Subject struct {
	Type          string
	Correspondent string
	Tags          []string
}

// PathContext is the complete set of values a path template may reference.
//
// There is deliberately no Folder field and no way to name one. Every directory
// level is literal text in the configuration, which is what makes the full set
// of destinations readable by reading the rules — a model mistake can move a
// document between two declared folders, but it can never invent a third.
//
// The date parts are strings rather than ints because a path must render 03 and
// not 3 to sort correctly, and a printf in every template is a trap waiting to
// be forgotten once.
type PathContext struct {
	Type          string
	Correspondent string
	Reference     string
	Title         string
	Year          string
	Month         string
	Day           string
	Tags          []string
}

// Parse compiles a path template with the options every rule needs.
//
// text/template rather than html/template: this is a filesystem path, and HTML
// escaping would corrupt it.
func Parse(name, text string) (*template.Template, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parsing path template for rule %q: %w", name, err)
	}
	return tmpl, nil
}

// IsDefault reports whether the rule carries no condition, and so matches any
// document. There may be at most one such rule; a second would be unreachable.
func (r Rule) IsDefault() bool {
	return r.Type == "" && r.Correspondent == "" && len(r.Tags) == 0
}

// Matches reports whether every condition the rule declares holds for s.
func (r Rule) Matches(s Subject) bool {
	if r.Type != "" && r.Type != s.Type {
		return false
	}
	if r.Correspondent != "" && r.Correspondent != s.Correspondent {
		return false
	}
	for _, want := range r.Tags {
		if !slices.Contains(s.Tags, want) {
			return false
		}
	}
	return true
}

// Match returns the first rule in file order whose conditions hold.
//
// The scan is linear and stops at the first hit: that is the whole of FR-037,
// and it is why the order of the rules in the configuration file is the order
// in which a reader should expect them to be considered.
//
// A false second return means nothing matched and no default rule was declared.
// The caller must treat that as a failure and move nothing.
func Match(rs []Rule, s Subject) (Rule, bool) {
	for _, r := range rs {
		if r.Matches(s) {
			return r, true
		}
	}
	return Rule{}, false
}

// Render produces the destination directory, relative to the archive root.
//
// A reference to a value the document does not carry fails loudly rather than
// collapsing: `factures/{{.Correspondent}}/{{.Year}}` on a document with no
// correspondent is an error, not `factures//2025`.
func (r Rule) Render(ctx PathContext) (string, error) {
	if r.Path == nil {
		return "", fmt.Errorf("rule %q has no compiled path template", r.Name)
	}

	var b strings.Builder
	if err := r.Path.Execute(&b, ctx); err != nil {
		return "", fmt.Errorf("rendering path for rule %q: %w", r.Name, err)
	}

	path := b.String()
	if err := checkSegments(r.Name, path); err != nil {
		return "", err
	}
	return path, nil
}

// checkSegments rejects a rendered path that would resolve to somewhere other
// than the directory the rule names. os.Root is what actually confines a write
// (research.md D10); this is the check that turns a silent collapse into a
// readable error naming the rule.
func checkSegments(rule, path string) error {
	if path == "" {
		return fmt.Errorf("rule %q rendered an empty path", rule)
	}
	if strings.ContainsRune(path, '\\') {
		return fmt.Errorf("rule %q rendered a path containing a backslash: %q", rule, path)
	}
	for seg := range strings.SplitSeq(path, "/") {
		switch seg {
		case "":
			return fmt.Errorf("rule %q rendered an empty path segment in %q: "+
				"a referenced value is missing from the document", rule, path)
		case ".", "..":
			return fmt.Errorf("rule %q rendered the traversal segment %q in %q", rule, seg, path)
		}
	}
	return nil
}
