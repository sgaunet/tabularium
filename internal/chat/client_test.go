package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/config"
)

// reply is the shape every OpenAI-compatible endpoint returns, and the tool
// reads exactly one field out of it.
func reply(content string) string {
	b, err := json.Marshal(map[string]any{
		"choices": []any{
			map[string]any{"message": map[string]any{"role": "assistant", "content": content}},
		},
	})
	if err != nil {
		panic("encoding a fixed test reply: " + err.Error())
	}
	return string(b)
}

// server records what it was sent and answers with handler.
type server struct {
	*httptest.Server

	requests atomic.Int32
	last     atomic.Pointer[recorded]
}

type recorded struct {
	path   string
	auth   string
	hasKey bool
	body   map[string]any
}

func newServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, n int)) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(s.requests.Add(1))

		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		auth, hasKey := r.Header["Authorization"]
		rec := &recorded{path: r.URL.Path, hasKey: hasKey, body: parsed}
		if hasKey {
			rec.auth = auth[0]
		}
		s.last.Store(rec)

		handler(w, r, n)
	}))
	t.Cleanup(s.Close)
	return s
}

func ok(content string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, _ *http.Request, _ int) {
		_, _ = io.WriteString(w, reply(content))
	}
}

func client(t *testing.T, o chat.Options) *chat.Client {
	t.Helper()
	c, err := chat.New(o)
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	chat.SetBackoffBase(c, time.Millisecond)
	return c
}

func TestPostsToChatCompletions(t *testing.T) {
	s := newServer(t, ok("hello"))
	c := client(t, chat.Options{BaseURL: s.URL + "/v1", Model: "m", Timeout: time.Second})

	got, err := c.Complete(context.Background(), chat.Request{User: "hi"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "hello" {
		t.Errorf("Complete() = %q, want %q", got, "hello")
	}
	if p := s.last.Load().path; p != "/v1/chat/completions" {
		t.Errorf("posted to %q, want %q", p, "/v1/chat/completions")
	}
}

func TestBaseURLTrailingSlashIsHarmless(t *testing.T) {
	s := newServer(t, ok("hello"))
	c := client(t, chat.Options{BaseURL: s.URL + "/v1/", Model: "m", Timeout: time.Second})

	if _, err := c.Complete(context.Background(), chat.Request{User: "hi"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if p := s.last.Load().path; p != "/v1/chat/completions" {
		t.Errorf("posted to %q, want %q — a trailing slash must not double up", p, "/v1/chat/completions")
	}
}

func TestAuthorizationHeaderOnlyWhenACredentialIsConfigured(t *testing.T) {
	// FR-012: local and remote differ by configuration alone. A local endpoint
	// with no credential must not be sent an empty Authorization header, which
	// several of them reject outright.
	t.Run("no credential means no header", func(t *testing.T) {
		s := newServer(t, ok("x"))
		c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second})

		if _, err := c.Complete(context.Background(), chat.Request{User: "hi"}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if s.last.Load().hasKey {
			t.Error("an Authorization header was sent with no credential configured")
		}
	})

	t.Run("a credential produces a bearer header", func(t *testing.T) {
		s := newServer(t, ok("x"))
		c := client(t, chat.Options{
			BaseURL: s.URL, Model: "m", Timeout: time.Second,
			APIKey: config.Credential("sk-test-value"),
		})

		if _, err := c.Complete(context.Background(), chat.Request{User: "hi"}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		got := s.last.Load()
		if !got.hasKey {
			t.Fatal("no Authorization header was sent with a credential configured")
		}
		if got.auth != "Bearer sk-test-value" {
			t.Errorf("Authorization = %q, want a bearer token", got.auth)
		}
	})
}

func TestAnImageIsSentAsADataURLImagePart(t *testing.T) {
	// research.md D5: a data: URL in an image_url content part is what Ollama,
	// llama.cpp, vLLM and the hosted APIs all accept.
	s := newServer(t, ok("transcribed"))
	c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second})

	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	_, err := c.Complete(context.Background(), chat.Request{
		User:   "transcribe this",
		Images: []chat.Image{{MediaType: "image/png", Data: png}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	raw, err := json.Marshal(s.last.Load().body)
	if err != nil {
		t.Fatalf("re-encoding the recorded request: %v", err)
	}
	sent := string(raw)

	if !strings.Contains(sent, `"image_url"`) {
		t.Errorf("the request has no image_url content part:\n%s", sent)
	}
	if !strings.Contains(sent, "data:image/png;base64,") {
		t.Errorf("the image was not sent as a base64 data URL:\n%s", sent)
	}
	if !strings.Contains(sent, "iVBORw0KGgo") { // the PNG magic, base64-encoded
		t.Errorf("the image bytes did not survive encoding:\n%s", sent)
	}
}

func TestTheModelIsSentVerbatim(t *testing.T) {
	s := newServer(t, ok("x"))
	c := client(t, chat.Options{BaseURL: s.URL, Model: "qwen2.5vl:7b", Timeout: time.Second})

	if _, err := c.Complete(context.Background(), chat.Request{User: "hi"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := s.last.Load().body["model"]; got != "qwen2.5vl:7b" {
		t.Errorf("model = %v, want %q", got, "qwen2.5vl:7b")
	}
}

func TestTimeoutIsExplicit(t *testing.T) {
	// FR-075: every I/O carries an explicit timeout. Unbounded I/O turns a
	// transient network fault into a hung terminal.
	blocked := make(chan struct{})
	s := newServer(t, func(_ http.ResponseWriter, r *http.Request, _ int) {
		select {
		case <-blocked:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(blocked) })

	c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: 50 * time.Millisecond})

	start := time.Now()
	_, err := c.Complete(context.Background(), chat.Request{User: "hi"})
	if err == nil {
		t.Fatal("Complete() against a server that never answers = nil error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Complete() took %v; the timeout was not applied", elapsed)
	}
}

func TestCancellingTheContextAbortsAnInFlightRequest(t *testing.T) {
	// Principle V: SIGINT must abort a model call, not wait it out.
	reached := make(chan struct{})
	blocked := make(chan struct{})
	s := newServer(t, func(_ http.ResponseWriter, r *http.Request, _ int) {
		close(reached)
		select {
		case <-blocked:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(blocked) })

	c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Minute})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-reached
		cancel()
	}()

	start := time.Now()
	_, err := c.Complete(ctx, chat.Request{User: "hi"})
	if err == nil {
		t.Fatal("Complete() with a cancelled context = nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Complete() = %v; want it to wrap context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Complete() took %v after cancellation", elapsed)
	}
}

func TestRetriesAreBoundedAtThree(t *testing.T) {
	// FR-076: bounded and backed off. Unbounded retries turn a transient fault
	// into an outage for whatever is on the other end.
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second, Retries: 3})

	if _, err := c.Complete(context.Background(), chat.Request{User: "hi"}); err == nil {
		t.Fatal("Complete() against a server that always fails = nil error")
	}
	if got := s.requests.Load(); got != 3 {
		t.Errorf("the endpoint saw %d requests, want exactly 3", got)
	}
}

func TestASuccessAfterATransientFailureIsNotAnError(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, reply("finally"))
	})
	c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second, Retries: 3})

	got, err := c.Complete(context.Background(), chat.Request{User: "hi"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "finally" {
		t.Errorf("Complete() = %q, want %q", got, "finally")
	}
}

func TestAClientErrorIsNeverRetried(t *testing.T) {
	// A 4xx other than 429 is a configuration error: retrying it wastes the
	// user's time and the endpoint's, and the answer will not change.
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized,
		http.StatusForbidden, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := newServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
				w.WriteHeader(status)
			})
			c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second, Retries: 3})

			if _, err := c.Complete(context.Background(), chat.Request{User: "hi"}); err == nil {
				t.Fatalf("Complete() against a %d = nil error", status)
			}
			if got := s.requests.Load(); got != 1 {
				t.Errorf("a %d was retried: the endpoint saw %d requests, want 1", status, got)
			}
		})
	}
}

