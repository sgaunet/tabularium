package chat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/sgaunet/tabularium/internal/config"
)

// Options describes one endpoint. OCR and analysis each construct their own
// client, because FR-017 makes them independently configurable — a vision model
// on this machine and analysis wherever you like.
type Options struct {
	BaseURL string
	Model   string
	APIKey  config.Credential
	Timeout time.Duration
	Retries int
}

// Client speaks POST {base_url}/chat/completions to any OpenAI-compatible
// endpoint.
//
// Nothing here branches on whether the endpoint is local or remote: Ollama,
// llama.cpp's server, vLLM and the hosted APIs all expose the same route, and
// the only difference is the base URL and whether a credential is sent (FR-012).
type Client struct {
	baseURL string
	model   string
	apiKey  config.Credential
	retries int
	http    *http.Client

	// backoffBase is the first wait; each attempt doubles it. It is a field so
	// a test can shorten it, because seven seconds of sleeping proves nothing.
	backoffBase time.Duration
}

// defaultRetries matches FR-076 when the configuration is silent.
const defaultRetries = 3

// New builds a client. The timeout is explicit and mandatory: FR-075 admits no
// unbounded I/O, and an http.Client with a zero Timeout waits forever.
func New(o Options) (*Client, error) {
	if o.BaseURL == "" {
		return nil, errors.New("no base_url configured for the model endpoint")
	}
	if o.Model == "" {
		return nil, errors.New("no model configured for the endpoint " + o.BaseURL)
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	retries := o.Retries
	if retries <= 0 {
		retries = defaultRetries
	}

	return &Client{
		baseURL:     strings.TrimSuffix(o.BaseURL, "/"),
		model:       o.Model,
		apiKey:      o.APIKey,
		retries:     retries,
		http:        &http.Client{Timeout: timeout},
		backoffBase: time.Second,
	}, nil
}

// Endpoint is the configured base URL, for an error that needs to name it.
func (c *Client) Endpoint() string { return c.baseURL }

// Model is the configured model, likewise.
func (c *Client) Model() string { return c.model }

// Image is one picture to transcribe.
type Image struct {
	MediaType string
	Data      []byte
}

// Request is one of the two shapes this tool sends: a text-only message with a
// response format, or a multimodal message carrying page images.
type Request struct {
	System string
	User   string
	Images []Image
	Schema *Schema
}

// wire types. Only what the tool actually sends and reads is modelled; the rest
// of the API surface — streaming, batching, assistants, embeddings — is not
// touched, which is what makes a hand-written client the smaller choice.
type wireRequest struct {
	Model          string        `json:"model"`
	Messages       []wireMessage `json:"messages"`
	ResponseFormat *wireFormat   `json:"response_format,omitempty"`
}

type wireMessage struct {
	Role string `json:"role"`
	// Content is either a plain string or a list of parts. Both spellings are
	// accepted everywhere; a string is used when there are no images because
	// some local servers only understand that form.
	Content any `json:"content"`
}

type wirePart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

type wireImageURL struct {
	URL string `json:"url"`
}

type wireFormat struct {
	Type       string `json:"type"`
	JSONSchema Schema `json:"json_schema"`
}

type wireResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Complete sends one request and returns choices[0].message.content — the only
// field of the response this tool reads.
func (c *Client) Complete(ctx context.Context, req Request) (string, error) {
	body, err := json.Marshal(c.wire(req))
	if err != nil {
		return "", fmt.Errorf("encoding the request: %w", err)
	}

	var lastErr error
	for attempt := range c.retries {
		if attempt > 0 {
			if err := c.wait(ctx, attempt-1); err != nil {
				return "", err
			}
		}

		content, status, err := c.attempt(ctx, body)
		switch {
		case err == nil:
			return content, nil
		case ctx.Err() != nil:
			// A cancelled context is the user's SIGINT, not a transient fault.
			return "", fmt.Errorf("%s: %w", c.baseURL, ctx.Err())
		}

		lastErr = err
		if !retryable(status, err) {
			// A refusal of the schema is a configuration problem the user can
			// fix, and saying so is worth more than the transport error.
			if req.Schema != nil {
				if reason, ok := schemaRefusal(status, err); ok {
					return "", &SchemaUnsupportedError{
						BaseURL: c.baseURL, Model: c.model, Reason: reason,
					}
				}
			}
			return "", err
		}
	}
	return "", fmt.Errorf("%s: giving up after %d attempts: %w", c.baseURL, c.retries, lastErr)
}

func (c *Client) wire(req Request) wireRequest {
	out := wireRequest{Model: c.model}

	if req.System != "" {
		out.Messages = append(out.Messages, wireMessage{Role: "system", Content: req.System})
	}

	if len(req.Images) == 0 {
		out.Messages = append(out.Messages, wireMessage{Role: "user", Content: req.User})
	} else {
		parts := make([]wirePart, 0, len(req.Images)+1)
		if req.User != "" {
			parts = append(parts, wirePart{Type: "text", Text: req.User})
		}
		for _, img := range req.Images {
			// A data: URL in an image_url part is the one image encoding every
			// OpenAI-compatible server accepts (research.md D5).
			url := "data:" + img.MediaType + ";base64," +
				base64.StdEncoding.EncodeToString(img.Data)
			parts = append(parts, wirePart{Type: "image_url", ImageURL: &wireImageURL{URL: url}})
		}
		out.Messages = append(out.Messages, wireMessage{Role: "user", Content: parts})
	}

	if req.Schema != nil {
		out.ResponseFormat = &wireFormat{Type: "json_schema", JSONSchema: *req.Schema}
	}
	return out
}

// httpError carries the status and body of a failed response, so the caller can
// classify it without a second round trip.
type httpError struct {
	Status int
	Body   string
	URL    string
}

func (e *httpError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 512 {
		body = body[:512] + "…"
	}
	if body == "" {
		return fmt.Sprintf("%s: HTTP %d %s", e.URL, e.Status, http.StatusText(e.Status))
	}
	return fmt.Sprintf("%s: HTTP %d %s: %s", e.URL, e.Status, http.StatusText(e.Status), body)
}

// attempt makes one request. The status is returned alongside the error so the
// retry classifier does not have to unwrap it.
func (c *Client) attempt(ctx context.Context, body []byte) (string, int, error) {
	url := c.baseURL + "/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("building the request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// Only when a credential is configured: a local endpoint with no auth
	// rejects an empty Authorization header outright.
	if key := c.apiKey.Secret(); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", 0, fmt.Errorf("%s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Enough of the body to explain a failure, and not enough to be a weapon.
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, &httpError{Status: resp.StatusCode, Body: string(payload), URL: url}
	}
	if readErr != nil {
		return "", resp.StatusCode, fmt.Errorf("%s: reading the response: %w", url, readErr)
	}

	var parsed wireResponse
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", resp.StatusCode, fmt.Errorf("%s: the response is not JSON: %w", url, err)
	}
	if len(parsed.Choices) == 0 {
		return "", resp.StatusCode, fmt.Errorf("%s: the response carried no choices", url)
	}
	return parsed.Choices[0].Message.Content, resp.StatusCode, nil
}

// retryable reports whether a failed attempt is worth repeating.
//
// Connection errors, 429 and 5xx are transient. Every other 4xx is a
// configuration error: the answer will not change, and retrying it wastes the
// user's time and the endpoint's alike.
func retryable(status int, err error) bool {
	if status == 0 && err != nil {
		return true // a transport failure: never reached the endpoint
	}
	if status == http.StatusTooManyRequests {
		return true
	}
	// 501 sits inside the 5xx range but is not transient: it says the endpoint
	// does not implement this at all, and it will say so again on every retry.
	if status == http.StatusNotImplemented {
		return false
	}
	return status >= 500 && status <= 599
}

// backoffCeiling is the upper bound of the wait before the given attempt.
func backoffCeiling(attempt int, base time.Duration) time.Duration {
	return base << attempt
}

// wait sleeps with full jitter — uniformly within the ceiling rather than at
// it, so a fleet of retries does not arrive together.
func (c *Client) wait(ctx context.Context, attempt int) error {
	ceiling := backoffCeiling(attempt, c.backoffBase)
	d := time.Duration(rand.Int64N(int64(ceiling))) //nolint:gosec // jitter, not a secret

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", c.baseURL, ctx.Err())
	case <-timer.C:
		return nil
	}
}
