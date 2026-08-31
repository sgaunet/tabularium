// Command gen writes the shared document corpus used by the test suite.
//
// It is deliberately deterministic: running it twice produces byte-identical
// files, so a regenerated corpus shows up as an empty diff. Run it with
//
//	go run ./testdata/gen
//
// from the repository root. See testdata/README.md for what each file is for.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"time"
)

// fixedTime keeps the zip archives reproducible. A zip header carries a
// modification time, so without pinning it the corpus would differ on every run.
var fixedTime = time.Date(2025, 3, 14, 9, 41, 12, 0, time.UTC)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	out := filepath.Join(dir, "testdata")
	if _, err := os.Stat(out); err != nil {
		fail(fmt.Errorf("run this from the repository root: %w", err))
	}

	write(out, "born-digital.pdf", bornDigitalPDF())
	write(out, "scanned.pdf", scannedPDF())
	write(out, "multipage.pdf", multipagePDF(25))
	write(out, "scanned-multipage.pdf", scannedMultipagePDF(5))
	write(out, "receipt.jpg", jpegBytes())
	write(out, "receipt.png", pngBytes())
	write(out, "little-endian.tiff", tiffBytes(binary.LittleEndian))
	write(out, "big-endian.tiff", tiffBytes(binary.BigEndian))
	write(out, "letter.docx", docxBytes())
	write(out, "letter.odt", odtBytes())
	write(out, "legacy.doc", ole2Bytes())
	write(out, "notes.md", []byte(markdown))
	write(out, "notes.txt", []byte(plainText))
	write(out, "plain.zip", plainZipBytes())
	write(out, "unsupported.bin", unsupportedBytes())
}

func write(dir, name string, b []byte) {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("%-22s %7d bytes\n", name, len(b))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gen:", err)
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// PDF
// ---------------------------------------------------------------------------

// pdf assembles a PDF incrementally, recording each object's byte offset so the
// cross-reference table at the end is correct. Hand-assembling is what keeps the
// corpus free of a PDF dependency.
type pdf struct {
	buf     bytes.Buffer
	offsets []int
}

func newPDF() *pdf {
	p := &pdf{}
	p.buf.WriteString("%PDF-1.7\n")
	p.buf.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})
	return p
}

// obj appends one indirect object and returns its number.
func (p *pdf) obj(body []byte) int {
	num := len(p.offsets) + 1
	p.offsets = append(p.offsets, p.buf.Len())
	fmt.Fprintf(&p.buf, "%d 0 obj\n", num)
	p.buf.Write(body)
	p.buf.WriteString("\nendobj\n")
	return num
}

func (p *pdf) dict(format string, args ...any) int {
	return p.obj([]byte(fmt.Sprintf(format, args...)))
}

// stream appends a stream object with a correct /Length.
func (p *pdf) stream(extra string, data []byte) int {
	var b bytes.Buffer
	fmt.Fprintf(&b, "<< /Length %d%s >>\nstream\n", len(data), extra)
	b.Write(data)
	b.WriteString("\nendstream")
	return p.obj(b.Bytes())
}

func (p *pdf) finish(root int) []byte {
	start := p.buf.Len()
	fmt.Fprintf(&p.buf, "xref\n0 %d\n", len(p.offsets)+1)
	p.buf.WriteString("0000000000 65535 f \n")
	for _, off := range p.offsets {
		fmt.Fprintf(&p.buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&p.buf, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(p.offsets)+1, root, start)
	return p.buf.Bytes()
}

// textPage renders one page of Helvetica text lines.
func textPage(lines []string) []byte {
	var b bytes.Buffer
	b.WriteString("BT\n/F1 12 Tf\n14 TL\n72 720 Td\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "(%s) Tj\nT*\n", escapePDFString(l))
	}
	b.WriteString("ET\n")
	return b.Bytes()
}

func escapePDFString(s string) string {
	var b bytes.Buffer
	for _, r := range []byte(s) {
		switch r {
		case '(', ')', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(r)
	}
	return b.String()
}

// bornDigitalPDF carries a real text layer: pdftotext recovers well over the
// 64-non-whitespace-rune threshold, so no vision call is ever needed (SC-001).
func bornDigitalPDF() []byte {
	p := newPDF()
	font := p.dict("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	content := p.stream("", textPage([]string{
		"Garage Central",
		"12 rue des Ateliers, 31000 Toulouse",
		"",
		"FACTURE FA-2025-0312",
		"Date: 2025-03-14",
		"Echeance: 2025-04-14",
		"",
		"Revision annuelle du vehicule",
		"Remplacement des plaquettes de frein",
		"",
		"Total TTC: 384.50 EUR",
	}))
	page := p.dict("<< /Type /Page /Parent 4 0 R /MediaBox [0 0 612 792] "+
		"/Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>", content, font)
	pages := p.dict("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", page)
	root := p.dict("<< /Type /Catalog /Pages %d 0 R >>", pages)
	return p.finish(root)
}

