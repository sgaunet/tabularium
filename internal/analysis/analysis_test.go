package analysis_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/analysis"
	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/config"
)

// vocab is the vocabulary of contracts/config.example.yaml, trimmed.
func vocab() analysis.Vocabularies {
	return analysis.Vocabularies{
		Types: config.Vocabulary{
			Fallback: "autre",
			Values:   []string{"facture", "releve-bancaire", "contrat", "autre"},
		},
	}
}

// bounded adds a tag vocabulary, which FR-030 makes optional.
func bounded() analysis.Vocabularies {
	v := vocab()
	v.Tags = config.Vocabulary{Values: []string{"voiture", "entretien", "sante"}}
	return v
}

// full is a complete, conforming response. Tests vary one thing from it at a
// time, so a failure names the thing that varied.
func full(overrides map[string]any) string {
	body := map[string]any{
		"title":         "Facture entretien véhicule",
		"type":          "facture",
		"correspondent": "Garage Central",
		"tags":          []string{"voiture", "entretien"},
		"document_date": "2025-03-14",
		"due_date":      "2025-04-14",
		"reference":     "FA-2025-0312",
		"description":   "Révision annuelle.",
		"amount":        "384.50",
		"currency":      "EUR",
		"filename":      "facture-entretien-vehicule",
	}
	for k, v := range overrides {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic("encoding a fixed test reply: " + err.Error())
	}
	return string(raw)
}

func TestDecodeAcceptsAConformingResponse(t *testing.T) {
	got, err := analysis.Decode(full(nil), vocab())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if got.Title != "Facture entretien véhicule" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Type != "facture" {
		t.Errorf("Type = %q, want %q", got.Type, "facture")
	}
	if got.DocumentDate == nil || got.DocumentDate.String() != "2025-03-14" {
		t.Errorf("DocumentDate = %v, want 2025-03-14", got.DocumentDate)
	}
	if got.Amount == nil || string(*got.Amount) != "384.50" {
		t.Errorf("Amount = %v, want 384.50", got.Amount)
	}
	if got.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", got.Currency)
	}
}

func TestDecodeRejectsAnythingThatIsNotTheSchemasObject(t *testing.T) {
	// FR-022. Nothing is stripped or repaired first: a model that wrapped its
	// answer in a fence has ignored the constraint, and papering over that
	// would hide the fault this requirement exists to surface.
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "prose",
			raw:  "This document appears to be an invoice from Garage Central dated 14 March 2025.",
		},
		{
			name: "prose introducing JSON",
			raw:  "Here is the metadata you asked for:\n" + full(nil),
		},
		{
			name: "a fenced code block",
			raw:  "```json\n" + full(nil) + "\n```",
		},
		{
			name: "a bare fence",
			raw:  "```\n" + full(nil) + "\n```",
		},
		{
			name: "an object with an extra field",
			raw:  full(map[string]any{"folder": "factures/voiture"}),
		},
		{
			name: "an object of a different shape entirely",
			raw:  `{"document":{"kind":"invoice"}}`,
		},
		{
			name: "a JSON array",
			raw:  `[` + full(nil) + `]`,
		},
		{
			name: "a bare string",
			raw:  `"facture"`,
		},
		{
			name: "two objects on the stream",
			raw:  full(nil) + full(nil),
		},
		{
			name: "an empty reply",
			raw:  "",
		},
		{
			name: "whitespace",
			raw:  "   \n  ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := analysis.Decode(tt.raw, vocab())
			if err == nil {
				t.Fatalf("Decode(%q) = nil error; want a rejection", snippetOf(tt.raw))
			}
			var malformed *analysis.MalformedError
			if !errors.As(err, &malformed) {
				t.Errorf("Decode() = %v; want a *analysis.MalformedError", err)
			}
		})
	}
}

