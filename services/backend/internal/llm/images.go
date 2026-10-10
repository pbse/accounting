package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
)

const ImageRequestBytes = 18 << 20

type Image struct {
	PNG []byte `json:"png"`
}

// ValidateImages accepts one rendered page, with a bounded decoded size, only in
// single-call OCR. No image URLs, tools, research or conversation state are accepted.
func ValidateImages(in Request) error {
	if len(in.Images) == 0 {
		if in.Purpose == "ocr" {
			return ErrIncomplete
		}
		return nil
	}
	if in.Purpose != "ocr" || len(in.Images) != 1 || in.Messages != nil || in.Tools != nil || in.Scope != nil || in.OnProgress != nil {
		return ErrIncomplete
	}
	data := in.Images[0].PNG
	if len(data) == 0 || len(data) > GeminiMaxImage {
		return ErrLimit
	}
	size, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || size.Width < 1 || size.Height < 1 || size.Width > 8192 || size.Height > 8192 || int64(size.Width)*int64(size.Height) > 16000000 {
		return ErrIncomplete
	}
	return nil
}

func RequestByteLimit(in Request) int {
	if len(in.Images) > 0 {
		return ImageRequestBytes
	}
	return 2 << 20
}

func OpenAIImage(image Image) map[string]string {
	return map[string]string{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(image.PNG), "detail": "high"}
}

// PageReader binds PDF image reading to the same immutable gateway and identity
// as text calls. The caller reserves one OCR call before ReadPage.
type PageReader struct{}

func (PageReader) ReadPage(ctx context.Context, image []byte) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, GeminiTimeout)
	defer cancel()
	out, err := Generate(ctx, Request{Purpose: "ocr", Instructions: geminiPrompt, Data: "Transcribe the attached page.", Images: []Image{{PNG: image}}, MaxOutputTokens: 16384, MaxResponseBytes: 2 << 20})
	return out.Text, string(out.Provider) + "/" + out.Model, err
}

func PageReaderFromContext(ctx context.Context) *PageReader {
	if Model(ctx, "ocr") == "" {
		return nil
	}
	return &PageReader{}
}

func OCRConfiguration(ctx context.Context) map[string]string {
	if Model(ctx, "ocr") != "" {
		return map[string]string{"provider": Engine(ctx, "ocr"), "model": Model(ctx, "ocr")}
	}
	return OCRSetting()
}