// scannedPDF carries an image and no text operators at all, so pdftotext
// recovers nothing and the rasterisation path must take over (FR-007).
func scannedPDF() []byte {
	img := jpegBytes()
	p := newPDF()
	xobj := p.stream(" /Type /XObject /Subtype /Image /Width 64 /Height 64"+
		" /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode", img)
	content := p.stream("", []byte("q\n612 0 0 792 0 0 cm\n/Im0 Do\nQ\n"))
	page := p.dict("<< /Type /Page /Parent 4 0 R /MediaBox [0 0 612 792] "+
		"/Contents %d 0 R /Resources << /XObject << /Im0 %d 0 R >> >> >>", content, xobj)
	pages := p.dict("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", page)
	root := p.dict("<< /Type /Catalog /Pages %d 0 R >>", pages)
	return p.finish(root)
}

// scannedMultipagePDF is several image-only pages: no text operator anywhere,
// so every page must be rasterised. It is what bounds the *cost* of the page
// cap, since a scanned page is one model call.
func scannedMultipagePDF(pages int) []byte {
	img := jpegBytes()
	p := newPDF()
	xobj := p.stream(" /Type /XObject /Subtype /Image /Width 64 /Height 64"+
		" /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode", img)

	contents := make([]int, pages)
	for i := range pages {
		contents[i] = p.stream("", []byte("q\n612 0 0 792 0 0 cm\n/Im0 Do\nQ\n"))
	}

	pagesNum := len(p.offsets) + pages + 1
	kids := make([]int, pages)
	for i := range pages {
		kids[i] = p.dict("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] "+
			"/Contents %d 0 R /Resources << /XObject << /Im0 %d 0 R >> >> >>",
			pagesNum, contents[i], xobj)
	}

	var kidRefs bytes.Buffer
	for i, k := range kids {
		if i > 0 {
			kidRefs.WriteByte(' ')
		}
		fmt.Fprintf(&kidRefs, "%d 0 R", k)
	}
	got := p.dict("<< /Type /Pages /Kids [%s] /Count %d >>", kidRefs.String(), pages)
	if got != pagesNum {
		fail(fmt.Errorf("pages object number drifted: predicted %d, allocated %d", pagesNum, got))
	}
	root := p.dict("<< /Type /Catalog /Pages %d 0 R >>", got)
	return p.finish(root)
}

