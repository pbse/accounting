package documents

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/budget"
	"close.local/backend/internal/llm"
)

// fakeReader stands in for Gemini (llm.Gemini, whose HTTP behaviour llm's own tests cover): it answers every page with
// text, or with err, and records each image it was sent.
type fakeReader struct {
	text string
	err  error
	sent [][]byte
}

func (f *fakeReader) ReadPage(_ context.Context, png []byte) (string, string, error) {
	f.sent = append(f.sent, png)
	if f.err != nil {
		return "", "", f.err
	}
	return f.text, "gemini-3.8-flash-001", nil
}

var pagePNG = []byte("\x89PNG synthetic page image")

func render() ([]byte, error) { return pagePNG, nil }

func TestGeminiReadsEveryPageAndNeverTakesAFigureTheTextLayerLacks(t *testing.T) {
	ctx := context.Background()
	noTesseract := func([]byte) (string, error) {
		t.Fatal("Tesseract is not needed when Gemini reads the page")
		return "", nil
	}

	// A scanned page, with no text layer, takes Gemini's reading, labelled to check against the original. The page goes
	// as its rendered image, and the call is reserved against the firm's daily limit first.
	g := &fakeReader{text: "SCANNED INVOICE 6,000.00"}
	reserved := 0
	reading := Reading{Gemini: g, Reserve: func(context.Context) error { reserved++; return nil }}
	text, method, err := reading.page(ctx, "  \n", render, noTesseract)
	if err != nil || text != "SCANNED INVOICE 6,000.00" || method != "Gemini gemini-3.8-flash-001 (verify against original)" || reserved != 1 || len(g.sent) != 1 || !bytes.Equal(g.sent[0], pagePNG) {
		t.Fatalf("a scanned page is read by Gemini: %q %q %v reserved %d", text, method, err, reserved)
	}

	// A page with its own text always keeps it, lines and all. Gemini's reading only checks it: the label says the two
	// agree when Gemini read the same text, laid out differently (even on joined lines),
	layer := "TEXT INVOICE      12000.00\nWidgets  100.00\nGadgets  200.00\nPage 1 of 2\n"
	for _, laidOut := range []string{"TEXT INVOICE  12000.00\n\nWidgets 100.00\nGadgets 200.00\nPage 1 of 2", "TEXT INVOICE 12000.00 Widgets 100.00 Gadgets 200.00 Page 1 of 2"} {
		reading = Reading{Gemini: &fakeReader{text: laidOut}}
		if text, method, _ = reading.page(ctx, layer, render, noTesseract); text != layer || method != "PDF text; Gemini gemini-3.8-flash-001 read the same text" {
			t.Fatalf("the text layer, which Gemini read the same: %q %q", text, method)
		}
	}
	// and flags the page to check against the original when it read anything else.
	for _, misread := range []string{
		"TEXT INVOICE  12000.00\nWidgets 100.00\nGadgets 200.00\nPage 1 of 2\nTax 960.00", // a figure the page lacks
		"TEXT INVOICE  12800.00\nWidgets 100.00\nGadgets 200.00\nPage 1 of 2",             // a changed figure
		"TEXT INVOICE\nWidgets 100.00\nGadgets 200.00\nPage 1 of 2",                       // a dropped figure
		"TEXT INVOICE  12000.00\nWidgets 200.00\nGadgets 100.00\nPage 1 of 2",             // amounts swapped between rows
		"TEXT INVOICE  12000.00\nWidgets (100.00)\nGadgets 200.00\nPage 1 of 2",           // a re-signed amount
		"TEXT INVOICE  12000.00\nWidgets 100.00\nGizmos 200.00\nPage 1 of 2",              // a changed word
	} {
		reading = Reading{Gemini: &fakeReader{text: misread}}
		if text, method, _ = reading.page(ctx, layer, render, noTesseract); text != layer || method != "PDF text; Gemini gemini-3.8-flash-001 read it differently, so check the original" {
			t.Fatalf("Gemini's reading %q is not taken over the text layer: %q %q", misread, text, method)
		}
	}

	// When Gemini cannot read a page (it errs, or reads nothing), a page with text keeps it and a scanned page is read
	// by Tesseract.
	tesseract := func(png []byte) (string, error) {
		if !bytes.Equal(png, pagePNG) {
			t.Fatal("Tesseract reads the rendered page")
		}
		return "SCANNED INVOICE 6000.00", nil
	}
	for _, failing := range []*fakeReader{{err: llm.ErrGeminiRefused}, {err: errors.New("Gemini could not be reached")}, {text: "  "}} {
		reading = Reading{Gemini: failing}
		if text, method, err = reading.page(ctx, "", render, tesseract); err != nil || text != "SCANNED INVOICE 6000.00" || method != "OCR (English; verify against original)" {
			t.Fatalf("%+v: a scanned page falls back to Tesseract: %q %q %v", failing, text, method, err)
		}
		if text, method, _ = reading.page(ctx, layer, render, noTesseract); text != layer || method != "PDF text; Image model could not compare it with the page image" {
			t.Fatalf("%+v: a page with text keeps it, saying it went unchecked: %q %q", failing, text, method)
		}
	}

	// Without Gemini nothing is sent: a page with text is not even rendered.
	reading = Reading{}
	if text, method, _ = reading.page(ctx, layer, func() ([]byte, error) { t.Fatal("not rendered"); return nil, nil }, noTesseract); text != layer || method != "PDF text" {
		t.Fatalf("without Gemini, the text layer: %q %q", text, method)
	}
	if text, method, _ = reading.page(ctx, "", render, tesseract); method != "OCR (English; verify against original)" {
		t.Fatalf("without Gemini, Tesseract: %q %q", text, method)
	}

	// A page image Gemini would refuse unsent is neither sent nor charged to the firm's limit.
	g = &fakeReader{text: "never sent"}
	reserved = 0
	reading = Reading{Gemini: g, Reserve: func(context.Context) error { reserved++; return nil }}
	huge := func() ([]byte, error) { return make([]byte, llm.GeminiMaxImage+1), nil }
	local := func([]byte) (string, error) { return "SCANNED INVOICE 6000.00", nil }
	if _, method, _ = reading.page(ctx, "", huge, local); method != "OCR (English; verify against original)" || reserved != 0 || len(g.sent) != 0 {
		t.Fatalf("an oversized page image is read locally, uncharged: %q reserved %d sent %d", method, reserved, len(g.sent))
	}

	// Once the firm's daily limit is reached, no more pages go to Gemini, and none is reserved again.
	g = &fakeReader{text: "never sent"}
	reserved = 0
	reading = Reading{Gemini: g, Reserve: func(context.Context) error { reserved++; return budget.ErrModelLimit }}
	for range 3 {
		if _, method, _ = reading.page(ctx, "", render, tesseract); method != "OCR (English; verify against original)" {
			t.Fatalf("over the limit, Tesseract: %q", method)
		}
	}
	if reserved != 1 || len(g.sent) != 0 {
		t.Fatalf("the limit stops Gemini for the rest of the document: reserved %d, sent %d", reserved, len(g.sent))
	}

	// With too little time left, the remaining pages are read locally so the document still finishes.
	g = &fakeReader{text: "never sent"}
	reading = Reading{Gemini: g}
	short, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, method, _ = reading.page(short, "", render, tesseract); method != "OCR (English; verify against original)" || len(g.sent) != 0 {
		t.Fatalf("near the deadline, Tesseract: %q sent %d", method, len(g.sent))
	}
	// Gemini is asked only while the time left covers its call and local reading of the scanned pages still to come.
	minute, cancelMinute := context.WithTimeout(ctx, 60*time.Second)
	defer cancelMinute()
	g = &fakeReader{text: "SCANNED INVOICE 6,000.00"}
	reading = Reading{Gemini: g, local: 5 * localPerPage}
	if _, method, _ = reading.page(minute, "", render, tesseract); method != "OCR (English; verify against original)" || len(g.sent) != 0 {
		t.Fatalf("five scanned pages to come leave no time for Gemini in a minute: %q sent %d", method, len(g.sent))
	}
	reading = Reading{Gemini: g, local: localPerPage}
	if _, method, _ = reading.page(minute, "", render, tesseract); !strings.HasPrefix(method, "Gemini ") || len(g.sent) != 1 {
		t.Fatalf("one scanned page to come leaves time for Gemini: %q sent %d", method, len(g.sent))
	}

	// A page that cannot be rendered fails only when nothing else can read it.
	broken := func() ([]byte, error) { return nil, errors.New("pdftoppm failed") }
	reading = Reading{Gemini: &fakeReader{text: "never sent"}}
	if text, method, err = reading.page(ctx, layer, broken, noTesseract); err != nil || text != layer || method != "PDF text; Image model could not compare it with the page image" {
		t.Fatalf("an unrenderable page with text keeps it: %q %q %v", text, method, err)
	}
	if _, _, err = reading.page(ctx, "", broken, noTesseract); err != errRender {
		t.Fatalf("an unrenderable scanned page fails: %v", err)
	}
}

