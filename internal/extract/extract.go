package extract

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/document"
)

// Origin records how the text was obtained. The spec insists on carrying it
// because "text-layer" is the observable form of SC-001: it means the run cost
// zero model calls.
type Origin string

// The four ways text is obtained.
const (
	// OriginTextLayer — the PDF already carried its text (FR-006).
	OriginTextLayer Origin = "text-layer"
	// OriginVision — the page was rasterised and transcribed (FR-007, FR-009).
	OriginVision Origin = "vision"
	// OriginOffice — read out of an OOXML, ODF or legacy container (FR-010).
	OriginOffice Origin = "office"
	// OriginPlain — text or Markdown, read directly (FR-011).
	OriginPlain Origin = "plain"
)

// Truncation records pages that were not processed.
//
// It is carried as a pointer so that "nothing was dropped" cannot be confused
// with "truncated to zero pages" — the distinction FR-008 and SC-010 turn on.
type Truncation struct {
	PagesProcessed int
	PagesTotal     int
}

// Text is the extracted text and its provenance.
type Text struct {
	Content    string
	Origin     Origin
	Truncation *Truncation
}

// Options configures extraction.
type Options struct {
	// Vision transcribes rasterised pages and raster images. It may be nil
	// when no document needing it will be processed; a document that does need
	// it then fails by name rather than by nil dereference.
	Vision *chat.Client

	// MaxPages caps how many pages of a PDF are processed. Pages beyond it are
	// dropped and the fact is recorded — never silently (FR-008).
	MaxPages int

	// DPI is the rasterisation resolution. 200 is the usual floor for reliable
	// OCR of 10pt text without inflating the image past what a local vision
	// model will accept.
	DPI int

	Log *slog.Logger
}

func (o Options) logger() *slog.Logger {
	if o.Log != nil {
		return o.Log
	}
	return slog.New(slog.DiscardHandler)
}

// MissingToolError is a host tool that is not on PATH.
//
// It names the binary and the package that provides it, because "command not
// found" leaves the user to work out which of poppler's dozen binaries they are
// missing and which package ships it (FR-013, FR-060).
type MissingToolError struct {
	Binary  string
	Package string
}

func (e *MissingToolError) Error() string {
	if e.Package == "" {
		return e.Binary + " is not installed or not on PATH"
	}
	return fmt.Sprintf("%s is not installed or not on PATH: install %s",
		e.Binary, e.Package)
}

// NotConfiguredError is a document that needs a model the configuration does
// not describe.
//
// It names the settings to add, and it will say the same thing on every retry —
// so it is a configuration error the caller maps to exit 2, not a runtime fault
// to try again.
type NotConfiguredError struct {
	Path     string
	Kind     document.Kind
	Settings []string
}

func (e *NotConfiguredError) Error() string {
	return fmt.Sprintf("%s needs a vision model to be read, and none is configured: set %s",
		e.Path, strings.Join(e.Settings, " and "))
}

// EmptyError is a document from which no text was recovered.
//
// It is an explicit failure and the document is not filed: a blank page must
// not become metadata invented from nothing (FR-015).
type EmptyError struct {
	Path   string
	Origin Origin
}

func (e *EmptyError) Error() string {
	return fmt.Sprintf("%s: no text could be recovered (via %s): "+
		"nothing can be inferred from it, so it has not been filed", e.Path, e.Origin)
}

// need looks a host tool up before its first use, so a PDF-free run never
// touches poppler and a run that does need it fails early and by name.
func need(binary, pkg string) (string, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", &MissingToolError{Binary: binary, Package: pkg}
	}
	return path, nil
}

// Extract recovers the text of src by whatever strategy its kind requires.
//
// Every path here is bounded and cancellable: a host tool runs under the
// context, a model call carries its own timeout, and an archive is read under
// three independent limits.
func Extract(ctx context.Context, src document.Source, o Options) (Text, error) {
	if err := ctx.Err(); err != nil {
		return Text{}, err
	}

	var (
		text Text
		err  error
	)
	switch {
	case src.Type == document.PDF:
		text, err = extractPDF(ctx, src, o)
	case src.Type.Image():
		text, err = extractImage(ctx, src, o)
	case src.Type == document.OLE2:
		text, err = extractLegacyOffice(ctx, src, o)
	case src.Type.Office():
		text, err = extractOpenOffice(src)
	case src.Type == document.Text:
		text, err = extractPlain(src)
	default:
		// document.Open refuses an unsupported kind, so reaching here means the
		// supported set and this switch have drifted apart.
		return Text{}, fmt.Errorf("%s: no extraction strategy for %q", src.Path, src.Type)
	}
	if err != nil {
		return Text{}, err
	}

	if strings.TrimSpace(text.Content) == "" {
		return Text{}, &EmptyError{Path: src.Path, Origin: text.Origin}
	}
	return text, nil
}

// vision returns the configured vision client, or an error naming what is
// missing rather than panicking on a nil pointer.
func (o Options) vision(src document.Source) (*chat.Client, error) {
	if o.Vision == nil {
		return nil, &NotConfiguredError{
			Path:     src.Path,
			Kind:     src.Type,
			Settings: []string{"ocr.base_url", "ocr.model"},
		}
	}
	return o.Vision, nil
}