func TestDecodeDistinguishesAbsentFromEmpty(t *testing.T) {
	// research.md D6 step 2: plain zero values would conflate the two, and a
	// missing `type` would sail through as "".
	tests := []struct {
		name    string
		raw     string
		wantSay string
	}{
		{name: "an absent title", raw: full(map[string]any{"title": nil}), wantSay: "absent"},
		{name: "an empty title", raw: full(map[string]any{"title": ""}), wantSay: "empty"},
		{name: "an absent type", raw: full(map[string]any{"type": nil}), wantSay: "absent"},
		{name: "an empty type", raw: full(map[string]any{"type": ""}), wantSay: "empty"},
		{name: "an absent filename", raw: full(map[string]any{"filename": nil}), wantSay: "absent"},
		{name: "an empty filename", raw: full(map[string]any{"filename": ""}), wantSay: "empty"},
		{name: "absent tags", raw: full(map[string]any{"tags": nil}), wantSay: "absent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := analysis.Decode(tt.raw, vocab())
			if err == nil {
				t.Fatal("Decode() = nil error; want a rejection")
			}
			if !strings.Contains(err.Error(), tt.wantSay) {
				t.Errorf("Decode() error = %v; want it to say the field was %s", err, tt.wantSay)
			}
		})
	}
}

func TestAnEmptyTagListIsALegitimateAnswer(t *testing.T) {
	got, err := analysis.Decode(full(map[string]any{"tags": []string{}}), vocab())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.Tags) != 0 {
		t.Errorf("Tags = %v, want empty", got.Tags)
	}
}

func TestTypeIsCheckedLocallyEvenWhenTheTransportAcceptedIt(t *testing.T) {
	// FR-025, FR-028, SC-012. The vocabulary went out as the schema's enum, but
	// a service honouring the constraint is never assumed — and this is the
	// check that stops an invented type from creating a new branch of the tree.
	_, err := analysis.Decode(full(map[string]any{"type": "facture-de-garage"}), vocab())
	if err == nil {
		t.Fatal("Decode() with a type outside the vocabulary = nil error")
	}

	var rejected *analysis.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("Decode() = %v; want a *analysis.RejectedError", err)
	}
	if !strings.Contains(err.Error(), "facture-de-garage") {
		t.Errorf("error = %v; want it to quote the offending value", err)
	}
}

func TestTagsAreCheckedOnlyWhenTheConfigurationBoundsThem(t *testing.T) {
	// FR-030: an omitted tag vocabulary leaves tags free.
	raw := full(map[string]any{"tags": []string{"voiture", "quelque-chose-de-neuf"}})

	t.Run("free when unbounded", func(t *testing.T) {
		got, err := analysis.Decode(raw, vocab())
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if len(got.Tags) != 2 {
			t.Errorf("Tags = %v, want both kept", got.Tags)
		}
	})

	t.Run("rejected when bounded", func(t *testing.T) {
		_, err := analysis.Decode(raw, bounded())
		if err == nil {
			t.Fatal("Decode() with a tag outside a bounded vocabulary = nil error")
		}
		if !strings.Contains(err.Error(), "quelque-chose-de-neuf") {
			t.Errorf("error = %v; want it to quote the offending tag", err)
		}
	})
}

func TestTheFallbackTypeIsAlwaysAccepted(t *testing.T) {
	// FR-029: the fallback exists so a document the model cannot place stays
	// recognisable as unplaced. It would be useless if validation rejected it.
	got, err := analysis.Decode(full(map[string]any{"type": "autre"}), vocab())
	if err != nil {
		t.Fatalf("Decode() with the fallback type: %v", err)
	}
	if got.Type != "autre" {
		t.Errorf("Type = %q, want %q", got.Type, "autre")
	}
}

func TestMetadataNamesNoFolder(t *testing.T) {
	// FR-016 as a property of the type: there is no field to put a folder in.
	raw, err := json.Marshal(mustDecode(t, full(nil)))
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	for name := range out {
		for _, forbidden := range []string{"folder", "path", "directory", "destination"} {
			if strings.Contains(strings.ToLower(name), forbidden) {
				t.Errorf("Metadata marshals a field %q, which names a place to file", name)
			}
		}
	}
}

