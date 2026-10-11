package main

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

func TestTheOCRPageIsAPNGGeminiAccepts(t *testing.T) {
	raw := page(ocrText)
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil || img.Bounds().Dx() != 880 || len(raw) > 4<<20 {
		t.Fatalf("a PNG of the enlarged page within Gemini's image limit: %v %v %d", err, img.Bounds(), len(raw))
	}
	if out := os.Getenv("PROVIDER_CHECK_PAGE"); out != "" {
		os.WriteFile(out, raw, 0o600)
	}
}