func TestSameReadingSetsOnlyLayoutAside(t *testing.T) {
	// Whitespace and line breaks are layout.
	for _, pair := range [][2]string{
		{"Total      1,200.00\nDue 2026-09-30", "Total 1,200.00 Due 2026-09-30"},
		{"Refund (1,200.00)\n\n", "  Refund   (1,200.00)"},
		{"Net 100.00\t\r\n", "Net 100.00"},
	} {
		if !sameReading(pair[0], pair[1]) {
			t.Fatalf("%q and %q say the same, laid out differently", pair[0], pair[1])
		}
	}
	// Anything else is a different reading: a figure, its sign, comma or decimal point, a word, case or order.
	for _, pair := range [][2]string{
		{"A 100\nB 200", "A 200\nB 100"},
		{"Item 1  234", "Item 12  34"},
		{"Refund (1,200.00)", "Refund ( 1,200.00 )"},
		{"Fee .50", "Fee 50"},
		{"Fee 1,50", "Fee 150"},
		{"Total 12,000.00", "Total 12000.00"},
		{"Refund -100.00", "Refund 100.00"},
		{"Refund (100.00)", "Refund 100.00"},
		{"Refund −100.00", "Refund -100.00"},
		{"Balance 100.00 CR", "Balance 100.00"},
		{"Due 09/30/2026", "Due 9/30/2026"},
		{"Amount 12", "Amount 12 12"},
		{"Paid by Acme", "Paid by ACME"},
		{"Rent 900 Water 40", "Water 40 Rent 900"},
	} {
		if sameReading(pair[0], pair[1]) {
			t.Fatalf("%q and %q are different readings", pair[0], pair[1])
		}
	}
}

