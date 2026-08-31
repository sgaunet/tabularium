package document

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Kind is the MIME type detected from the file's content.
//
// It is kept as the detected string rather than mapped to an internal
// enumeration, because FR-004 requires an unsupported file's error to name what
// was actually found — and "unsupported file" tells a user nothing, while
// "application/zip" tells them their .docx is a plain archive.
type Kind string

// The supported set.
const (
	PDF  Kind = "application/pdf"
	JPEG Kind = "image/jpeg"
	PNG  Kind = "image/png"
	TIFF Kind = "image/tiff"
	WebP Kind = "image/webp"

	OOXMLWord   Kind = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	OOXMLSheet  Kind = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	OOXMLSlides Kind = "application/vnd.openxmlformats-officedocument.presentationml.presentation"

	ODFText   Kind = "application/vnd.oasis.opendocument.text"
	ODFSheet  Kind = "application/vnd.oasis.opendocument.spreadsheet"
	ODFSlides Kind = "application/vnd.oasis.opendocument.presentation"

	// OLE2 is the Compound File Binary container behind legacy .doc, .xls and
	// .ppt. The three are not distinguished here: libreoffice converts them all
	// the same way, and the distinction would buy nothing.
	OLE2 Kind = "application/x-ole-storage"

	Text Kind = "text/plain"
)

var supported = map[Kind]struct{}{
	PDF: {}, JPEG: {}, PNG: {}, TIFF: {}, WebP: {},
	OOXMLWord: {}, OOXMLSheet: {}, OOXMLSlides: {},
	ODFText: {}, ODFSheet: {}, ODFSlides: {},
	OLE2: {}, Text: {},
}

// Supported reports whether the tool can extract text from this kind.
func (k Kind) Supported() bool {
	_, ok := supported[k]
	return ok
}

// Image reports whether the kind goes straight to the vision model.
func (k Kind) Image() bool {
	switch k {
	case JPEG, PNG, TIFF, WebP:
		return true
	default:
		return false
	}
}

// Office reports whether the kind is a word-processor, spreadsheet or
// presentation document.
func (k Kind) Office() bool {
	switch k {
	case OOXMLWord, OOXMLSheet, OOXMLSlides, ODFText, ODFSheet, ODFSlides, OLE2:
		return true
	default:
		return false
	}
}

// Source is the submitted file, identified by its content.
type Source struct {
	Path   string
	Type   Kind
	Size   int64
	Digest [32]byte
	Ext    string
}

// HexDigest is the digest as the sidecar and the JSON output record it.
func (s Source) HexDigest() string { return hex.EncodeToString(s.Digest[:]) }

// SourceError is a problem with the file the user named: it is missing, it is
// not a regular file, it cannot be read, or it is empty.
//
// Every failure Open can produce is one of these or an UnsupportedError, and
// both are the user's mistake rather than a runtime fault — which is what makes
// the whole of this package's error surface exit 2 (FR-004, FR-005).
type SourceError struct {
	Path string
	Err  error
}

func (e *SourceError) Error() string {
	return fmt.Sprintf("%s: %v", e.Path, e.Err)
}

func (e *SourceError) Unwrap() error { return e.Err }

// UnsupportedError is a file whose type was detected but is not one this tool
// can read. It carries the detected type so the message can name it.
type UnsupportedError struct {
	Path string
	Type Kind
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("%s: unsupported document type %q", e.Path, e.Type)
}

// sniffLen is what http.DetectContentType reads. Reading exactly that much
// keeps the two refinements below working on the same buffer.
const sniffLen = 512

// Open identifies the file at path: what it is, how big it is, and the digest
// every later stage reuses.
//
// The caller maps any error from here to exit 2. All of them are the user's
// mistake rather than a runtime fault: the file is missing, is not a file, is
// unreadable, or is of a type the tool does not handle (FR-004, FR-005).
func Open(path string) (Source, error) {
	fail := func(err error) error { return &SourceError{Path: path, Err: err} }

	abs, err := filepath.Abs(path)
	if err != nil {
		return Source{}, fail(err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return Source{}, fail(err)
	}
	// One check for directories, devices, sockets and FIFOs alike.
	if !info.Mode().IsRegular() {
		return Source{}, fail(errors.New("not a regular file"))
	}
	if info.Size() == 0 {
		return Source{}, fail(errors.New("the file is empty: there is nothing to read"))
	}

	f, err := os.Open(abs) //nolint:gosec // the path is the document the user named
	if err != nil {
		return Source{}, fail(err)
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, sniffLen)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Source{}, fail(err)
	}
	head = head[:n]

	kind, err := detect(abs, head)
	if err != nil {
		return Source{}, fail(err)
	}
	if !kind.Supported() {
		return Source{}, &UnsupportedError{Path: path, Type: kind}
	}

	digest, err := digestOf(f)
	if err != nil {
		return Source{}, fail(fmt.Errorf("hashing: %w", err))
	}

	return Source{
		Path:   abs,
		Type:   kind,
		Size:   info.Size(),
		Digest: digest,
		Ext:    filepath.Ext(abs),
	}, nil
}

