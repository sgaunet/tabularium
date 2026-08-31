package archiver

import "github.com/sgaunet/tabularium/internal/analysis"

// Subject is one document, as the archiver's templates see it.
type Subject struct {
	// Path is the archived file, absolute. It is what the external system is
	// being asked to take.
	Path     string
	Metadata analysis.Metadata
}

// templateContext is exactly what an argv template may reference — the eleven
// fields of contracts/cli.md and nothing else.
//
// Dates and the amount are rendered as strings rather than left as pointers,
// because a template that reaches a nil pointer prints "<nil>" into an argument
// and the external command receives the word.
type templateContext struct {
	Path          string
	Title         string
	Type          string
	Correspondent string
	Reference     string
	Description   string
	Amount        string
	Currency      string
	Tags          []string
	DocumentDate  string
	DueDate       string
}

func (s Subject) context() templateContext {
	md := s.Metadata
	ctx := templateContext{
		Path:          s.Path,
		Title:         md.Title,
		Type:          md.Type,
		Correspondent: md.Correspondent,
		Reference:     md.Reference,
		Description:   md.Description,
		Currency:      md.Currency,
		Tags:          md.Tags,
	}
	if md.Amount != nil {
		ctx.Amount = md.Amount.String()
	}
	if md.DocumentDate != nil {
		ctx.DocumentDate = md.DocumentDate.String()
	}
	if md.DueDate != nil {
		ctx.DueDate = md.DueDate.String()
	}
	return ctx
}
