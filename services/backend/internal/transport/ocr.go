package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// workerOCR is how the document worker reads PDF pages, as its heartbeat reported in the last five minutes: provider
// "gemini" with its model, "tesseract", or "" when the worker has not reported. The API does not hold the Gemini key, so
// it asks the worker that does. The heartbeat is platform state, read outside any one firm: under a fresh system context,
// since a signed-in member's firm would otherwise decide the row-level security tenant.
// A worker holding a key whose paid service is not confirmed reports provider "held".
func workerOCR(ctx context.Context, db *sql.DB) (provider, model string) {
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
		return "held", ""
	}
	return setting.Provider, setting.Model
}

// ocrLabel says, for Firm settings, how PDF pages are read.
func ocrLabel(provider, model string) string {
	switch provider {
	case "gemini":
		return "Google Gemini (" + model + ") reads every PDF page; a scanned page it cannot read is read locally by Tesseract"
	case "tesseract":
		return "Local only: each page's own PDF text, and Tesseract for scanned pages"
	case "held":
		return "Local only, for now: a Gemini key is set, but the operator has not confirmed its paid service (GEMINI_PAID_SERVICE), so no page goes to Google"
	}
	return "Not reported: the document worker has not checked in during the last five minutes"
}
