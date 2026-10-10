package machine

import (
	"bytes"
	"close.local/backend/internal/llm"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testPageImage(t *testing.T) []byte {
	t.Helper()
	var page bytes.Buffer
	if err := png.Encode(&page, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return page.Bytes()
}
func TestProxyAttachesPageForBothCLIsWithNoExecutableTools(t *testing.T) {
	page := testPageImage(t)
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			calls := 0
			proxy := &modelProxy{ctx: context.Background(), token: "run", key: "private", agent: agent, model: "fixed", effort: "medium", authMode: "api_key", limit: 1, tokens: 16384, completion: true, images: []llm.Image{{PNG: page}}}
			proxy.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				key := "input"
				kind := "input_image"
				if agent == "claude" {
					key = "messages"
					kind = "image"
				}
				messages := body[key].([]any)
				image := messages[len(messages)-1].(map[string]any)["content"].([]any)[0].(map[string]any)
				if image["type"] != kind || len(body["tools"].([]any)) != 0 {
					t.Fatal(body)
				}
				if agent == "codex" && image["image_url"] != llm.OpenAIImage(llm.Image{PNG: page})["image_url"] {
					t.Fatal("lost image bytes")
				}
				if agent == "claude" && image["source"].(map[string]any)["media_type"] != "image/png" {
					t.Fatal("lost MIME")
				}
				raw := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
				if agent == "claude" {
					raw = "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))}, nil
			})}
			path := "/v1/responses"
			key := "input"
			if agent == "claude" {
				path = "/v1/messages"
				key = "messages"
			}
			req := httptest.NewRequest("POST", path, strings.NewReader(`{"`+key+`":[{"role":"user","content":"Transcribe attached page"}],"tools":[{"type":"function","name":"shell"}]}`))
			req.Header.Set("Authorization", "Bearer run")
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, req)
			if w.Code != 200 || calls != 1 {
				t.Fatal(w.Code, w.Body.String(), calls)
			}
		})
	}
}