func TestRetryClassifier(t *testing.T) {
	// The classifier reached through export_test.go, because the interesting
	// cases are cheaper to enumerate than to provoke.
	tests := []struct {
		name   string
		status int
		err    error
		want   bool
	}{
		{name: "a connection error is transient", status: 0, err: errors.New("dial tcp: connection refused"), want: true},
		{name: "429 is transient", status: http.StatusTooManyRequests, want: true},
		{name: "500 is transient", status: http.StatusInternalServerError, want: true},
		{name: "502 is transient", status: http.StatusBadGateway, want: true},
		{name: "503 is transient", status: http.StatusServiceUnavailable, want: true},
		{name: "504 is transient", status: http.StatusGatewayTimeout, want: true},
		{name: "400 is a configuration error", status: http.StatusBadRequest, want: false},
		{name: "401 is a configuration error", status: http.StatusUnauthorized, want: false},
		{name: "404 is a configuration error", status: http.StatusNotFound, want: false},
		{name: "422 is a configuration error", status: http.StatusUnprocessableEntity, want: false},
		{name: "501 is a configuration error", status: http.StatusNotImplemented, want: false},
		{name: "200 is not retried", status: http.StatusOK, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chat.Retryable(tt.status, tt.err); got != tt.want {
				t.Errorf("Retryable(%d, %v) = %v, want %v", tt.status, tt.err, got, tt.want)
			}
		})
	}
}

func TestBackoffScheduleIsOneTwoFourSeconds(t *testing.T) {
	// FR-076 names the schedule. The ceiling is exact and assertable; the wait
	// itself is jittered within it, which is what keeps a fleet of retries from
	// arriving together.
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	for attempt, w := range want {
		if got := chat.BackoffCeiling(attempt, time.Second); got != w {
			t.Errorf("BackoffCeiling(%d, 1s) = %v, want %v", attempt, got, w)
		}
	}
}

func TestAContextCancelledBetweenRetriesStopsImmediately(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	c, err := chat.New(chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second, Retries: 3})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	// A backoff long enough that the run would obviously wait it out.
	chat.SetBackoffBase(c, 30*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := c.Complete(ctx, chat.Request{User: "hi"}); err == nil {
		t.Fatal("Complete() = nil error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Complete() waited %v through the backoff after cancellation", elapsed)
	}
}

func TestAnEmptyChoicesListIsAnError(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		_, _ = io.WriteString(w, `{"choices":[]}`)
	})
	c := client(t, chat.Options{BaseURL: s.URL, Model: "m", Timeout: time.Second})

	if _, err := c.Complete(context.Background(), chat.Request{User: "hi"}); err == nil {
		t.Fatal("Complete() on a reply with no choices = nil error")
	}
}

func TestNewRejectsAnIncompleteConfiguration(t *testing.T) {
	tests := []struct {
		name string
		opts chat.Options
	}{
		{name: "no base URL", opts: chat.Options{Model: "m", Timeout: time.Second}},
		{name: "no model", opts: chat.Options{BaseURL: "http://x/v1", Timeout: time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := chat.New(tt.opts); err == nil {
				t.Error("chat.New() = nil error; want a configuration error")
			}
		})
	}
}
