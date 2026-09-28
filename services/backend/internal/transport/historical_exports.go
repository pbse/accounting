package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// exportLabel is a person's label on the uploaded exports a close uses in place of a live ledger connection.
type exportLabel struct {
	Exports          []string  `json:"exports"`
	Current          bool      `json:"current"`
	ExportedOn       string    `json:"exported_on"`
	Complete         bool      `json:"complete"`
	FinalAdjustments bool      `json:"final_adjustments"`
	Note             string    `json:"note"`
	Actor            string    `json:"actor"`
	CreatedAt        time.Time `json:"created_at"`
}

// latestExportLabel reads a close's label, naming its exports and whether each is still the current version; nil when
// the close has none.
func latestExportLabel(ctx context.Context, q querier, closeID string) (*exportLabel, error) {
	var l exportLabel
	var names []byte
	err := q.QueryRowContext(ctx, `SELECT l.exported_on::text,l.complete,l.final_adjustments,l.note,l.actor,l.created_at,
 COALESCE((SELECT jsonb_agg(e.name ORDER BY e.id) FROM evidence e WHERE e.id=ANY(l.evidence_ids)),'[]'),
 NOT EXISTS(SELECT 1 FROM unnest(l.evidence_ids) x(id) WHERE NOT EXISTS(SELECT 1 FROM evidence e WHERE e.id=x.id AND e.close_id=l.close_id AND e.superseded_by IS NULL))
 FROM close_export_labels l WHERE l.close_id::text=$1 ORDER BY l.id DESC LIMIT 1`, closeID).Scan(&l.ExportedOn, &l.Complete, &l.FinalAdjustments, &l.Note, &l.Actor, &l.CreatedAt, &names, &l.Current)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, json.Unmarshal(names, &l.Exports)
}

// sentence says what the label reports, as reported rather than verified.
func (l *exportLabel) sentence() string {
	coverage, adjustments := "complete for the month", "do not include"
	if !l.Complete {
		coverage = "partial"
	}
	if l.FinalAdjustments {
		adjustments = "already include"
	}
	text := fmt.Sprintf("Uploaded historical exports: %s reported that %s, exported %s, are %s and %s the final close adjustments; this is not verified against the books.", l.Actor, strings.Join(l.Exports, ", "), l.ExportedOn, coverage, adjustments)
	if !l.Current {
		text += " A labeled export was replaced, so label the current exports again."
	}
	return text
}

func registerHistoricalExports(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/historical-exports", func(w http.ResponseWriter, r *http.Request) {
		label, err := latestExportLabel(r.Context(), db, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Historical exports are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"label": label})
	})
	mux.HandleFunc("POST /v1/closes/{id}/historical-exports", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			EvidenceIDs      []string `json:"evidence_ids"`
			ExportedOn       string   `json:"exported_on"`
			Complete         *bool    `json:"complete"`
			FinalAdjustments *bool    `json:"final_adjustments"`
			Note             string   `json:"note"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Note = strings.TrimSpace(in.Note)
		// A date is refused only past tomorrow in UTC, so a firm ahead of UTC can label today's exports.
		day, err := time.Parse("2006-01-02", in.ExportedOn)
		if n := utf8.RuneCountInString(in.Note); err != nil || day.After(time.Now().UTC().AddDate(0, 0, 1)) || in.Complete == nil || in.FinalAdjustments == nil || n < 5 || n > 500 || len(in.EvidenceIDs) < 1 || len(in.EvidenceIDs) > 20 {
			problem(w, 422, "Choose 1–20 uploaded exports, the date they were exported (not in the future), whether they cover the whole month and already include the final close adjustments, and a note of 5–500 characters.")
			return
		}
		// The close row is locked, as a replacement locks it, so a document cannot be replaced between the check and the save.
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Historical exports are unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		var current int
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT count(DISTINCT id) FROM evidence WHERE close_id=$1::bigint AND id::text=ANY($2) AND superseded_by IS NULL AND purpose='work'`, closeID, in.EvidenceIDs).Scan(&current)
		}
		if err == sql.ErrNoRows {
			problem(w, 404, "Close not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Historical exports are unavailable.")
			return
		}
		if current != len(in.EvidenceIDs) {
			problem(w, 409, "Label only current documents uploaded to this close, each once.")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO close_export_labels(close_id,evidence_ids,exported_on,complete,final_adjustments,note,actor) VALUES($1,$2::bigint[],$3,$4,$5,$6,$7)`, closeID, in.EvidenceIDs, in.ExportedOn, *in.Complete, *in.FinalAdjustments, in.Note, reviewerName(r)); err != nil || tx.Commit() != nil {
			problem(w, 503, "The label could not be saved.")
			return
		}
		label, err := latestExportLabel(r.Context(), db, closeID)
		if err != nil {
			problem(w, 503, "The label was saved but could not be read back.")
			return
		}
		jsonReply(w, 200, map[string]any{"label": label})
	})
}
