package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// standingNote ends the review note an automatic approval records after "Standing approval #<grant>".
const standingNote = ": this month's carried schedule, unchanged, approved automatically."

// coveringGrant is SQL for the active standing approval on a paper ($1) or its carry_forward ancestors.
const coveringGrant = `WITH RECURSIVE chain(id) AS (SELECT $1::bigint UNION SELECT (p.paper->'carry_forward'->>'paper_id')::bigint FROM chain c JOIN workpapers p ON p.id=c.id WHERE p.paper->'carry_forward'->>'paper_id' IS NOT NULL)
 SELECT a.id::text,a.workpaper_id::text,a.review_id::text,a.granted_by,a.reason,a.created_at FROM standing_approvals a JOIN chain c ON c.id=a.workpaper_id JOIN workpapers w ON w.id=$1 JOIN client_closes wc ON wc.id=w.close_id
 WHERE a.revoked_at IS NULL AND a.organization_id=wc.organization_id AND lower(a.client_entity)=lower(wc.entity) ORDER BY a.id DESC LIMIT 1`

// grantInForce is SQL: the granting paper ($1) still carries the approval ($2) the grant rests on, and the firm ($3) and granter ($4) are still enabled.
const grantInForce = `SELECT EXISTS(SELECT 1 FROM workpaper_review_state WHERE id::text=$1 AND review_state='approved' AND review_id=$2) AND EXISTS(SELECT 1 FROM organizations WHERE id=$3 AND enabled) AND ($3='local-development' OR EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$3 AND subject=$4 AND granted))`

// standingApproval grants, revokes or reads the approval that lets a prepaid schedule's later carried months be approved without a new review.
func standingApproval(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		closeID, paperID := r.PathValue("id"), r.PathValue("paper")
		var inClose bool
		if err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM workpapers WHERE id::text=$1 AND close_id::text=$2)`, paperID, closeID).Scan(&inClose); err != nil || !inClose {
			problem(w, 404, "Workpaper not found.")
			return
		}
		if r.Method == "GET" {
			var grant struct {
				ID          string    `json:"id"`
				WorkpaperID string    `json:"workpaper_id"`
				ReviewID    string    `json:"review_id"`
				GrantedBy   string    `json:"granted_by"`
				Reason      string    `json:"reason"`
				CreatedAt   time.Time `json:"created_at"`
				InForce     bool      `json:"in_force"`
			}
			err := db.QueryRowContext(r.Context(), coveringGrant, paperID).Scan(&grant.ID, &grant.WorkpaperID, &grant.ReviewID, &grant.GrantedBy, &grant.Reason, &grant.CreatedAt)
			if err == sql.ErrNoRows {
				jsonReply(w, 200, map[string]any{"grant": nil})
				return
			}
			if err == nil {
				err = db.QueryRowContext(r.Context(), grantInForce, grant.WorkpaperID, grant.ReviewID, organization(r), grant.GrantedBy).Scan(&grant.InForce)
			}
			if err != nil {
				problem(w, 503, "Standing approval is unavailable.")
				return
			}
			jsonReply(w, 200, map[string]any{"grant": grant})
			return
		}
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Action    string `json:"action"`
			GrantID   string `json:"grant_id"`
			Reason    string `json:"reason"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Reason = strings.TrimSpace(in.Reason)
		if !in.Confirmed || in.Action == "grant" && (in.Reason == "" || len(in.Reason) > 2000) || in.Action != "grant" && in.Action != "revoke" {
			problem(w, 422, "Confirm the standing approval and give a reason (1–2,000 characters).")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Standing approval is unavailable.")
			return
		}
		defer tx.Rollback()
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		var id string
		if in.Action == "revoke" {
			var grant, granted, reviewID, by, reason string
			var at time.Time
			if err = tx.QueryRowContext(r.Context(), coveringGrant, paperID).Scan(&grant, &granted, &reviewID, &by, &reason, &at); err != nil || grant != in.GrantID {
				problem(w, 404, "No active standing approval covers this workpaper.")
				return
			}
			err = tx.QueryRowContext(r.Context(), `UPDATE standing_approvals SET revoked_by=$2,revoked_at=now() WHERE id::text=$1 AND revoked_at IS NULL RETURNING id::text`, grant, actor).Scan(&id)
		} else {
			var state, reviewID string
			var raw []byte
			err = tx.QueryRowContext(r.Context(), `SELECT w.review_state,w.review_id,w.paper FROM workpaper_review_state w WHERE w.id::text=$1 AND w.close_id::text=$2 AND w.kind='prepaid' AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text)`, paperID, closeID).Scan(&state, &reviewID, &raw)
			var paper struct {
				Blockers []string `json:"blockers"`
			}
			if err != nil || json.Unmarshal(raw, &paper) != nil || state != "approved" || reviewID == "" || len(paper.Blockers) > 0 {
				problem(w, 409, "Only a current, approved prepaid schedule without blockers can carry a standing approval.")
				return
			}
			err = tx.QueryRowContext(r.Context(), `INSERT INTO standing_approvals(organization_id,client_entity,workpaper_id,review_id,granted_by,reason) SELECT c.organization_id,c.entity,w.id,$3::bigint,$4,$5 FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE w.id::text=$1 AND w.close_id::text=$2 ON CONFLICT (workpaper_id) WHERE revoked_at IS NULL DO NOTHING RETURNING id::text`, paperID, closeID, reviewID, actor, in.Reason).Scan(&id)
			if err == sql.ErrNoRows {
				problem(w, 409, "This schedule already has an active standing approval.")
				return
			}
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Standing approval could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "action": in.Action})
	}
}

