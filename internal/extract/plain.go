package extract

import (
	"fmt"
	"os"

	"github.com/sgaunet/tabularium/internal/document"
)

// maxPlainBytes bounds a "text file" that turns out not to be one. 32 MiB of
// plain text is far more than any document, and far less than a file that would
// exhaust memory.
const maxPlainBytes = 32 << 20

// extractPlain reads text and Markdown directly. No model call, no host tool:
// the text is already the text (FR-011).
func extractPlain(src document.Source) (Text, error) {
	if src.Size > maxPlainBytes {
		return Text{}, fmt.Errorf("%s is %d bytes: too large to read as plain text",
			src.Path, src.Size)
	}

	body, err := os.ReadFile(src.Path) //nolint:gosec // the document the user named
	if err != nil {
		return Text{}, fmt.Errorf("reading %s: %w", src.Path, err)
	}
	return Text{Content: string(body), Origin: OriginPlain}, nil
}
