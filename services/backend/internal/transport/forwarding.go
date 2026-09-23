package transport

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"unicode/utf8"

	"close.local/backend/internal/preparation"
)

func forwardedFiles(raw []byte) ([]clientFile, string, error) {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	rawSubject := message.Header.Get("Subject")
	subject, err := new(mime.WordDecoder).DecodeHeader(rawSubject)
	if err != nil {
		subject = rawSubject
	}
	if !utf8.ValidString(subject) {
		return nil, "", errors.New("Email subject encoding is invalid")
	}
	if len(subject) > 500 {
		subject = subject[:500]
		for !utf8.ValidString(subject) {
			subject = subject[:len(subject)-1]
		}
	}
	media, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(media, "multipart/") {
		return nil, subject, errors.New("Attach a TXT, CSV, or PDF file to a multipart email")
	}
	files := []clientFile{}
	parts := 0
	var walk func(*multipart.Reader, int) error
	walk = func(reader *multipart.Reader, depth int) error {
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return errors.New("Email attachments could not be decoded")
			}
			parts++
			if parts > 30 {
				return errors.New("Email contains too many parts")
			}
			name, err := new(mime.WordDecoder).DecodeHeader(part.FileName())
			if err != nil {
				return errors.New("Attachment filename encoding is invalid")
			}
			if name == "" {
				media, nested, e := mime.ParseMediaType(part.Header.Get("Content-Type"))
				if e == nil && strings.HasPrefix(media, "multipart/") {
					if depth >= 3 || nested["boundary"] == "" {
						return errors.New("Email nesting is unsupported")
					}
					e = walk(multipart.NewReader(part, nested["boundary"]), depth+1)
					part.Close()
					if e != nil {
						return e
					}
					continue
				}
				if strings.Contains(strings.ToLower(part.Header.Get("Content-Disposition")), "attachment") {
					return errors.New("An attachment has no filename")
				}
				part.Close()
				continue
			}
			if len(files) >= 5 {
				return errors.New("Email contains more than five attachments")
			}
			var body io.Reader = part
			if strings.EqualFold(part.Header.Get("Content-Transfer-Encoding"), "base64") {
				body = base64.NewDecoder(base64.StdEncoding, part)
			}
			content, e := io.ReadAll(io.LimitReader(body, 5*1024*1024+1))
			part.Close()
			if e != nil || !validEvidenceFile(name, content) {
				return errors.New("Use complete TXT/CSV up to 512 KiB or PDF up to 5 MiB")
			}
			files = append(files, clientFile{Name: name, Body: content, Digest: preparation.Digest(content)})
		}
	}
	if err := walk(multipart.NewReader(message.Body, params["boundary"]), 0); err != nil {
		return nil, subject, err
	}
	if len(files) == 0 {
		return nil, subject, errors.New("Email contains no supported file attachment")
	}
	return files, subject, nil
}

