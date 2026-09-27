package transport

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/preparation"
)

// issueLink stores a new private reply link. When the firm requires passcodes it also returns an 8-digit
// passcode, salted by the raw token the database never holds; both are shown only to the issuing person.
func issueLink(ctx context.Context, tx *sql.Tx, requestID, actor, token string) (string, error) {
	var required bool
	// The firm's row stays locked until commit, so a policy change cannot slip between this read and the insert.
	if err := tx.QueryRowContext(ctx, `SELECT o.require_link_passcode FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id JOIN organizations o ON o.id=c.organization_id WHERE q.id::text=$1 FOR SHARE OF o`, requestID).Scan(&required); err != nil {
		return "", err
	}
	passcode, digest := "", ""
	if required {
		n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
		if err != nil {
			return "", err
		}
		passcode = fmt.Sprintf("%08d", n)
		digest = preparation.Digest([]byte(token + ":" + passcode))
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO client_request_links(digest,request_id,issued_by,passcode_digest) VALUES($1,$2::bigint,$3,NULLIF($4,''))`, preparation.Digest([]byte(token)), requestID, actor, digest)
	return passcode, err
}

// linkPasscode checks the passcode a link may need before anything about its request is read. Each wrong try is
// recorded on its own, and the fifth revokes the link.
func linkPasscode(w http.ResponseWriter, r *http.Request, db *sql.DB, token string) bool {
	var stored string
	err := db.QueryRowContext(r.Context(), `SELECT COALESCE(passcode_digest,'') FROM client_request_links WHERE digest=$1`, preparation.Digest([]byte(token))).Scan(&stored)
	if err == sql.ErrNoRows || err == nil && stored == "" {
		return true
	}
	if err != nil {
		problem(w, 503, "Request service unavailable.")
		return false
	}
	supplied := r.Header.Get("X-Request-Passcode")
	if supplied == "" {
		problemCode(w, 401, "PASSCODE_REQUIRED", "Enter the passcode your accounting team gave you.")
		return false
	}
	if subtle.ConstantTimeCompare([]byte(preparation.Digest([]byte(token+":"+supplied))), []byte(stored)) == 1 {
		return true
	}
	var failures int
	var revoked bool
	if err = db.QueryRowContext(r.Context(), `UPDATE client_request_links SET passcode_failures=passcode_failures+1,revoked_at=CASE WHEN passcode_failures>=4 THEN COALESCE(revoked_at,now()) ELSE revoked_at END WHERE digest=$1 RETURNING passcode_failures,revoked_at IS NOT NULL`, preparation.Digest([]byte(token))).Scan(&failures, &revoked); err != nil {
		problem(w, 503, "Request service unavailable.")
		return false
	}
	if revoked {
		problem(w, 404, "This link no longer works after too many wrong passcodes, or it was revoked. Ask your accounting team for a new link.")
		return false
	}
	problem(w, 403, fmt.Sprintf("That passcode is not right. %d tries left.", 5-failures))
	return false
}

// Staff capture and scoped client replies share the same evidence/queue writer; every reply is redacted first.
func saveWorkReply(ctx context.Context, tx *sql.Tx, closeID, jobID, requestID, body, actor, from string) (int, error) {
	_, status, err := recordReply(ctx, tx, closeID, jobID, requestID, body, actor, from)
	if err == nil && jobID != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state='waiting_evidence',error='' WHERE id=$1`, jobID); err != nil {
			return 503, fmt.Errorf("Reply could not be saved. Try again.")
		}
	}
	return status, err
}

