package transport

import (
	"bytes"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"unicode/utf8"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/preparation"
)

func replyText(raw []byte) (string, string, error) {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	from, err := mail.ParseAddressList(message.Header.Get("From"))
	if err != nil || len(from) != 1 {
		return "", "", io.ErrUnexpectedEOF
	}
	contentType := message.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", "", err
	}
	parts := []struct {
		header mail.Header
		body   io.Reader
	}{{message.Header, message.Body}}
	if media == "multipart/alternative" {
		parts = nil
		reader := multipart.NewReader(message.Body, params["boundary"])
		for count := 0; ; count++ {
			part, e := reader.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil || count >= 8 || part.Header.Get("Content-Disposition") != "" {
				return "", "", io.ErrUnexpectedEOF
			}
			body, e := io.ReadAll(io.LimitReader(part, 20001))
			if e != nil {
				return "", "", e
			}
			parts = append(parts, struct {
				header mail.Header
				body   io.Reader
			}{mail.Header(part.Header), bytes.NewReader(body)})
		}
	} else if media != "text/plain" {
		return "", "", io.ErrUnexpectedEOF
	}
	for _, part := range parts {
		contentType := part.header.Get("Content-Type")
		if contentType == "" {
			contentType = "text/plain"
		}
		kind, options, e := mime.ParseMediaType(contentType)
		if e != nil || kind != "text/plain" || (options["charset"] != "" && !strings.EqualFold(options["charset"], "utf-8") && !strings.EqualFold(options["charset"], "us-ascii")) {
			continue
		}
		if part.header.Get("Content-Disposition") != "" {
			return "", "", io.ErrUnexpectedEOF
		}
		body := part.body
		switch strings.ToLower(part.header.Get("Content-Transfer-Encoding")) {
		case "base64":
			body = base64.NewDecoder(base64.StdEncoding, body)
		case "quoted-printable":
			body = quotedprintable.NewReader(body)
		case "", "7bit", "8bit":
		default:
			return "", "", io.ErrUnexpectedEOF
		}
		value, e := io.ReadAll(io.LimitReader(body, 20001))
		if e != nil || len(value) > 20000 || !utf8.Valid(value) || bytes.IndexByte(value, 0) >= 0 || strings.TrimSpace(string(value)) == "" {
			return "", "", io.ErrUnexpectedEOF
		}
		return strings.ToLower(from[0].Address), string(value), nil
	}
	return "", "", io.ErrUnexpectedEOF
}