func receiveForwardedEmail(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := os.Getenv("EMAIL_INTAKE_KEY")
		if len(key) < 32 || subtle.ConstantTimeCompare([]byte(key), []byte(r.Header.Get("X-Close-Email-Key"))) != 1 || db == nil {
			problem(w, 403, "Evidence intake is unavailable.")
			return
		}
		base := strings.ToLower(os.Getenv("EMAIL_INTAKE_ADDRESS"))
		parts := strings.Split(base, "@")
		to := strings.ToLower(r.Header.Get("X-Close-Envelope-To"))
		from := r.Header.Get("X-Close-Envelope-From")
		if len(parts) != 2 || !strings.HasPrefix(to, parts[0]+"+") || !strings.HasSuffix(to, "@"+parts[1]) || len(from) == 0 || len(from) > 200 || !utf8.ValidString(from) {
			problem(w, 422, "Evidence recipient or sender is invalid.")
			return
		}
		token := strings.TrimSuffix(strings.TrimPrefix(to, parts[0]+"+"), "@"+parts[1])
		if len(token) < 26 || len(token) > 64 || strings.ContainsAny(token, "+@ /\\\r\n") {
			problem(w, 422, "Evidence address is invalid.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 6*1024*1024)
		raw, err := io.ReadAll(r.Body)
		if err != nil || len(raw) == 0 {
			problem(w, 413, "Evidence email is too large.")
			return
		}
		files, subject, err := forwardedFiles(raw)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Evidence intake storage unavailable.")
			return
		}
		defer tx.Rollback()
		link := preparation.Digest([]byte(token))
		var closeID string
		var active bool
		err = tx.QueryRowContext(r.Context(), `SELECT l.close_id::text,l.expires_at>now() AND l.revoked_at IS NULL AND o.enabled FROM forwarding_links l JOIN client_closes c ON c.id=l.close_id JOIN organizations o ON o.id=c.organization_id WHERE l.digest=$1 FOR UPDATE OF l,c`, link).Scan(&closeID, &active)
		if err != nil {
			problem(w, 422, "Evidence address expired or revoked.")
			return
		}
		messageDigest := preparation.Digest(raw)
		var existing int
		if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM forwarded_files WHERE close_id=$1 AND message_digest=$2`, closeID, messageDigest).Scan(&existing); err != nil {
			problem(w, 503, "Evidence delivery status unavailable.")
			return
		}
		unique := map[string]int{}
		for _, file := range files {
			unique[file.Digest] = len(file.Body)
		}
		if existing == len(unique) {
			jsonReply(w, 202, map[string]string{"state": "already_quarantined"})
			return
		}
		if existing != 0 || !active {
			problem(w, 422, "Evidence address expired, revoked, or message conflicts with an earlier delivery.")
			return
		}
		var count, size int
		err = tx.QueryRowContext(r.Context(), `SELECT count(*),COALESCE(sum(octet_length(body)),0) FROM forwarded_files WHERE close_id=$1`, closeID).Scan(&count, &size)
		incoming := 0
		for _, length := range unique {
			incoming += length
		}
		if err != nil || count+len(unique) > 100 || size+incoming > 20*1024*1024 {
			problem(w, 413, "Forwarded evidence capacity is full.")
			return
		}
		for _, file := range files {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO forwarded_files(close_id,link_digest,message_digest,from_address,subject,name,body,digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(close_id,message_digest,digest) DO NOTHING`, closeID, link, messageDigest, from, subject, file.Name, file.Body, file.Digest)
			if err != nil {
				problem(w, 503, "Evidence email could not be saved. Retry delivery.")
				return
			}
		}
		if tx.Commit() != nil {
			problem(w, 503, "Evidence email could not be confirmed. Retry delivery.")
			return
		}
		jsonReply(w, 202, map[string]string{"state": "quarantined"})
	}
}

