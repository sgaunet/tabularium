package extract

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sgaunet/tabularium/internal/document"
)

// The three independent bounds of FR-014. Any one alone is defeatable, which is
// why there are three.
const (
	// maxEntries refuses an archive that declares an absurd number of members,
	// before a single one is read.
	maxEntries = 1024

	// maxDecompressed bounds the CUMULATIVE bytes actually read. A limit on
	// each entry alone is defeated by many entries, and a limit on the declared
	// size is defeated by declaring a smaller one — the header is under the
	// attacker's control, the bytes coming out of the reader are not.
	maxDecompressed = 64 << 20 // 64 MiB
)

// BombError is an archive that exceeded one of the extraction bounds.
type BombError struct {
	Path   string
	Reason string
}

func (e *BombError) Error() string {
	return fmt.Sprintf("%s: refusing to read this archive: %s", e.Path, e.Reason)
}

// extractOpenOffice reads OOXML and ODF with the standard library alone.
//
// Depth is fixed at one: a nested archive is never entered, which is the third
// bound and the one that needs no arithmetic to enforce.
func extractOpenOffice(src document.Source) (Text, error) {
	r, err := zip.OpenReader(src.Path)
	if err != nil {
		return Text{}, fmt.Errorf("opening %s: %w", src.Path, err)
	}
	defer func() { _ = r.Close() }()

	if len(r.File) > maxEntries {
		return Text{}, &BombError{
			Path:   src.Path,
			Reason: fmt.Sprintf("it declares %d entries, and the limit is %d", len(r.File), maxEntries),
		}
	}

	wanted := partsFor(src.Type)
	budget := int64(maxDecompressed)

	var out strings.Builder
	for _, f := range r.File {
		if !wanted(f.Name) {
			continue
		}
		text, used, err := readPart(src.Path, f, budget)
		if err != nil {
			return Text{}, err
		}
		budget -= used
		if text == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(text)
	}

	return Text{Content: out.String(), Origin: OriginOffice}, nil
}

// partsFor names the members worth reading for each format. Everything else in
// the package — styles, themes, images, relationships — is skipped, which is
// both faster and a smaller attack surface.
func partsFor(kind document.Kind) func(name string) bool {
	switch kind {
	case document.OOXMLWord:
		return func(n string) bool { return n == "word/document.xml" }
	case document.OOXMLSheet:
		return func(n string) bool {
			return n == "xl/sharedStrings.xml" ||
				(strings.HasPrefix(n, "xl/worksheets/") && strings.HasSuffix(n, ".xml"))
		}
	case document.OOXMLSlides:
		return func(n string) bool {
			return strings.HasPrefix(n, "ppt/slides/slide") && strings.HasSuffix(n, ".xml")
		}
	case document.ODFText, document.ODFSheet, document.ODFSlides:
		return func(n string) bool { return n == "content.xml" }
	default:
		return func(string) bool { return false }
	}
}

// readPart streams one archive member, collecting character data and stopping
// the moment the shared budget is exhausted.
func readPart(archive string, f *zip.File, budget int64) (text string, used int64, err error) {
	if budget <= 0 {
		return "", 0, &BombError{
			Path:   archive,
			Reason: fmt.Sprintf("its members decompress to more than %d MiB", maxDecompressed>>20),
		}
	}

	rc, err := f.Open()
	if err != nil {
		return "", 0, fmt.Errorf("%s: reading %s: %w", archive, f.Name, err)
	}
	defer func() { _ = rc.Close() }()

	// One byte past the budget, so exhaustion is detectable rather than merely
	// coincident with the end of the data.
	counted := &countingReader{r: io.LimitReader(rc, budget+1)}

	out, err := collectCharData(counted)
	if counted.n > budget {
		return "", counted.n, &BombError{
			Path: archive,
			Reason: fmt.Sprintf("%s alone decompresses past the %d MiB limit",
				f.Name, maxDecompressed>>20),
		}
	}
	if err != nil {
		return "", counted.n, fmt.Errorf("%s: parsing %s: %w", archive, f.Name, err)
	}
	return out, counted.n, nil
}

// countingReader records how much was actually read, which is the only number
// an archive cannot lie about.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// collectCharData walks the XML and keeps the text.
//
// Streaming with a token reader rather than unmarshalling into a struct: the
// three formats put their runs in different elements, and every one of them
// puts the words in character data.
func collectCharData(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	// Entity expansion is where XML parsers go wrong. The standard library does
	// not expand external entities and has no DTD support to abuse, and leaving
	// Entity nil keeps it that way.
	dec.Strict = false

	var (
		out   strings.Builder
		depth int
	)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out.String(), err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			// A paragraph or a table cell is a line break in the output;
			// without this a whole document arrives as one run-on line.
			if isBlock(t.Name.Local) && out.Len() > 0 {
				out.WriteString("\n")
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth > 0 {
				out.Write(t)
			}
		}
	}
	return out.String(), nil
}

// isBlock names the elements that end a line in every format handled here.
func isBlock(local string) bool {
	switch local {
	case "p", "br", "tab", "tr", "row", "table-row", "line-break":
		return true
	default:
		return false
	}
}

// extractLegacyOffice converts .doc, .xls and .ppt through libreoffice.
//
// This mirrors the precedent poppler already sets: a host tool, named in
// --help, reported by name when missing, never bundled. It costs zero Go
// dependencies and keeps the common paths pure standard library.
func extractLegacyOffice(ctx context.Context, src document.Source, o Options) (Text, error) {
	soffice, err := lookLibreOffice()
	if err != nil {
		return Text{}, err
	}

	scratch, err := os.MkdirTemp("", "tabularium-office-")
	if err != nil {
		return Text{}, fmt.Errorf("creating a scratch directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	if _, err := runTool(ctx, soffice, "--headless", "--convert-to", "txt:Text",
		"--outdir", scratch, src.Path); err != nil {
		return Text{}, fmt.Errorf("converting %s: %w", src.Path, err)
	}

	matches, err := filepath.Glob(filepath.Join(scratch, "*.txt"))
	if err != nil || len(matches) == 0 {
		return Text{}, fmt.Errorf("converting %s produced no text", src.Path)
	}

	body, err := os.ReadFile(matches[0]) //nolint:gosec // a path this function just created
	if err != nil {
		return Text{}, fmt.Errorf("reading the converted %s: %w", src.Path, err)
	}

	o.logger().Debug("converted a legacy office document", "path", src.Path)
	return Text{Content: string(body), Origin: OriginOffice}, nil
}

// lookLibreOffice accepts either spelling. Debian installs `libreoffice`,
// several other distributions and the macOS bundle install `soffice`, and a
// user with one and not the other has libreoffice.
func lookLibreOffice() (string, error) {
	for _, name := range []string{"libreoffice", "soffice"} {
		if path, err := need(name, ""); err == nil {
			return path, nil
		}
	}
	return "", &MissingToolError{Binary: "libreoffice", Package: "libreoffice"}
}
