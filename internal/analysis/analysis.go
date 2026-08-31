package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/config"
)

// Vocabularies are the closed sets the response is checked against.
type Vocabularies struct {
	Types config.Vocabulary
	Tags  config.Vocabulary
}

// MalformedError is a response that did not conform to the schema's *shape*:
// prose, a fenced code block, an unknown field, a missing required field.
//
// It is kept distinct from a semantic rejection because the two mean different
// things about the endpoint. A response of the wrong shape means the endpoint
// ignored response_format; a response of the right shape carrying a type
// outside the vocabulary means the endpoint honoured the constraint and the
// model simply chose badly. The first is a configuration problem (exit 2), the
// second a runtime one (exit 1).
type MalformedError struct {
	Reason  string
	Snippet string
}

func (e *MalformedError) Error() string {
	if e.Snippet == "" {
		return "the response does not conform to the schema: " + e.Reason
	}
	return fmt.Sprintf("the response does not conform to the schema: %s (got %q)",
		e.Reason, e.Snippet)
}

// RejectedError is a well-shaped response carrying a value the configuration
// does not allow.
type RejectedError struct {
	Field  string
	Value  string
	Reason string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("%s %q is rejected: %s", e.Field, e.Value, e.Reason)
}

// wireIn mirrors contracts/analysis.schema.json.
//
// Every field is a pointer so that "absent" stays distinct from "empty". Plain
// zero values would conflate the two, and a missing `type` would sail through
// as "" — which is exactly the failure FR-022 exists to catch.
type wireIn struct {
	Title         *string   `json:"title"`
	Type          *string   `json:"type"`
	Correspondent *string   `json:"correspondent"`
	Tags          *[]string `json:"tags"`
	DocumentDate  *string   `json:"document_date"`
	DueDate       *string   `json:"due_date"`
	Reference     *string   `json:"reference"`
	Description   *string   `json:"description"`
	Amount        *string   `json:"amount"`
	Currency      *string   `json:"currency"`
	Filename      *string   `json:"filename"`
}

// snippetLen bounds what an error quotes back, so a model that answered with an
// essay does not put the essay in the log.
const snippetLen = 120

func snippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > snippetLen {
		return s[:snippetLen] + "…"
	}
	return s
}

// Decode turns one raw model response into validated, normalised metadata.
//
// It is a pure function, and deliberately not the code that built the request:
// FR-025 requires local validation precisely because the service's compliance
// cannot be assumed, so a validator sharing a code path with the request
// builder would validate the assumption rather than the response.
func Decode(raw string, v Vocabularies) (Metadata, error) {
	in, err := decodeStrict(raw)
	if err != nil {
		return Metadata{}, err
	}
	if err := checkPresence(in); err != nil {
		return Metadata{}, err
	}
	out := normalise(in)
	if err := checkVocabularies(out, v); err != nil {
		return Metadata{}, err
	}
	return out, nil
}

// decodeStrict rejects anything that is not exactly the schema's object.
//
// DisallowUnknownFields is what catches prose, a fenced code block, and an
// object of a different shape — all in one place, and all as FR-022 requires.
// Nothing is stripped first: a model that wraps its answer in ``` has ignored
// the constraint, and papering over that would hide the very fault the
// requirement exists to surface.
func decodeStrict(raw string) (wireIn, error) {
	var in wireIn

	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return wireIn{}, &MalformedError{Reason: err.Error(), Snippet: snippet(raw)}
	}
	// A second value on the stream means the reply was not one object.
	if dec.More() {
		return wireIn{}, &MalformedError{
			Reason:  "the reply carries more than one JSON value",
			Snippet: snippet(raw),
		}
	}
	return in, nil
}

