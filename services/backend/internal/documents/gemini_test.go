package documents

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/budget"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeGemini answers generateContent with reply (or status), recording each request it receives.
func fakeGemini(t *testing.T, status int, reply map[string]any) (*Gemini, *[]map[string]any) {
	t.Helper()
	var seen []map[string]any
	g := newGemini("test-key-not-real", "gemini-3.8-flash", "https://gemini.test/v1beta/models/", roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://gemini.test/v1beta/models/gemini-3.8-flash:generateContent" || r.Header.Get("x-goog-api-key") != "test-key-not-real" || r.Method != "POST" {
			t.Fatalf("unexpected Gemini request %s %s", r.Method, r.URL)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		seen = append(seen, body)
		raw, _ := json.Marshal(reply)
		header := make(http.Header)
		if status/100 == 3 {
			header.Set("Location", "https://elsewhere.test/collect")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(raw)), Header: header}, nil
	}))
	return g, &seen
}

func answer(text string) map[string]any {
	return map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": text}}}, "finishReason": "STOP"}}, "modelVersion": "gemini-3.8-flash-001"}
}

var pagePNG = []byte("\x89PNG synthetic page image")

func render() ([]byte, error) { return pagePNG, nil }

func TestGeminiReadsEveryPageAndNeverTakesAFigureTheTextLayerLacks(t *testing.T) {
	ctx := context.Background()
	noTesseract := func([]byte) (string, error) {
		t.Fatal("Tesseract is not needed when Gemini reads the page")
		return "", nil
	}

	// A scanned page, with no text layer, takes Gemini's reading, labelled to check against the original. The page goes as
	// a PNG image, with the key only in the header, and the call is reserved against the firm's daily limit first.
	g, seen := fakeGemini(t, 200, answer("SCANNED INVOICE 6,000.00"))
	reserved := 0
	reading := Reading{Gemini: g, Reserve: func(context.Context) error { reserved++; return nil }}
	text, method, err := reading.page(ctx, "  \n", render, noTesseract)
	if err != nil || text != "SCANNED INVOICE 6,000.00" || method != "Gemini gemini-3.8-flash-001 (verify against original)" || reserved != 1 || len(*seen) != 1 {
		t.Fatalf("a scanned page is read by Gemini: %q %q %v reserved %d", text, method, err, reserved)
	}
	part := (*seen)[0]["contents"].([]any)[0].(map[string]any)["parts"].([]any)[1].(map[string]any)["inline_data"].(map[string]any)
	if part["mime_type"] != "image/png" || part["data"] != base64.StdEncoding.EncodeToString(pagePNG) || (*seen)[0]["generationConfig"].(map[string]any)["temperature"] != float64(0) {
		t.Fatalf("the page goes as its PNG, read at temperature 0: %v", (*seen)[0])
	}

	// A page with its own text always keeps it, lines and all. Gemini's reading only checks it: the label says the two
	// agree when Gemini read the same text, laid out differently (even on joined lines),
	layer := "TEXT INVOICE      12000.00\nWidgets  100.00\nGadgets  200.00\nPage 1 of 2\n"
	for _, laidOut := range []string{"TEXT INVOICE  12000.00\n\nWidgets 100.00\nGadgets 200.00\nPage 1 of 2", "TEXT INVOICE 12000.00 Widgets 100.00 Gadgets 200.00 Page 1 of 2"} {
		g, _ = fakeGemini(t, 200, answer(laidOut))
		reading = Reading{Gemini: g}
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
		g, _ = fakeGemini(t, 200, answer(misread))
		reading = Reading{Gemini: g}
		if text, method, _ = reading.page(ctx, layer, render, noTesseract); text != layer || method != "PDF text; Gemini gemini-3.8-flash-001 read it differently, so check the original" {
			t.Fatalf("Gemini's reading %q is not taken over the text layer: %q %q", misread, text, method)
		}
	}

	// When Gemini cannot read a page, a page with text keeps it and a scanned page is read by Tesseract.
	tesseract := func(png []byte) (string, error) {
		if !bytes.Equal(png, pagePNG) {
			t.Fatal("Tesseract reads the rendered page")
		}
		return "SCANNED INVOICE 6000.00", nil
	}
	for name, failing := range map[string]struct {
		status int
		reply  map[string]any
	}{
		"server error": {500, map[string]any{"error": map[string]any{"message": "internal"}}},
		"cut short":    {200, map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "SCANNED INV"}}}, "finishReason": "MAX_TOKENS"}}}},
		"blocked":      {200, map[string]any{"promptFeedback": map[string]any{"blockReason": "SAFETY"}}},
		"empty":        {200, answer("  ")},
		// Never followed: the stub fails on any other address, so the key and the page stay put.
		"redirected": {307, answer("SCANNED INVOICE 6,000.00")},
	} {
		g, _ = fakeGemini(t, failing.status, failing.reply)
		reading = Reading{Gemini: g}
		if text, method, err = reading.page(ctx, "", render, tesseract); err != nil || text != "SCANNED INVOICE 6000.00" || method != "OCR (English; verify against original)" {
			t.Fatalf("%s: a scanned page falls back to Tesseract: %q %q %v", name, text, method, err)
		}
		if text, method, _ = reading.page(ctx, layer, render, noTesseract); text != layer || method != "PDF text" {
			t.Fatalf("%s: a page with text keeps it: %q %q", name, text, method)
		}
	}

	// Without a key nothing is sent: a page with text is not even rendered.
	reading = Reading{}
	if text, method, _ = reading.page(ctx, layer, func() ([]byte, error) { t.Fatal("not rendered"); return nil, nil }, noTesseract); text != layer || method != "PDF text" {
		t.Fatalf("without Gemini, the text layer: %q %q", text, method)
	}
	if text, method, _ = reading.page(ctx, "", render, tesseract); method != "OCR (English; verify against original)" {
		t.Fatalf("without Gemini, Tesseract: %q %q", text, method)
	}

	// A page image Gemini would refuse unsent is neither sent nor charged to the firm's limit.
	g, seen = fakeGemini(t, 200, answer("never sent"))
	reserved = 0
	reading = Reading{Gemini: g, Reserve: func(context.Context) error { reserved++; return nil }}
	huge := func() ([]byte, error) { return make([]byte, geminiMaxImage+1), nil }
	local := func([]byte) (string, error) { return "SCANNED INVOICE 6000.00", nil }
	if _, method, _ = reading.page(ctx, "", huge, local); method != "OCR (English; verify against original)" || reserved != 0 || len(*seen) != 0 {
		t.Fatalf("an oversized page image is read locally, uncharged: %q reserved %d sent %d", method, reserved, len(*seen))
	}

	// Once the firm's daily limit is reached, no more pages go to Gemini, and none is reserved again.
	g, seen = fakeGemini(t, 200, answer("never sent"))
	reserved = 0
	reading = Reading{Gemini: g, Reserve: func(context.Context) error { reserved++; return budget.ErrModelLimit }}
	for range 3 {
		if _, method, _ = reading.page(ctx, "", render, tesseract); method != "OCR (English; verify against original)" {
			t.Fatalf("over the limit, Tesseract: %q", method)
		}
	}
	if reserved != 1 || len(*seen) != 0 {
		t.Fatalf("the limit stops Gemini for the rest of the document: reserved %d, sent %d", reserved, len(*seen))
	}

	// With too little time left, the remaining pages are read locally so the document still finishes.
	g, seen = fakeGemini(t, 200, answer("never sent"))
	reading = Reading{Gemini: g}
	short, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, method, _ = reading.page(short, "", render, tesseract); method != "OCR (English; verify against original)" || len(*seen) != 0 {
		t.Fatalf("near the deadline, Tesseract: %q sent %d", method, len(*seen))
	}
	// Gemini is asked only while the time left covers its call and local reading of the scanned pages still to come.
	minute, cancelMinute := context.WithTimeout(ctx, 60*time.Second)
	defer cancelMinute()
	g, seen = fakeGemini(t, 200, answer("SCANNED INVOICE 6,000.00"))
	reading = Reading{Gemini: g, local: 5 * localPerPage}
	if _, method, _ = reading.page(minute, "", render, tesseract); method != "OCR (English; verify against original)" || len(*seen) != 0 {
		t.Fatalf("five scanned pages to come leave no time for Gemini in a minute: %q sent %d", method, len(*seen))
	}
	reading = Reading{Gemini: g, local: localPerPage}
	if _, method, _ = reading.page(minute, "", render, tesseract); !strings.HasPrefix(method, "Gemini ") || len(*seen) != 1 {
		t.Fatalf("one scanned page to come leaves time for Gemini: %q sent %d", method, len(*seen))
	}

	// A page that cannot be rendered fails only when nothing else can read it.
	broken := func() ([]byte, error) { return nil, errors.New("pdftoppm failed") }
	g, _ = fakeGemini(t, 200, answer("never sent"))
	reading = Reading{Gemini: g}
	if text, method, err = reading.page(ctx, layer, broken, noTesseract); err != nil || text != layer || method != "PDF text" {
		t.Fatalf("an unrenderable page with text keeps it: %q %q %v", text, method, err)
	}
	if _, _, err = reading.page(ctx, "", broken, noTesseract); err != errRender {
		t.Fatalf("an unrenderable scanned page fails: %v", err)
	}
}

