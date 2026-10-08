package transport

import (
	"context"
	"database/sql"
	"encoding/json"
)

// workerOCR is how the document worker reads PDF pages, as its heartbeat reported in the last five minutes: provider
// "gemini" with its model, "tesseract", or "" when the worker has not reported. The API does not hold the Gemini key, so
// it asks the worker that does; the heartbeat is platform state, read outside any one firm.
func workerOCR(ctx context.Context, db *sql.DB) (provider, model string) {
	var raw []byte
	if db.QueryRowContext(context.WithValue(ctx, systemKey{}, true), `SELECT COALESCE(detail->'ocr','{}'::jsonb) FROM worker_heartbeats WHERE loop='worker' AND beat_at>now()-interval '5 minutes'`).Scan(&raw) != nil {
		return "", ""
	}
	var setting struct{ Provider, Model string }
	if json.Unmarshal(raw, &setting) != nil {
		return "", ""
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
	}
	return "Not reported: the document worker has not checked in during the last five minutes"
}
