package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"close.local/backend/internal/preparation"
)

// Staff capture and scoped client replies share the same evidence/queue writer.
func saveWorkReply(ctx context.Context, tx *sql.Tx, closeID, jobID, requestID, body, actor, from string) (int, error) {
	if len(strings.TrimSpace(body)) == 0 || len(body) > 20000 || !utf8.ValidString(body) || strings.ContainsRune(body, 0) || len(from) == 0 || len(from) > 254 {
		return 422, fmt.Errorf("Provide an original reply of at most 20,000 bytes and its reported sender.")
	}
	var count, size int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0) FROM evidence WHERE close_id=$1`, closeID).Scan(&count, &size); err != nil {
		return 503, fmt.Errorf("Could not check evidence storage.")
	}
	if count >= 100 || size+len(body) > 20*1024*1024 {
		return 413, fmt.Errorf("Close evidence limit reached. Contact the firm.")
	}
	name := "Client reply to request " + requestID + ".txt"
	if requestID == "" {
		name = "Preparer answer for work " + jobID + ".txt"
	}
	var source string
	err := tx.QueryRowContext(ctx, `INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,$5) ON CONFLICT(close_id,digest) DO UPDATE SET digest=evidence.digest RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body, actor).Scan(&source)
	if err == nil {
		if requestID == "" {
			_, err = tx.ExecContext(ctx, `INSERT INTO preparation_answers(job_id,source_id,created_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, jobID, source, actor)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO work_replies(request_id,source_id,recorded_by,received_from) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, requestID, source, actor, from)
		}
	}
	if err == nil && jobID != "" {
		_, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state='waiting_evidence',error='' WHERE id=$1`, jobID)
	}
	if err != nil {
		return 503, fmt.Errorf("Reply could not be saved. Try again.")
	}
	return 200, nil
}

func clientRequest(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if db == nil {
			problem(w, 503, "Request service unavailable.")
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || (len(token) < 26 || len(token) > 128) {
			problem(w, 404, "Request link unavailable. Ask the firm for a new link.")
			return
		}
		var bodyInput string
		var confirmed bool
		files := []clientFile{}
		if r.Method == "POST" {
			var valid bool
			bodyInput, confirmed, files, valid = clientSubmission(w, r)
			if !valid {
				return
			}
		}
		receipt := preparation.Digest([]byte(bodyInput))
		if len(files) > 0 {
			parts := []string{bodyInput}
			for _, f := range files {
				parts = append(parts, f.Name, f.Digest)
			}
			raw, _ := json.Marshal(parts)
			receipt = preparation.Digest(raw)
		}
		digest := preparation.Digest([]byte(token))
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Request service unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID, jobID string
		err = tx.QueryRowContext(r.Context(), `SELECT d.close_id::text,COALESCE(q.job_id::text,'') FROM client_request_links l JOIN work_requests q ON q.id=l.request_id JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id JOIN organizations o ON o.id=c.organization_id WHERE l.digest=$1 AND o.enabled`, digest).Scan(&closeID, &jobID)
		if err != nil {
			problem(w, 404, "Request link unavailable. Ask the firm for a new link.")
			return
		}
		var entity, period string
		if err = tx.QueryRowContext(r.Context(), `SELECT entity,period FROM client_closes WHERE id=$1 FOR UPDATE`, closeID).Scan(&entity, &period); err != nil {
			problem(w, 503, "Request unavailable.")
			return
		}
		var state string
		if jobID != "" {
			if err = tx.QueryRowContext(r.Context(), `SELECT state FROM preparation_jobs WHERE id=$1 FOR UPDATE`, jobID).Scan(&state); err != nil {
				problem(w, 503, "Request unavailable.")
				return
			}
		} else {
			state = "waiting_client"
		}
		var requestID, body, replyDigest, expires string
		var valid, used, latest bool
		err = tx.QueryRowContext(r.Context(), `SELECT q.id::text,d.body,COALESCE(l.reply_digest,''),l.expires_at::text,l.revoked_at IS NULL AND l.expires_at>now(),l.used_at IS NOT NULL,(q.job_id IS NULL OR q.id=(SELECT max(id) FROM work_requests WHERE job_id::text=$2)) FROM client_request_links l JOIN work_requests q ON q.id=l.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE l.digest=$1 FOR UPDATE OF l`, digest, jobID).Scan(&requestID, &body, &replyDigest, &expires, &valid, &used, &latest)
		if err != nil || !valid {
			problem(w, 404, "Request link expired or was revoked. Ask the firm for a new link.")
			return
		}
		if used {
			if r.Method == "POST" && (!confirmed || receipt != replyDigest) {
				problem(w, 409, "A reply was already received. Contact the firm to make a correction.")
				return
			}
			jsonReply(w, 200, map[string]string{"status": "received"})
			return
		}
		if !latest || state != "waiting_client" {
			problem(w, 409, "This request is no longer accepting replies. Contact the firm.")
			return
		}
		if err = preparation.FreshClientRequest(r.Context(), tx, closeID, requestID); err != nil {
			problem(w, 409, err.Error())
			return
		}
		if r.Method == "GET" {
			jsonReply(w, 200, map[string]string{"status": "open", "entity": entity, "period": period, "body": body, "expires_at": expires})
			return
		}
		if !confirmed {
			problem(w, 422, "Confirm that your reply is for this client and period.")
			return
		}
		var pendingCount, pendingSize int
		if err = tx.QueryRowContext(r.Context(), `SELECT count(*),COALESCE(sum(octet_length(body)),0) FROM client_attachments WHERE close_id=$1`, closeID).Scan(&pendingCount, &pendingSize); err != nil {
			problem(w, 503, "Attachment capacity unavailable.")
			return
		}
		size := 0
		for _, file := range files {
			size += len(file.Body)
		}
		if pendingCount+len(files) > 100 || pendingSize+size > 20*1024*1024 {
			problem(w, 413, "Client attachment capacity reached. Contact the firm.")
			return
		}
		status, err := saveWorkReply(r.Context(), tx, closeID, jobID, requestID, bodyInput, "request-link holder (identity unverified)", "Unverified request-link holder")
		if err != nil {
			problem(w, status, err.Error())
			return
		}
		for _, file := range files {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO client_attachments(request_id,close_id,name,digest,body) VALUES($1,$2,$3,$4,$5)`, requestID, closeID, file.Name, file.Digest, file.Body)
			if err != nil {
				break
			}
		}
		if err == nil {
			err = preparation.ResumeRequestJobs(r.Context(), tx, requestID)
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET used_at=now(),reply_digest=$2 WHERE digest=$1`, digest, receipt)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Reply could not be confirmed. Retry the same reply.")
			return
		}
		jsonReply(w, 200, map[string]string{"status": "received"})
	}
}
