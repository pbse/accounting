package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"close.local/backend/internal/llm"
)

// workerOCR is how the document worker reads PDF pages, as its heartbeat reported in the last five minutes: provider
// "gemini" with its model, "tesseract", "held" with why a key it holds is unused, or "" when the worker has not
// reported. The API does not hold the Gemini key, so it asks the worker that does. The heartbeat is platform state,
// read outside any one firm: under a fresh system context, since a signed-in member's firm would otherwise decide the
// row-level security tenant.
func workerOCR(ctx context.Context, db *sql.DB) (provider, detail string) {
	platform, cancel := context.WithTimeout(context.WithValue(context.Background(), systemKey{}, true), 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	var raw []byte
	if db.QueryRowContext(platform, `SELECT COALESCE(detail->'ocr','{}'::jsonb) FROM worker_heartbeats WHERE loop='worker' AND beat_at>now()-interval '5 minutes'`).Scan(&raw) != nil {
		return "", ""
	}
	var setting struct{ Provider, Model, Held string }
	if json.Unmarshal(raw, &setting) != nil {
		return "", ""
	}
	if setting.Held != "" {
		return "held", setting.Held
	}
	return setting.Provider, setting.Model
}

// heldBecause says why a Gemini key on the worker is unused.
func heldBecause(reason string) string {
	if reason == llm.HeldModel {
		return "GEMINI_OCR_MODEL is not a valid model name"
	}
	return "it was not installed with the operator's paid-service confirmation (reinstall it with the credential script's --paid-service)"
}

// ocrLabel says, for Settings → Policies, how PDF pages are read.
func ocrLabel(provider, detail string) string {
	switch provider {
	case "gemini":
		return "Google Gemini (" + detail + ") reads every PDF page; a scanned page it cannot read is read locally by Tesseract"
	case "openai":
		return "OpenAI API (" + detail + ") reads authorized PDF pages; a scanned page it cannot read is read locally by Tesseract"
	case "context_machine":
		return "Private context machine (" + detail + ") reads PDF pages for authorized uploaders with current client grants; other pages use local PDF text/Tesseract"
	case "tesseract":
		return "Local only: each page's own PDF text, and Tesseract for scanned pages"
	case "held":
		return "Local only, for now: a Gemini key is set, but " + heldBecause(detail) + ", so no page goes to Google"
	}
	return "Not reported: the document worker has not checked in during the last five minutes"
}