// checkPresence enforces the required fields, distinguishing a field that was
// absent from one that arrived empty.
func checkPresence(in wireIn) error {
	required := []struct {
		name    string
		present bool
		empty   bool
	}{
		{"title", in.Title != nil, in.Title != nil && strings.TrimSpace(*in.Title) == ""},
		{"type", in.Type != nil, in.Type != nil && strings.TrimSpace(*in.Type) == ""},
		{"filename", in.Filename != nil, in.Filename != nil && strings.TrimSpace(*in.Filename) == ""},
		{"tags", in.Tags != nil, false}, // an empty tag list is a legitimate answer
	}

	for _, f := range required {
		switch {
		case !f.present:
			return &MalformedError{Reason: "the required field " + f.name + " is absent"}
		case f.empty:
			return &MalformedError{Reason: "the required field " + f.name + " is empty"}
		}
	}
	return nil
}

// normalise applies the four normalisation rules of FR-020, FR-021 and FR-031.
func normalise(in wireIn) Metadata {
	out := Metadata{
		Title:    strings.TrimSpace(deref(in.Title)),
		Type:     strings.TrimSpace(deref(in.Type)),
		Filename: strings.TrimSpace(deref(in.Filename)),

		Correspondent: strings.TrimSpace(deref(in.Correspondent)),
		Reference:     strings.TrimSpace(deref(in.Reference)),
		Description:   strings.TrimSpace(deref(in.Description)),
	}

	out.Tags = normaliseTags(in.Tags)

	// A date that does not match YYYY-MM-DD is dropped, never coerced (FR-020).
	if in.DocumentDate != nil {
		if d, ok := ParseDate(strings.TrimSpace(*in.DocumentDate)); ok {
			out.DocumentDate = &d
		}
	}
	if in.DueDate != nil {
		if d, ok := ParseDate(strings.TrimSpace(*in.DueDate)); ok {
			out.DueDate = &d
		}
	}

	// Amount and currency are kept only as a pair: either alone drops both
	// (FR-021). An amount with no currency is not a fact about the document,
	// and a currency with no amount says nothing at all.
	amount, amountOK := "", false
	if in.Amount != nil {
		amount, amountOK = strings.TrimSpace(*in.Amount), true
	}
	currency, currencyOK := "", false
	if in.Currency != nil {
		currency, currencyOK = strings.TrimSpace(*in.Currency), true
	}
	if amountOK && currencyOK && amount != "" && currency != "" {
		d, decOK := ParseDecimal(amount)
		if decOK && currencyPattern.MatchString(currency) {
			out.Amount = &d
			out.Currency = currency
		}
	}

	return out
}