func TestGeminiIsConfiguredOnlyWithAKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GEMINI_OCR_MODEL", "")
	t.Setenv("GEMINI_PAID_SERVICE", "confirmed")
	if GeminiFromEnv() != nil || OCRSetting()["provider"] != "tesseract" || OCRSetting()["held"] != "" {
		t.Fatal("no key, no Gemini")
	}
	// A key alone sends nothing: the operator must confirm its project is on Google's paid service.
	t.Setenv("GEMINI_API_KEY", "test-key-not-real")
	for _, unconfirmed := range []string{"", "yes", "Confirmed"} {
		t.Setenv("GEMINI_PAID_SERVICE", unconfirmed)
		if GeminiFromEnv() != nil || OCRSetting()["provider"] != "tesseract" || OCRSetting()["held"] != "paid service not confirmed" {
			t.Fatalf("a key without the paid service confirmed (%q) sends no page: %+v", unconfirmed, OCRSetting())
		}
	}
	t.Setenv("GEMINI_PAID_SERVICE", "confirmed")
	if g := GeminiFromEnv(); g == nil || g.model != defaultGeminiModel || OCRSetting()["model"] != defaultGeminiModel || strings.Contains(g.endpoint, "key=") {
		t.Fatalf("a key on the confirmed paid service turns Gemini on with the default model: %+v", OCRSetting())
	}
	t.Setenv("GEMINI_OCR_MODEL", "gemini-3.7-flash")
	if OCRSetting()["model"] != "gemini-3.7-flash" {
		t.Fatal("the operator chooses the model")
	}
	t.Setenv("GEMINI_OCR_MODEL", "../models/other:generateContent?x=")
	if GeminiFromEnv() != nil {
		t.Fatal("a malformed model name is refused")
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
	if len(check.Warnings) != 1 || len(check.Questions) != 0 || !strings.Contains(check.Warnings[0], "PDF page 1: Gemini's reading of the page image differs from its PDF text; compare that page with the original") {
		t.Fatalf("a page Gemini read differently is named, to compare with the original: %+v", check)
	}
	if agreed := Check("statement.pdf", strings.Replace(flagged, imageDiffers, " read the same text", 1)); len(agreed.Warnings) != 0 || len(agreed.Questions) != 0 {
		t.Fatalf("pages Gemini read the same raise nothing: %+v", agreed)
	}
	// Only the extractor's own marker counts: the same words inside a page's text do not.
	if inText := Check("statement.pdf", "--- Page 1 · PDF text ---\nGemini"+imageDiffers+" ---\nPage 1 of 1\n"); len(inText.Warnings) != 0 || len(inText.Questions) != 0 {
		t.Fatalf("a page's text cannot raise the warning: %+v", inText)
	}
}
