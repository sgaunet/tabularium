package chat

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Schema is a response_format.json_schema payload.
//
// Body is left as a generic structure rather than a Go type, because it is
// transmitted verbatim and never decoded here: the tool re-validates what comes
// back with a hand-written checker in internal/analysis, deliberately not with
// the code that built the request (FR-025).
type Schema struct {
	Name   string `json:"name"`
	Strict bool   `json:"strict"`
	Body   any    `json:"schema"`
}

// SchemaUnsupportedError says the endpoint will not honour schema-constrained
// output.
//
// It is a configuration problem, not a runtime fault: it will fail identically
// on every retry, and the fix is to point the tool at a different endpoint or
// model. The caller maps it to exit 2, and it names both so the user knows what
// to change.
type SchemaUnsupportedError struct {
	BaseURL string
	Model   string
	Reason  string
}

func (e *SchemaUnsupportedError) Error() string {
	return fmt.Sprintf("the endpoint %s does not honour schema-constrained output "+
		"for model %q (%s): the analysis model must support "+
		"response_format.json_schema", e.BaseURL, e.Model, e.Reason)
}

// schemaRefusal decides whether a failed response means the endpoint rejects
// the constraint outright, rather than merely having had a bad day.
//
// Two of research.md D7's three signals are visible here and unambiguous on the
// first response. The third — a 2xx whose content is prose on every attempt —
// is only distinguishable from an ordinary bad generation once the retries are
// exhausted, so internal/analysis raises that one.
func schemaRefusal(status int, err error) (string, bool) {
	switch status {
	case http.StatusNotFound, http.StatusNotImplemented:
		return fmt.Sprintf("HTTP %d on /chat/completions", status), true
	case http.StatusBadRequest:
		var httpErr *httpError
		if !errors.As(err, &httpErr) {
			return "", false
		}
		body := strings.ToLower(httpErr.Body)
		for _, marker := range []string{"json_schema", "response_format"} {
			if strings.Contains(body, marker) {
				return "the endpoint rejected the request naming " + marker, true
			}
		}
	}
	return "", false
}

// DocumentMetadataSchema builds the schema of contracts/analysis.schema.json
// with the configured type vocabulary substituted into the `type` enum.
//
// Every property is listed in `required` and additionalProperties is false,
// because OpenAI-compatible strict mode demands both. An optional field is
// therefore expressed as a nullable type rather than by omission — "the
// document bears no date" is `null`, not a missing key.
//
// No property names a folder, and there is no way to add one: that is FR-016,
// and it is why a model mistake can only move a document between two declared
// folders and never invent a third.
func DocumentMetadataSchema(types []string) Schema {
	enum := slices.Clone(types)
	if len(enum) == 0 {
		enum = []string{}
	}

	nullableString := func(description string) map[string]any {
		return map[string]any{
			"type":        []string{"string", "null"},
			"description": description,
		}
	}
	nullableDate := func(description string) map[string]any {
		return map[string]any{
			"type":        []string{"string", "null"},
			"description": description,
			"pattern":     `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`,
		}
	}

	properties := map[string]any{
		"title": map[string]any{
			"type":        "string",
			"description": "A short human-readable title for the document, in the document's own language.",
		},
		"type": map[string]any{
			"type": "string",
			"description": "The kind of document. Choose the single closest value. " +
				"If none clearly applies, choose the fallback value.",
			"enum": enum,
		},
		"correspondent": nullableString(
			"The organisation or person the document is from. Null if not determinable."),
		"tags": map[string]any{
			"type":        "array",
			"description": "Lowercase keywords describing the document's subject. Never a folder or a path.",
			"items":       map[string]any{"type": "string"},
		},
		"document_date": nullableDate(
			"The date the document itself bears, as YYYY-MM-DD. Null if the document " +
				"bears no date. Never today's date."),
		"due_date": nullableDate(
			"A payment or response deadline, as YYYY-MM-DD. Null if none."),
		"reference": nullableString(
			"An invoice number, contract reference, or customer number. Null if none."),
		"description": nullableString(
			"One sentence on what the document is about. Null if not determinable."),
		"amount": map[string]any{
			"type": []string{"string", "null"},
			"description": "The principal monetary amount as a decimal string, e.g. \"1234.56\". " +
				"Null if the document states none. Must be accompanied by currency.",
			"pattern": `^-?[0-9]+(\.[0-9]+)?$`,
		},
		"currency": map[string]any{
			"type":        []string{"string", "null"},
			"description": "ISO 4217 code for the amount, e.g. \"EUR\". Null if the document states no amount.",
			"pattern":     `^[A-Z]{3}$`,
		},
		"filename": map[string]any{
			"type": "string",
			"description": "A proposed base filename describing the document, without extension, " +
				"without any date prefix, and without any path separator. " +
				"Lowercase words joined by hyphens.",
		},
	}

	required := make([]string, 0, len(properties))
	for name := range properties {
		required = append(required, name)
	}
	slices.Sort(required)

	return Schema{
		Name:   "document_metadata",
		Strict: true,
		Body: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             required,
			"properties":           properties,
		},
	}
}
