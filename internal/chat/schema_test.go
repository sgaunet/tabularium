package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/chat"
)

func TestDocumentMetadataSchemaMatchesTheContract(t *testing.T) {
	// contracts/analysis.schema.json. Strict mode demands that every property
	// be required and that additionalProperties be false; an optional field is
	// expressed as a nullable type, not by omission from `required`.
	vocab := []string{"facture", "releve-bancaire", "autre"}
	s := chat.DocumentMetadataSchema(vocab)

	if s.Name != "document_metadata" {
		t.Errorf("Name = %q, want %q", s.Name, "document_metadata")
	}
	if !s.Strict {
		t.Error("Strict = false; FR-023 requires the constraint to apply at generation time")
	}

	body := marshalled(t, s.Body)
	if body["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", body["additionalProperties"])
	}

	props, _ := body["properties"].(map[string]any)
	required := stringsOf(body["required"])
	want := []string{
		"title", "type", "correspondent", "tags", "document_date",
		"due_date", "reference", "description", "amount", "currency", "filename",
	}
	for _, name := range want {
		if _, ok := props[name]; !ok {
			t.Errorf("the schema has no property %q", name)
		}
		if !slices.Contains(required, name) {
			t.Errorf("%q is not in `required`; strict mode requires every property to be", name)
		}
	}
	if len(props) != len(want) {
		t.Errorf("the schema has %d properties, want exactly %d", len(props), len(want))
	}
}

func TestSchemaNamesNoFolder(t *testing.T) {
	// FR-016 and SC-014: the model produces a bounded type and tags. It is
	// never asked where a document should go, so it can never answer.
	body := marshalled(t, chat.DocumentMetadataSchema([]string{"autre"}).Body)
	props, _ := body["properties"].(map[string]any)

	for name := range props {
		for _, forbidden := range []string{"folder", "path", "directory", "destination"} {
			if strings.Contains(strings.ToLower(name), forbidden) {
				t.Errorf("the schema has a property %q, which names a place to file", name)
			}
		}
	}
}

func TestTheVocabularyIsSubstitutedIntoTheTypeEnum(t *testing.T) {
	// FR-026, FR-027: the closed set is transmitted as the schema's enum, so
	// the constraint applies at generation time rather than only on the way
	// back.
	vocab := []string{"facture", "contrat", "autre"}
	body := marshalled(t, chat.DocumentMetadataSchema(vocab).Body)

	props, _ := body["properties"].(map[string]any)
	typ, _ := props["type"].(map[string]any)
	got := stringsOf(typ["enum"])

	if !slices.Equal(got, vocab) {
		t.Errorf("type enum = %v, want the configured vocabulary %v in order", got, vocab)
	}
}

func TestTheSchemaTravelsInResponseFormat(t *testing.T) {
	s := newServer(t, ok(`{"title":"x"}`))
	c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second})

	schema := chat.DocumentMetadataSchema([]string{"facture", "autre"})
	if _, err := c.Complete(context.Background(), chat.Request{User: "hi", Schema: &schema}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	sent := s.last.Load().body

	rf, ok := sent["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("the request carried no response_format:\n%v", sent)
	}
	if rf["type"] != "json_schema" {
		t.Errorf(`response_format.type = %v, want "json_schema"`, rf["type"])
	}
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("response_format has no json_schema member:\n%v", rf)
	}
	if js["strict"] != true {
		t.Errorf("json_schema.strict = %v, want true", js["strict"])
	}
	if js["name"] != "document_metadata" {
		t.Errorf("json_schema.name = %v, want %q", js["name"], "document_metadata")
	}
	if _, ok := js["schema"]; !ok {
		t.Error("json_schema carries no schema")
	}
}

func TestARequestWithNoSchemaCarriesNoResponseFormat(t *testing.T) {
	// The OCR call is a plain transcription. Sending an empty response_format
	// is what several local endpoints choke on.
	s := newServer(t, ok("text"))
	c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second})

	if _, err := c.Complete(context.Background(), chat.Request{User: "transcribe"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	sent := s.last.Load().body
	if _, present := sent["response_format"]; present {
		t.Errorf("a request with no schema carried a response_format anyway:\n%v", sent)
	}
}

func TestAnEndpointThatRefusesTheSchemaIsAConfigurationError(t *testing.T) {
	// research.md D7 and FR-024. Each of these is unambiguous on the first
	// response, and each must surface as a configuration problem naming the
	// endpoint — not as an intermittent runtime fault.
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "a 400 that names the parameter",
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"response_format.json_schema is not supported"}}`,
		},
		{
			name:   "a 400 that names json_schema alone",
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"unknown field json_schema"}}`,
		},
		{
			name:   "a 404: not an OpenAI-compatible service at all",
			status: http.StatusNotFound,
			body:   "not found",
		},
		{
			name:   "a 501",
			status: http.StatusNotImplemented,
			body:   "not implemented",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			c := client(t, chat.Options{
				BaseURL: s.URL + "/v1", Model: "qwen2.5:14b",
				Timeout: time.Second, Retries: 3,
			})

			schema := chat.DocumentMetadataSchema([]string{"autre"})
			_, err := c.Complete(context.Background(), chat.Request{User: "hi", Schema: &schema})
			if err == nil {
				t.Fatal("Complete() = nil error")
			}

			var unsupported *chat.SchemaUnsupportedError
			if !errors.As(err, &unsupported) {
				t.Fatalf("Complete() = %v; want a *chat.SchemaUnsupportedError so main exits 2", err)
			}
			if !strings.Contains(err.Error(), s.URL) {
				t.Errorf("error = %v; want it to name the base URL so the user knows what to fix", err)
			}
			if !strings.Contains(err.Error(), "qwen2.5:14b") {
				t.Errorf("error = %v; want it to name the model", err)
			}
		})
	}
}

func TestA400WithoutTheSchemaIsAnOrdinaryFailure(t *testing.T) {
	// Not every 400 is a refusal of the constraint. Reporting one as such would
	// send the user off to change an endpoint that is fine.
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"context length exceeded"}}`)
	})
	c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second, Retries: 3})

	schema := chat.DocumentMetadataSchema([]string{"autre"})
	_, err := c.Complete(context.Background(), chat.Request{User: "hi", Schema: &schema})
	if err == nil {
		t.Fatal("Complete() = nil error")
	}
	var unsupported *chat.SchemaUnsupportedError
	if errors.As(err, &unsupported) {
		t.Errorf("a 400 about context length was read as a schema refusal: %v", err)
	}
}

func TestA400IsNotASchemaRefusalWhenNoSchemaWasSent(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"json_schema"}}`)
	})
	c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second, Retries: 3})

	_, err := c.Complete(context.Background(), chat.Request{User: "hi"})
	if err == nil {
		t.Fatal("Complete() = nil error")
	}
	var unsupported *chat.SchemaUnsupportedError
	if errors.As(err, &unsupported) {
		t.Errorf("a request that sent no schema was reported as a schema refusal: %v", err)
	}
}

// marshalled round-trips a value through JSON, so the test inspects what the
// endpoint will actually receive rather than the Go value that produced it.
func marshalled(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return out
}

func stringsOf(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
