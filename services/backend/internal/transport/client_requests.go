package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"close.local/backend/internal/preparation"
)

var (
	secretPattern  = regexp.MustCompile(`(?i)\b(password|passcode|passphrase|pin|pwd|one[- ]time (?:pass)?code|otp|verification code|security code|security answer|user ?name|user ?id|login)\s*(?::|=|\bis\b|\bwas\b)[^\n]*`)
	accountPattern = regexp.MustCompile(`(?i)(\b(?:bank|checking|savings|chequing|deposit)\s+(?:account|acct)\b|\b(?:account|acct|a/c)\s*(?:number|num|no\b\.?|#))([^\d\n]{0,20})(\d(?:[ -]?\d){5,19})\b`)
	ibanPattern    = regexp.MustCompile(`(?i)\b[a-z]{2}\d{2}(?: ?[a-z0-9]){11,30}\b`)
	ssnPattern     = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	cardPattern    = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
)

// redactSensitive removes card, IBAN and SSN numbers, bank account numbers written after a bank label, and
// everything after a password or sign-in label on its line. Each removal leaves a marker with at most the last
// four digits; the original is never stored.
func redactSensitive(text string) (string, []string) {
	removed := []string{}
	note := func(kind string) {
		if !slices.Contains(removed, kind) {
			removed = append(removed, kind)
		}
	}
	plain := func(s string) string { return strings.NewReplacer(" ", "", "-", "").Replace(s) }
	last4 := func(s string) string { return s[len(s)-4:] }
	text = secretPattern.ReplaceAllStringFunc(text, func(m string) string {
		note("password or sign-in detail")
		return secretPattern.FindStringSubmatch(m)[1] + " [removed]"
	})
	text = accountPattern.ReplaceAllStringFunc(text, func(m string) string {
		p := accountPattern.FindStringSubmatch(m)
		note("bank account number")
		return p[1] + p[2] + "[bank account number ending " + last4(plain(p[3])) + " removed]"
	})
	// The match may run into the following words; drop trailing ones until the check digits hold.
	text = ibanPattern.ReplaceAllStringFunc(text, func(m string) string {
		for tokens := strings.Fields(m); len(tokens) > 0; tokens = tokens[:len(tokens)-1] {
			iban, remainder := strings.ToUpper(strings.Join(tokens, "")), 0
			if len(iban) < 15 {
				break
			}
			for _, c := range iban[4:] + iban[:4] {
				if c >= 'A' {
					remainder = (remainder*100 + int(c-'A') + 10) % 97
				} else {
					remainder = (remainder*10 + int(c-'0')) % 97
				}
			}
			if remainder == 1 {
				note("IBAN")
				return "[IBAN ending " + last4(iban) + " removed]" + strings.TrimPrefix(m, strings.Join(tokens, " "))
			}
		}
		return m
	})
	text = ssnPattern.ReplaceAllStringFunc(text, func(m string) string {
		note("SSN")
		return "[SSN ending " + last4(m) + " removed]"
	})
	text = cardPattern.ReplaceAllStringFunc(text, func(m string) string {
		digits, sum := plain(m), 0
		for i := range len(digits) {
			d := int(digits[len(digits)-1-i] - '0')
			if i%2 == 1 {
				if d *= 2; d > 9 {
					d -= 9
				}
			}
			sum += d
		}
		if sum%10 != 0 {
			return m
		}
		note("card number")
		return "[card number ending " + last4(digits) + " removed]"
	})
	return text, removed
}

// Staff capture and scoped client replies share the same evidence/queue writer; every reply is redacted first.
func saveWorkReply(ctx context.Context, tx *sql.Tx, closeID, jobID, requestID, body, actor, from string) (int, error) {
	if len(strings.TrimSpace(body)) == 0 || len(body) > 20000 || !utf8.ValidString(body) || strings.ContainsRune(body, 0) || len(from) == 0 || len(from) > 254 {
		return 422, fmt.Errorf("Provide an original reply of at most 20,000 bytes and its reported sender.")
	}
	body, _ = redactSensitive(body)
	from, _ = redactSensitive(from)
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
		removed := []string{}
		if r.Method == "POST" {
			var valid bool
			bodyInput, confirmed, files, valid = clientSubmission(w, r)
			if !valid {
				return
			}
			// Receipts and retries use the redacted text, so no fingerprint of a removed value is kept.
			bodyInput, removed = redactSensitive(bodyInput)
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
			jsonReply(w, 200, map[string]any{"status": "received", "removed": removed})
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
		jsonReply(w, 200, map[string]any{"status": "received", "removed": removed})
	}
}