func registerForwardedEvidence(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/forwarding/files/{file}", func(w http.ResponseWriter, r *http.Request) {
		var name string
		var body []byte
		err := db.QueryRowContext(r.Context(), `SELECT name,body FROM forwarded_files WHERE id::text=$1 AND close_id::text=$2 AND state IN ('clean','released') AND scanned_digest=digest AND scanned_at>now()-interval '24 hours'`, r.PathValue("file"), r.PathValue("id")).Scan(&name, &body)
		if err != nil {
			problem(w, 409, "A current clean scan is required before download.")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(body)
	})
	mux.HandleFunc("POST /v1/closes/{id}/forwarding/files/{file}", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Action, Reason string
			Confirmed      bool
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed || len(strings.TrimSpace(in.Reason)) < 5 || len(in.Reason) > 2000 {
			problem(w, 422, "Confirm the decision and record a reason.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Evidence review storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close unavailable.")
			return
		}
		var id, name, digest, state, from string
		var body []byte
		var clean bool
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,name,digest,state,from_address,body,state='clean' AND scanned_digest=digest AND scanned_at>now()-interval '24 hours' FROM forwarded_files WHERE id::text=$1 AND close_id=$2 FOR UPDATE`, r.PathValue("file"), closeID).Scan(&id, &name, &digest, &state, &from, &body, &clean)
		if err != nil {
			problem(w, 404, "Forwarded attachment unavailable.")
			return
		}
		if state == "released" {
			jsonReply(w, 200, map[string]bool{"saved": true})
			return
		}
		switch in.Action {
		case "rescan":
			if state != "failed" && state != "clean" {
				problem(w, 409, "Only failed or previously clean files can be rescanned.")
				return
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE forwarded_files SET state='queued',attempts=0,claim=NULL,scanned_digest=NULL,scanned_at=NULL,scan_version=NULL,error='',next_attempt_at=now(),decided_by=$2,decision_note=$3 WHERE id=$1`, id, actor, in.Reason)
		case "reject":
			_, err = tx.ExecContext(r.Context(), `UPDATE forwarded_files SET state='rejected',decided_by=$2,decision_note=$3 WHERE id=$1 AND state IN ('queued','scanning','clean','failed')`, id, actor, in.Reason)
		case "release":
			if !clean || preparation.Digest(body) != digest || !validEvidenceFile(name, body) {
				problem(w, 409, "The exact attachment needs a recent clean scan before release.")
				return
			}
			var count, size int
			var copyExists bool
			err = tx.QueryRowContext(r.Context(), `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0),COALESCE(bool_or(digest=$2),false) FROM evidence WHERE close_id=$1`, closeID, digest).Scan(&count, &size, &copyExists)
			if err != nil || (!copyExists && (count >= 100 || size+len(body) > 20*1024*1024)) {
				problem(w, 413, "Close evidence capacity is full.")
				return
			}
			content, extraction := string(body), "ready"
			var original []byte
			if strings.HasSuffix(strings.ToLower(name), ".pdf") {
				content, extraction, original = "", "queued", body
			}
			var sourceID string
			err = tx.QueryRowContext(r.Context(), `INSERT INTO evidence(close_id,name,digest,content,original,extraction_state,created_by) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(close_id,digest) DO UPDATE SET digest=evidence.digest RETURNING id::text`, closeID, name, digest, content, original, extraction, "Forwarded from unverified "+from+"; reviewed by "+actor).Scan(&sourceID)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE forwarded_files SET state='released',source_id=$2,decided_by=$3,decision_note=$4 WHERE id=$1`, id, sourceID, actor, in.Reason)
			}
			if err == nil {
				_, err = preparation.StartIntake(r.Context(), tx, closeID, sourceID, actor)
			}
		default:
			problem(w, 422, "Choose release or reject.")
			return
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Decision could not be confirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]bool{"saved": true})
	})
	mux.HandleFunc("GET /v1/closes/{id}/forwarding", func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT jsonb_build_object('configured',$2::boolean,'links',COALESCE((SELECT jsonb_agg(jsonb_build_object('digest',l.digest,'issued_at',l.issued_at,'expires_at',l.expires_at,'revoked_at',l.revoked_at) ORDER BY l.issued_at DESC) FROM forwarding_links l WHERE l.close_id::text=$1),'[]'::jsonb),'files',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',f.id::text,'name',f.name,'digest',f.digest,'state',f.state,'error',f.error,'from_address',f.from_address,'subject',f.subject,'received_at',f.received_at,'source_id',f.source_id::text,'scanned_at',f.scanned_at) ORDER BY f.id DESC) FROM forwarded_files f WHERE f.close_id::text=$1),'[]'::jsonb))`, r.PathValue("id"), os.Getenv("EMAIL_INTAKE_ADDRESS") != "" && len(os.Getenv("EMAIL_INTAKE_KEY")) >= 32).Scan(&raw)
		if err != nil {
			problem(w, 503, "Forwarded evidence unavailable.")
			return
		}
		jsonReply(w, 200, json.RawMessage(raw))
	})
	mux.HandleFunc("POST /v1/closes/{id}/forwarding", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Action, Digest string
			Confirmed      bool
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm the evidence address decision.")
			return
		}
		if in.Action == "revoke" {
			result, err := db.ExecContext(r.Context(), `UPDATE forwarding_links SET revoked_at=now() WHERE digest=$1 AND close_id::text=$2 AND revoked_at IS NULL`, in.Digest, r.PathValue("id"))
			if err != nil {
				problem(w, 503, "Could not revoke evidence address.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Address unavailable or already revoked.")
				return
			}
			jsonReply(w, 200, map[string]string{"state": "revoked"})
			return
		}
		parts := strings.Split(os.Getenv("EMAIL_INTAKE_ADDRESS"), "@")
		if in.Action != "create" || len(parts) != 2 || parts[0] == "" || parts[1] == "" || len(os.Getenv("EMAIL_INTAKE_KEY")) < 32 {
			problem(w, 503, "Cloudflare email intake is not configured.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Address storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close unavailable.")
			return
		}
		var active int
		if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM forwarding_links WHERE close_id=$1 AND revoked_at IS NULL AND expires_at>now()`, closeID).Scan(&active); err != nil || active >= 5 {
			problem(w, 409, "Revoke an address before issuing another.")
			return
		}
		token := strings.ToLower(rand.Text())
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO forwarding_links(digest,close_id,issued_by) VALUES($1,$2,$3)`, preparation.Digest([]byte(token)), closeID, actor); err != nil || tx.Commit() != nil {
			problem(w, 503, "Address could not be issued.")
			return
		}
		jsonReply(w, 200, map[string]string{"address": parts[0] + "+" + token + "@" + parts[1]})
	})
}
