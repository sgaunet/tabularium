package extract

import (
	"context"
	"fmt"
	"os"

	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/document"
)

// maxImageBytes bounds what is base64-encoded into a request body. Beyond this
// the endpoint would refuse it anyway, and failing here says why.
const maxImageBytes = 32 << 20

// extractImage sends a raster image straight to the multimodal model. There is
// no page structure to walk and nothing to rasterise: the file already is the
// picture (FR-009).
func extractImage(ctx context.Context, src document.Source, o Options) (Text, error) {
	vision, err := o.vision(src)
	if err != nil {
		return Text{}, err
	}
	if src.Size > maxImageBytes {
		return Text{}, fmt.Errorf("%s is %d bytes: too large to send to a vision model",
			src.Path, src.Size)
	}

	body, err := os.ReadFile(src.Path) //nolint:gosec // the document the user named
	if err != nil {
		return Text{}, fmt.Errorf("reading %s: %w", src.Path, err)
	}

	o.logger().Debug("transcribing an image", "path", src.Path, "type", src.Type)
	content, err := vision.Complete(ctx, chat.Request{
		User:   transcribePrompt,
		Images: []chat.Image{{MediaType: string(src.Type), Data: body}},
	})
	if err != nil {
		return Text{}, fmt.Errorf("transcribing %s: %w", src.Path, err)
	}
	return Text{Content: content, Origin: OriginVision}, nil
}
