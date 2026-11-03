package chat_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/chat"
)

// tracing builds a client whose prompt trace goes to the returned buffer.
func tracing(t *testing.T, url string, level slog.Level, trace *bytes.Buffer) *chat.Client {
	t.Helper()
	return client(t, chat.Options{
		BaseURL: url, Model: "m", Timeout: time.Second,
		Log:   slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: level})),
		Trace: trace,
	})
}

func TestBothPromptsAreTracedLineByLine(t *testing.T) {
	// Asked what the tool sends the model, the only answer used to be "read the
	// source". At --verbose the request says so itself.
	var trace bytes.Buffer
	s := newServer(t, ok("{}"))
	c := tracing(t, s.URL, slog.LevelDebug, &trace)

	_, err := c.Complete(context.Background(), chat.Request{
		System: "You extract metadata.\nAnswer with JSON.",
		User:   "Document text:\n\nhello",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	for _, want := range []string{
		"system| You extract metadata.",
		"system| Answer with JSON.",
		"user  | Document text:",
		"user  | hello",
	} {
		if !strings.Contains(trace.String(), want) {
			t.Errorf("the trace does not carry %q:\n%s", want, trace.String())
		}
	}
}

func TestAnImageIsTracedAsACountAndNeverAsItsBytes(t *testing.T) {
	// The data URL runs to 32 MiB. A count is what a reader can act on; the
	// base64 is what would fill their terminal.
	var trace bytes.Buffer
	s := newServer(t, ok("transcribed"))
	c := tracing(t, s.URL, slog.LevelDebug, &trace)

	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	_, err := c.Complete(context.Background(), chat.Request{
		User:   "Transcribe this image.",
		Images: []chat.Image{{MediaType: "image/png", Data: png}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if !strings.Contains(trace.String(), "user  | Transcribe this image.") {
		t.Errorf("the trace does not carry the prompt:\n%s", trace.String())
	}
	for _, never := range []string{"data:image/png;base64,", "iVBORw0KGgo"} {
		if strings.Contains(trace.String(), never) {
			t.Errorf("the image bytes reached the trace (%q):\n%s", never, trace.String())
		}
	}
}

func TestALongPromptKeepsItsHeadAndItsTail(t *testing.T) {
	// The analysis prompt puts the document first and the vocabulary that
	// decides the answer last, so cutting the tail would hide the very part
	// worth reading. The middle goes instead, and the cut is marked.
	var trace bytes.Buffer
	s := newServer(t, ok("{}"))
	c := tracing(t, s.URL, slog.LevelDebug, &trace)

	user := "FIRST-LINE\n" + strings.Repeat("padding text here\n", 2000) + "LAST-LINE"
	if _, err := c.Complete(context.Background(), chat.Request{User: user}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got := trace.String()
	for _, want := range []string{"user  | FIRST-LINE", "user  | LAST-LINE", "elided"} {
		if !strings.Contains(got, want) {
			t.Errorf("the trace does not carry %q:\n%s", want, got[:min(len(got), 400)])
		}
	}
	if len(got) >= len(user) {
		t.Errorf("the trace is %d bytes for a %d-byte prompt; nothing was elided",
			len(got), len(user))
	}
}

func TestNothingIsTracedBelowDebug(t *testing.T) {
	// --verbose moves the level; it does not gate this call site.
	var trace bytes.Buffer
	s := newServer(t, ok("{}"))
	c := tracing(t, s.URL, slog.LevelInfo, &trace)

	if _, err := c.Complete(context.Background(), chat.Request{User: "hello"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if trace.Len() != 0 {
		t.Errorf("the prompt was traced at the default level:\n%s", trace.String())
	}
}

func TestWithoutATraceWriterNothingIsWritten(t *testing.T) {
	s := newServer(t, ok("{}"))
	c := client(t, chat.Options{
		BaseURL: s.URL, Model: "m", Timeout: time.Second,
		Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	if _, err := c.Complete(context.Background(), chat.Request{User: "hello"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}
