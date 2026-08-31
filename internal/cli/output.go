package cli

import (
	"github.com/sgaunet/tabularium/internal/analysis"
	"github.com/sgaunet/tabularium/internal/archiver"
	"github.com/sgaunet/tabularium/internal/extract"
	"github.com/sgaunet/tabularium/internal/triage"
)

// Action is what became of the document. It is the `action` field of
// contracts/output.schema.json, and its values are part of the public contract.
type Action string

const (
	// ActionFiled — written to the destination.
	ActionFiled Action = "filed"
	// ActionDuplicate — identical content was already there, nothing written,
	// exit 0 (FR-049).
	ActionDuplicate Action = "duplicate"
	// ActionPlanned — --dry-run, nothing written (FR-043).
	ActionPlanned Action = "planned"
	// ActionExtracted — --no-analysis, text only (FR-019).
	ActionExtracted Action = "extracted"
	// ActionNotFiled — filing disabled by flag (FR-062).
	ActionNotFiled Action = "not-filed"
)

// result is everything the two formatters need. It is assembled once, from the
// plan and the outcome, so the text and JSON renderings cannot disagree about
// what happened.
type result struct {
	Plan    triage.Plan
	Action  Action
	Archive *archiver.Result

	// IncludeText is set for --no-analysis, where the extracted text is the
	// whole of the answer. It is omitted otherwise to keep the common output
	// small.
	IncludeText bool
}

func (r result) destination() string {
	switch r.Action {
	case ActionFiled, ActionDuplicate, ActionPlanned:
		return r.Plan.Dest
	case ActionExtracted, ActionNotFiled:
		return ""
	default:
		return ""
	}
}

func (r result) hasMetadata() bool {
	return r.Action != ActionExtracted && r.Plan.Metadata.Type != ""
}

func (r result) text() *extract.Text {
	if r.Plan.Text.Origin == "" {
		return nil
	}
	t := r.Plan.Text
	return &t
}

func (r result) metadata() *analysis.Metadata {
	if !r.hasMetadata() {
		return nil
	}
	md := r.Plan.Metadata
	return &md
}