// multipagePDF has more pages than any sane max_pages, so the page cap and the
// truncation record it produces are both exercised (FR-008, SC-010).
func multipagePDF(pages int) []byte {
	p := newPDF()
	font := p.dict("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	contents := make([]int, pages)
	for i := range pages {
		contents[i] = p.stream("", textPage([]string{
			fmt.Sprintf("Releve de compte - page %d sur %d", i+1, pages),
			"Banque Populaire du Sud",
			fmt.Sprintf("Operation numero %04d, montant 12.%02d EUR", i+1, i),
		}))
	}

	// The Pages node is allocated after its children, so its number is known
	// only once every page object exists; pages point at it by that number.
	pagesNum := len(p.offsets) + pages + 1
	kids := make([]int, pages)
	for i := range pages {
		kids[i] = p.dict("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] "+
			"/Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>",
			pagesNum, contents[i], font)
	}

	var kidRefs bytes.Buffer
	for i, k := range kids {
		if i > 0 {
			kidRefs.WriteByte(' ')
		}
		fmt.Fprintf(&kidRefs, "%d 0 R", k)
	}
	got := p.dict("<< /Type /Pages /Kids [%s] /Count %d >>", kidRefs.String(), pages)
	if got != pagesNum {
		fail(fmt.Errorf("pages object number drifted: predicted %d, allocated %d", pagesNum, got))
	}
	root := p.dict("<< /Type /Catalog /Pages %d 0 R >>", got)
	return p.finish(root)
}

// ---------------------------------------------------------------------------
// Images
// ---------------------------------------------------------------------------

func sampleImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			// A fixed gradient with a darker band, so the bytes are neither
			// uniform nor random — and identical on every run.
			v := uint8((x*4 + y*3) % 256)
			if y > 24 && y < 40 {
				v /= 4
			}
			img.Set(x, y, color.RGBA{R: v, G: v, B: uint8(255 - int(v)), A: 255})
		}
	}
	return img
}

func jpegBytes() []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, sampleImage(), &jpeg.Options{Quality: 80}); err != nil {
		fail(err)
	}
	return b.Bytes()
}

func pngBytes() []byte {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&b, sampleImage()); err != nil {
		fail(err)
	}
	return b.Bytes()
}

// tiffBytes hand-builds a minimal 1x1 grayscale TIFF. TIFF is absent from the
// standard library's sniff table, which is exactly why both byte orders are in
// the corpus (research.md D2).
func tiffBytes(order binary.ByteOrder) []byte {
	const (
		entries   = 9
		ifdOffset = 8
		short     = 3
		long      = 4
	)
	// header + entry count + entries + next-IFD pointer
	dataOffset := ifdOffset + 2 + entries*12 + 4

	var b bytes.Buffer
	if order == binary.LittleEndian {
		b.WriteString("II")
		_ = binary.Write(&b, order, uint16(42))
	} else {
		b.WriteString("MM")
		_ = binary.Write(&b, order, uint16(42))
	}
	_ = binary.Write(&b, order, uint32(ifdOffset))
	_ = binary.Write(&b, order, uint16(entries))

	entry := func(tag, typ uint16, value uint32) {
		_ = binary.Write(&b, order, tag)
		_ = binary.Write(&b, order, typ)
		_ = binary.Write(&b, order, uint32(1))
		if typ == short {
			// A SHORT is left-justified in the four-byte value field.
			_ = binary.Write(&b, order, uint16(value))
			_ = binary.Write(&b, order, uint16(0))
			return
		}
		_ = binary.Write(&b, order, value)
	}

	entry(256, short, 1)                 // ImageWidth
	entry(257, short, 1)                 // ImageLength
	entry(258, short, 8)                 // BitsPerSample
	entry(259, short, 1)                 // Compression: none
	entry(262, short, 1)                 // PhotometricInterpretation: BlackIsZero
	entry(273, long, uint32(dataOffset)) // StripOffsets
	entry(277, short, 1)                 // SamplesPerPixel
	entry(278, short, 1)                 // RowsPerStrip
	entry(279, long, 1)                  // StripByteCounts

	_ = binary.Write(&b, order, uint32(0)) // no next IFD
	b.WriteByte(0x7f)                      // the single pixel

	return b.Bytes()
}

// ---------------------------------------------------------------------------
// Office
// ---------------------------------------------------------------------------

type zipEntry struct {
	name   string
	body   string
	stored bool
}

func buildZip(entries []zipEntry) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Modified: fixedTime, Method: zip.Deflate}
		if e.stored {
			h.Method = zip.Store
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			fail(err)
		}
		if _, err := f.Write([]byte(e.body)); err != nil {
			fail(err)
		}
	}
	if err := w.Close(); err != nil {
		fail(err)
	}
	return b.Bytes()
}

