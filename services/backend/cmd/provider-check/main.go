// Command provider-check runs one read-only check against each provider configured in the environment and records the
// result for the release's evidence (docs/slice6-evidence.md): a model call for each configured accounting purpose,
// the OCR route (Gemini, when configured) reading a page of known text, and Krith's email sender (key, verified domain, sending enabled, no click
// tracking). It sends no email, writes to no ledger and stores nothing. A provider that is not configured is reported
// as a named dependency, never as a pass. It exits 1 when any configured check fails.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"strings"
	"time"

	"close.local/backend/internal/delivery"
	"close.local/backend/internal/llm"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Check is one provider check's outcome: passed, failed, or dependency (not configured here, with what it needs).
type Check struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Detail   string `json:"detail"`
	Model    string `json:"model,omitempty"`
	Millis   int64  `json:"millis,omitempty"`
}

// ocrText is the page the OCR route must read back, digit for digit.
var ocrText = []string{"INVOICE 4471", "TOTAL DUE 1,234.56", "DUE 2026-09-30"}

func main() {
	only := flag.String("only", "model,email", "comma-separated checks to run: model (every route, OCR included), email")
	report := flag.String("report", "", "write the dated JSON record here")
	organization := flag.String("organization", "", "firm the model calls are made for (a Context Machine profile needs it)")
	subject := flag.String("subject", "", "member the model calls are made for")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	run := map[string]bool{}
	for _, name := range strings.Split(*only, ",") {
		name = strings.TrimSpace(name)
		if name != "model" && name != "email" {
			log.Fatalf("-only takes model and email, not %q", name)
		}
		run[name] = true
	}
	checks := []Check{}
	if run["model"] {
		checks = append(checks, modelChecks(ctx, *organization, *subject)...)
	}
	if run["email"] {
		checks = append(checks, emailCheck(ctx))
	}
	failed := false
	fmt.Printf("# Provider checks, %s\n\n| Provider | Check | Result | Detail |\n| --- | --- | --- | --- |\n", time.Now().UTC().Format(time.RFC3339))
	for _, c := range checks {
		fmt.Printf("| %s | %s | %s | %s |\n", c.Provider, c.Name, c.State, strings.ReplaceAll(c.Detail, "|", "/"))
		failed = failed || c.State == "failed"
	}
	if *report != "" {
		raw, _ := json.MarshalIndent(map[string]any{"at": time.Now().UTC(), "checks": checks}, "", "  ")
		if err := os.WriteFile(*report, raw, 0o600); err != nil {
			log.Fatal(err)
		}
	}
	if failed {
		os.Exit(1)
	}
}

// modelChecks asks each configured accounting route for a one-field structured answer, as accounting readings are
// structured, and reports its model and time.
func modelChecks(ctx context.Context, organization, subject string) []Check {
	// The gateway is built as the app builds it. Settings present but unusable are a failure; a route nothing configures
	// is a dependency below.
	client, err := llm.FromEnv()
	if err != nil {
		return []Check{{Provider: "model", Name: "configuration", State: "failed", Detail: err.Error()}}
	}
	ctx = llm.WithClient(ctx, client)
	if organization != "" {
		ctx = llm.WithIdentity(ctx, organization, subject)
	}
	schema := &llm.OutputSchema{Name: "provider_check", Definition: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["ok"],"properties":{"ok":{"type":"boolean"}}}`)}
	out := []Check{}
	for _, purpose := range []string{"evidence_selection", "preparation", "procedure_draft", "statement", "work_command", "plan", "written_answer", "investigation", "chat", "ocr"} {
		model := llm.Model(ctx, purpose)
		if model == "" {
			detail := "no model route is configured for " + purpose
			if purpose == "ocr" {
				detail = "GEMINI_API_KEY with GEMINI_PAID_SERVICE=confirmed (LLM_PROVIDER api, the default), or an image-capable route"
			}
			out = append(out, Check{Provider: "model", Name: purpose, State: "dependency", Detail: detail})
			continue
		}
		if purpose == "ocr" {
			// The OCR route reads the rendered page, as it reads a scanned client page.
			start := time.Now()
			text, used, err := llm.PageReader{}.ReadPage(ctx, page(ocrText))
			out = append(out, readBack(Check{Provider: "model", Name: "ocr", Model: cmpOr(used, model), Millis: time.Since(start).Milliseconds()}, text, err))
			continue
		}
		start := time.Now()
		answer, err := llm.Generate(ctx, llm.Request{Purpose: purpose, Instructions: `Answer {"ok": true}.`, Data: map[string]string{"check": "provider"}, Schema: schema, MaxOutputTokens: 64})
		c := Check{Provider: "model", Name: purpose, Model: model, Millis: time.Since(start).Milliseconds()}
		var parsed struct{ OK bool }
		switch {
		case err != nil:
			c.State, c.Detail = "failed", err.Error()
		case json.Unmarshal([]byte(answer.Text), &parsed) != nil || !parsed.OK:
			c.State, c.Detail = "failed", "the answer was not the requested structure"
		default:
			c.State, c.Detail, c.Model = "passed", "structured answer from "+string(answer.Provider), answer.Model
		}
		out = append(out, c)
	}
	return out
}

// readBack passes a reading only when it is the rendered page's text exactly, line for line (spacing aside), with
// nothing missing and nothing added.
func readBack(c Check, text string, err error) Check {
	if err != nil {
		c.State, c.Detail = "failed", err.Error()
		return c
	}
	got := []string{}
	for _, line := range strings.Split(text, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			got = append(got, line)
		}
	}
	if strings.Join(got, "\n") != strings.Join(ocrText, "\n") {
		c.State, c.Detail = "failed", fmt.Sprintf("read %q, not the page's %q", strings.Join(got, " / "), strings.Join(ocrText, " / "))
		return c
	}
	c.State, c.Detail = "passed", "the page read back exactly, nothing missing or added"
	return c
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// page draws lines of text in a bitmap font, enlarged so each glyph is crisp, as a PNG.
func page(lines []string) []byte {
	small := image.NewRGBA(image.Rect(0, 0, 220, 20*len(lines)+16))
	draw.Draw(small, small.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	d := font.Drawer{Dst: small, Src: image.NewUniform(color.Black), Face: basicfont.Face7x13}
	for i, line := range lines {
		d.Dot = fixed.P(10, 22+20*i)
		d.DrawString(line)
	}
	large := image.NewRGBA(image.Rect(0, 0, small.Bounds().Dx()*4, small.Bounds().Dy()*4))
	draw.NearestNeighbor.Scale(large, large.Bounds(), small, small.Bounds(), draw.Src, nil)
	var b bytes.Buffer
	png.Encode(&b, large)
	return b.Bytes()
}

// emailCheck verifies Krith's sender as a send would need it, without sending.
func emailCheck(ctx context.Context) Check {
	if os.Getenv("PLATFORM_EMAIL_KEY") == "" || delivery.KrithAddress() == "" {
		return Check{Provider: "resend", Name: "sender", State: "dependency", Detail: "PLATFORM_EMAIL_KEY and CLIENT_EMAIL_ADDRESS (Krith's sender)"}
	}
	if err := delivery.VerifyKrithSender(ctx); err != nil {
		return Check{Provider: "resend", Name: "sender", State: "failed", Detail: err.Error()}
	}
	return Check{Provider: "resend", Name: "sender", State: "passed", Detail: "the key reads the sender's domain, verified and enabled for sending, with click tracking off"}
}