func receiveEmailReply(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := os.Getenv("EMAIL_INTAKE_KEY")
		if db == nil || len(key) < 32 || subtle.ConstantTimeCompare([]byte(key), []byte(r.Header.Get("X-Close-Email-Key"))) != 1 {
			problem(w, 403, "Email intake unavailable.")
			return
		}
		base, domain, ok := strings.Cut(strings.ToLower(os.Getenv("EMAIL_INTAKE_ADDRESS")), "@")
		to := strings.ToLower(r.Header.Get("X-Close-Envelope-To"))
		prefix := base + "+reply-"
		if !ok || base == "" || domain == "" || !strings.HasPrefix(to, prefix) || !strings.HasSuffix(to, "@"+domain) {
			problem(w, 422, "Reply address unavailable.")
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(to, prefix), "@"+domain)
		if len(id) < 20 || len(id) > 64 || strings.ContainsAny(id, "+@ /\\\r\n") {
			problem(w, 422, "Reply address unavailable.")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256*1024))
		if err != nil || len(raw) == 0 {
			problem(w, 413, "Use a text reply of at most 20,000 bytes; use the secure link for attachments.")
			return
		}
		headerFrom, body, err := replyText(raw)
		if err != nil {
			problem(w, 422, "Use a UTF-8 plain-text reply without attachments, or use the secure request link.")
			return
		}
		body, _ = documents.Redact(body)
		envelope := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Close-Envelope-From")))
		address, err := mail.ParseAddress(envelope)
		if err != nil || address.Address != envelope || len(envelope) > 254 {
			problem(w, 422, "Sender address unavailable.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Reply storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID, requestID, recipient string
		err = tx.QueryRowContext(r.Context(), `SELECT d.close_id::text,q.id::text,q.recipient FROM email_outbox m JOIN work_requests q ON q.id=m.request_id JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id JOIN organizations o ON o.id=c.organization_id WHERE m.id=upper($1) AND m.state='accepted' AND o.enabled`, id).Scan(&closeID, &requestID, &recipient)
		if err != nil {
			problem(w, 422, "Reply address unavailable.")
			return
		}
		authorized := strings.EqualFold(envelope, recipient) && strings.EqualFold(headerFrom, recipient)
		// A delivery retry repeats the Message-ID and Date; a digest of the raw message could fingerprint a removed value.
		message, _ := mail.ReadMessage(bytes.NewReader(raw))
		digest := preparation.Digest([]byte(envelope + "\n" + headerFrom + "\n" + message.Header.Get("Message-Id") + "\n" + message.Header.Get("Date") + "\n" + body))
		var count int
		var duplicate bool
		if err = tx.QueryRowContext(r.Context(), `SELECT count(*),COALESCE(bool_or(message_digest=$2),false) FROM inbound_replies WHERE outbox_id=upper($1)`, id, digest).Scan(&count, &duplicate); err != nil {
			problem(w, 503, "Reply storage unavailable.")
			return
		}
		if count >= 100 && !duplicate {
			problem(w, 422, "This email thread has reached its reply limit; use the secure request link.")
			return
		}
		var received string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO inbound_replies(close_id,request_id,outbox_id,message_digest,envelope_from,header_from,body,addresses_match) VALUES($1,$2,upper($3),$4,$5,$6,$7,$8) ON CONFLICT(outbox_id,message_digest) DO UPDATE SET message_digest=inbound_replies.message_digest RETURNING id::text`, closeID, requestID, id, digest, envelope, headerFrom, body, authorized).Scan(&received)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Reply could not be held. Retry delivery.")
			return
		}
		jsonReply(w, 202, map[string]string{"state": "held"})
	}
}

func registerEmailReplies(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/email-replies", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := reviewActor(w, r); !ok {
			return
		}
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.id::bigint DESC),'[]'::jsonb) FROM (SELECT id::text,request_id::text,envelope_from,header_from,body,addresses_match,state,received_at,decided_by,decision_note,verification_reference FROM inbound_replies WHERE close_id::text=$1 ORDER BY inbound_replies.id DESC LIMIT 100) x`, r.PathValue("id")).Scan(&raw)
		if err != nil {
			problem(w, 503, "Held replies unavailable.")
			return
		}
		jsonReply(w, 200, json.RawMessage(raw))
	})
	mux.HandleFunc("POST /v1/closes/{id}/email-replies/{reply}", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Action, Reason        string
			VerificationReference string `json:"verification_reference"`
			Confirmed             bool
		}
		if !decode(w, r, &in) {
			return
		}
		in.Reason = strings.TrimSpace(in.Reason)
		in.VerificationReference = strings.TrimSpace(in.VerificationReference)
		if !in.Confirmed || (in.Action != "release" && in.Action != "reject") || len(in.Reason) < 5 || len(in.Reason) > 2000 {
			problem(w, 422, "Confirm release or rejection with a reason.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Reply decision unavailable.")
			return
		}
		defer tx.Rollback()
		var lockedClose string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&lockedClose); err != nil {
			problem(w, 404, "Close unavailable.")
			return
		}
		var closeID, requestID, jobID, body, from, link string
		var authorized bool
		err = tx.QueryRowContext(r.Context(), `SELECT i.close_id::text,i.request_id::text,COALESCE(q.job_id::text,''),i.body,i.envelope_from,i.addresses_match,l.digest FROM inbound_replies i JOIN work_requests q ON q.id=i.request_id JOIN email_outbox m ON m.id=i.outbox_id JOIN client_request_links l ON l.digest=m.link_digest WHERE i.id::text=$1 AND i.close_id::text=$2 AND i.state='held' FOR UPDATE OF i`, r.PathValue("reply"), r.PathValue("id")).Scan(&closeID, &requestID, &jobID, &body, &from, &authorized, &link)
		if err != nil {
			problem(w, 409, "Reply already decided or unavailable.")
			return
		}
		if in.Action == "release" {
			if len(in.VerificationReference) < 10 || len(in.VerificationReference) > 500 {
				problem(w, 422, "Record how you independently verified the sender using a known contact channel.")
				return
			}
			if !authorized {
				problem(w, 409, "Sender did not match the approved recipient. Keep this reply held and ask the client to use the secure link.")
				return
			}
			var current bool
			err = tx.QueryRowContext(r.Context(), `SELECT l.revoked_at IS NULL AND l.used_at IS NULL AND l.expires_at>now() AND (q.job_id IS NULL OR EXISTS(SELECT 1 FROM preparation_jobs j WHERE j.id=q.job_id AND j.state='waiting_client' AND q.id=(SELECT max(id) FROM work_requests WHERE job_id=q.job_id))) FROM client_request_links l JOIN work_requests q ON q.id=l.request_id WHERE l.digest=$1 FOR UPDATE OF l`, link).Scan(&current)
			if err != nil || !current {
				problem(w, 409, "Request is no longer open; retain the held reply for manual review.")
				return
			}
			if err = preparation.FreshClientRequest(r.Context(), tx, closeID, requestID); err != nil {
				problem(w, 409, err.Error())
				return
			}
			status, saveErr := saveWorkReply(r.Context(), tx, closeID, jobID, requestID, body, actor, "Email from "+from+" (reviewed, identity not independently verified)")
			if saveErr != nil {
				problem(w, status, saveErr.Error())
				return
			}
			if err = preparation.ResumeRequestJobs(r.Context(), tx, requestID); err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET used_at=now(),reply_digest=$2 WHERE digest=$1`, link, preparation.Digest([]byte(body)))
			}
			if err != nil {
				problem(w, 503, "Reply could not resume work.")
				return
			}
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE inbound_replies SET state=$2,decided_by=$3,decision_note=$4,verification_reference=NULLIF($5,''),decided_at=now() WHERE id::text=$1`, r.PathValue("reply"), map[string]string{"release": "released", "reject": "rejected"}[in.Action], actor, in.Reason, in.VerificationReference)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Reply decision could not be confirmed.")
			return
		}
		jsonReply(w, 200, map[string]string{"state": map[string]string{"release": "released", "reject": "rejected"}[in.Action]})
	})
}