// sweepStandingApprovals approves up to 20 unreviewed carried schedules after the cursor whose lineage holds an active standing approval.
func sweepStandingApprovals(ctx context.Context, db *sql.DB, cursor string) (string, int, error) {
	rows, err := db.QueryContext(ctx, `SELECT w.id::text,w.close_id::text FROM workpapers w WHERE w.kind='prepaid' AND w.paper ? 'carry_forward' AND w.id>$1::bigint AND NOT EXISTS(SELECT 1 FROM workpaper_reviews v WHERE v.workpaper_id=w.id) ORDER BY w.id LIMIT 20`, cursor)
	if err != nil {
		return cursor, 0, err
	}
	type candidate struct{ paper, close string }
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.paper, &c.close); err != nil {
			break
		}
		candidates = append(candidates, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return cursor, 0, err
	}
	next := "0"
	if len(candidates) == 20 {
		next = candidates[19].paper
	}
	approved := 0
	for _, c := range candidates {
		ok, err := standingApprove(ctx, db, c.close, c.paper)
		if err != nil {
			return next, approved, err
		}
		if ok {
			approved++
		}
	}
	return next, approved, nil
}

func standingApprove(ctx context.Context, db *sql.DB, closeID, paperID string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var org, decision string
	if err = tx.QueryRowContext(ctx, `SELECT organization_id,COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'') FROM client_closes c WHERE id::text=$1 FOR UPDATE`, closeID).Scan(&org, &decision); err != nil || decision == "signed_off" {
		return false, err
	}
	var grant, granted, grantReview, by, reason string
	var at time.Time
	err = tx.QueryRowContext(ctx, coveringGrant, paperID).Scan(&grant, &granted, &grantReview, &by, &reason, &at)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Lock what a revoke, a new review of the granting schedule or a firm suspension would change, then recheck the grant.
	var standing bool
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM client_closes c JOIN workpapers w ON w.close_id=c.id WHERE w.id::text=$1 FOR UPDATE OF c`, granted); err != nil {
		return false, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM standing_approvals WHERE id::text=$1 AND revoked_at IS NULL FOR UPDATE`, grant).Scan(&grant); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM organizations WHERE id=$1 FOR SHARE`, org); err != nil {
		return false, err
	}
	if err = tx.QueryRowContext(ctx, grantInForce, granted, grantReview, org, by).Scan(&standing); err != nil || !standing {
		return false, err
	}
	var state, digest, reviewID string
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT review_state,digest,review_id,paper FROM workpaper_review_state WHERE id::text=$1 AND close_id::text=$2`, paperID, closeID).Scan(&state, &digest, &reviewID, &raw); err != nil {
		return false, err
	}
	var paper struct {
		Revises  string          `json:"revises"`
		Proposal json.RawMessage `json:"proposal"`
	}
	json.Unmarshal(raw, &paper)
	if state != "pending" || paper.Revises != "" || len(paper.Proposal) > 0 && string(paper.Proposal) != "null" {
		return false, nil
	}
	if _, status, _ := decideReview(ctx, tx, closeID, paperID, by, "approved", "Standing approval #"+grant+standingNote, digest, reviewID, false); status != 0 {
		return false, nil
	}
	return true, tx.Commit()
}

// RunStandingApprovals sweeps carried schedules once a minute, moving a cursor so refused drafts cannot starve later ones.
func RunStandingApprovals(ctx context.Context, db *sql.DB) {
	cursor := "0"
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		next, _, err := sweepStandingApprovals(ctx, db, cursor)
		if err != nil && ctx.Err() == nil {
			log.Print("Standing approvals could not run: ", err)
		}
		cursor = next
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