// recordReply saves a reply as evidence and links it to its request (or a preparer answer to its job), without resuming work.
func recordReply(ctx context.Context, tx *sql.Tx, closeID, jobID, requestID, body, actor, from string) (string, int, error) {
	if len(strings.TrimSpace(body)) == 0 || len(body) > 20000 || !utf8.ValidString(body) || strings.ContainsRune(body, 0) || len(from) == 0 || len(from) > 254 {
		return "", 422, fmt.Errorf("Provide an original reply of at most 20,000 bytes and its reported sender.")
	}
	body, _ = documents.Redact(body)
	from, _ = documents.Redact(from)
	var count, size int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0) FROM evidence WHERE close_id=$1`, closeID).Scan(&count, &size); err != nil {
		return "", 503, fmt.Errorf("Could not check evidence storage.")
	}
	if count >= 100 || size+len(body) > 20*1024*1024 {
		return "", 413, fmt.Errorf("Close evidence limit reached. Contact the firm.")
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
	if err != nil {
		return "", 503, fmt.Errorf("Reply could not be saved. Try again.")
	}
	return source, 200, nil
}

var requestQuestionLine = regexp.MustCompile(`^\s*[0-9]{1,2}[.)]\s+(\S.*)$`)

// requestQuestions splits a firm-approved request into its numbered questions; a line continues its question until a
// blank line, and a request without numbered lines, or with more than 50, is one question.
func requestQuestions(body string) []string {
	questions := []string{}
	open := false
	for _, line := range strings.Split(body, "\n") {
		if m := requestQuestionLine.FindStringSubmatch(line); m != nil {
			questions = append(questions, strings.TrimSpace(m[1]))
			open = true
		} else if strings.TrimSpace(line) == "" {
			open = false
		} else if open {
			questions[len(questions)-1] += " " + strings.TrimSpace(line)
		}
	}
	if len(questions) == 0 || len(questions) > 50 {
		return []string{strings.TrimSpace(body)}
	}
	return questions
}

type clientAnswer struct {
	Position int    `json:"position"`
	State    string `json:"state"`
	Answer   string `json:"answer"`
}

type requestQuestion struct {
	Position int    `json:"position"`
	Text     string `json:"text"`
	State    string `json:"state"`
	Answer   string `json:"answer"`
}

// requestAnswers reads the client's current answer to each question.
func requestAnswers(ctx context.Context, tx *sql.Tx, requestID string, questions []string) ([]requestQuestion, error) {
	out := make([]requestQuestion, len(questions))
	for i, q := range questions {
		out[i] = requestQuestion{Position: i + 1, Text: q}
	}
	rows, err := tx.QueryContext(ctx, `SELECT position,state,answer FROM work_request_answers WHERE request_id::text=$1`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a requestQuestion
		if err = rows.Scan(&a.Position, &a.State, &a.Answer); err != nil {
			return nil, err
		}
		if a.Position >= 1 && a.Position <= len(out) {
			out[a.Position-1].State, out[a.Position-1].Answer = a.State, a.Answer
		}
	}
	return out, rows.Err()
}

// compiledReply is the client's whole current reply, one document listing every question's answer or what is outstanding.
func compiledReply(entity, period string, update int, questions []requestQuestion) (string, bool) {
	var b strings.Builder
	complete := true
	fmt.Fprintf(&b, "Client reply for %s · %s (update %d)\n", entity, period, update)
	for _, q := range questions {
		fmt.Fprintf(&b, "\n%d. %s\n", q.Position, q.Text)
		switch q.State {
		case "answered":
			fmt.Fprintf(&b, "Answer: %s\n", q.Answer)
		case "awaiting_document":
			complete = false
			b.WriteString("Will send the document later.")
			if q.Answer != "" {
				b.WriteString(" " + q.Answer)
			}
			b.WriteString("\n")
		default:
			complete = false
			b.WriteString("Not answered yet.\n")
		}
	}
	return b.String(), complete
}

// requestOpen reports whether a replied request still takes additions: every job it covers is unfinished with no newer
// request, and a consolidated request's coverage is unchanged. A request for accounts alone covers no job, so it stays
// open while its coverage is.
func requestOpen(ctx context.Context, tx *sql.Tx, closeID, requestID string) (bool, error) {
	var open bool
	err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM request_jobs rj JOIN preparation_jobs j ON j.id=rj.job_id WHERE rj.request_id=$1 AND (j.state IN ('completed','cancelled') OR EXISTS(SELECT 1 FROM request_jobs newer WHERE newer.job_id=rj.job_id AND newer.request_id>rj.request_id)))`, requestID).Scan(&open)
	if err == nil && open {
		open = preparation.RequestCoverageCurrent(ctx, tx, closeID, requestID) == nil
	}
	return open, err
}

// requestReplied is whether request q has a reply: answers through any of its links, or an emailed reply that used one.
func requestReplied(q string) string {
	return `(EXISTS(SELECT 1 FROM request_submissions s WHERE s.request_id=` + q + `) OR EXISTS(SELECT 1 FROM client_request_links u WHERE u.request_id=` + q + ` AND u.used_at IS NOT NULL))`
}

// requestLinkable is why a request cannot get a new link: before any reply it must be exactly as approved, after one it
// must still take additions.
func requestLinkable(ctx context.Context, tx *sql.Tx, closeID, requestID string) error {
	var replied bool
	if err := tx.QueryRowContext(ctx, `SELECT `+requestReplied("$1"), requestID).Scan(&replied); err != nil {
		return fmt.Errorf("Request unavailable.")
	}
	if !replied {
		return preparation.FreshClientRequest(ctx, tx, closeID, requestID)
	}
	if open, err := requestOpen(ctx, tx, closeID, requestID); err != nil || !open {
		return fmt.Errorf("This request is complete or was replaced, so it takes no more answers.")
	}
	return nil
}