const docxDocument = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Assurance Habitation</w:t></w:r></w:p>
    <w:p><w:r><w:t>Contrat numero CT-2024-8891</w:t></w:r></w:p>
    <w:p><w:r><w:t>Prise d'effet le 2024-09-01.</w:t></w:r>
      <w:r><w:t xml:space="preserve"> Cotisation annuelle 512.00 EUR.</w:t></w:r></w:p>
  </w:body>
</w:document>`

const docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

func docxBytes() []byte {
	return buildZip([]zipEntry{
		{name: "[Content_Types].xml", body: docxContentTypes},
		{name: "_rels/.rels", body: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`</Relationships>`},
		{name: "word/document.xml", body: docxDocument},
	})
}

const odtContent = `<?xml version="1.0" encoding="UTF-8"?>
<office:document-content
    xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
    xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">
  <office:body><office:text>
    <text:p>Avis d'impot sur le revenu</text:p>
    <text:p>Direction generale des Finances publiques</text:p>
    <text:p>Reference 24 31 000 123 456. Montant a payer 1240.00 EUR avant le 2024-09-15.</text:p>
  </office:text></office:body>
</office:document-content>`

func odtBytes() []byte {
	// The mimetype entry must come first and be stored uncompressed: that is
	// what distinguishes an ODF package from any other zip (research.md D2).
	return buildZip([]zipEntry{
		{name: "mimetype", body: "application/vnd.oasis.opendocument.text", stored: true},
		{name: "content.xml", body: odtContent},
		{name: "META-INF/manifest.xml", body: `<?xml version="1.0" encoding="UTF-8"?>` +
			`<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"/>`},
	})
}

// plainZipBytes is an ordinary archive: neither an ODF mimetype entry nor an
// OOXML content-types part, so it must be reported as unsupported (FR-004).
func plainZipBytes() []byte {
	return buildZip([]zipEntry{
		{name: "readme.txt", body: "just an ordinary archive\n"},
		{name: "data/values.csv", body: "a,b\n1,2\n"},
	})
}

// ole2Bytes is the Compound File Binary signature followed by a plausible
// header. A real legacy .doc needs libreoffice to produce, and the tests that
// use this one only need it to be *detected* as OLE2 and routed accordingly.
func ole2Bytes() []byte {
	b := make([]byte, 1536)
	copy(b, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	binary.LittleEndian.PutUint16(b[0x18:], 0x003E) // minor version
	binary.LittleEndian.PutUint16(b[0x1A:], 0x0003) // major version
	binary.LittleEndian.PutUint16(b[0x1C:], 0xFFFE) // little-endian byte order
	binary.LittleEndian.PutUint16(b[0x1E:], 0x0009) // sector shift: 512 bytes
	for i := 0x4C; i < 512; i += 4 {
		binary.LittleEndian.PutUint32(b[i:], 0xFFFFFFFF) // free sector
	}
	return b
}

// ---------------------------------------------------------------------------
// Plain text
// ---------------------------------------------------------------------------

const markdown = `# Releve trimestriel

Emis par **Banque Populaire du Sud** le 2025-01-31.

| Poste      | Montant |
|------------|---------|
| Cotisation |   24.00 |
| Interets   |    3.12 |

Reference du compte : FR76 3000 4000 0300 0000 0000 123.
`

const plainText = `Attestation de domicile

Fournie par Energie du Sud le 2025-02-02.
Client numero 88213344, 12 rue des Ateliers, 31000 Toulouse.
Consommation annuelle 4210 kWh pour un montant de 892.40 EUR.
`

// unsupportedBytes sniffs as application/octet-stream: no known magic, and
// deliberately not the OLE2 signature, so it exercises FR-004's "name the type
// you detected" path.
func unsupportedBytes() []byte {
	b := make([]byte, 1024)
	for i := range b {
		b[i] = byte((i*7 + 13) % 251)
	}
	// Make sure the first bytes cannot be mistaken for any sniffable format.
	copy(b, []byte{0x00, 0x01, 0x02, 0x03, 0xFE, 0xFD, 0xFC, 0xFB})
	return b
}
