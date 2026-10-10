package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"time"
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
	if err != nil || size.Width < 1 || size.Height < 1 || size.Width > 8000 || size.Height > 8000 || int64(size.Width)*int64(size.Height) > 16000000 {
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
	ctx, cancel := context.WithTimeout(ctx, PageReadTimeout(ctx))
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
	// Explicit OpenAI selection bypasses Gemini, including unused-key warnings.
	c, _ := ctx.Value(clientKey{}).(*Client)
	if c != nil && c.apiImages {
		return map[string]string{"provider": "tesseract"}
	}
	status := OCRSetting()
	// Never report a usable adapter without a corresponding gateway route.
	if status["provider"] == "gemini" {
		return map[string]string{"provider": "tesseract"}
	}
	return status
}

// Claude's direct endpoint caps each base64 image at 10 MB. Reject oversized
// pages before dispatch rather than consuming an upstream attempt on a known limit.
func ValidateAgentImages(agent string, images []Image) error {
	if agent == "claude" {
		for _, image := range images {
			if base64.StdEncoding.EncodedLen(len(image.PNG)) > 10000000 {
				return ErrLimit
			}
		}
	}
	return nil
}

// Context-machine image inference includes CLI startup. Admission is idle-only,
// so this budget never includes waiting behind another model run.
func PageReadTimeout(ctx context.Context) time.Duration {
	if Engine(ctx, "ocr") == string(ContextMachine) {
		return 45 * time.Second
	}
	return GeminiTimeout
}

// ValidatePage lets extraction reject known limits before reserving a model call.
func (PageReader) ValidatePage(ctx context.Context, image []byte) error {
	if err := ValidateImages(Request{Purpose: "ocr", Images: []Image{{PNG: image}}}); err != nil {
		return err
	}
	c, _ := ctx.Value(clientKey{}).(*Client)
	if c == nil {
		return ErrUnavailable
	}
	return ValidateAgentImages(c.routes["ocr"].Agent, []Image{{PNG: image}})
}
