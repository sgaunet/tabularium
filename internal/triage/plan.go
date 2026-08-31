package triage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/sgaunet/tabularium/internal/analysis"
	"github.com/sgaunet/tabularium/internal/config"
	"github.com/sgaunet/tabularium/internal/document"
	"github.com/sgaunet/tabularium/internal/extract"
	"github.com/sgaunet/tabularium/internal/naming"
	"github.com/sgaunet/tabularium/internal/rules"
)

// Disposition is what becomes of the source once it has been filed.
type Disposition string

// What may become of the source (FR-042).
const (
	// Move — the default: the source is unlinked once the copy is verified.
	Move Disposition = "move"
	// Copy — the source stays where it is.
	Copy Disposition = "copy"
	// Keep — nothing is written at the source end at all.
	Keep Disposition = "keep"
)

// Plan is the complete result of computation, before a single byte is written.
//
// This type is the load-bearing invariant of the whole design: building a Plan
// cannot write, so --dry-run cannot write either. FR-043 and SC-009 are
// therefore structural rather than a flag check inside the write path that
// somebody could one day forget.
type Plan struct {
	Source      document.Source
	Text        extract.Text
	Metadata    analysis.Metadata
	Name        naming.Name
	Rule        rules.Rule
	Disposition Disposition

	// Dest is relative to the archive root, never absolute, because that is
	// what the (*os.Root) operations take. Storing it absolute would invite
	// exactly the string concatenation os.Root exists to eliminate.
	Dest string
}

// NoRuleError is a document that matched no rule where no default rule was
// declared.
//
// Nothing moves. Inventing a destination would put the document somewhere the
// user never named, which is the one outcome the rules design exists to prevent
// (FR-040).
type NoRuleError struct {
	Path string
	Type string
	Tags []string
}

func (e *NoRuleError) Error() string {
	return fmt.Sprintf("%s: no rule matches a %q document tagged [%s], and no default "+
		"rule is configured: it has not been moved", e.Path, e.Type, strings.Join(e.Tags, ", "))
}

// Deps are the collaborators the pipeline needs. They are passed in rather than
// constructed here so a test can substitute an endpoint without a network.
type Deps struct {
	Config   *config.Config
	Extract  extract.Options
	Analyser *analysis.Analyser
	Log      *slog.Logger
}

func (d Deps) logger() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Options are the per-run choices the command line makes.
type Options struct {
	Disposition Disposition

	// NoAnalysis stops after extraction. Without metadata no rule can match and
	// no name can be computed, so it implies no filing and no archiving.
	NoAnalysis bool
}

// Build computes the plan: sniff, extract, analyse, name, match.
//
// It writes nothing. Every failure here leaves the filesystem exactly as it
// was, which is what a caller relies on when it stops at --dry-run.
func Build(ctx context.Context, docPath string, deps Deps, o Options) (Plan, error) {
	src, err := document.Open(docPath)
	if err != nil {
		return Plan{}, err
	}
	deps.logger().Debug("identified the document",
		"path", src.Path, "type", src.Type, "size", src.Size)

	text, err := extract.Extract(ctx, src, deps.Extract)
	if err != nil {
		return Plan{}, err
	}
	deps.logger().Debug("extracted text",
		"path", src.Path, "origin", text.Origin, "runes", len([]rune(text.Content)))
	if text.Truncation != nil {
		// FR-008: never silent. The user has to be able to tell that the
		// metadata was inferred from part of the document.
		deps.logger().Warn("the document was truncated",
			"path", src.Path,
			"pages_processed", text.Truncation.PagesProcessed,
			"pages_total", text.Truncation.PagesTotal)
	}

	plan := Plan{Source: src, Text: text, Disposition: o.Disposition}
	if o.NoAnalysis {
		return plan, nil
	}

	if deps.Analyser == nil {
		return Plan{}, errors.New("no analysis endpoint is configured: " +
			"set analysis.base_url and analysis.model")
	}
	md, err := deps.Analyser.Analyse(ctx, text.Content)
	if err != nil {
		return Plan{}, err
	}
	plan.Metadata = md
	deps.logger().Debug("inferred metadata",
		"path", src.Path, "type", md.Type, "tags", md.Tags, "date", md.DatePrefix())

	plan.Name = naming.Derive(md.Filename, baseWithoutExt(src), src.Ext, md.DatePrefix())

	rule, ok := rules.Match(deps.Config.RuleSet(), rules.Subject{
		Type:          md.Type,
		Correspondent: md.Correspondent,
		Tags:          md.Tags,
	})
	if !ok {
		return Plan{}, &NoRuleError{Path: src.Path, Type: md.Type, Tags: md.Tags}
	}
	plan.Rule = rule

	dir, err := rule.Render(pathContext(md))
	if err != nil {
		return Plan{}, err
	}
	plan.Dest = joinDest(dir, plan.Name.Base)

	deps.logger().Debug("planned a destination",
		"path", src.Path, "rule", rule.Name, "dest", plan.Dest)
	return plan, nil
}

// pathContext is the bridge between the inferred metadata and what a rule's
// template may see. It is a narrowing in two senses: the template gets these
// fields and nothing else, and every one of them is sanitised on the way in.
//
// The sanitising is what makes SC-014 hold. Without it a correspondent of
// "Garage / Central" would render an extra directory level that appears in no
// rule — a folder the model invented rather than one the user declared. After
// it, a value that would have introduced a separator collapses to a hyphen, and
// a value that was nothing but separators collapses to nothing and fails the
// empty-segment check loudly.
//
// os.Root still confines the write either way; this is about the destination
// staying readable in the configuration, not about escaping the root.
func pathContext(md analysis.Metadata) rules.PathContext {
	ctx := rules.PathContext{
		Type:          naming.Sanitise(md.Type),
		Correspondent: naming.Sanitise(md.Correspondent),
		Reference:     naming.Sanitise(md.Reference),
		Title:         naming.Sanitise(md.Title),
		Tags:          sanitiseAll(md.Tags),
	}
	if md.DocumentDate != nil {
		// Already digits and hyphens; sanitising them would be theatre.
		ctx.Year, ctx.Month, ctx.Day = md.DocumentDate.Parts()
	}
	return ctx
}

func sanitiseAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, naming.Sanitise(s))
	}
	return out
}

// baseWithoutExt is the source's own name, which is what naming falls back to
// when the model's proposal sanitises to nothing.
func baseWithoutExt(src document.Source) string {
	base := filepath.Base(src.Path)
	return strings.TrimSuffix(base, src.Ext)
}

// joinDest joins with a forward slash on every platform. Dest is a path inside
// the archive, resolved by (*os.Root) rather than by the local filesystem's
// separator conventions, and keeping it slash-separated is what lets the same
// destination string appear unchanged in the JSON output and the sidecar.
func joinDest(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}
