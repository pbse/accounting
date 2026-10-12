package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"close.local/backend/internal/ledgers"
	"close.local/backend/internal/preparation"
)

// A request for a client's books (docs/books-sources.md, Client-provided books; delivery step 12): the client is asked,
// through a private request link, for the month's Trial Balance or General Ledger, with their ledger's export steps.
// The file they send is scanned whole, and once a reviewer releases it, it is kept as one of the client's files for
// that month, read and accepted like one a member sent. The request is marked by the month it asks for in its scope
// ("books"), binding no ledger capture and no work.

// booksRequestBody is the request's wording: the month, the files that serve, and the ledger's export steps if known.
func booksRequestBody(client, period, ledger string) string {
	month := period
	if t, err := time.Parse("2006-01", period); err == nil {
		month = t.Format("January 2006")
	}
	body := "Please upload " + client + "'s books for " + month + ": the Trial Balance as of the month's last day, or the month's General Ledger, as a CSV or Excel workbook (.xlsx) of up to 5 MiB."
	if l, ok := ledgers.Find(ledger); ok && l.Exports.TrialBalance != "" {
		body += "\n\nIn " + l.Name + ": " + l.Exports.TrialBalance
		if l.Exports.GeneralLedger != "" {
			body += " For the General Ledger: " + l.Exports.GeneralLedger
		}
	}
	return body
}

func registerBooksRequests(mux *http.ServeMux, db *sql.DB) {
	// A reviewer asks the client for a month's books: a request on that month's close with a private link, whose email
	// is approved apart, as any request's is.
	mux.HandleFunc("POST /v1/closes/client/books-requests", func(w http.ResponseWriter, r *http.Request) {
		name, ok := importClient(w, r, db)
		if !ok {
			return
		}
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Period    string `json:"period"`
			Recipient string `json:"recipient"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !importPeriod.MatchString(in.Period) {
			problem(w, 422, "Say which month's books to ask for, as YYYY-MM.")
			return
		}
		recipient, e := mail.ParseAddress(strings.TrimSpace(in.Recipient))
		if !in.Confirmed || e != nil || recipient.Address != strings.TrimSpace(in.Recipient) || len(in.Recipient) > 254 {
			problem(w, 422, "Confirm the request and its exact recipient email address.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Request storage unavailable.")
			return
		}
		defer tx.Rollback()
		org := organization(r)
		// The month's close, under any of the client's names, that the member can open: a close opened under an alias is
		// opened by its own name's access, as every close route checks it.
		rows, err := tx.QueryContext(r.Context(), `SELECT id::text,entity FROM client_closes WHERE organization_id=$1 AND client_identity(organization_id,entity)=client_identity($1,$2) AND period=$3 ORDER BY id`, org, name, in.Period)
		if err != nil {
			problem(w, 503, "Request storage unavailable.")
			return
		}
		var closes [][2]string
		for rows.Next() {
			var c [2]string
			if err = rows.Scan(&c[0], &c[1]); err != nil {
				break
			}
			closes = append(closes, c)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		var closeID string
		for _, c := range closes {
			if err != nil || closeID != "" {
				break
			}
			var allowed bool
			if allowed, err = clientAccess(db, r, c[1]); allowed {
				closeID = c[0]
			}
		}
		if err == nil && closeID != "" {
			_, err = tx.ExecContext(r.Context(), `SELECT 1 FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID)
		}
		switch {
		case err != nil:
			problem(w, 503, "Request storage unavailable.")
			return
		case closeID == "":
			problem(w, 409, name+" has no close for "+in.Period+" that you can open yet. Open the month, then ask for its books.")
			return
		}
		var ledger string
		if err = tx.QueryRowContext(r.Context(), `SELECT ledger FROM current_client_ledgers WHERE organization_id=$1 AND client=client_identity($1,$2)`, org, name).Scan(&ledger); err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Request storage unavailable.")
			return
		}
		body := booksRequestBody(name, in.Period, ledger)
		var draft string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,$2,'[]',$3,$4) RETURNING id::text`,
			closeID, body, preparation.Digest([]byte(body)), actor).Scan(&draft); err != nil {
			problem(w, 503, "Request storage unavailable.")
			return
		}
		scope, _ := json.Marshal(map[string]any{"snapshot_id": "", "bindings": []any{}, "jobs": []string{}, "books": in.Period})
		rid, token, passcode, err := openConsolidated(r.Context(), tx, draft, recipient.Address, actor, scope, nil)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			problem(w, 503, "The request could not be saved.")
			return
		}
		jsonReply(w, 201, map[string]string{"id": rid, "close_id": closeID, "body": body, "link_token": token, "passcode": passcode})
	})
}
