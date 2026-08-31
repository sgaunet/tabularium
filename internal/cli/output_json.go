package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/sgaunet/tabularium/internal/analysis"
	"github.com/sgaunet/tabularium/internal/extract"
)

// outputVersion is the schema version of --output=json. It is incremented only
// on a breaking change, because Principle II makes this shape public and
// breaking it breaks callers.
const outputVersion = 1

// jsonOutput is contracts/output.schema.json, field for field.
//
// Every optional member is a pointer or an explicit nullable rather than being
// omitted, because a consumer must be able to tell "no truncation" from "the
// tool forgot to say".
type jsonOutput struct {
	Version     int                `json:"version"`
	Source      jsonSource         `json:"source"`
	Action      Action             `json:"action"`
	Destination *string            `json:"destination"`
	Rule        *string            `json:"rule"`
	Disposition *string            `json:"disposition"`
	Text        *jsonText          `json:"text"`
	Truncation  *jsonTruncation    `json:"truncation"`
	Metadata    *analysis.Metadata `json:"metadata"`
	Archive     *jsonArchive       `json:"archive"`
}

type jsonSource struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type jsonText struct {
	Origin  extract.Origin `json:"origin"`
	Content *string        `json:"content"`
}

type jsonTruncation struct {
	PagesProcessed int `json:"pages_processed"`
	PagesTotal     int `json:"pages_total"`
}

type jsonArchive struct {
	Attempted bool    `json:"attempted"`
	Success   bool    `json:"success"`
	ExitCode  *int    `json:"exit_code"`
	Output    *string `json:"output"`
	Error     *string `json:"error"`
}

// writeJSON writes the single object of the output contract, and nothing else.
//
// SetEscapeHTML(false) so an ampersand in a title stays an ampersand rather
// than becoming & — this is a document title, not a web page.
func writeJSON(w io.Writer, r result) error {
	out := jsonOutput{
		Version: outputVersion,
		Source: jsonSource{
			Path:   r.Plan.Source.Path,
			Type:   string(r.Plan.Source.Type),
			Size:   r.Plan.Source.Size,
			Digest: r.Plan.Source.HexDigest(),
		},
		Action:   r.Action,
		Metadata: r.metadata(),
	}

	if dest := r.destination(); dest != "" {
		out.Destination = &dest
	}
	if name := r.Plan.Rule.Name; name != "" {
		out.Rule = &name
	}
	if d := string(r.Plan.Disposition); d != "" && r.Action != ActionExtracted {
		out.Disposition = &d
	}

	if text := r.text(); text != nil {
		out.Text = &jsonText{Origin: text.Origin}
		if r.IncludeText {
			content := text.Content
			out.Text.Content = &content
		}
		if t := text.Truncation; t != nil {
			out.Truncation = &jsonTruncation{
				PagesProcessed: t.PagesProcessed,
				PagesTotal:     t.PagesTotal,
			}
		}
	}

	if a := r.Archive; a != nil && a.Attempted {
		entry := &jsonArchive{Attempted: a.Attempted, Success: a.Success, ExitCode: a.ExitCode}
		if a.Output != "" {
			output := a.Output
			entry.Output = &output
		}
		if a.Err != "" {
			msg := a.Err
			entry.Error = &msg
		}
		out.Archive = entry
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("writing the JSON output: %w", err)
	}
	return nil
}
