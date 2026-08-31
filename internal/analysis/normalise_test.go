package analysis_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/sgaunet/tabularium/internal/analysis"
)

func TestDatesAreDroppedRatherThanCoerced(t *testing.T) {
	// FR-020. Guessing at "14/03/25" means guessing at the locale, and a wrong
	// date sorts wrong forever while looking authoritative. No date is better.
	tests := []struct {
		name string
		in   any
		want string // "" means the date must be dropped
	}{
		{name: "a well-formed date is kept", in: "2025-03-14", want: "2025-03-14"},
		{name: "a leap day is kept", in: "2024-02-29", want: "2024-02-29"},
		{name: "null is no date", in: nil, want: ""},
		{name: "an empty string is no date", in: "", want: ""},
		{name: "day-first is dropped, not reinterpreted", in: "14/03/2025", want: ""},
		{name: "month-first is dropped, not reinterpreted", in: "03/14/2025", want: ""},
		{name: "a two-digit year is dropped", in: "25-03-14", want: ""},
		{name: "an unpadded month is dropped", in: "2025-3-14", want: ""},
		{name: "a written month is dropped", in: "14 March 2025", want: ""},
		{name: "an impossible day is dropped", in: "2025-02-30", want: ""},
		{name: "an impossible month is dropped", in: "2025-13-01", want: ""},
		{name: "a timestamp is dropped: this is a date, not an instant", in: "2025-03-14T09:41:12Z", want: ""},
		{name: "prose is dropped", in: "sometime in March", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var override map[string]any
			if tt.in == nil {
				override = map[string]any{"document_date": nil}
			} else {
				override = map[string]any{"document_date": tt.in}
			}

			got, err := analysis.Decode(full(override), vocab())
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			switch {
			case tt.want == "":
				if got.DocumentDate != nil {
					t.Errorf("DocumentDate = %v for input %v; want it dropped",
						got.DocumentDate, tt.in)
				}
			case got.DocumentDate == nil:
				t.Errorf("DocumentDate was dropped for input %v; want %s", tt.in, tt.want)
			case got.DocumentDate.String() != tt.want:
				t.Errorf("DocumentDate = %s, want %s", got.DocumentDate, tt.want)
			}
		})
	}
}

func TestADroppedDateMeansNoDatePrefix(t *testing.T) {
	// FR-035: today's date is never substituted. The prefix is derived from the
	// document's own date or it does not exist.
	got, err := analysis.Decode(full(map[string]any{"document_date": "14/03/2025"}), vocab())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if prefix := got.DatePrefix(); prefix != "" {
		t.Errorf("DatePrefix() = %q after a dropped date; want empty", prefix)
	}
}

func TestTags(t *testing.T) {
	// FR-031: lowercased and deduplicated before any rule sees them, in stable
	// order so a diff of two runs is readable.
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "already normal", in: []string{"voiture", "entretien"}, want: []string{"voiture", "entretien"}},
		{name: "case is folded", in: []string{"Voiture", "ENTRETIEN"}, want: []string{"voiture", "entretien"}},
		{name: "duplicates are removed", in: []string{"voiture", "voiture"}, want: []string{"voiture"}},
		{name: "duplicates differing only in case are removed", in: []string{"Voiture", "voiture", "VOITURE"}, want: []string{"voiture"}},
		{name: "order is the model's, and stable", in: []string{"zeta", "alpha", "mu"}, want: []string{"zeta", "alpha", "mu"}},
		{name: "surrounding whitespace is trimmed", in: []string{"  voiture  ", "\tentretien\n"}, want: []string{"voiture", "entretien"}},
		{name: "blank tags are dropped", in: []string{"voiture", "", "   "}, want: []string{"voiture"}},
		{name: "an empty list stays empty", in: []string{}, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := analysis.Decode(full(map[string]any{"tags": tt.in}), vocab())
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !slices.Equal(got.Tags, tt.want) {
				t.Errorf("Tags = %v, want %v", got.Tags, tt.want)
			}
		})
	}
}

func TestAmountAndCurrencyAreKeptOnlyAsAPair(t *testing.T) {
	// FR-021. An amount with no currency is not a fact about the document, and
	// a currency with no amount says nothing at all.
	tests := []struct {
		name         string
		amount       any
		currency     any
		wantAmount   string
		wantCurrency string
	}{
		{name: "both present", amount: "384.50", currency: "EUR", wantAmount: "384.50", wantCurrency: "EUR"},
		{name: "a whole number is a decimal", amount: "384", currency: "EUR", wantAmount: "384", wantCurrency: "EUR"},
		{name: "a negative amount is a decimal", amount: "-12.34", currency: "EUR", wantAmount: "-12.34", wantCurrency: "EUR"},
		{name: "an amount alone drops both", amount: "384.50", currency: nil},
		{name: "a currency alone drops both", amount: nil, currency: "EUR"},
		{name: "neither present", amount: nil, currency: nil},
		{name: "an empty amount drops both", amount: "", currency: "EUR"},
		{name: "an empty currency drops both", amount: "384.50", currency: ""},
		{name: "an amount that is not a decimal drops both", amount: "384,50 €", currency: "EUR"},
		{name: "a thousands separator is not a decimal", amount: "1,234.56", currency: "EUR"},
		{name: "a currency symbol is not a code", amount: "384.50", currency: "€"},
		{name: "a lowercase code is not ISO 4217", amount: "384.50", currency: "eur"},
		{name: "a four-letter code is not ISO 4217", amount: "384.50", currency: "EURO"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := analysis.Decode(full(map[string]any{
				"amount": tt.amount, "currency": tt.currency,
			}), vocab())
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			if tt.wantAmount == "" {
				if got.Amount != nil {
					t.Errorf("Amount = %v, want it dropped", *got.Amount)
				}
				if got.Currency != "" {
					t.Errorf("Currency = %q, want it dropped alongside the amount", got.Currency)
				}
				return
			}
			if got.Amount == nil || string(*got.Amount) != tt.wantAmount {
				t.Errorf("Amount = %v, want %q", got.Amount, tt.wantAmount)
			}
			if got.Currency != tt.wantCurrency {
				t.Errorf("Currency = %q, want %q", got.Currency, tt.wantCurrency)
			}
		})
	}
}