func TestAPageGeminiReadDifferentlyIsNamedToCompare(t *testing.T) {
	flagged := "--- Page 1 · PDF text; Gemini gemini-3.8-flash-001" + imageDiffers + " ---\nStatement\nPage 1 of 2\n--- Page 2 · PDF text; Gemini gemini-3.8-flash-001 read the same text ---\nPage 2 of 2\n"
	check := Check("statement.pdf", flagged)
	if len(check.Warnings) != 1 || len(check.Questions) != 0 || !strings.Contains(check.Warnings[0], "PDF page 1: The image model's reading of the page image differs from its PDF text; compare that page with the original") {
		t.Fatalf("a page Gemini read differently is named, to compare with the original: %+v", check)
	}
	if agreed := Check("statement.pdf", strings.Replace(flagged, imageDiffers, " read the same text", 1)); len(agreed.Warnings) != 0 || len(agreed.Questions) != 0 {
		t.Fatalf("pages Gemini read the same raise nothing: %+v", agreed)
	}
	// Only the extractor's own marker counts: the same words inside a page's text do not.
	if inText := Check("statement.pdf", "--- Page 1 · PDF text ---\nGemini"+imageDiffers+" ---\nPage 1 of 1\n"); len(inText.Warnings) != 0 || len(inText.Questions) != 0 {
		t.Fatalf("a page's text cannot raise the warning: %+v", inText)
	}
	// Pages the image model could not compare are named together, in a warning that holds nothing.
	unchecked := Check("statement.pdf", "--- Page 1 · PDF text"+imageUnchecked+" ---\nPage 1 of 3\n--- Page 2 · PDF text ---\nPage 2 of 3\n--- Page 3 · PDF text"+imageUnchecked+" ---\nPage 3 of 3\n")
	if len(unchecked.Questions) != 0 || len(unchecked.Warnings) != 1 || !strings.HasPrefix(unchecked.Warnings[0], "The image model could not compare PDF pages 1 and 3 with the page image") {
		t.Fatalf("pages the image model could not compare are named: %+v", unchecked)
	}
	if !strings.Contains(strings.Join(Check("invoice.pdf", "--- Page 1 · PDF text"+imageUnchecked+" ---\nTotal 5\n").Warnings, " "), "The image model could not compare PDF page 1 with") {
		t.Fatal("one page the image model could not compare is named")
	}
}

func TestLegacyGeminiUncheckedLabelsStillWarn(t *testing.T) {
	old := Check("statement.pdf", "--- Page 1 · PDF text"+legacyImageUnchecked+" ---\nPage 1 of 1\n")
	if !strings.Contains(strings.Join(old.Warnings, " "), "could not compare PDF page 1") {
		t.Fatal(old)
	}
}

func TestBusyImageServiceFallsBackForRemainingPagesWithoutMoreReservations(t *testing.T) {
	reserved := 0
	reader := &fakeReader{err: llm.ErrBusy}
	reading := Reading{Gemini: reader, Provider: "Image model", Reserve: func(context.Context) error { reserved++; return nil }}
	for i := 0; i < 3; i++ {
		text, method, err := reading.page(t.Context(), "PDF text", func() ([]byte, error) { return []byte("synthetic"), nil }, func([]byte) (string, error) { t.Fatal("text layer needs no Tesseract"); return "", nil })
		if err != nil || text != "PDF text" || method != "PDF text"+imageUnchecked {
			t.Fatal(text, method, err)
		}
	}
	if reserved != 1 || len(reader.sent) != 1 {
		t.Fatal("busy service was retried", reserved, len(reader.sent))
	}
}

type validatedReader struct{ fakeReader }

func (*validatedReader) ValidatePage(context.Context, []byte) error { return llm.ErrLimit }
func TestPageKnownLimitsAreRejectedBeforeBudgetReservation(t *testing.T) {
	reserved := 0
	reader := &validatedReader{}
	reading := Reading{Gemini: reader, Provider: "Image model", Reserve: func(context.Context) error { reserved++; return nil }}
	text, _, err := reading.page(t.Context(), "PDF text", func() ([]byte, error) { return []byte("page"), nil }, func([]byte) (string, error) { t.Fatal("text layer needs no Tesseract"); return "", nil })
	if err != nil || text != "PDF text" || reserved != 0 || len(reader.sent) != 0 {
		t.Fatal("known invalid image was charged or dispatched", reserved, text, err)
	}
}