// normaliseTags lowercases, trims, drops blanks, and deduplicates while keeping
// the model's ordering — stable order so a diff of two runs is readable, and
// deduplicated before any rule sees them (FR-031).
func normaliseTags(tags *[]string) []string {
	if tags == nil {
		return []string{}
	}
	seen := make(map[string]struct{}, len(*tags))
	out := make([]string, 0, len(*tags))
	for _, raw := range *tags {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// checkVocabularies rejects values outside the configured closed sets.
//
// This runs locally even though the vocabulary was transmitted as the schema's
// enum, because a service honouring the constraint is never assumed (FR-025,
// FR-028). It is what makes SC-012 hold: a type outside the vocabulary can
// never create a new branch of the tree.
func checkVocabularies(m Metadata, v Vocabularies) error {
	if !v.Types.Allows(m.Type) {
		return &RejectedError{
			Field: "type", Value: m.Type,
			Reason: "not one of the configured types (" + strings.Join(v.Types.Values, ", ") + ")",
		}
	}
	if !v.Tags.Bounded() {
		return nil // FR-030: an omitted tag vocabulary leaves tags free
	}
	for _, tag := range m.Tags {
		if !v.Tags.Allows(tag) {
			return &RejectedError{
				Field: "tag", Value: tag,
				Reason: "not one of the configured tags (" + strings.Join(v.Tags.Values, ", ") + ")",
			}
		}
	}
	return nil
}

// systemPrompt frames the task. It says nothing about where a document should
// go, because there is no field through which the model could answer that.
const systemPrompt = `You extract metadata from documents.

You are given the text of one document. Answer only with the JSON object the
schema describes. Never wrap it in a code fence, never explain it, and never add
a field the schema does not name.

Report what the document says, not what you infer about it. If the document does
not state something, the answer for that field is null — never a guess, and
never today's date.`

// Analyser runs the analysis step against a configured endpoint.
type Analyser struct {
	client   *chat.Client
	vocab    Vocabularies
	attempts int
}

// New builds an analyser. attempts bounds how many times a malformed response
// is asked for again (FR-018).
func New(client *chat.Client, vocab Vocabularies, attempts int) *Analyser {
	if attempts <= 0 {
		attempts = 3
	}
	return &Analyser{client: client, vocab: vocab, attempts: attempts}
}

// Analyse infers metadata from the extracted text.
//
// Retries here carry no backoff, deliberately: the request succeeded and the
// content was wrong, so there is no server-side pressure to relieve. The
// backoff of FR-076 belongs to the transport, and internal/chat owns it.
func (a *Analyser) Analyse(ctx context.Context, text string) (Metadata, error) {
	schema := chat.DocumentMetadataSchema(a.vocab.Types.Values)

	var (
		lastErr               error
		everyAttemptMalformed = true
	)
	for range a.attempts {
		if err := ctx.Err(); err != nil {
			return Metadata{}, err
		}

		raw, err := a.client.Complete(ctx, chat.Request{
			System: systemPrompt,
			User:   userPrompt(text, a.vocab),
			Schema: &schema,
		})
		if err != nil {
			// Transport failures, including the endpoint refusing the schema
			// outright, are already classified by internal/chat.
			return Metadata{}, err
		}

		md, err := Decode(raw, a.vocab)
		if err == nil {
			return md, nil
		}
		lastErr = err

		var malformed *MalformedError
		if !errors.As(err, &malformed) {
			everyAttemptMalformed = false
		}
	}

	// research.md D7, third signal: a 2xx whose content never conforms is the
	// endpoint having accepted response_format and ignored it. That is a
	// configuration problem the user can fix, and reporting it as an
	// intermittent runtime fault would send them looking in the wrong place.
	if everyAttemptMalformed {
		return Metadata{}, &chat.SchemaUnsupportedError{
			BaseURL: a.client.Endpoint(),
			Model:   a.client.Model(),
			Reason: fmt.Sprintf("%d of %d replies did not conform to the schema; the last was: %v",
				a.attempts, a.attempts, lastErr),
		}
	}

	// The shape was honoured and the values were not: an ordinary bad
	// generation, and nothing is filed on that basis (FR-018).
	return Metadata{}, fmt.Errorf("no usable metadata after %d attempts: %w", a.attempts, lastErr)
}

// userPrompt carries the document text and restates the closed vocabularies, so
// the constraint is present in the prompt as well as in the schema. Belt and
// braces: some endpoints honour one and not the other.
func userPrompt(text string, v Vocabularies) string {
	var b strings.Builder
	b.WriteString("Document text:\n\n")
	b.WriteString(text)
	b.WriteString("\n\n---\n\n")

	fmt.Fprintf(&b, "Choose `type` from exactly these values: %s.\n",
		strings.Join(v.Types.Values, ", "))
	if v.Types.Fallback != "" {
		fmt.Fprintf(&b, "If none of them clearly applies, answer %q.\n", v.Types.Fallback)
	}
	if v.Tags.Bounded() {
		fmt.Fprintf(&b, "Choose every tag from exactly these values: %s. "+
			"An empty list is a valid answer.\n", strings.Join(v.Tags.Values, ", "))
	} else {
		b.WriteString("Tags are free lowercase keywords. An empty list is a valid answer.\n")
	}
	b.WriteString("`filename` is a short hyphenated description, with no date and no extension.\n")
	return b.String()
}

// SortedTags is a copy of the tags in sorted order, for a caller that needs a
// canonical form. The metadata itself keeps the model's ordering.
func SortedTags(m Metadata) []string {
	out := slices.Clone(m.Tags)
	slices.Sort(out)
	return out
}
