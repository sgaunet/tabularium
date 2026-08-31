package extract_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/extract"
)

func TestARasterImageGoesStraightToTheModel(t *testing.T) {
	// FR-009: there is no page structure to walk and nothing to rasterise. The
	// file already is the picture.
	tests := []struct {
		name string
		file string
	}{
		{name: "JPEG", file: "receipt.jpg"},
		{name: "PNG", file: "receipt.png"},
		{name: "little-endian TIFF", file: "little-endian.tiff"},
		{name: "big-endian TIFF", file: "big-endian.tiff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vision := stubVision(t, "Ticket de caisse, 12.40 EUR, le 2025-02-02.")

			got, err := extract.Extract(context.Background(), corpus(t, tt.file), opts(vision))
			if err != nil {
				t.Fatalf("Extract(%s): %v", tt.file, err)
			}
			if got.Origin != extract.OriginVision {
				t.Errorf("Origin = %q, want %q", got.Origin, extract.OriginVision)
			}
			if n := vision.calls.Load(); n != 1 {
				t.Errorf("the vision endpoint was called %d times for one image, want 1", n)
			}
			if !strings.Contains(got.Content, "Ticket de caisse") {
				t.Errorf("the transcription did not come through:\n%s", got.Content)
			}
		})
	}
}

func TestTheImageIsSentWithItsOwnDetectedMediaType(t *testing.T) {
	// The media type comes from sniffing, not from the extension: sending a
	// TIFF labelled image/jpeg is how an endpoint ends up refusing it.
	var body string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		reply, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "some text"}}},
		})
		_, _ = w.Write(reply)
	}))
	t.Cleanup(s.Close)

	c, err := chat.New(chat.Options{BaseURL: s.URL, Model: "stub", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}

	o := extract.Options{Vision: c, MaxPages: 20, DPI: 72}
	if _, err := extract.Extract(context.Background(), corpus(t, "little-endian.tiff"), o); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if !strings.Contains(body, "data:image/tiff;base64,") {
		t.Errorf("the image was not sent as a TIFF data URL:\n%s", body)
	}
}

func TestTheImageBytesArriveIntact(t *testing.T) {
	var body string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		reply, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "some text"}}},
		})
		_, _ = w.Write(reply)
	}))
	t.Cleanup(s.Close)

	c, err := chat.New(chat.Options{BaseURL: s.URL, Model: "stub", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}

	src := corpus(t, "receipt.png")
	o := extract.Options{Vision: c, MaxPages: 20, DPI: 72}
	if _, err := extract.Extract(context.Background(), src, o); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	const marker = "data:image/png;base64,"
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no PNG data URL in the request:\n%s", body)
	}
	rest := body[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		t.Fatal("the data URL is unterminated")
	}

	decoded, err := base64.StdEncoding.DecodeString(rest[:j])
	if err != nil {
		t.Fatalf("decoding the data URL: %v", err)
	}
	if int64(len(decoded)) != src.Size {
		t.Errorf("the endpoint received %d bytes, want the file's %d", len(decoded), src.Size)
	}
}
