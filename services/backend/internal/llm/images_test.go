package llm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func pagePNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPageReadingFollowsProviderSwitchAndModelPolicy(t *testing.T) {
	// An actual, poorly compressible PNG exceeds the old 2 MiB text limit.
	picture := image.NewRGBA(image.Rect(0, 0, 1024, 1024))
	if _, err := rand.Read(picture.Pix); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	page := encoded.Bytes()
	if len(page) <= 2<<20 {
		t.Fatal("fixture must exercise image-sized request limit")
	}
	scope := Scope{Organization: "org", Subject: "owner", AccessProfile: "personal"}
	for _, provider := range []string{"api", "context_machine"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if provider == "context_machine" {
					var wire CompletionRequest
					if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
						t.Fatal(err)
					}
					if r.URL.Path != "/v1/complete" || wire.Scope != scope || wire.Model != "gpt-6.1-sol" || wire.Effort != "medium" || !bytes.Equal(wire.Request.Images[0].PNG, page) || wire.Request.Purpose != "ocr" {
						t.Fatalf("bad context-machine image request: %+v", wire)
					}
					json.NewEncoder(w).Encode(CompletionResponse{Version: 1, Status: "completed", Scope: scope, Response: Response{ID: "done", Model: "gpt-6.1-sol", ReasoningEffort: "medium", AuthMode: "chatgpt", Text: "Amount 123.45", ModelCalls: 1}})
				} else {
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["model"] != "gpt-6.1-sol" || body["reasoning"].(map[string]any)["effort"] != "medium" {
						t.Fatal(body)
					}
					content := body["input"].([]any)[1].(map[string]any)["content"].([]any)
					if content[1].(map[string]any)["image_url"] != OpenAIImage(Image{PNG: page})["image_url"] {
						t.Fatal("lost page")
					}
					io.WriteString(w, `{"id":"done","status":"completed","model":"gpt-6.1-sol","output":[{"type":"message","content":[{"type":"output_text","text":"Amount 123.45"}]}]}`)
				}
			}))
			defer front.Close()
			c, err := New(Config{APIImages: true, Provider: provider, APIKey: "key", Model: "gpt-6.1-sol", ReasoningEffort: "medium", ContextMachineToken: "private", ContextMachineModel: "codex", CodexModel: "gpt-6.1-sol", CodexEffort: "medium", ContextMachineEndpoint: front.URL + "/v1/run", Profiles: []Scope{scope}, AllowUncappedOutputTokens: true})
			if err != nil {
				t.Fatal(err)
			}
			if provider == "api" {
				r := c.routes["ocr"]
				r.URL = front.URL
				c.routes["ocr"] = r
			}
			ctx := WithIdentity(WithClient(context.Background(), c), "org", "owner")
			text, model, err := (PageReader{}).ReadPage(ctx, page)
			if err != nil || text != "Amount 123.45" || !strings.Contains(model, "gpt-6.1-sol") || calls != 1 {
				t.Fatal(text, model, err, calls)
			}
			if provider == "context_machine" {
				_, _, err = (PageReader{}).ReadPage(WithIdentity(ctx, "org", "other"), page)
				if err == nil || calls != 1 {
					t.Fatal("foreign owner sent an image")
				}
			}
		})
	}
}

func TestImagesRejectInvalidDataAndAmbiguousRequestsBeforeNetwork(t *testing.T) {
	base := Request{Purpose: "ocr", Instructions: "Transcribe", Images: []Image{{PNG: pagePNG(t)}}}
	if err := ValidateImages(base); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Request){
		func(r *Request) { r.Images = nil }, func(r *Request) { r.Images[0].PNG = []byte("not a PNG") }, func(r *Request) { r.Images[0].PNG = make([]byte, GeminiMaxImage+1) }, func(r *Request) { r.Purpose = "research" }, func(r *Request) { r.Messages = []Message{{Role: "user", Content: "read"}} }, func(r *Request) { r.Images = append(r.Images, Image{PNG: pagePNG(t)}) }, func(r *Request) { r.Scope = &Scope{} },
	} {
		in := base
		in.Images = append([]Image(nil), base.Images...)
		change(&in)
		if err := ValidateImages(in); err == nil {
			t.Fatal("accepted invalid image request")
		}
	}
}

func TestContextMachineDoesNotUseConfiguredGeminiAdapter(t *testing.T) {
	g := newGemini("google-key", "gemini-test", "https://unused.invalid/", nil)
	c, err := New(Config{Provider: "context_machine", ContextMachineToken: "private", ContextMachineModel: "codex", CodexModel: "gpt-6.1-sol", Profiles: []Scope{{Organization: "org", Subject: "owner", AccessProfile: "own"}}, Gemini: g})
	if err != nil {
		t.Fatal(err)
	}
	if c.routes["ocr"].Provider != ContextMachine || c.gemini != nil {
		t.Fatal("Gemini bypasses routing switch")
	}
}

func TestClaudeImageLimitIsCheckedBeforeDispatch(t *testing.T) {
	if err := ValidateAgentImages("claude", []Image{{PNG: make([]byte, 7500001)}}); err == nil {
		t.Fatal("Claude would reject an image over 10 MB base64")
	}
	if err := ValidateAgentImages("codex", []Image{{PNG: make([]byte, 7500001)}}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAITextKeyDoesNotOptPDFImagesIn(t *testing.T) {
	c, err := New(Config{APIKey: "key", Model: "gpt-6.1-sol"})
	if err != nil {
		t.Fatal(err)
	}
	if c.routes["ocr"].Token != "" || c.routes["chat"].Token == "" {
		t.Fatal("text credentials enabled image upload")
	}
	if PageReaderFromContext(WithClient(t.Context(), c)) != nil {
		t.Fatal("image reader enabled without consent")
	}
}
