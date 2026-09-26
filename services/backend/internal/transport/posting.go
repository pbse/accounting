package transport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
)

// draftApproved requires a current approved draft before a posting decision. Approving the posting of a
// pending draft approves the draft too, in the caller's transaction, when the approver may review drafts.
func draftApproved(ctx context.Context, tx *sql.Tx, closeID, paperID string, actor *auth.Claims, approving bool) (int, string) {
	var state, digest, reviewID string
	if err := tx.QueryRowContext(ctx, `SELECT review_state,digest,review_id FROM workpaper_review_state WHERE id::text=$1 AND close_id::text=$2`, paperID, closeID).Scan(&state, &digest, &reviewID); err != nil {
		return 409, "A current accountant-approved workpaper is required."
	}
	if state == "approved" {
		return 0, ""
	}
	if !approving || state != "pending" || !slices.Contains(actor.Permissions, "workpapers:review") {
		return 409, "A current accountant-approved workpaper is required. Approving a posting approves its pending draft only for someone who can review drafts."
	}
	_, status, message := decideReview(ctx, tx, closeID, paperID, actor.Subject, "approved", "Approved together with its posting.", digest, reviewID, false)
	return status, message
}

// refuseSignedOff answers and reports true when the close's latest sign-off stands, or cannot be read; call it while
// holding the close's row lock.
func refuseSignedOff(w http.ResponseWriter, ctx context.Context, tx *sql.Tx, closeID string) bool {
	var signed bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT decision='signed_off' FROM close_signoffs WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1),false)`, closeID).Scan(&signed); err != nil {
		problem(w, 503, "Sign-off state is unavailable.")
		return true
	}
	if signed {
		problem(w, 409, "This close is signed off. Reopen it before approving or executing a journal.")
	}
	return signed
}

// approvalLapsed reports whether a saved posting approval's ten minutes ran out before execution.
func approvalLapsed(state string, expires *time.Time) bool {
	return state == "approved" && expires != nil && !expires.After(time.Now())
}

const lapsedMessage = "The posting approval expired after 10 minutes. Approve it again; no write was made."

type posting struct {
	RequestID  string          `json:"request_id"`
	ID         string          `json:"id"`
	Digest     string          `json:"digest"`
	Realm      string          `json:"realm"`
	State      string          `json:"state"`
	Journal    json.RawMessage `json:"journal"`
	ApprovedBy string          `json:"approved_by"`
	Expires    *time.Time      `json:"approval_expires_at"`
	ExternalID string          `json:"external_id"`
	Error      string          `json:"error"`
}

func sandboxConfiguration() quickbooks.Sandbox {
	return quickbooks.Sandbox{Realm: os.Getenv("QBO_SANDBOX_REALM_ID"), Token: os.Getenv("QBO_SANDBOX_ACCESS_TOKEN"), Entity: os.Getenv("QBO_SANDBOX_ENTITY"), DebitAccount: os.Getenv("QBO_SANDBOX_PREPAID_ACCOUNT_ID"), CreditAccount: os.Getenv("QBO_SANDBOX_EXPENSE_ACCOUNT_ID")}
}
func registerPosting(mux *http.ServeMux, db *sql.DB) {
	path := "/v1/closes/{id}/workpapers/{paper}/posting"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		record, err := readPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
		if err == sql.ErrNoRows {
			jsonReply(w, 200, map[string]any{"state": "not_prepared", "configured": sandboxConfiguration().Ready() && organization(r) == os.Getenv("WORKOS_ORGANIZATION_ID")})
			return
		}
		if err != nil {
			problem(w, 503, "Posting status is unavailable.")
			return
		}
		jsonReply(w, 200, record)
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		actor := auth.Actor(r.Context())
		if actor == nil || actor.Organization == "" {
			problem(w, 403, "Sandbox posting requires a WorkOS organization session; the development token cannot authorize it.")
			return
		}
		if actor.Organization != os.Getenv("WORKOS_ORGANIZATION_ID") {
			problem(w, 403, "This organization has no configured QuickBooks sandbox connection.")
			return
		}
		var input struct {
			Action       string `json:"action"`
			Digest       string `json:"digest"`
			Confirmation string `json:"confirmation"`
		}
		if !decode(w, r, &input) {
			return
		}
		permission := "posting:approve"
		if input.Action == "execute" || input.Action == "verify" {
			permission = "posting:execute"
		}
		if !slices.Contains(actor.Permissions, permission) {
			problem(w, 403, "Your organization role lacks "+permission+".")
			return
		}
		sandbox := sandboxConfiguration()
		if !sandbox.Ready() {
			problem(w, 503, "Configure the QuickBooks sandbox company, access token and account mapping first.")
			return
		}
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT paper FROM workpapers WHERE id::text=$1 AND close_id::text=$2 AND kind='prepaid'`, r.PathValue("paper"), r.PathValue("id")).Scan(&raw)
		if err == sql.ErrNoRows {
			problem(w, 404, "Saved prepaid workpaper not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Workpaper unavailable.")
			return
		}
		var paper prepaids.Workpaper
		if json.Unmarshal(raw, &paper) != nil {
			problem(w, 500, "Saved workpaper is invalid.")
			return
		}
		if input.Action == "preview" {
			journal, err := sandbox.Prepare(r.Context(), paper)
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			body, _ := json.Marshal(journal)
			digest := fmt.Sprintf("%x", sha256.Sum256(append([]byte(sandbox.Realm+"/"+r.PathValue("paper")+"/"), body...)))
			sourceKey := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(paper.Input.SourceReference)))))
			_, err = db.ExecContext(r.Context(), `INSERT INTO sandbox_postings(close_id,workpaper_id,source_key,realm,digest,journal,state) VALUES($1,$2,$3,$4,$5,$6,'preview') ON CONFLICT DO NOTHING`, r.PathValue("id"), r.PathValue("paper"), sourceKey, sandbox.Realm, digest, string(body))
			if err != nil {
				problem(w, 503, "Could not persist posting preview.")
				return
			}
			record, err := readPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
			if err != nil {
				problem(w, 409, "Another version of this source already has a posting intent. Review that intent; do not create a second adjustment.")
				return
			}
			if record.Digest != digest {
				problem(w, 409, "Sandbox mapping changed after this intent was created. This intent cannot be reused.")
				return
			}
			jsonReply(w, 200, record)
			return
		}
		record, err := readPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
		if err != nil {
			problem(w, 409, "Prepare a posting preview first.")
			return
		}
		if record.Digest != input.Digest || record.Realm != sandbox.Realm {
			problem(w, 409, "The approved digest or sandbox company no longer matches.")
			return
		}
		var journal quickbooks.Journal
		if json.Unmarshal(record.Journal, &journal) != nil {
			problem(w, 500, "Invalid posting intent.")
			return
		}
		switch input.Action {
		case "approve", "execute":
			fresh, err := sandbox.Prepare(r.Context(), paper)
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			body, _ := json.Marshal(fresh)
			digest := fmt.Sprintf("%x", sha256.Sum256(append([]byte(sandbox.Realm+"/"+r.PathValue("paper")+"/"), body...)))
			if digest != record.Digest {
				problem(w, 409, "Account mapping changed; the intent cannot be approved or executed.")
				return
			}
			var newer bool
			err = db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM workpapers WHERE close_id::text=$1 AND id>$2 AND kind='prepaid' AND lower(trim(paper->'input'->>'source_reference'))=$3)`, r.PathValue("id"), r.PathValue("paper"), strings.ToLower(strings.TrimSpace(paper.Input.SourceReference))).Scan(&newer)
			if err != nil || newer {
				problem(w, 409, "A newer source draft exists or source freshness could not be checked.")
				return
			}
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				problem(w, 503, "Could not check review freshness.")
				return
			}
			defer tx.Rollback()
			var closeID string
			if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
				problem(w, 503, "Could not lock the close.")
				return
			}
			if refuseSignedOff(w, r.Context(), tx, closeID) {
				return
			}
			if status, message := draftApproved(r.Context(), tx, closeID, r.PathValue("paper"), actor, input.Action == "approve"); status != 0 {
				problem(w, status, message)
				return
			}
			if input.Action == "approve" {
				if input.Confirmation != "APPROVE SANDBOX DRAFT" {
					problem(w, 422, "Type APPROVE SANDBOX DRAFT to bind your approval to this exact proposal.")
					return
				}
				result, err := tx.ExecContext(r.Context(), `UPDATE sandbox_postings SET state='approved',approved_by=$2,approval_expires_at=now()+interval '10 minutes' WHERE id=$1 AND state IN ('preview','approved')`, record.ID, actor.Subject)
				if err != nil {
					problem(w, 503, "Approval could not be saved.")
					return
				}
				n, _ := result.RowsAffected()
				if n != 1 {
					problem(w, 409, "This intent already has an execution attempt.")
					return
				}
				if err = tx.Commit(); err != nil {
					problem(w, 503, "Approval save could not be confirmed.")
					return
				}
			} else {
				if input.Confirmation != "POST TO SANDBOX" {
					problem(w, 422, "Type POST TO SANDBOX to execute this approved test entry.")
					return
				}
				result, err := tx.ExecContext(r.Context(), `UPDATE sandbox_postings SET state='executing',attempted_by=$2,attempted_at=now() WHERE id=$1 AND state='approved' AND approval_expires_at>now() AND approved_by=$2`, record.ID, actor.Subject)
				if err != nil {
					problem(w, 503, "Could not reserve the execution attempt; no provider call was made.")
					return
				}
				n, _ := result.RowsAffected()
				if n != 1 {
					if approvalLapsed(record.State, record.Expires) {
						problemCode(w, 409, "APPROVAL_EXPIRED", lapsedMessage)
						return
					}
					problem(w, 409, "Approval expired, belongs to another reviewer, or an attempt already exists. No retry was made.")
					return
				}
				if err = tx.Commit(); err != nil {
					problem(w, 503, "Execution reservation could not be confirmed; no provider call was made.")
					return
				}
				id, callErr := sandbox.Create(r.Context(), journal, record.RequestID)
				persist, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
				defer cancel()
				message := ""
				if callErr != nil {
					message = callErr.Error()
				}
				if _, err = db.ExecContext(persist, `UPDATE sandbox_postings SET state='unknown',external_id=NULLIF($2,''),last_error=$3 WHERE id=$1`, record.ID, id, message); err != nil {
					problem(w, 503, "Execution outcome could not be recorded. Do not retry the write; investigate the saved attempt.")
					return
				}
				if callErr != nil {
					problemCode(w, 502, "EXECUTION_UNKNOWN", "Execution outcome is unknown. No automatic retry is permitted. Refresh posting status for investigation.")
					return
				}
				if err = sandbox.Verify(r.Context(), id, journal); err != nil {
					db.ExecContext(persist, `UPDATE sandbox_postings SET last_error=$2 WHERE id=$1`, record.ID, err.Error())
					problemCode(w, 502, "EXECUTION_UNKNOWN", "Entry was returned but readback is unverified. Use Verify saved entry; do not post again.")
					return
				}
				if _, err = db.ExecContext(persist, `UPDATE sandbox_postings SET state='verified',last_error='',verified_at=now() WHERE id=$1`, record.ID); err != nil {
					problem(w, 503, "Readback passed but the receipt could not be saved. Verify the existing entry again.")
					return
				}
			}
		case "verify":
			if record.ExternalID == "" {
				problem(w, 409, "No external ID was recorded. Investigate this attempt in QuickBooks; do not issue another write.")
				return
			}
			if err = sandbox.Verify(r.Context(), record.ExternalID, journal); err != nil {
				persist, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
				defer cancel()
				if _, saveErr := db.ExecContext(persist, `UPDATE sandbox_postings SET state='unknown',last_error=$2 WHERE id=$1`, record.ID, err.Error()); saveErr != nil {
					problem(w, 503, "Readback failed and status could not be updated. Investigate the existing entry; do not post again.")
					return
				}
				problem(w, 422, err.Error())
				return
			}
			if _, err = db.ExecContext(r.Context(), `UPDATE sandbox_postings SET state='verified',last_error='',verified_at=now() WHERE id=$1`, record.ID); err != nil {
				problem(w, 503, "Readback passed but the receipt could not be saved.")
				return
			}
		default:
			problem(w, 422, "Choose preview, approve, execute or verify.")
			return
		}
		result, err := readPosting(r.Context(), db, r.PathValue("id"), r.PathValue("paper"))
		if err != nil {
			problem(w, 503, "Refresh posting status to confirm the outcome.")
			return
		}
		jsonReply(w, 200, result)
	})
}
func readPosting(ctx context.Context, db *sql.DB, closeID, paperID string) (posting, error) {
	var record posting
	err := db.QueryRowContext(ctx, `SELECT request_id::text,id::text,digest,realm,state,journal,COALESCE(approved_by,''),approval_expires_at,COALESCE(external_id,''),last_error FROM sandbox_postings WHERE close_id::text=$1 AND workpaper_id::text=$2`, closeID, paperID).Scan(&record.RequestID, &record.ID, &record.Digest, &record.Realm, &record.State, &record.Journal, &record.ApprovedBy, &record.Expires, &record.ExternalID, &record.Error)
	return record, err
}