func TestParseDecimal(t *testing.T) {
	// The constructor is what makes the type worth having: money never becomes
	// a float64, and a value that is not a decimal never becomes a Decimal.
	tests := []struct {
		in   string
		want bool
	}{
		{in: "0", want: true},
		{in: "384", want: true},
		{in: "384.50", want: true},
		{in: "-384.50", want: true},
		{in: "0.01", want: true},
		{in: "19.99", want: true},
		{in: "1234567890.123456789", want: true},
		{in: "", want: false},
		{in: "-", want: false},
		{in: ".5", want: false},
		{in: "5.", want: false},
		{in: "1,234.56", want: false},
		{in: "384,50", want: false},
		{in: "+384", want: false},
		{in: "1e3", want: false},
		{in: "384.50 EUR", want: false},
		{in: "€384.50", want: false},
		{in: "NaN", want: false},
		{in: "Infinity", want: false},
		{in: "0x10", want: false},
		{in: " 384.50", want: false},
		{in: "384.50\n", want: false},
		{in: "3.8.4", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := analysis.ParseDecimal(tt.in)
			if ok != tt.want {
				t.Fatalf("ParseDecimal(%q) ok = %v, want %v", tt.in, ok, tt.want)
			}
			if ok && string(got) != tt.in {
				t.Errorf("ParseDecimal(%q) = %q; the value must survive verbatim", tt.in, got)
			}
		})
	}
}

func TestParseDecimalKeepsTrailingZeroes(t *testing.T) {
	// The whole reason money is a string here: 19.90 and 19.9 are the same
	// number and a different thing to print on an invoice.
	got, ok := analysis.ParseDecimal("19.90")
	if !ok {
		t.Fatal(`ParseDecimal("19.90") = not ok`)
	}
	if string(got) != "19.90" {
		t.Errorf("ParseDecimal = %q, want the trailing zero kept", got)
	}
}

func TestParseDate(t *testing.T) {
	got, ok := analysis.ParseDate("2025-03-14")
	if !ok {
		t.Fatal(`ParseDate("2025-03-14") = not ok`)
	}
	if got.Year != 2025 || got.Month != 3 || got.Day != 14 {
		t.Errorf("ParseDate = %+v, want 2025/3/14", got)
	}

	year, month, day := got.Parts()
	if year != "2025" || month != "03" || day != "14" {
		t.Errorf("Parts() = %q %q %q; want zero-padded parts so paths sort correctly",
			year, month, day)
	}
}

func TestDateHasNoClockAndNoZone(t *testing.T) {
	// A time.Time would carry both, and formatting one back out invites a
	// timezone shift across a day boundary — a document dated the 1st filed
	// under the 31st.
	d, ok := analysis.ParseDate("2025-01-01")
	if !ok {
		t.Fatal("ParseDate failed")
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(raw) != `"2025-01-01"` {
		t.Errorf("marshalled date = %s, want a bare YYYY-MM-DD string", raw)
	}
}

func TestOptionalTextFieldsAreTrimmedAndMayBeAbsent(t *testing.T) {
	got, err := analysis.Decode(full(map[string]any{
		"correspondent": "  Garage Central  ",
		"reference":     nil,
		"description":   "",
	}), vocab())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if got.Correspondent != "Garage Central" {
		t.Errorf("Correspondent = %q, want it trimmed", got.Correspondent)
	}
	if got.Reference != "" {
		t.Errorf("Reference = %q, want empty for an absent field", got.Reference)
	}
	if got.Description != "" {
		t.Errorf("Description = %q, want empty", got.Description)
	}
}

func TestAnAbsentOptionalFieldMarshalsAsNull(t *testing.T) {
	// The contracts type these as ["string","null"]. A consumer checking for
	// null should not also have to check for "".
	md, err := analysis.Decode(full(map[string]any{
		"correspondent": nil, "reference": nil, "description": nil,
		"amount": nil, "currency": nil, "due_date": nil,
	}), vocab())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	raw, err := json.Marshal(md)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	for _, name := range []string{"correspondent", "reference", "description",
		"amount", "currency", "due_date"} {
		if out[name] != nil {
			t.Errorf("%s = %v, want null", name, out[name])
		}
	}
	// tags is the exception: always an array, never null.
	if out["tags"] == nil {
		t.Error("tags = null; it must always be an array")
	}
}

func TestDecodeIsPure(t *testing.T) {
	raw := full(nil)
	first, err := analysis.Decode(raw, vocab())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for range 50 {
		got, err := analysis.Decode(raw, vocab())
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got.Title != first.Title || got.Type != first.Type ||
			!slices.Equal(got.Tags, first.Tags) {
			t.Fatalf("Decode drifted: %+v, want %+v", got, first)
		}
	}
}
