package documents

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic two-page fixture: one text page and one image-only page. This proves
// OCR fallback does not silently drop scanned pages in a mixed document.
func TestExtractMixedPDF(t *testing.T) {
	for _, name := range []string{"pdfinfo", "pdftotext", "pdftoppm", "tesseract"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("Install Poppler and Tesseract to test PDF extraction: %s", name)
		}
	}
	pdf := func(objects []string) []byte {
		var b bytes.Buffer
		fmt.Fprintln(&b, "%PDF-1.4")
		offsets := []int{0}
		for i, obj := range objects {
			offsets = append(offsets, b.Len())
			fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
		}
		xref := b.Len()
		fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
		for _, offset := range offsets[1:] {
			fmt.Fprintf(&b, "%010d 00000 n \n", offset)
		}
		fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
		return b.Bytes()
	}
	stream := func(s string) string { return fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(s), s) }
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", stream("BT /F1 24 Tf 50 700 Td (SCANNED INVOICE 6000.00) Tj ET\n")}
	dir := t.TempDir()
	path := filepath.Join(dir, "source.pdf")
	os.WriteFile(path, pdf(objects), 0600)
	if _, err := command(context.Background(), "pdftoppm", "-singlefile", "-scale-to", "1600", "-jpeg", path, filepath.Join(dir, "scan")); err != nil {
		t.Fatal(err)
	}
	jpg, err := os.ReadFile(filepath.Join(dir, "scan.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	objects[1] = "<< /Type /Pages /Kids [3 0 R 6 0 R] /Count 2 >>"
	objects[4] = stream("BT /F1 24 Tf 50 700 Td (TEXT INVOICE 12000.00) Tj ET\n")
	objects = append(objects, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] /Resources << /XObject << /Im0 8 0 R >> >> /Contents 7 0 R >>", stream("q 600 0 0 800 0 0 cm /Im0 Do Q\n"), fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 1200 /Height 1600 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n%s\nendstream", len(jpg), jpg))
	text, err := Extract(context.Background(), pdf(objects))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Page 1 · PDF text", "12000.00", "Page 2 · OCR", "6000.00"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Missing %q in %s", want, text)
		}
	}
	if _, err = Extract(context.Background(), []byte("%PDF-1.4 invalid %%EOF")); err == nil {
		t.Fatal("Malformed PDF accepted")
	}
}