func TestMetadataMarshalsTheContractShape(t *testing.T) {
	// contracts/output.schema.json and sidecar.schema.json share this shape.
	// The model's proposed `filename` is an input to naming and has no place in
	// the tool's own output.
	raw, err := json.Marshal(mustDecode(t, full(nil)))
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	want := []string{"title", "type", "correspondent", "tags", "document_date",
		"due_date", "reference", "description", "amount", "currency"}
	for _, name := range want {
		if _, ok := out[name]; !ok {
			t.Errorf("the marshalled metadata has no %q", name)
		}
	}
	if len(out) != len(want) {
		t.Errorf("the marshalled metadata has %d fields (%v), want exactly %d", len(out), keys(out), len(want))
	}
	if _, leaked := out["filename"]; leaked {
		t.Error("the model's proposed filename reached the output")
	}
}

func TestMetadataRoundTripsThroughJSON(t *testing.T) {
	// The sidecar is written and read back by this same type; a shape that does
	// not round-trip would make every re-run reprocess from the start.
	original := mustDecode(t, full(nil))

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var back analysis.Metadata
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if back.Title != original.Title || back.Type != original.Type {
		t.Errorf("round trip lost the title or type: %+v", back)
	}
	if back.DocumentDate == nil || *back.DocumentDate != *original.DocumentDate {
		t.Errorf("round trip lost the document date: %v", back.DocumentDate)
	}
	if back.Amount == nil || *back.Amount != *original.Amount {
		t.Errorf("round trip lost the amount: %v", back.Amount)
	}
	if strings.Join(back.Tags, ",") != strings.Join(original.Tags, ",") {
		t.Errorf("round trip changed the tags: %v, want %v", back.Tags, original.Tags)
	}
}

// --- Analyse, against a stub endpoint ---------------------------------------

func endpoint(t *testing.T, handler func(w http.ResponseWriter, n int)) (string, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handler(w, int(calls.Add(1)))
	}))
	t.Cleanup(s.Close)
	return s.URL, &calls
}

func answer(content string) string {
	raw, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
	})
	if err != nil {
		panic("encoding a fixed test reply: " + err.Error())
	}
	return string(raw)
}