// staleRequest is what the client sees when the work behind their request changed before they replied.
const staleRequest = "The work behind this request changed after it was sent, so it takes no replies. Contact the firm."

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
		if !linkPasscode(w, r, db, token) {
			return
		}
		var bodyInput string
		var confirmed bool
		files := []clientFile{}
		removed := []string{}
		// A structured reply answers numbered questions; plain text answers a request with one question.
		var answers []clientAnswer
		plain := false
		if r.Method == "POST" {
			var valid bool
			bodyInput, confirmed, files, valid = clientSubmission(w, r)
			if !valid {
				return
			}
			var structured struct {
				Answers []clientAnswer `json:"answers"`
			}
			if strings.HasPrefix(strings.TrimSpace(bodyInput), "{") {
				if preparation.Strict([]byte(bodyInput), &structured) != nil || structured.Answers == nil {
					problem(w, 422, "Send answers as {\"answers\": [...]}.")
					return
				}
				answers = structured.Answers
			} else if strings.TrimSpace(bodyInput) != "" {
				answers, plain = []clientAnswer{{1, "answered", bodyInput}}, true
			}
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
		var requestID, body, expires string
		var valid, used, latest bool
		err = tx.QueryRowContext(r.Context(), `SELECT q.id::text,d.body,l.expires_at::text,l.revoked_at IS NULL AND l.expires_at>now(),`+requestReplied("q.id")+`,(q.job_id IS NULL OR q.id=(SELECT max(id) FROM work_requests WHERE job_id::text=$2)) FROM client_request_links l JOIN work_requests q ON q.id=l.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE l.digest=$1 FOR UPDATE OF l`, digest, jobID).Scan(&requestID, &body, &expires, &valid, &used, &latest)
		if err != nil || !valid {
			problem(w, 404, "Request link expired or was revoked. Ask the firm for a new link.")
			return
		}
		questions, err := requestAnswers(r.Context(), tx, requestID, requestQuestions(body))
		if err != nil {
			problem(w, 503, "Request unavailable.")
			return
		}
		active, err := requestOpen(r.Context(), tx, closeID, requestID)
		if err != nil {
			problem(w, 503, "Request unavailable.")
			return
		}
		var rescan bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM client_attachments WHERE request_id::text=$1 AND state IN ('queued','scanning','failed'))`, requestID).Scan(&rescan); err != nil {
			problem(w, 503, "Request unavailable.")
			return
		}
		reply := func(status string) {
			jsonReply(w, 200, map[string]any{"status": status, "entity": entity, "period": period, "body": body, "expires_at": expires, "questions": questions, "open": active, "removed": removed, "rescan": rescan})
		}
		if r.Method == "GET" {
			switch {
			case used:
				reply("received")
			case !latest || state != "waiting_client":
				problem(w, 409, "This request is no longer accepting replies. Contact the firm.")
			default:
				if err = preparation.FreshClientRequest(r.Context(), tx, closeID, requestID); err != nil {
					problem(w, 409, staleRequest)
					return
				}
				reply("open")
			}
			return
		}
		if !confirmed {
			problem(w, 422, "Confirm that your reply is for this client and period.")
			return
		}
		if plain && len(questions) != 1 {
			problem(w, 422, "This request has numbered questions. Answer each one.")
			return
		}
		sent := []clientAnswer{}
		for _, a := range answers {
			a.Answer = strings.TrimSpace(a.Answer)
			if a.Position < 1 || a.Position > len(questions) || utf8.RuneCountInString(a.Answer) > 4000 || !slices.Contains([]string{"answered", "awaiting_document", ""}, a.State) || a.State == "answered" && a.Answer == "" {
				problem(w, 422, "Answer each question in up to 4,000 characters, or mark it as a document you will send later.")
				return
			}
			if a.State == "" {
				continue
			}
			redacted, gone := documents.Redact(a.Answer)
			for _, kind := range gone {
				if !slices.Contains(removed, kind) {
					removed = append(removed, kind)
				}
			}
			questions[a.Position-1].State, questions[a.Position-1].Answer = a.State, redacted
			sent = append(sent, clientAnswer{a.Position, a.State, redacted})
		}
		if len(sent) == 0 && len(files) == 0 {
			problem(w, 422, "Answer at least one question, mark a document you will send later, or attach a document.")
			return
		}
		// A retry sends the same answers, so its receipt comes from them (redacted), not from the compiled reply.
		normalized, _ := json.Marshal(sent)
		receipt := preparation.Digest(normalized)
		if len(files) > 0 {
			parts := []string{string(normalized)}
			for _, f := range files {
				parts = append(parts, f.Name, f.Digest)
			}
			raw, _ := json.Marshal(parts)
			receipt = preparation.Digest(raw)
		}
		// Only the latest reply can be a retry, so returning to an earlier answer is saved as a correction.
		var repeat bool
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT receipt=$2 FROM request_submissions WHERE request_id::text=$1 ORDER BY id DESC LIMIT 1),false)`, requestID, receipt).Scan(&repeat); err != nil {
			problem(w, 503, "Request unavailable.")
			return
		}
		if repeat {
			reply("received")
			return
		}
		// The first reply needs the request exactly as approved; later ones need its work still open to them.
		if !used {
			if !latest || state != "waiting_client" {
				problem(w, 409, "This request is no longer accepting replies. Contact the firm.")
				return
			}
			if err = preparation.FreshClientRequest(r.Context(), tx, closeID, requestID); err != nil {
				problem(w, 409, staleRequest)
				return
			}
		} else if !active {
			problem(w, 409, "This request is complete or was replaced, so it takes no more answers. Contact the firm to change anything.")
			return
		}
		var update int
		if err = tx.QueryRowContext(r.Context(), `SELECT count(*)+1 FROM request_submissions WHERE request_id::text=$1`, requestID).Scan(&update); err != nil {
			problem(w, 503, "Request unavailable.")
			return
		}
		compiled, complete := compiledReply(entity, period, update, questions)
		if len(compiled) > 20000 {
			problem(w, 422, "Your answers together are too long. Shorten them to under 20,000 characters in total.")
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
		// Each reply replaces the previous one as the client's current reply; the earlier one stays in history, and work that
		// cited it is reviewed again.
		var previous sql.NullString
		if err = tx.QueryRowContext(r.Context(), `SELECT (SELECT s.source_id::text FROM request_submissions s JOIN evidence e ON e.id=s.source_id WHERE s.request_id::text=$1 AND e.superseded_by IS NULL ORDER BY s.id DESC LIMIT 1)`, requestID).Scan(&previous); err != nil {
			problem(w, 503, "Request unavailable.")
			return
		}
		// Documents alone leave the current reply as it is.
		source := previous.String
		if len(sent) > 0 || !previous.Valid {
			var status int
			if source, status, err = recordReply(r.Context(), tx, closeID, jobID, requestID, compiled, "request-link holder (identity unverified)", "Unverified request-link holder"); err != nil {
				problem(w, status, err.Error())
				return
			}
			if previous.Valid && previous.String != source {
				_, err = tx.ExecContext(r.Context(), `UPDATE evidence SET superseded_by=$2,replacement_reason='Replaced by the client''s later reply' WHERE id::text=$1 AND superseded_by IS NULL`, previous.String, source)
			}
		}
		for _, q := range questions {
			if err == nil && q.State != "" {
				_, err = tx.ExecContext(r.Context(), `INSERT INTO work_request_answers(request_id,position,state,answer) VALUES($1,$2,$3,$4) ON CONFLICT (request_id,position) DO UPDATE SET state=EXCLUDED.state,answer=EXCLUDED.answer`, requestID, q.Position, q.State, q.Answer)
			}
		}
		for _, file := range files {
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `INSERT INTO client_attachments(request_id,close_id,name,digest,body) VALUES($1,$2,$3,$4,$5) ON CONFLICT (request_id,digest) DO NOTHING`, requestID, closeID, file.Name, file.Digest, file.Body)
			}
		}
		// The reply offers a scan retry for the files just saved too, in case the page's scan of them fails.
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM client_attachments WHERE request_id::text=$1 AND state IN ('queued','scanning','failed'))`, requestID).Scan(&rescan)
		}
		// Work resumes on its own only once every question is answered; a partial reply waits for the firm.
		if err == nil && complete {
			if jobID != "" {
				_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='waiting_evidence',error='' WHERE id=$1 AND state='waiting_client'`, jobID)
			}
			if err == nil {
				err = preparation.ResumeRequestJobs(r.Context(), tx, requestID)
			}
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO request_submissions(request_id,receipt,source_id,complete) VALUES($1,$2,$3,$4)`, requestID, receipt, source, complete)
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET used_at=COALESCE(used_at,now()),reply_digest=$2 WHERE digest=$1`, digest, receipt)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Reply could not be confirmed. Retry the same reply.")
			return
		}
		used = true
		reply("received")
	}
}
