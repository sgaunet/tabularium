package extract

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/document"
)

// minTextLayerRunes is the threshold below which a PDF is treated as carrying
// no usable text layer.
//
// A scanned page yields zero or a stray ligature; a born-digital page yields
// hundreds. 64 sits in the wide gap between them, so the classification is not
// a close call in either direction.
const minTextLayerRunes = 64

// pageBreak is what pdftotext emits after each page. Counting these is how the
// page total is known without a third poppler binary.
const pageBreak = '\f'

// transcribePrompt asks for the page's text and nothing else.
const transcribePrompt = `Transcribe all of the text in this image, exactly as it appears.

Preserve the reading order and the line breaks. Do not summarise, do not
translate, do not describe the layout, and do not add any commentary. If the
image contains no text, answer with nothing at all.`

// extractPDF prefers the text layer and rasterises only when there is not one.
//
// The text-layer pass runs over the whole document even when the page cap will
// drop most of it: pdftotext does no rendering, so it is cheap, and it is what
// makes the page *total* known — which FR-008 requires in order to report the
// truncation rather than silently perform it.
func extractPDF(ctx context.Context, src document.Source, o Options) (Text, error) {
	pdftotext, err := need("pdftotext", "poppler-utils")
	if err != nil {
		return Text{}, err
	}

	layer, err := runTool(ctx, pdftotext, "-q", src.Path, "-")
	if err != nil {
		return Text{}, fmt.Errorf("reading the text layer of %s: %w", src.Path, err)
	}

	pages := splitPages(layer)
	total := len(pages)
	processed, truncation := capPages(total, o.MaxPages)

	if usableTextLayer(pages[:processed]) {
		o.logger().Debug("using the PDF text layer",
			"path", src.Path, "pages", processed, "of", total)
		return Text{
			Content:    strings.Join(pages[:processed], "\n"),
			Origin:     OriginTextLayer,
			Truncation: truncation,
		}, nil
	}

	o.logger().Debug("no usable text layer, rasterising",
		"path", src.Path, "pages", processed, "of", total, "dpi", o.DPI)
	return rasterise(ctx, src, o, processed, truncation)
}

// splitPages turns pdftotext's output into one string per page.
//
// pdftotext writes a form feed after every page including the last, so the
// split leaves one trailing empty element, which is dropped. A document with no
// form feeds at all is one page.
func splitPages(out string) []string {
	pages := strings.Split(out, string(pageBreak))
	if n := len(pages); n > 0 && strings.TrimSpace(pages[n-1]) == "" {
		pages = pages[:n-1]
	}
	if len(pages) == 0 {
		return []string{""}
	}
	return pages
}

// capPages applies the page cap and records what it dropped.
func capPages(total, maxPages int) (processed int, truncation *Truncation) {
	if maxPages <= 0 || total <= maxPages {
		return total, nil
	}
	return maxPages, &Truncation{PagesProcessed: maxPages, PagesTotal: total}
}

// usableTextLayer reports whether the pages carry enough text to be worth
// using.
//
// Whitespace and the form feeds pdftotext emits are stripped first: a scanned
// PDF's "text layer" is typically nothing but those.
func usableTextLayer(pages []string) bool {
	n := 0
	for _, page := range pages {
		for _, r := range page {
			if !unicode.IsSpace(r) {
				n++
				if n >= minTextLayerRunes {
					return true
				}
			}
		}
	}
	return false
}

// rasterise renders each page and sends it to the vision model, one call per
// page, concatenated in page order.
func rasterise(ctx context.Context, src document.Source, o Options,
	pages int, truncation *Truncation,
) (Text, error) {
	pdftoppm, err := need("pdftoppm", "poppler-utils")
	if err != nil {
		return Text{}, err
	}
	vision, err := o.vision(src)
	if err != nil {
		return Text{}, err
	}

	scratch, err := os.MkdirTemp("", "tabularium-raster-")
	if err != nil {
		return Text{}, fmt.Errorf("creating a scratch directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	var out strings.Builder
	for page := 1; page <= pages; page++ {
		if err := ctx.Err(); err != nil {
			return Text{}, err
		}

		png, err := renderPage(ctx, pdftoppm, src.Path, scratch, page, o.DPI)
		if err != nil {
			return Text{}, err
		}

		content, err := vision.Complete(ctx, chat.Request{
			User:   transcribePrompt,
			Images: []chat.Image{{MediaType: "image/png", Data: png}},
		})
		if err != nil {
			return Text{}, fmt.Errorf("transcribing page %d of %s: %w", page, src.Path, err)
		}

		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(strings.TrimSpace(content))
		o.logger().Debug("transcribed a page", "path", src.Path, "page", page, "of", pages)
	}

	return Text{Content: out.String(), Origin: OriginVision, Truncation: truncation}, nil
}

// defaultDPI is used when the configuration is silent.
const defaultDPI = 200

// renderPage rasterises one page and returns the PNG bytes.
//
// Each page renders into its own directory and the result is found by globbing,
// because pdftoppm zero-pads the page number in the output name according to
// the document's total page count — so predicting the name means duplicating
// poppler's arithmetic.
func renderPage(ctx context.Context, pdftoppm, src, scratch string, page, dpi int) ([]byte, error) {
	if dpi <= 0 {
		dpi = defaultDPI
	}

	dir := filepath.Join(scratch, fmt.Sprintf("p%d", page))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating a scratch directory: %w", err)
	}

	num := strconv.Itoa(page)
	if _, err := runTool(ctx, pdftoppm, "-png", "-r", strconv.Itoa(dpi),
		"-f", num, "-l", num, src, filepath.Join(dir, "page")); err != nil {
		return nil, fmt.Errorf("rasterising page %d of %s: %w", page, src, err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.png"))
	if err != nil || len(matches) == 0 {
		return nil, fmt.Errorf("rasterising page %d of %s produced no image", page, src)
	}

	png, err := os.ReadFile(matches[0]) //nolint:gosec // a path this function just created
	if err != nil {
		return nil, fmt.Errorf("reading the rasterised page %d: %w", page, err)
	}
	return png, nil
}

// runTool runs a host tool under the context and returns its stdout.
//
// The context is what makes SIGINT abort a long rasterisation, and stderr is
// folded into the error so poppler's own diagnosis reaches the user.
func runTool(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s: %w: %s", filepath.Base(name), err, msg)
		}
		return "", fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return stdout.String(), nil
}
