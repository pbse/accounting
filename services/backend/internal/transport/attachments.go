package transport

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"close.local/backend/internal/bookfiles"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/wf"
)

type clientFile struct {
	Name   string
	Body   []byte
	Digest string
}

// validFileName is a client file's name: a plain name, no path or control characters.
func validFileName(name string) bool {
	if name == "" || len(name) > 200 || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// validBooksFile is a client's books file (docs/books-sources.md, Client-provided books): a CSV in whatever encoding its
// ledger wrote, or an Excel workbook, up to the scanner's 5 MiB.
func validBooksFile(name string, body []byte) bool {
	if !validFileName(name) || len(body) == 0 || len(body) > 5*1024*1024 {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".csv":
		return !bytes.Contains(body, []byte{0})
	case ".xlsx":
		return bytes.HasPrefix(body, []byte("PK\x03\x04"))
	}
	return false
}

func validEvidenceFile(name string, body []byte) bool {
	if !validFileName(name) || len(body) == 0 {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".pdf" {
		return len(body) <= 5*1024*1024 && bytes.HasPrefix(body, []byte("%PDF-")) && bytes.Contains(body, []byte("%%EOF"))
	}
	if (ext != ".txt" && ext != ".csv") || len(body) > 512*1024 || !utf8.Valid(body) {
		return false
	}
	for _, c := range string(body) {
		if unicode.IsControl(c) && c != '\n' && c != '\r' && c != '\t' {
			return false
		}
	}
	return true
}
func clientSubmission(w http.ResponseWriter, r *http.Request) (string, bool, []clientFile, bool) {
	var in struct {
		Body      string `json:"body"`
		Confirmed bool   `json:"confirmed"`
	}
	files := []clientFile{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, 21*1024*1024)
		if r.ParseMultipartForm(512*1024) != nil {
			problem(w, 413, "Submit up to five files, 20 MiB total, plus a reply.")
			return "", false, nil, false
		}
		defer r.MultipartForm.RemoveAll()
		for name := range r.MultipartForm.File {
			if name != "files" {
				problem(w, 400, "Unknown attachment field.")
				return "", false, nil, false
			}
		}
		for name := range r.MultipartForm.Value {
			if name != "body" && name != "confirmed" {
				problem(w, 400, "Unknown reply field.")
				return "", false, nil, false
			}
		}
		if len(r.MultipartForm.Value["body"]) != 1 || len(r.MultipartForm.Value["confirmed"]) != 1 || len(r.MultipartForm.File) > 1 || len(r.MultipartForm.File["files"]) > 5 {
			problem(w, 422, "Provide one reply and at most five files.")
			return "", false, nil, false
		}
		in.Body = r.FormValue("body")
		in.Confirmed = r.FormValue("confirmed") == "true"
		total := 0
		seen := map[string]bool{}
		for _, h := range r.MultipartForm.File["files"] {
			f, err := h.Open()
			if err != nil {
				problem(w, 422, "Attachment unavailable.")
				return "", false, nil, false
			}
			body, e := io.ReadAll(io.LimitReader(f, 5*1024*1024+1))
			f.Close()
			total += len(body)
			digest := preparation.Digest(body)
			// Which a request takes is checked once its request is known: books files for a request for the month's books.
			if e != nil || !(validEvidenceFile(h.Filename, body) || validBooksFile(h.Filename, body)) || total > 20*1024*1024 || seen[digest] {
				problem(w, 422, "Use distinct UTF-8 TXT/CSV up to 512 KiB or complete PDF up to 5 MiB (for a request for books, CSV or Excel up to 5 MiB); 20 MiB total.")
				return "", false, nil, false
			}
			seen[digest] = true
			files = append(files, clientFile{h.Filename, body, digest})
		}
	} else if !decode(w, r, &in) {
		return "", false, nil, false
	}
	return in.Body, in.Confirmed, files, true
}
func registerAttachments(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/attachments/{attachment}", func(w http.ResponseWriter, r *http.Request) {
		// Quarantined bytes are never served. Only a recent clean scan permits review.
		var name string
		var body []byte
		err := db.QueryRowContext(r.Context(), `SELECT name,body FROM client_attachments WHERE id::text=$1 AND close_id::text=$2 AND state IN ('clean','released') AND scanned_digest=digest AND scanned_at>now()-interval '24 hours'`, r.PathValue("attachment"), r.PathValue("id")).Scan(&name, &body)
		if err != nil {
			problem(w, 409, "Attachment needs a current clean automated scan before download.")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Write(body)
	})
	mux.HandleFunc("POST /v1/closes/{id}/attachments/{attachment}", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Action    string `json:"action"`
			Reason    string `json:"reason"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed || len(strings.TrimSpace(in.Reason)) == 0 || len(in.Reason) > 2000 {
			problem(w, 422, "Confirm your decision and record its reason.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Attachment storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		var id, rid, name, digest, state string
		var body []byte
		var clean bool
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,request_id::text,name,digest,state,body,state='clean' AND scanned_digest=digest AND scanned_at>now()-interval '24 hours' FROM client_attachments WHERE id::text=$1 AND close_id=$2 FOR UPDATE`, r.PathValue("attachment"), closeID).Scan(&id, &rid, &name, &digest, &state, &body, &clean)
		if err != nil {
			problem(w, 404, "Attachment not found.")
			return
		}
		if state == "released" {
			jsonReply(w, 200, map[string]bool{"saved": true})
			return
		}
		switch in.Action {
		case "reject":
			_, err = tx.ExecContext(r.Context(), `UPDATE client_attachments SET state='rejected',claim=NULL,decided_by=$2,decision_note=$3 WHERE id=$1`, id, actor, in.Reason)
		case "release":
			var books string
			if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(coverage_scope->>'books','') FROM work_requests WHERE id::text=$1`, rid).Scan(&books); err != nil {
				problem(w, 503, "Attachment storage unavailable.")
				return
			}
			fileValid := validEvidenceFile
			if books != "" {
				fileValid = validBooksFile
			}
			if !clean || preparation.Digest(body) != digest || !fileValid(name, body) {
				problem(w, 409, "A current clean scan of these exact bytes is required. No bypass is available.")
				return
			}
			// A file answering a request for the month's books is kept as one of the client's files for that month, to be
			// read and accepted like one a member sent, not as evidence.
			if books != "" {
				var org, entity string
				var on bool
				if err = tx.QueryRowContext(r.Context(), `SELECT c.organization_id,c.entity,o.file_imports FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE c.id::text=$1`, closeID).Scan(&org, &entity, &on); err != nil {
					problem(w, 503, "Attachment storage unavailable.")
					return
				}
				if !on {
					problem(w, 409, "File imports are not switched on for your firm, so a books file cannot be released.")
					return
				}
				read, e := bookfiles.Read(name, body)
				if e != nil {
					problem(w, 409, "The file cannot be read as books: "+e.Error()+". Reject it and ask the client for another export.")
					return
				}
				format := "csv"
				if read.Format == "xlsx" {
					format = "xlsx"
				}
				var importID string
				if importID, _, err = keepImport(r.Context(), tx, org, entity, books, name, body, format, "", "", actor); err == nil {
					_, err = tx.ExecContext(r.Context(), `UPDATE client_attachments SET state='released',import_id=$2::bigint,decided_by=$3,decision_note=$4 WHERE id=$1`, id, importID, actor, in.Reason)
				}
				if err == nil {
					err = tx.Commit()
				}
				if err != nil {
					problem(w, 503, "The books file could not be kept.")
					return
				}
				jsonReply(w, 200, map[string]any{"saved": true, "import_id": importID})
				return
			}
			if err = preparation.RequestCoverageFollows(r.Context(), tx, closeID, rid); err != nil {
				problem(w, 409, err.Error())
				return
			}
			// Work the request was approved for must still be waiting; work from its released documents may have moved on.
			var valid bool
			err = tx.QueryRowContext(r.Context(), `SELECT NOT EXISTS(SELECT 1 FROM request_jobs rj JOIN work_requests q ON q.id=rj.request_id JOIN preparation_jobs j ON j.id=rj.job_id JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id WHERE rj.request_id=$1 AND ((j.state IN ('queued','completed','cancelled') AND NOT `+preparation.RequestReleased+`) OR e.superseded_by IS NOT NULL OR EXISTS(SELECT 1 FROM request_jobs newer WHERE newer.job_id=j.id AND newer.request_id>rj.request_id)))`, rid).Scan(&valid)
			if err != nil || !valid {
				problem(w, 409, "Affected work or ledger changed. Create a fresh reviewed request before releasing this attachment.")
				return
			}
			var count, size int
			if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0) FROM evidence WHERE close_id=$1`, closeID).Scan(&count, &size); err != nil || count >= 100 || size+len(body) > 20*1024*1024 {
				problem(w, 413, "Close evidence capacity reached.")
				return
			}
			content, extraction := string(body), "ready"
			var original []byte
			if strings.EqualFold(filepath.Ext(name), ".pdf") {
				content = ""
				extraction = "queued"
				original = body
			}
			var sid string
			var current bool
			err = tx.QueryRowContext(r.Context(), `INSERT INTO evidence(close_id,name,digest,content,original,extraction_state,created_by) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(close_id,digest) DO UPDATE SET digest=evidence.digest RETURNING id::text,superseded_by IS NULL`, closeID, name, digest, content, original, extraction, fmt.Sprintf("Client request %s; released by %s", rid, actor)).Scan(&sid, &current)
			if err == nil && !current {
				problem(w, 409, "This file is already in the close as a document that was replaced. Reject it, or ask the client for the current version.")
				return
			}
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `UPDATE client_attachments SET state='released',source_id=$2,decided_by=$3,decision_note=$4 WHERE id=$1`, id, sid, actor, in.Reason)
			}
			if err == nil {
				var newJob string
				newJob, err = preparation.StartIntake(r.Context(), tx, closeID, sid, actor)
				if err == nil {
					_, err = tx.ExecContext(r.Context(), `INSERT INTO work_request_targets(request_id,job_id,revision) SELECT $1,job_id,revision FROM preparation_intakes WHERE job_id=$2 ON CONFLICT DO NOTHING`, rid, newJob)
				}
			}
			if err == nil {
				var saved sql.Result
				saved, err = tx.ExecContext(r.Context(), `INSERT INTO work_replies(request_id,source_id,recorded_by,received_from) VALUES($1,$2,$3,'Unverified request-link holder') ON CONFLICT DO NOTHING`, rid, sid, actor)
				if n, _ := rowsAffected(saved, err); n > 0 {
					_, err = wf.EmitForClose(r.Context(), tx, closeID, "client_request.replied:"+rid, map[string]string{"request_id": rid, "close_id": closeID, "source_id": sid}, actor)
				}
			}
			// Work waiting on a partial reply stays with the firm until the client completes it or the firm resumes it; a
			// complete reply resumes with its last document's release, not before.
			var partial bool
			if err == nil {
				err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT NOT complete FROM request_submissions WHERE request_id=$1 ORDER BY id DESC LIMIT 1),false)
 OR `+documentAnswersPending, rid).Scan(&partial)
			}
			if err == nil && !partial {
				err = preparation.ResumeRequestJobs(r.Context(), tx, rid)
			}
		default:
			problem(w, 422, "Unknown attachment decision.")
			return
		}
		if err != nil {
			jobProblem(w, err, "Attachment decision unconfirmed. Refresh before retrying.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Attachment decision unconfirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]bool{"saved": true})
	})
}

// One raw file per request makes the WAF verdict unambiguous. The edge strips
// caller-supplied attestations and forwards its private key only after scanning.
func clientAttachmentScan(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// A books file is scanned whole by the antivirus service's binary endpoint, as text is by its text one.
		fileScan := r.URL.Path == "/v1/client-request/scan-file"
		textScan := r.URL.Path == "/v1/client-request/scan-text" || fileScan
		key := os.Getenv("UPLOAD_SCAN_KEY")
		trusted := len(key) >= 32 && subtle.ConstantTimeCompare([]byte(key), []byte(r.Header.Get("X-Close-Upload-Scan-Key"))) == 1
		if !textScan && !trusted {
			problem(w, 503, "A trusted Cloudflare upload scan is required. Local uploads remain quarantined until scanned through the configured Cloudflare hostname.")
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || len(token) < 26 || len(token) > 128 || db == nil {
			problem(w, 404, "Request unavailable.")
			return
		}
		if !linkPasscode(w, r, db, token) {
			return
		}
		nameHint, limit := "upload.pdf", int64(5*1024*1024)
		if textScan && !fileScan {
			nameHint, limit = "upload.txt", 512*1024
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		body, err := io.ReadAll(r.Body)
		// A books file is checked as what its name says once its attachment is found.
		if err != nil || (!fileScan && !validEvidenceFile(nameHint, body)) || (fileScan && !validBooksFile("upload.csv", body) && !validBooksFile("upload.xlsx", body)) {
			problem(w, 422, "Submit one original file within its size and type limits for scanning.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Scan receipt storage unavailable.")
			return
		}
		defer tx.Rollback()
		var id, state, name string
		var books bool
		const lookup = `SELECT a.id::text,a.state,a.name,COALESCE(q.coverage_scope ? 'books',false) FROM client_request_links l JOIN client_attachments a ON a.request_id=l.request_id JOIN work_requests q ON q.id=a.request_id JOIN client_closes c ON c.id=a.close_id JOIN organizations o ON o.id=c.organization_id WHERE l.digest=$1 AND l.used_at IS NOT NULL AND l.revoked_at IS NULL AND l.expires_at>now() AND o.enabled AND a.digest=$2 FOR UPDATE OF a`
		err = tx.QueryRowContext(r.Context(), lookup, preparation.Digest([]byte(token)), preparation.Digest(body)).Scan(&id, &state, &name, &books)
		ext := strings.ToLower(filepath.Ext(name))
		if err != nil || (!textScan && ext != ".pdf") || (textScan && !fileScan && ext != ".txt" && ext != ".csv") || (fileScan && (!books || !validBooksFile(name, body))) {
			problem(w, 404, "Original attachment unavailable for this request. Use the same file and unexpired private link.")
			return
		}
		if state == "rejected" || state == "infected" {
			problem(w, 409, "This file cannot be released. Contact the firm for a new request.")
			return
		}
		provider := "Cloudflare WAF malicious uploads detection"
		if textScan && state != "released" {
			// Never hold database locks or a connection during a cold scanner start.
			tx.Rollback()
			url := os.Getenv("TEXT_SCANNER_URL")
			if url == "" || len(key) < 32 {
				problem(w, 503, "Text scanner is not configured. File remains quarantined.")
				return
			}
			if fileScan {
				u, e := neturl.Parse(url)
				if e != nil {
					problem(w, 503, "Text scanner configuration invalid.")
					return
				}
				u.Path = "/scan-file"
				url = u.String()
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(4 * time.Minute))
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
			defer cancel()
			scanRequest, e := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
			if e != nil {
				problem(w, 503, "Text scanner configuration invalid.")
				return
			}
			scanRequest.Header.Set("Authorization", "Bearer "+key)
			scanRequest.Header.Set("Content-Type", "application/octet-stream")
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			response, e := client.Do(scanRequest)
			if e != nil {
				problem(w, 503, "Text scanner unavailable. Retry the same original file.")
				return
			}
			defer response.Body.Close()
			var receipt struct {
				Status  string `json:"status"`
				Version string `json:"version"`
			}
			if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&receipt) != nil || receipt.Status != "clean" || receipt.Version == "" || len(receipt.Version) > 500 {
				problem(w, 422, "File was not cleared by the text scanner. It remains quarantined.")
				return
			}
			provider = "ClamAV text scanner: " + receipt.Version
			tx, err = db.BeginTx(r.Context(), nil)
			if err != nil {
				problem(w, 503, "Scan receipt storage unavailable.")
				return
			}
			defer tx.Rollback()
			// Recheck authorization after scanning; a revoked link or rejection wins.
			err = tx.QueryRowContext(r.Context(), lookup, preparation.Digest([]byte(token)), preparation.Digest(body)).Scan(&id, &state, &name, &books)
			if err != nil || state == "rejected" || state == "infected" {
				problem(w, 409, "Request or attachment changed during scanning.")
				return
			}
		}
		if state != "released" {
			_, err = tx.ExecContext(r.Context(), `UPDATE client_attachments SET state='clean',claim=NULL,scanned_at=now(),scanned_digest=digest,scan_version=$2,error='' WHERE id=$1`, id, provider)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Scan receipt unconfirmed. Retry scanning the same file.")
			return
		}
		jsonReply(w, 200, map[string]string{"status": "scanned"})
	}
}
