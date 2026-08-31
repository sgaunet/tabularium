package analysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Date is a calendar date: no clock, no zone.
//
// A time.Time would carry both, and formatting one back out invites a timezone
// shift across a day boundary — a document dated the 1st filed under the 31st.
// The zero value is meaningfully invalid, and the field is used through a
// pointer so "no date" stays distinct from "year zero": FR-035 turns on exactly
// that distinction.
//
// The receivers below are deliberately mixed: MarshalJSON takes a value so a
// Date held by value still marshals through it, and UnmarshalJSON must take a
// pointer in order to assign. That is what encoding/json requires, not an
// oversight.
//
//nolint:recvcheck // required by encoding/json, see above
type Date struct {
	Year  int
	Month int
	Day   int
}

// ParseDate accepts YYYY-MM-DD and nothing else.
//
// A date that does not match is dropped, never coerced (FR-020). Guessing at
// "14/03/25" means guessing at the locale, and a wrong date sorts wrong forever
// while looking authoritative.
func ParseDate(s string) (Date, bool) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, false
	}
	return Date{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}, true
}

// String renders the date as YYYY-MM-DD.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// Parts are the zero-padded components a path template renders.
func (d Date) Parts() (year, month, day string) {
	return fmt.Sprintf("%04d", d.Year), fmt.Sprintf("%02d", d.Month), fmt.Sprintf("%02d", d.Day)
}

// MarshalJSON writes the date as the string the contracts specify.
func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON reads a date back from a sidecar.
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, ok := ParseDate(s)
	if !ok {
		return fmt.Errorf("not a YYYY-MM-DD date: %q", s)
	}
	*d = parsed
	return nil
}

// decimalPattern is the whole of what a monetary amount may look like.
var decimalPattern = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// Decimal is a validated decimal string.
//
// Money in binary floating point is a defect waiting for the first 19.99. The
// tool never does arithmetic on this value — it records it and puts it in a
// template — so an exact string with a validating constructor is both
// sufficient and correct, where a float64 would be neither.
type Decimal string

// ParseDecimal accepts an optional sign, digits, and at most one fractional
// part. Anything else is not an amount.
func ParseDecimal(s string) (Decimal, bool) {
	if !decimalPattern.MatchString(s) {
		return "", false
	}
	return Decimal(s), true
}

func (d Decimal) String() string { return string(d) }

// currencyPattern is ISO 4217: three capitals.
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// Metadata is what the model inferred, after local validation and
// normalisation.
//
// No field names a folder. That is FR-016, and it is the invariant the whole
// filing design rests on: the model produces a bounded type and some tags, and
// every directory level is literal text in a rule, so a model mistake can move
// a document between two declared folders but can never invent a third.
//
// As with Date, the mixed receivers below are what encoding/json requires.
//
//nolint:recvcheck // required by encoding/json, see Date
type Metadata struct {
	Title         string
	Type          string
	Correspondent string
	Tags          []string
	DocumentDate  *Date
	DueDate       *Date
	Reference     string
	Description   string
	Amount        *Decimal
	Currency      string

	// Filename is the model's proposal. It is never used raw: internal/naming
	// sanitises it, and keeps the original name when nothing survives (FR-032,
	// FR-033).
	Filename string
}

// DatePrefix is "YYYY-MM-DD" when the document bore a date that survived
// validation, and empty otherwise — which is what internal/naming needs, and
// all it is given.
func (m Metadata) DatePrefix() string {
	if m.DocumentDate == nil {
		return ""
	}
	return m.DocumentDate.String()
}

// wireOut is the metadata shape both output.schema.json and
// sidecar.schema.json specify. It is written out explicitly rather than reusing
// the struct tags, because the two shapes are deliberately not the same: the
// model's proposed `filename` is an input to naming and has no place in the
// tool's own output.
type wireOut struct {
	Title         string   `json:"title"`
	Type          string   `json:"type"`
	Correspondent *string  `json:"correspondent"`
	Tags          []string `json:"tags"`
	DocumentDate  *string  `json:"document_date"`
	DueDate       *string  `json:"due_date"`
	Reference     *string  `json:"reference"`
	Description   *string  `json:"description"`
	Amount        *string  `json:"amount"`
	Currency      *string  `json:"currency"`
}

// nilIfEmpty renders an absent optional string as JSON null rather than "".
// The contracts type these fields as ["string","null"], and a consumer that
// checks for null should not have to also check for the empty string.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// MarshalJSON writes the metadata in the shape the output and sidecar contracts
// share.
func (m Metadata) MarshalJSON() ([]byte, error) {
	out := wireOut{
		Title:         m.Title,
		Type:          m.Type,
		Correspondent: nilIfEmpty(m.Correspondent),
		// Never null: a consumer iterating tags should not have to nil-check.
		Tags:        append([]string{}, m.Tags...),
		Reference:   nilIfEmpty(m.Reference),
		Description: nilIfEmpty(m.Description),
		Currency:    nilIfEmpty(m.Currency),
	}
	if m.DocumentDate != nil {
		out.DocumentDate = nilIfEmpty(m.DocumentDate.String())
	}
	if m.DueDate != nil {
		out.DueDate = nilIfEmpty(m.DueDate.String())
	}
	if m.Amount != nil {
		out.Amount = nilIfEmpty(string(*m.Amount))
	}

	// Not json.Marshal: it escapes &, < and > for safe embedding in HTML, and
	// it would do so here even though the enclosing encoder has escaping
	// switched off — the outer encoder only copies the bytes this returns. A
	// document title is not a web page, and FR-066 says an ampersand stays an
	// ampersand.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	// Encoder.Encode appends a newline, which is not part of the value.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// UnmarshalJSON reads metadata back out of a sidecar.
//
// It is deliberately forgiving of nothing: a sidecar that does not round-trip
// is treated as absent by the caller, and the document is reprocessed from the
// start.
func (m *Metadata) UnmarshalJSON(b []byte) error {
	var in wireOut
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return err
	}

	out := Metadata{
		Title: in.Title,
		Type:  in.Type,
		Tags:  in.Tags,
	}
	if in.Correspondent != nil {
		out.Correspondent = *in.Correspondent
	}
	if in.Reference != nil {
		out.Reference = *in.Reference
	}
	if in.Description != nil {
		out.Description = *in.Description
	}
	if in.Currency != nil {
		out.Currency = *in.Currency
	}
	if in.DocumentDate != nil {
		d, ok := ParseDate(*in.DocumentDate)
		if !ok {
			return fmt.Errorf("document_date is not YYYY-MM-DD: %q", *in.DocumentDate)
		}
		out.DocumentDate = &d
	}
	if in.DueDate != nil {
		d, ok := ParseDate(*in.DueDate)
		if !ok {
			return fmt.Errorf("due_date is not YYYY-MM-DD: %q", *in.DueDate)
		}
		out.DueDate = &d
	}
	if in.Amount != nil {
		a, ok := ParseDecimal(*in.Amount)
		if !ok {
			return fmt.Errorf("amount is not a decimal: %q", *in.Amount)
		}
		out.Amount = &a
	}

	*m = out
	return nil
}