// digestOf hashes the whole file. f has already been read from, so it is
// rewound first; the digest is computed once here and reused by the collision
// check and the sidecar.
func digestOf(f *os.File) ([32]byte, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return [32]byte{}, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// TIFF magic numbers, in both byte orders. The standard library's sniff table
// covers PDF, PNG, JPEG, GIF, WebP, BMP, ICO and ZIP — but not TIFF, which
// FR-003 requires. Verified against the Go 1.26.1 source.
var (
	tiffLE = []byte{'I', 'I', 0x2A, 0x00}
	tiffBE = []byte{'M', 'M', 0x00, 0x2A}
)

// ole2Magic is the Compound File Binary signature behind legacy .doc, .xls and
// .ppt, which otherwise sniff as application/octet-stream.
var ole2Magic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// detect names the file's type from its bytes.
func detect(path string, head []byte) (Kind, error) {
	// TIFF first: the standard library would call it octet-stream.
	if bytes.HasPrefix(head, tiffLE) || bytes.HasPrefix(head, tiffBE) {
		return TIFF, nil
	}
	if bytes.HasPrefix(head, ole2Magic) {
		return OLE2, nil
	}

	// DetectContentType always produces a name, falling back to
	// application/octet-stream — which is what makes FR-004's "name the type"
	// possible even in the unsupported case.
	detected := http.DetectContentType(head)

	// text/plain arrives with a charset; Markdown and plain text are the same
	// path, so the parameters are not worth keeping.
	if mediaType, _, found := strings.Cut(detected, ";"); found {
		detected = strings.TrimSpace(mediaType)
	}

	if detected == "application/zip" {
		return zipKind(path)
	}
	return Kind(detected), nil
}

// zipKind disambiguates a zip archive.
//
// OOXML, ODF and an ordinary archive all sniff as application/zip, so the only
// way to tell them apart is to open the central directory and look: ODF
// declares a stored, uncompressed `mimetype` entry as its first member, and
// OOXML carries `[Content_Types].xml`. Neither present means a plain archive,
// which is unsupported.
func zipKind(path string) (Kind, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		// A file that sniffs as zip but will not open is not a zip. Report what
		// was detected rather than inventing a diagnosis.
		return "application/zip", nil //nolint:nilerr // the detected name is the answer
	}
	defer func() { _ = r.Close() }()

	if len(r.File) == 0 {
		return "application/zip", nil
	}

	// ODF: the mimetype entry must be first and stored uncompressed. It names
	// the exact document type, which is better than guessing from the parts.
	if first := r.File[0]; first.Name == "mimetype" && first.Method == zip.Store {
		if kind, err := odfKind(first); err == nil {
			return kind, nil
		}
	}

	var hasContentTypes bool
	var kind Kind
	for _, f := range r.File {
		switch {
		case f.Name == "[Content_Types].xml":
			hasContentTypes = true
		case strings.HasPrefix(f.Name, "word/"):
			kind = OOXMLWord
		case strings.HasPrefix(f.Name, "xl/"):
			kind = OOXMLSheet
		case strings.HasPrefix(f.Name, "ppt/"):
			kind = OOXMLSlides
		}
	}
	if hasContentTypes && kind != "" {
		return kind, nil
	}

	return "application/zip", nil
}

// odfKind reads the ODF package's declared media type.
func odfKind(entry *zip.File) (Kind, error) {
	rc, err := entry.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()

	// The longest ODF media type is well under this; a longer one is not an
	// ODF mimetype entry.
	body, err := io.ReadAll(io.LimitReader(rc, 128))
	if err != nil {
		return "", err
	}

	kind := Kind(strings.TrimSpace(string(body)))
	if !kind.Supported() {
		return "", fmt.Errorf("not a supported ODF media type: %q", kind)
	}
	return kind, nil
}
