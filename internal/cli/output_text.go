package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// writeText writes the aligned, borderless table of FR-065.
//
// Borderless because the output is meant to be readable by a person and cuttable
// by awk, and box-drawing characters serve neither.
func writeText(w io.Writer, r result) error {
	if r.Action == ActionExtracted {
		// --no-analysis: the extracted text is the whole of the answer, and
		// wrapping it in a table would only get in the way of piping it.
		_, err := io.WriteString(w, r.Plan.Text.Content+"\n")
		if err != nil {
			return fmt.Errorf("writing the extracted text: %w", err)
		}
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SOURCE\tDEST\tTYPE\tTAGS")
	fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
		filepath.Base(r.Plan.Source.Path),
		orDash(r.destination()),
		orDash(r.Plan.Metadata.Type),
		orDash(strings.Join(r.Plan.Metadata.Tags, ", ")),
	)
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("writing the output table: %w", err)
	}

	if note := archiveNote(r); note != "" {
		if _, err := fmt.Fprintf(w, "\n%s\n", note); err != nil {
			return fmt.Errorf("writing the archive note: %w", err)
		}
	}

	if r.Action == ActionPlanned {
		return writePlanDetail(w, r)
	}
	return nil
}

// archiveNote is what the text output says about the external hand-off.
//
// It appears only when the step actually ran: a user with no archiver
// configured should not see a line about one on every document.
func archiveNote(r result) string {
	a := r.Archive
	if a == nil || !a.Attempted {
		return ""
	}
	if a.Success {
		return "archived"
	}
	if a.ExitCode != nil {
		return fmt.Sprintf("archiver failed (exit %d)", *a.ExitCode)
	}
	return "archiver failed"
}

// writePlanDetail is the extra block --dry-run prints.
//
// Seeing the plan before anything moves is the entire point of the mode, and
// the rule that produced the destination is the part a user actually needs in
// order to correct it (FR-043).
func writePlanDetail(w io.Writer, r result) error {
	md := r.Plan.Metadata

	rows := [][2]string{
		{"rule", r.Plan.Rule.Name},
		{"rule path", r.Plan.Rule.PathText},
		{"disposition", string(r.Plan.Disposition)},
		{"text origin", string(r.Plan.Text.Origin)},
		{"title", md.Title},
		{"correspondent", md.Correspondent},
		{"document date", md.DatePrefix()},
		{"reference", md.Reference},
		{"description", md.Description},
	}
	if md.DueDate != nil {
		rows = append(rows, [2]string{"due date", md.DueDate.String()})
	}
	if md.Amount != nil {
		rows = append(rows, [2]string{"amount", md.Amount.String() + " " + md.Currency})
	}
	if !r.Plan.Name.FromProposal {
		rows = append(rows, [2]string{"filename", "kept from the source: the proposal sanitised to nothing"})
	}
	if t := r.Plan.Text.Truncation; t != nil {
		rows = append(rows, [2]string{"truncated",
			fmt.Sprintf("%d of %d pages processed", t.PagesProcessed, t.PagesTotal)})
	}
	if note := archiveNote(r); note != "" {
		rows = append(rows, [2]string{"archive", note})
	}

	if _, err := io.WriteString(w, "\n"); err != nil {
		return fmt.Errorf("writing the plan detail: %w", err)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range rows {
		if row[1] == "" {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\n", row[0], row[1])
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("writing the plan detail: %w", err)
	}
	return nil
}

// orDash renders an absent value as a dash, so a column never collapses and the
// table stays alignable.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