func analyser(t *testing.T, url string, v analysis.Vocabularies, attempts int) *analysis.Analyser {
	t.Helper()
	c, err := chat.New(chat.Options{BaseURL: url, Model: "test-model", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	return analysis.New(c, v, attempts)
}

func TestAnalyseReturnsMetadataFromAConformingEndpoint(t *testing.T) {
	url, calls := endpoint(t, func(w http.ResponseWriter, _ int) {
		_, _ = io.WriteString(w, answer(full(nil)))
	})

	got, err := analyser(t, url, vocab(), 3).Analyse(context.Background(), "some text")
	if err != nil {
		t.Fatalf("Analyse: %v", err)
	}
	if got.Type != "facture" {
		t.Errorf("Type = %q, want facture", got.Type)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("the endpoint was called %d times, want 1", n)
	}
}

func TestAnalyseRetriesAMalformedReplyAndSucceeds(t *testing.T) {
	url, calls := endpoint(t, func(w http.ResponseWriter, n int) {
		if n < 3 {
			_, _ = io.WriteString(w, answer("I think this is an invoice."))
			return
		}
		_, _ = io.WriteString(w, answer(full(nil)))
	})

	got, err := analyser(t, url, vocab(), 3).Analyse(context.Background(), "some text")
	if err != nil {
		t.Fatalf("Analyse: %v", err)
	}
	if got.Type != "facture" {
		t.Errorf("Type = %q, want facture", got.Type)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("the endpoint was called %d times, want 3", n)
	}
}

func TestAnEndpointThatAlwaysAnswersProseIsAConfigurationError(t *testing.T) {
	// research.md D7, third signal: 2xx with non-conforming content on every
	// bounded attempt is the endpoint having accepted response_format and
	// ignored it. FR-024 requires that to surface as a configuration problem
	// naming the endpoint, not as an intermittent runtime fault.
	url, calls := endpoint(t, func(w http.ResponseWriter, _ int) {
		_, _ = io.WriteString(w, answer("This looks like an invoice from a garage."))
	})

	_, err := analyser(t, url, vocab(), 3).Analyse(context.Background(), "some text")
	if err == nil {
		t.Fatal("Analyse() = nil error")
	}

	var unsupported *chat.SchemaUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Analyse() = %v; want a *chat.SchemaUnsupportedError so main exits 2", err)
	}
	if !strings.Contains(err.Error(), url) {
		t.Errorf("error = %v; want it to name the base URL", err)
	}
	if !strings.Contains(err.Error(), "test-model") {
		t.Errorf("error = %v; want it to name the model", err)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("the endpoint was called %d times, want the bounded 3", n)
	}
}

func TestAWellShapedReplyWithABadValueIsARuntimeFailure(t *testing.T) {
	// FR-018 rather than FR-024: the endpoint honoured the schema's shape, so
	// the configuration is fine and the model simply chose badly. Nothing is
	// filed on that basis, but nor is the user sent to change their endpoint.
	url, _ := endpoint(t, func(w http.ResponseWriter, _ int) {
		_, _ = io.WriteString(w, answer(full(map[string]any{"type": "invente"})))
	})

	_, err := analyser(t, url, vocab(), 3).Analyse(context.Background(), "some text")
	if err == nil {
		t.Fatal("Analyse() = nil error")
	}

	var unsupported *chat.SchemaUnsupportedError
	if errors.As(err, &unsupported) {
		t.Errorf("a well-shaped reply with a bad value was blamed on the endpoint: %v", err)
	}
	if !strings.Contains(err.Error(), "invente") {
		t.Errorf("error = %v; want it to quote the rejected value", err)
	}
}

func TestAnalyseHonoursCancellation(t *testing.T) {
	url, _ := endpoint(t, func(w http.ResponseWriter, _ int) {
		_, _ = io.WriteString(w, answer(full(nil)))
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := analyser(t, url, vocab(), 3).Analyse(ctx, "some text"); err == nil {
		t.Fatal("Analyse() with a cancelled context = nil error")
	}
}

func TestThePromptNeverAsksWhereToFileTheDocument(t *testing.T) {
	// SC-014: the complete set of destinations is readable in the
	// configuration. A prompt that asked for a folder would put one somewhere
	// else.
	var sent string
	url := endpointRecording(t, &sent, answer(full(nil)))

	if _, err := analyser(t, url, vocab(), 1).Analyse(context.Background(), "text"); err != nil {
		t.Fatalf("Analyse: %v", err)
	}

	// Only the messages: the schema's own descriptions do say "Never a folder
	// or a path", which is the contract's wording and an instruction against
	// naming one rather than an invitation to.
	lower := strings.ToLower(messagesOf(t, sent))
	for _, forbidden := range []string{"folder", "directory", "where to file", "destination"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("the prompt asks the model about %q:\n%s", forbidden, lower)
		}
	}
}

// messagesOf extracts just the prompt text from a recorded request body.
func messagesOf(t *testing.T, body string) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decoding the recorded request: %v", err)
	}
	var b strings.Builder
	for _, m := range req.Messages {
		fmt.Fprintf(&b, "%v\n", m.Content)
	}
	return b.String()
}

func TestThePromptCarriesTheVocabulary(t *testing.T) {
	var sent string
	url := endpointRecording(t, &sent, answer(full(nil)))

	if _, err := analyser(t, url, bounded(), 1).Analyse(context.Background(), "text"); err != nil {
		t.Fatalf("Analyse: %v", err)
	}

	prompt := messagesOf(t, sent)
	for _, want := range []string{"facture", "releve-bancaire", "autre", "voiture", "entretien"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not carry the vocabulary value %q", want)
		}
	}
}

func endpointRecording(t *testing.T, sink *string, reply string) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*sink = string(body)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func mustDecode(t *testing.T, raw string) analysis.Metadata {
	t.Helper()
	md, err := analysis.Decode(raw, vocab())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return md
}

func snippetOf(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
